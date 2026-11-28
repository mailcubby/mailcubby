package linkcheck

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

type fakeResolver struct {
	mu      sync.Mutex
	lookup  func(ctx context.Context, host string) ([]netip.Addr, error)
	lookups int
}

func (r *fakeResolver) LookupNetIP(ctx context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	r.lookups++
	fn := r.lookup
	r.mu.Unlock()
	return fn(ctx, host)
}

func (r *fakeResolver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lookups
}

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort(%s): %v", srv.Listener.Addr().String(), err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi(%s): %v", portStr, err)
	}
	return p
}

// FR-120, ADR 0010: simulated DNS rebinding and multi-record private+public
// responses are rejected inside DialContext without ever connecting.
func TestCheckerDNSRebindingAndMultiRecordBlocked(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	victim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(victim.Close)
	port := serverPort(t, victim)

	t.Run("rebinding to loopback on dial is blocked", func(t *testing.T) {
		t.Parallel()
		res := &fakeResolver{
			lookup: func(_ context.Context, _ string) ([]netip.Addr, error) {
				// Even if a pre-check had seen a public IP earlier, DialContext resolves
				// to loopback and MUST reject before dialing.
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			},
		}
		chk := New(nil, Config{
			AllowedPorts: []int{port},
			Resolver:     res,
		}, clock.NewFake(time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)), nil)

		report := chk.Check(t.Context(), 1, "msg1", []string{fmt.Sprintf("http://rebind.example.test:%d/secret", port)})
		if len(report.Results) != 1 {
			t.Fatalf("len(Results) = %d, want 1", len(report.Results))
		}
		got := report.Results[0]
		if got.Status != StatusBlocked || got.ErrorCode == nil || *got.ErrorCode != ErrCodeBlockedIP {
			t.Fatalf("got = %+v, want blocked/blocked_ip", got)
		}
		if hits.Load() != 0 {
			t.Fatalf("loopback server received %d hits, want 0", hits.Load())
		}
		if res.count() != 1 {
			t.Errorf("resolver calls = %d, want 1 (resolved once inside DialContext)", res.count())
		}
	})

	t.Run("dual-record public plus IMDS is blocked", func(t *testing.T) {
		t.Parallel()
		res := &fakeResolver{
			lookup: func(_ context.Context, _ string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("93.184.216.34"),
					netip.MustParseAddr("169.254.169.254"),
				}, nil
			},
		}
		chk := New(nil, Config{
			AllowedPorts: []int{port},
			Resolver:     res,
		}, clock.NewFake(time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)), nil)

		report := chk.Check(t.Context(), 1, "msg2", []string{fmt.Sprintf("http://dual.example.test:%d/", port)})
		got := report.Results[0]
		if got.Status != StatusBlocked || got.ErrorCode == nil || *got.ErrorCode != ErrCodeBlockedIP {
			t.Fatalf("got = %+v, want blocked/blocked_ip", got)
		}
	})

	t.Run("ipv4-mapped ipv6 resolved by DNS is blocked", func(t *testing.T) {
		t.Parallel()
		res := &fakeResolver{
			lookup: func(_ context.Context, _ string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1")}, nil
			},
		}
		chk := New(nil, Config{
			AllowedPorts: []int{port},
			Resolver:     res,
		}, clock.NewFake(time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)), nil)

		report := chk.Check(t.Context(), 1, "msg3", []string{fmt.Sprintf("http://mapped.example.test:%d/", port)})
		if report.Results[0].Status != StatusBlocked {
			t.Fatalf("got = %+v, want blocked", report.Results[0])
		}
	})

	t.Run("rebind sequence: first check allowlisted, second check after TTL rebinds to non-allowlisted IMDS", func(t *testing.T) {
		t.Parallel()
		allowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(allowSrv.Close)
		allowPort := serverPort(t, allowSrv)

		clk := clock.NewFake(time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC))
		var seq atomic.Int32
		res := &fakeResolver{
			lookup: func(_ context.Context, _ string) ([]netip.Addr, error) {
				if seq.Add(1) == 1 {
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}
				return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
			},
		}
		chk := New(nil, Config{
			AllowedPorts: []int{allowPort},
			AllowCIDRs:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
			Resolver:     res,
		}, clk, nil)

		target := fmt.Sprintf("http://flip.example.test:%d/", allowPort)
		r1 := chk.Check(t.Context(), 1, "msg4", []string{target})
		if r1.Results[0].Status != StatusOK {
			t.Fatalf("first check = %+v, want ok", r1.Results[0])
		}

		clk.Advance(DefaultCacheTTL + time.Second)
		r2 := chk.Check(t.Context(), 1, "msg4", []string{target})
		if r2.Results[0].Status != StatusBlocked {
			t.Fatalf("rebound check = %+v, want blocked", r2.Results[0])
		}
	})
}

