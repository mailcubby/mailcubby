package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/linkcheck"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func testServerPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi: %v", err)
	}
	return p
}

// FR-120, ADR 0010: GET /api/v1/meta exposes features.link_check with mode-dependent
// defaults (true in local mode, false in server mode) and respects overrides.
func TestMetaExposesLinkCheckFeatureFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "local default true", env: nil, want: true},
		{name: "local override false", env: map[string]string{"MAILCUBBY_LINK_CHECK_ENABLED": "false"}, want: false},
		{name: "server default false", env: map[string]string{
			"MAILCUBBY_MODE":                     "server",
			"MAILCUBBY_BASE_URL":                 testBaseURL,
			"MAILCUBBY_APP_KEY":                  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
		}, want: false},
		{name: "server override true", env: map[string]string{
			"MAILCUBBY_MODE":                     "server",
			"MAILCUBBY_BASE_URL":                 testBaseURL,
			"MAILCUBBY_APP_KEY":                  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
			"MAILCUBBY_LINK_CHECK_ENABLED":       "true",
		}, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			envMap := make(map[string]string, len(tc.env)+1)
			for k, v := range tc.env {
				envMap[k] = v
			}
			envMap["MAILCUBBY_DATA_DIR"] = t.TempDir()
			cfg, err := config.Load(func(k string) (string, bool) {
				v, ok := envMap[k]
				return v, ok
			})
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			st := testutil.NewStore(t)
			blobs, err := blob.NewStore(cfg.DataDir, cfg.ZstdLevel)
			if err != nil {
				t.Fatalf("blob.NewStore: %v", err)
			}
			srv := api.New(cfg, st, blobs, nil, api.Options{Version: "test"})
			t.Cleanup(srv.Close)

			rec := doReq(t, srv.Handler(), http.MethodGet, "/api/v1/meta", nil, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /meta = %d, want 200", rec.Code)
			}
			var meta struct {
				Features struct {
					LinkCheck bool `json:"link_check"`
				} `json:"features"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
				t.Fatalf("unmarshal meta: %v", err)
			}
			if meta.Features.LinkCheck != tc.want {
				t.Errorf("meta.features.link_check = %v, want %v", meta.Features.LinkCheck, tc.want)
			}
		})
	}
}

// FR-120, ADR 0010: offline link extraction (GET .../links) and active checking
// (POST .../links/check) including allowlist, SSRF blocking, subset checking,
// arbitrary URL rejection (400), and feature_disabled (403).
func TestMessageLinksAndCheckEndpoints(t *testing.T) {
	t.Parallel()

	var okHits atomic.Int32
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			okHits.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/head-405":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("x"))
		case "/broken":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(targetSrv.Close)
	port := testServerPort(t, targetSrv)

	fixed := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixed)
	st := testutil.NewStore(t)
	dataDir := t.TempDir()
	blobs, err := blob.NewStore(dataDir, 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	bus := events.NewBus()
	t.Cleanup(bus.Close)

	proj, err := st.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "links-local",
		Name:     "Links Local",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      fixed,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	okURL := fmt.Sprintf("http://127.0.0.1:%d/ok", port)
	fallbackURL := fmt.Sprintf("http://127.0.0.1:%d/head-405", port)
	brokenURL := fmt.Sprintf("http://127.0.0.1:%d/broken", port)
	imdsURL := "http://169.254.169.254/latest/meta-data/"

	rawEML := fmt.Sprintf(
		"From: sender@example.test\r\n"+
			"To: rcpt@example.test\r\n"+
			"Subject: Link Test\r\n"+
			"List-Unsubscribe: <%s>, <mailto:unsub@example.test>\r\n"+
			"MIME-Version: 1.0\r\n"+
			"Content-Type: multipart/alternative; boundary=\"b\"\r\n\r\n"+
			"--b\r\n"+
			"Content-Type: text/plain; charset=utf-8\r\n\r\n"+
			"Plain link: %s\r\n"+
			"--b\r\n"+
			"Content-Type: text/html; charset=utf-8\r\n\r\n"+
			"<html><body>"+
			"<a href=\"%s\">OK Link</a>"+
			"<a href=\"%s\">Broken Link</a>"+
			"<a href=\"%s\">IMDS Link</a>"+
			"<a href=\"javascript:alert(1)\">JS</a>"+
			"</body></html>\r\n"+
			"--b--\r\n",
		fallbackURL, okURL, okURL, brokenURL, imdsURL,
	)

	ing := ingest.New(st, blobs, clk, bus, nil)
	refs, err := ing.Ingest(t.Context(), ingest.Envelope{
		MailFrom: "sender@example.test",
		RcptTo:   []string{"rcpt@example.test"},
	}, []ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug}}, bytes.NewReader([]byte(rawEML)))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	msgID := refs[0].ID.String()

	cfg, err := config.Load(func(k string) (string, bool) {
		m := map[string]string{
			"MAILCUBBY_DATA_DIR":                 dataDir,
			"MAILCUBBY_LINK_CHECK_ALLOWED_PORTS": fmt.Sprintf("80,443,%d", port),
			"MAILCUBBY_LINK_CHECK_ALLOW_CIDRS":   "127.0.0.1/32",
		}
		v, ok := m[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	srv := api.New(cfg, st, blobs, bus, api.Options{Clock: clk, Ingestor: ing})
	t.Cleanup(srv.Close)
	h := srv.Handler()

	linksPath := "/api/v1/projects/links-local/messages/" + msgID + "/links"
	checkPath := linksPath + "/check"

	// 1. Offline GET .../links extracts HTML + text + List-Unsubscribe without making any HTTP request.
	rec := doReq(t, h, http.MethodGet, linksPath, nil, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /links status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Content-Security-Policy", wantAppCSP)
	assertHeader(t, rec.Header(), "Cache-Control", "private, no-store")
	if okHits.Load() != 0 {
		t.Fatalf("GET /links must be strictly offline, got %d hits", okHits.Load())
	}

	var extracted linkcheck.ExtractResult
	if err := json.Unmarshal(rec.Body.Bytes(), &extracted); err != nil {
		t.Fatalf("unmarshal links: %v", err)
	}
	if extracted.Total != 6 || extracted.CheckableCount != 4 {
		t.Fatalf("extracted totals = (total=%d, checkable=%d), want (6, 4); links=%+v", extracted.Total, extracted.CheckableCount, extracted.Links)
	}

	// 2. Arbitrary URL not in the message -> 400 bad_request.
	badRec := doReq(t, h, http.MethodPost, checkPath, map[string]any{
		"urls": []string{"https://attacker.example/ssrf"},
	}, nil, nil)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("arbitrary URL status = %d, want 400; body=%s", badRec.Code, badRec.Body.String())
	}

	// Empty urls array -> 400 bad_request.
	emptyRec := doReq(t, h, http.MethodPost, checkPath, map[string]any{
		"urls": []string{},
	}, nil, nil)
	if emptyRec.Code != http.StatusBadRequest {
		t.Fatalf("empty urls status = %d, want 400; body=%s", emptyRec.Code, emptyRec.Body.String())
	}

	// 3. Subset check (single URL) checks only that URL.
	subRec := doReq(t, h, http.MethodPost, checkPath, map[string]any{
		"urls": []string{okURL},
	}, nil, nil)
	if subRec.Code != http.StatusOK {
		t.Fatalf("subset check status = %d, want 200; body=%s", subRec.Code, subRec.Body.String())
	}
	var subResp struct {
		CheckedAt string                  `json:"checked_at"`
		Summary   linkcheck.CheckSummary  `json:"summary"`
		Results   []linkcheck.CheckResult `json:"results"`
	}
	if err := json.Unmarshal(subRec.Body.Bytes(), &subResp); err != nil {
		t.Fatalf("unmarshal subset response: %v", err)
	}
	if len(subResp.Results) != 1 || subResp.Results[0].Status != linkcheck.StatusOK || subResp.Results[0].Cached {
		t.Fatalf("subset result = %+v, want 1 uncached ok", subResp.Results)
	}
	if !strings.HasSuffix(subResp.CheckedAt, "Z") || !strings.Contains(subResp.CheckedAt, ".") {
		t.Errorf("checked_at = %q, want RFC 3339 UTC with ms", subResp.CheckedAt)
	}

	// 4. Full check (empty body): okURL is served from cache, fallbackURL uses HEAD->GET, brokenURL is 404, imdsURL is blocked!
	fullRec := doReq(t, h, http.MethodPost, checkPath, nil, nil, nil)
	if fullRec.Code != http.StatusOK {
		t.Fatalf("full check status = %d, want 200; body=%s", fullRec.Code, fullRec.Body.String())
	}
	var fullResp struct {
		Summary linkcheck.CheckSummary  `json:"summary"`
		Results []linkcheck.CheckResult `json:"results"`
	}
	if err := json.Unmarshal(fullRec.Body.Bytes(), &fullResp); err != nil {
		t.Fatalf("unmarshal full response: %v", err)
	}
	if fullResp.Summary.Total != 4 || fullResp.Summary.OK != 2 || fullResp.Summary.Broken != 1 || fullResp.Summary.Blocked != 1 {
		t.Fatalf("unexpected full summary: %+v; results=%+v", fullResp.Summary, fullResp.Results)
	}
	byURL := make(map[string]linkcheck.CheckResult, len(fullResp.Results))
	for _, r := range fullResp.Results {
		byURL[r.URL] = r
	}
	if !byURL[okURL].Cached {
		t.Errorf("okURL should be cached on second call")
	}
	if byURL[fallbackURL].Status != linkcheck.StatusOK || *byURL[fallbackURL].StatusCode != http.StatusPartialContent {
		t.Errorf("fallbackURL = %+v, want ok/206", byURL[fallbackURL])
	}
	if byURL[brokenURL].Status != linkcheck.StatusClientError || *byURL[brokenURL].StatusCode != http.StatusNotFound {
		t.Errorf("brokenURL = %+v, want client_error/404", byURL[brokenURL])
	}
	if byURL[imdsURL].Status != linkcheck.StatusBlocked {
		t.Errorf("imdsURL = %+v, want blocked", byURL[imdsURL])
	}

	// 5. Disabled config returns 403 feature_disabled on POST .../links/check while GET .../links stays 200.
	disabledCfg := cfg
	disabledCfg.LinkCheckEnabled = false
	disabledSrv := api.New(disabledCfg, st, blobs, bus, api.Options{Clock: clk, Ingestor: ing})
	t.Cleanup(disabledSrv.Close)

	if rec := doReq(t, disabledSrv.Handler(), http.MethodGet, linksPath, nil, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /links when disabled = %d, want 200", rec.Code)
	}
	disRec := doReq(t, disabledSrv.Handler(), http.MethodPost, checkPath, nil, nil, nil)
	if disRec.Code != http.StatusForbidden {
		t.Fatalf("POST /links/check when disabled = %d, want 403; body=%s", disRec.Code, disRec.Body.String())
	}
	if !strings.Contains(disRec.Body.String(), `"code":"feature_disabled"`) {
		t.Errorf("disabled error body = %s, want code=feature_disabled", disRec.Body.String())
	}
}

// FR-120, SPEC 12.4, ADR 0010: server mode defaults to link_check disabled
// (403 feature_disabled), restricts POST .../links/check to PermMember (viewer
// and read token get 403 forbidden even when enabled), and enforces 10/m per
// session/token rate limit (429 with Retry-After) while local mode is exempt.
func TestServerModeLinkCheckPermissionsDisabledAndRateLimit(t *testing.T) {
	t.Parallel()

	const pass = "correct-horse-battery-staple"

	t.Run("server mode default disabled returns 403 feature_disabled for member", func(t *testing.T) {
		t.Parallel()
		env := newServerModeEnv(t)
		h := env.srv.Handler()
		owner := env.createUser(t, "lc-dis-owner@example.test", "Owner", pass, false, true, false)
		proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
			Slug: "lc-dis", Name: "LC Dis", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if _, err := env.store.AddProjectMember(t.Context(), proj.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember: %v", err)
		}
		cookie, csrf := loginAndGetCSRF(t, h, owner.Email, pass)
		sendRec := doReq(t, h, http.MethodPost, "/api/v1/projects/lc-dis/send-test", nil, cookie, csrfHeaders(csrf))
		var sent struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(sendRec.Body.Bytes(), &sent)

		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/lc-dis/messages/"+sent.ID+"/links/check", nil, cookie, csrfHeaders(csrf))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"feature_disabled"`) {
			t.Fatalf("default server POST /links/check = (%d, %s), want (403, feature_disabled)", rec.Code, rec.Body.String())
		}
	})

	t.Run("server mode enabled enforces 10/m rate limit per session and token, and blocks viewer/read-token", func(t *testing.T) {
		t.Parallel()
		env := newServerModeEnvWithOverrides(t, map[string]string{
			"MAILCUBBY_LINK_CHECK_ENABLED": "true",
		})
		h := env.srv.Handler()

		owner := env.createUser(t, "lc-rl-owner@example.test", "Owner", pass, false, true, false)
		viewer := env.createUser(t, "lc-rl-viewer@example.test", "Viewer", pass, false, false, false)

		proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
			Slug: "lc-rl", Name: "LC RL", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if _, err := env.store.AddProjectMember(t.Context(), proj.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember(owner): %v", err)
		}
		if _, err := env.store.AddProjectMember(t.Context(), proj.ID, viewer.ID, "viewer", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember(viewer): %v", err)
		}

		// Ingest a message with zero links so active check returns 200 without outbound network.
		refs, err := env.ing.Ingest(t.Context(), ingest.Envelope{
			MailFrom: "a@example.test", RcptTo: []string{"b@example.test"},
		}, []ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug}}, strings.NewReader(
			"From: a@example.test\r\nTo: b@example.test\r\nSubject: RL\r\n\r\nNo links here.\r\n",
		))
		if err != nil {
			t.Fatalf("Ingest: %v", err)
		}
		checkPath := "/api/v1/projects/lc-rl/messages/" + refs[0].ID.String() + "/links/check"

		ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, owner.Email, pass)
		viewerCookie, viewerCSRF := loginAndGetCSRF(t, h, viewer.Email, pass)

		// Create read and write API tokens owned by owner.
		makeTok := func(scope string) string {
			t.Helper()
			r := doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
				"name": "lc-" + scope, "scope": scope,
			}, ownerCookie, csrfHeaders(ownerCSRF))
			if r.Code != http.StatusCreated {
				t.Fatalf("create %s token = %d", scope, r.Code)
			}
			var out struct {
				Token string `json:"token"`
			}
			_ = json.Unmarshal(r.Body.Bytes(), &out)
			return out.Token
		}
		readTok := makeTok("read")
		writeTok := makeTok("write")

		// Viewer and read token are rejected with 403 forbidden.
		if rec := doReq(t, h, http.MethodPost, checkPath, nil, viewerCookie, csrfHeaders(viewerCSRF)); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
			t.Fatalf("viewer POST /links/check = (%d, %s), want (403, forbidden)", rec.Code, rec.Body.String())
		}
		if rec := doReq(t, h, http.MethodPost, checkPath, nil, nil, map[string]string{"Authorization": "Bearer " + readTok}); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
			t.Fatalf("read token POST /links/check = (%d, %s), want (403, forbidden)", rec.Code, rec.Body.String())
		}

		// Session gets 10 requests per minute; 11th returns 429 with Retry-After.
		for i := range 10 {
			rec := doReq(t, h, http.MethodPost, checkPath, nil, ownerCookie, csrfHeaders(ownerCSRF))
			if rec.Code != http.StatusOK {
				t.Fatalf("session link check #%d = %d, want 200; body=%s", i+1, rec.Code, rec.Body.String())
			}
		}
		rec11 := doReq(t, h, http.MethodPost, checkPath, nil, ownerCookie, csrfHeaders(ownerCSRF))
		if rec11.Code != http.StatusTooManyRequests {
			t.Fatalf("11th session link check = %d, want 429; body=%s", rec11.Code, rec11.Body.String())
		}
		if ra := rec11.Header().Get("Retry-After"); ra == "" {
			t.Fatalf("missing Retry-After header on 429")
		}

		// Write API token has its own independent 10/m bucket.
		writeHdr := map[string]string{"Authorization": "Bearer " + writeTok}
		for i := range 10 {
			rec := doReq(t, h, http.MethodPost, checkPath, nil, nil, writeHdr)
			if rec.Code != http.StatusOK {
				t.Fatalf("write token link check #%d = %d, want 200", i+1, rec.Code)
			}
		}
		recTok11 := doReq(t, h, http.MethodPost, checkPath, nil, nil, writeHdr)
		if recTok11.Code != http.StatusTooManyRequests {
			t.Fatalf("11th write token link check = %d, want 429", recTok11.Code)
		}

		// Advancing fake clock by 1 minute restores the bucket.
		env.clk.Advance(time.Minute + time.Second)
		if rec := doReq(t, h, http.MethodPost, checkPath, nil, ownerCookie, csrfHeaders(ownerCSRF)); rec.Code != http.StatusOK {
			t.Fatalf("post-window session link check = %d, want 200", rec.Code)
		}
	})
}