// FR-120, ADR 0010: direct literal IP dialing preserves Host header and TLS SNI
// while enforcing TLS certificate verification (InsecureSkipVerify: false).
func TestCheckerPreservesTLSSNIAndHostHeader(t *testing.T) {
	t.Parallel()

	// Generate a self-signed certificate valid specifically for "tls-sni.example.test".
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tls-sni.example.test"},
		DNSNames:     []string{"tls-sni.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)

	var gotHost, gotSNI, gotUA string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		if r.TLS != nil {
			gotSNI = r.TLS.ServerName
		}
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{der},
			PrivateKey:  priv,
		}},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	port := serverPort(t, srv)

	res := &fakeResolver{
		lookup: func(_ context.Context, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
	}

	target := fmt.Sprintf("https://tls-sni.example.test:%d/check", port)

	// With trusted RootCAs: SNI and Host are preserved and cert verifies.
	chk := New(nil, Config{
		AllowedPorts: []int{port},
		AllowCIDRs:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Resolver:     res,
		RootCAs:      pool,
		Version:      "0.2.0-test",
	}, clock.NewFake(time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)), nil)

	rep := chk.Check(t.Context(), 1, "m-tls", []string{target})
	if rep.Results[0].Status != StatusOK {
		t.Fatalf("TLS check = %+v, want ok", rep.Results[0])
	}
	if gotSNI != "tls-sni.example.test" {
		t.Errorf("TLS ServerName (SNI) = %q, want tls-sni.example.test", gotSNI)
	}
	if gotHost != fmt.Sprintf("tls-sni.example.test:%d", port) {
		t.Errorf("Host header = %q, want tls-sni.example.test:%d", gotHost, port)
	}
	if gotUA != "Mailcubby-LinkChecker/0.2.0-test" {
		t.Errorf("User-Agent = %q, want Mailcubby-LinkChecker/0.2.0-test", gotUA)
	}

	// Without RootCAs: InsecureSkipVerify MUST be false, yielding StatusTLSError.
	untrustedChk := New(nil, Config{
		AllowedPorts: []int{port},
		AllowCIDRs:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Resolver:     res,
	}, clock.NewFake(time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)), nil)
	untrustedRep := untrustedChk.Check(t.Context(), 1, "m-tls-bad", []string{target})
	if untrustedRep.Results[0].Status != StatusTLSError {
		t.Fatalf("untrusted TLS = %+v, want tls_error", untrustedRep.Results[0])
	}
}

// FR-120, ADR 0010: redirects capped at 5, cookies/Referer never forwarded,
// redirects to private/loopback IPs or disallowed ports are blocked.
func TestCheckerRedirectsAndPrivateRedirectBlocked(t *testing.T) {
	t.Parallel()

	var secretHits atomic.Int32
	secretSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secretHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(secretSrv.Close)
	secretPort := serverPort(t, secretSrv)

	var seenCookie, seenReferer string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/r1":
			w.Header().Set("Set-Cookie", "sid=should-not-persist; Path=/")
			http.Redirect(w, r, "/r2", http.StatusFound)
		case "/r2":
			http.Redirect(w, r, "/r3", http.StatusFound)
		case "/r3":
			http.Redirect(w, r, "/r4", http.StatusFound)
		case "/r4":
			http.Redirect(w, r, "/r5", http.StatusFound)
		case "/r5":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			seenCookie = r.Header.Get("Cookie")
			seenReferer = r.Header.Get("Referer")
			w.WriteHeader(http.StatusOK)
		case "/too-many-0":
			http.Redirect(w, r, "/r1", http.StatusFound) // 6 total redirects -> fails
		case "/to-imds":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		case "/to-secret-port":
			http.Redirect(w, r, fmt.Sprintf("http://127.0.0.1:%d/admin", secretPort), http.StatusFound)
		case "/to-rebind":
			http.Redirect(w, r, fmt.Sprintf("http://private-hop.example.test:%d/x", serverPort(t, srv)), http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	port := serverPort(t, srv)

	res := &fakeResolver{
		lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			if host == "private-hop.example.test" {
				return []netip.Addr{netip.MustParseAddr("10.0.0.5")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
	}

	chk := New(nil, Config{
		AllowedPorts: []int{port},
		AllowCIDRs:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Resolver:     res,
	}, clock.NewFake(time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)), nil)

	// Exactly 5 redirects succeeds.
	rep5 := chk.Check(t.Context(), 1, "m-r5", []string{fmt.Sprintf("http://ok.example.test:%d/r1", port)})
	r5 := rep5.Results[0]
	if r5.Status != StatusRedirect || r5.RedirectCount != 5 || r5.StatusCode == nil || *r5.StatusCode != http.StatusOK {
		t.Fatalf("5 redirects = %+v, want status=redirect count=5 code=200", r5)
	}
	if r5.FinalURL == nil || !strings.HasSuffix(*r5.FinalURL, "/final") {
		t.Errorf("FinalURL = %v, want .../final", r5.FinalURL)
	}
	if seenCookie != "" || seenReferer != "" {
		t.Errorf("redirect leaked headers: Cookie=%q Referer=%q", seenCookie, seenReferer)
	}

	// 6 redirects fails with too_many_redirects.
	rep6 := chk.Check(t.Context(), 1, "m-r6", []string{fmt.Sprintf("http://ok.example.test:%d/too-many-0", port)})
	if rep6.Results[0].Status != StatusTooManyRedirects {
		t.Fatalf("6 redirects = %+v, want too_many_redirects", rep6.Results[0])
	}

	// Redirect to IMDS, disallowed port, and private DNS hop are all blocked with nil FinalURL.
	for _, path := range []string{"/to-imds", "/to-secret-port", "/to-rebind"} {
		rep := chk.Check(t.Context(), 1, "m-blk-"+path, []string{fmt.Sprintf("http://ok.example.test:%d%s", port, path)})
		got := rep.Results[0]
		if got.Status != StatusBlocked {
			t.Errorf("redirect %s = %+v, want blocked", path, got)
		}
		if got.FinalURL != nil {
			t.Errorf("redirect %s leaked FinalURL = %q, want nil", path, *got.FinalURL)
		}
	}
	if secretHits.Load() != 0 {
		t.Fatalf("blocked redirect reached secret server (%d hits)", secretHits.Load())
	}
}

// FR-120, ADR 0010: HEAD fallback to GET with Range: bytes=0-0 on 405 and 501,
// plus bounded 1 KiB body discard, status codes, Proxy: nil, and MaxLinks limit.
func TestCheckerHeadFallbackLimitAndProxyNil(t *testing.T) {
	t.Parallel()

	var (
		mu            sync.Mutex
		methodsByPath = make(map[string][]string)
		rangeByPath   = make(map[string]string)
	)
	bigBody := bytes.Repeat([]byte("A"), 64*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methodsByPath[r.URL.Path] = append(methodsByPath[r.URL.Path], r.Method)
		if r.Method == http.MethodGet {
			rangeByPath[r.URL.Path] = r.Header.Get("Range")
		}
		mu.Unlock()

		switch r.URL.Path {
		case "/405":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("X"))
		case "/501":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotImplemented)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bigBody)
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/503":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	port := serverPort(t, srv)

	chk := New(nil, Config{
		AllowedPorts: []int{port},
		AllowCIDRs:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		MaxLinks:     4,
	}, clock.NewFake(time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)), nil)

	if chk.transport.Proxy != nil {
		t.Fatalf("transport.Proxy must be nil (ADR 0010)")
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	rep := chk.Check(t.Context(), 1, "m-fb", []string{
		base + "/405",
		base + "/501",
		base + "/404",
		base + "/503",
		base + "/skipped-5th",
	})

	if len(rep.Results) != 5 {
		t.Fatalf("len(Results) = %d, want 5", len(rep.Results))
	}
	if rep.Results[0].Status != StatusOK || *rep.Results[0].StatusCode != http.StatusPartialContent {
		t.Errorf("/405 fallback = %+v, want ok/206", rep.Results[0])
	}
	if rep.Results[1].Status != StatusOK || *rep.Results[1].StatusCode != http.StatusOK {
		t.Errorf("/501 fallback = %+v, want ok/200", rep.Results[1])
	}
	if rep.Results[2].Status != StatusClientError || *rep.Results[2].StatusCode != http.StatusNotFound {
		t.Errorf("/404 = %+v, want client_error/404", rep.Results[2])
	}
	if rep.Results[3].Status != StatusServerError || *rep.Results[3].StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/503 = %+v, want server_error/503", rep.Results[3])
	}
	if rep.Results[4].Status != StatusSkippedLimit || rep.Results[4].ErrorCode == nil || *rep.Results[4].ErrorCode != ErrCodeSkippedLimit {
		t.Errorf("5th link = %+v, want skipped_limit", rep.Results[4])
	}
	if rep.Summary.Total != 5 || rep.Summary.OK != 2 || rep.Summary.Broken != 2 || rep.Summary.Skipped != 1 {
		t.Errorf("unexpected summary: %+v", rep.Summary)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, p := range []string{"/405", "/501"} {
		if rangeByPath[p] != "bytes=0-0" {
			t.Errorf("fallback %s Range = %q, want bytes=0-0", p, rangeByPath[p])
		}
		if len(methodsByPath[p]) != 2 || methodsByPath[p][0] != http.MethodHead || methodsByPath[p][1] != http.MethodGet {
			t.Errorf("fallback %s methods = %v, want [HEAD GET]", p, methodsByPath[p])
		}
	}
}

// FR-120, ADR 0010: tenant-scoped LRU cache with 5-minute TTL via injected clock,
// bounded concurrency, and zero logging of checked URLs or raw *url.Error strings.
func TestCheckerCacheIsolationTTLConcurrencyAndLogPrivacy(t *testing.T) {
	t.Parallel()

	var (
		hits        atomic.Int32
		inFlight    atomic.Int32
		maxInFlight atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			cur := inFlight.Add(1)
			for {
				prev := maxInFlight.Load()
				if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			inFlight.Add(-1)
		}
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	port := serverPort(t, srv)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	clk := clock.NewFake(time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC))

	res := &fakeResolver{
		lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			if host == "nxdomain.example.test" {
				return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
			}
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
	}

	chk := New(nil, Config{
		AllowedPorts:          []int{port},
		AllowCIDRs:            []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		CacheSize:             2,
		CacheTTL:              5 * time.Minute,
		PerRequestConcurrency: 3,
		GlobalConcurrency:     4,
		Resolver:              res,
	}, clk, logger)

	secretURL := fmt.Sprintf("http://127.0.0.1:%d/magic?token=TopSecretResetToken999", port)
	dnsFailURL := fmt.Sprintf("http://nxdomain.example.test:%d/reset?secret=DNSSecretToken888", port)
	blockedURL := fmt.Sprintf("http://169.254.169.254:%d/meta?secret=IMDSSecretToken777", port)

	// 1. First check populates Tenant 1 cache.
	r1 := chk.Check(t.Context(), 1, "msg-a", []string{secretURL, dnsFailURL, blockedURL})
	if r1.Results[0].Cached || r1.Results[0].Status != StatusOK {
		t.Fatalf("first check = %+v, want uncached ok", r1.Results[0])
	}
	if r1.Results[1].Status != StatusDNSError {
		t.Fatalf("dns fail = %+v, want dns_error", r1.Results[1])
	}
	if r1.Results[2].Status != StatusBlocked {
		t.Fatalf("blocked = %+v, want blocked", r1.Results[2])
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}

	// 2. Second check within 5m for same {projectID, msgID} hits cache (no new HTTP request).
	clk.Advance(4 * time.Minute)
	r2 := chk.Check(t.Context(), 1, "msg-a", []string{secretURL})
	if !r2.Results[0].Cached || hits.Load() != 1 {
		t.Fatalf("expected cache hit: cached=%v hits=%d", r2.Results[0].Cached, hits.Load())
	}

	// 3. Different tenant with identical msgID + URL MUST NOT hit Tenant 1's cache.
	rTenant2 := chk.Check(t.Context(), 2, "msg-a", []string{secretURL})
	if rTenant2.Results[0].Cached || hits.Load() != 2 {
		t.Fatalf("cross-tenant cache hit detected: cached=%v hits=%d", rTenant2.Results[0].Cached, hits.Load())
	}

	// 4. Advance past 5m TTL -> Tenant 1 expires and re-fetches.
	clk.Advance(2 * time.Minute)
	rExpired := chk.Check(t.Context(), 1, "msg-a", []string{secretURL})
	if rExpired.Results[0].Cached || hits.Load() != 3 {
		t.Fatalf("expected expired cache miss: cached=%v hits=%d", rExpired.Results[0].Cached, hits.Load())
	}

	// 5. Concurrency stays <= PerRequestConcurrency (3) on 8 slow URLs.
	slowURLs := make([]string, 8)
	for i := range slowURLs {
		slowURLs[i] = fmt.Sprintf("http://127.0.0.1:%d/slow?i=%d", port, i)
	}
	repSlow := chk.Check(t.Context(), 3, "msg-conc", slowURLs)
	if repSlow.Summary.OK != 8 {
		t.Fatalf("slow summary = %+v, want 8 ok", repSlow.Summary)
	}
	if peak := maxInFlight.Load(); peak > 3 {
		t.Errorf("peak per-request concurrency = %d, want <= 3", peak)
	}

	// 6. Log privacy: ZERO checked URLs, tokens, or raw *url.Error strings in Debug logs.
	logs := logBuf.String()
	for _, forbidden := range []string{
		"TopSecretResetToken999",
		"DNSSecretToken888",
		"IMDSSecretToken777",
		"nxdomain.example.test",
		"169.254.169.254",
		"/magic",
	} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("SECURITY LEAK: %q appeared in linkcheck logs:\n%s", forbidden, logs)
		}
	}
}

func TestCheckerTimeoutClassification(t *testing.T) {
	t.Parallel()

	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-unblock
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(unblock)
		srv.Close()
	})
	port := serverPort(t, srv)

	chk := New(nil, Config{
		Timeout:      60 * time.Millisecond,
		AllowedPorts: []int{port},
		AllowCIDRs:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
	}, clock.NewReal(), nil)

	rep := chk.Check(t.Context(), 1, "m-to", []string{fmt.Sprintf("http://127.0.0.1:%d/hang", port)})
	got := rep.Results[0]
	if got.Status != StatusTimeout || got.ErrorCode == nil || *got.ErrorCode != ErrCodeTimeout {
		t.Fatalf("timeout check = %+v, want timeout", got)
	}
	if got.Error != nil && strings.Contains(*got.Error, "127.0.0.1") {
		t.Errorf("error message leaked URL/IP: %q", *got.Error)
	}

	// Ensure dns timeout also classifies as timeout, not generic network_error.
	dnsToRes := &fakeResolver{
		lookup: func(_ context.Context, _ string) ([]netip.Addr, error) {
			return nil, &net.DNSError{Err: "i/o timeout", IsTimeout: true}
		},
	}
	chk2 := New(nil, Config{
		AllowedPorts: []int{port},
		Resolver:     dnsToRes,
	}, clock.NewReal(), nil)
	rep2 := chk2.Check(t.Context(), 1, "m-dnsto", []string{fmt.Sprintf("http://slow-dns.example.test:%d/", port)})
	if rep2.Results[0].Status != StatusTimeout {
		t.Fatalf("dns timeout = %+v, want timeout", rep2.Results[0])
	}

	// Non-timeout generic dial error classifies as network_error without leaking raw error text.
	dialErrRes := &fakeResolver{
		lookup: func(_ context.Context, _ string) ([]netip.Addr, error) {
			return nil, errors.New("custom resolver boom https://secret.example/tok")
		},
	}
	chk3 := New(nil, Config{
		AllowedPorts: []int{port},
		Resolver:     dialErrRes,
	}, clock.NewReal(), nil)
	rep3 := chk3.Check(t.Context(), 1, "m-dnserr", []string{fmt.Sprintf("http://err.example.test:%d/", port)})
	if rep3.Results[0].Status != StatusDNSError || (rep3.Results[0].Error != nil && strings.Contains(*rep3.Results[0].Error, "secret")) {
		t.Fatalf("dns error = %+v, want sanitized dns_error", rep3.Results[0])
	}
}
