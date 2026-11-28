package linkcheck

import (
	"container/list"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/mailcubby/mailcubby/internal/clock"
)

const (
	// DefaultTimeout is the default per-URL check timeout (ADR 0010).
	DefaultTimeout = 5 * time.Second
	// MaxTimeout is the maximum allowed per-URL timeout (ADR 0010).
	MaxTimeout = 30 * time.Second
	// DefaultOverallTimeout caps an entire Check request (ADR 0010).
	DefaultOverallTimeout = 30 * time.Second
	// DefaultMaxLinks is the default cap on unique URLs checked per request.
	DefaultMaxLinks = 50
	// MaxAllowedLinks is the upper bound for MaxLinks.
	MaxAllowedLinks = 200
	// DefaultCacheSize is the LRU entry capacity in {projectID, msgID} keys (ADR 0010).
	DefaultCacheSize = 64
	// DefaultCacheTTL is the TTL for cached URL check results (ADR 0010).
	DefaultCacheTTL = 5 * time.Minute
	// DefaultPerRequestConcurrency caps concurrent checks within one Check call.
	DefaultPerRequestConcurrency = 5
	// DefaultGlobalConcurrency caps concurrent checks across all callers.
	DefaultGlobalConcurrency = 16
	// MaxRedirects is the maximum number of HTTP redirects followed per URL check.
	MaxRedirects = 5
	// maxDiscardBytes caps response body bytes read and discarded per HTTP response.
	maxDiscardBytes = 1024
	// maxResponseHeaderBytes bounds response header buffering per probe.
	maxResponseHeaderBytes = 64 << 10 // 64 KiB
)

type cacheKey struct {
	projectID int64
	msgID     string
}

type cachedURLResult struct {
	result    CheckResult
	checkedAt time.Time
}

type cacheEntry struct {
	key     cacheKey
	results map[string]cachedURLResult
}

// Checker performs offline link extraction and SSRF-hardened active HTTP link
// checks with bounded concurrency and a tenant-scoped TTL LRU cache (ADR 0010).
type Checker struct {
	blobs          BlobOpener
	clk            clock.Clock
	log            *slog.Logger
	userAgent      string
	timeout        time.Duration
	overallTimeout time.Duration
	maxLinks       int
	allowedPorts   map[int]struct{}
	allowCIDRs     []netip.Prefix
	perRequestConc int
	globalSem      *semaphore.Weighted
	transport      *http.Transport

	cacheSize int
	cacheTTL  time.Duration
	mu        sync.Mutex
	order     *list.List
	items     map[cacheKey]*list.Element
}

// New constructs a Checker configured according to cfg.
func New(blobs BlobOpener, cfg Config, clk clock.Clock, log *slog.Logger) *Checker {
	if clk == nil {
		clk = clock.NewReal()
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}

	overallTimeout := cfg.OverallTimeout
	if overallTimeout <= 0 {
		overallTimeout = DefaultOverallTimeout
	}

	maxLinks := cfg.MaxLinks
	if maxLinks <= 0 {
		maxLinks = DefaultMaxLinks
	}
	if maxLinks > MaxAllowedLinks {
		maxLinks = MaxAllowedLinks
	}

	allowedPorts := make(map[int]struct{})
	if len(cfg.AllowedPorts) == 0 {
		allowedPorts[80] = struct{}{}
		allowedPorts[443] = struct{}{}
	} else {
		for _, p := range cfg.AllowedPorts {
			if p >= 1 && p <= 65535 {
				allowedPorts[p] = struct{}{}
			}
		}
		if len(allowedPorts) == 0 {
			allowedPorts[80] = struct{}{}
			allowedPorts[443] = struct{}{}
		}
	}

	allowCIDRs := normalizeAllowCIDRs(cfg.AllowCIDRs)

	cacheSize := cfg.CacheSize
	if cacheSize == 0 {
		cacheSize = DefaultCacheSize
	} else if cacheSize < 0 {
		cacheSize = 0
	}

	cacheTTL := cfg.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = DefaultCacheTTL
	}

	perReqConc := cfg.PerRequestConcurrency
	if perReqConc <= 0 {
		perReqConc = DefaultPerRequestConcurrency
	}

	globalConc := cfg.GlobalConcurrency
	if globalConc <= 0 {
		globalConc = DefaultGlobalConcurrency
	}

	ver := cfg.Version
	if ver == "" {
		ver = "dev"
	}

	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: -1,
	}

	transport := &http.Transport{
		Proxy:                  nil, // ADR 0010: ignore HTTP_PROXY / HTTPS_PROXY
		DialContext:            newSafeDialContext(cfg.Resolver, dialer, allowedPorts, allowCIDRs),
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		TLSHandshakeTimeout:    timeout,
		ResponseHeaderTimeout:  timeout,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: false,
			RootCAs:            cfg.RootCAs,
		},
	}

	return &Checker{
		blobs:          blobs,
		clk:            clk,
		log:            log,
		userAgent:      "Mailcubby-LinkChecker/" + ver,
		timeout:        timeout,
		overallTimeout: overallTimeout,
		maxLinks:       maxLinks,
		allowedPorts:   allowedPorts,
		allowCIDRs:     allowCIDRs,
		perRequestConc: perReqConc,
		globalSem:      semaphore.NewWeighted(int64(globalConc)),
		transport:      transport,
		cacheSize:      cacheSize,
		cacheTTL:       cacheTTL,
		order:          list.New(),
		items:          make(map[cacheKey]*list.Element, cacheSize),
	}
}

// Extract extracts and deduplicates all links offline from a stored message blob.
func (c *Checker) Extract(ctx context.Context, blobPath string) (ExtractResult, error) {
	return ExtractFromBlob(ctx, c.blobs, blobPath)
}

// Check actively checks the supplied unique URLs (up to MaxLinks) under ADR 0010
// SSRF protections, concurrency limits, timeouts, and the tenant-scoped LRU cache.
// Callers MUST validate that urls belong to the target message before calling Check.
func (c *Checker) Check(ctx context.Context, projectID int64, msgID string, urls []string) CheckReport {
	start := c.clk.Now().UTC()

	unique := deduplicateOrder(urls)
	results := make([]CheckResult, len(unique))
	key := cacheKey{projectID: projectID, msgID: msgID}

	limit := min(len(unique), c.maxLinks)
	for i := limit; i < len(unique); i++ {
		code := ErrCodeSkippedLimit
		msg := "exceeded maximum links per check"
		results[i] = CheckResult{
			URL:       unique[i],
			Status:    StatusSkippedLimit,
			ErrorCode: &code,
			Error:     &msg,
		}
	}

	overallCtx, cancelOverall := context.WithTimeout(ctx, c.overallTimeout)
	defer cancelOverall()

	toFetch := make([]int, 0, limit)
	for i := range limit {
		if cached, ok := c.getCachedURL(key, unique[i], start); ok {
			cached.Cached = true
			results[i] = cached
			continue
		}
		toFetch = append(toFetch, i)
	}

	if len(toFetch) > 0 {
		cacheable := make([]bool, len(unique))
		var g errgroup.Group
		g.SetLimit(c.perRequestConc)

		for _, idx := range toFetch {
			targetURL := unique[idx]
			g.Go(func() error {
				res, okToCache := c.checkSingleBounded(overallCtx, targetURL)
				results[idx] = res
				cacheable[idx] = okToCache
				return nil
			})
		}
		_ = g.Wait()

		checkedAt := c.clk.Now().UTC()
		fresh := make(map[string]CheckResult, len(toFetch))
		for _, idx := range toFetch {
			res := results[idx]
			if cacheable[idx] && res.Status != StatusSkippedLimit {
				fresh[res.URL] = res
			}
		}
		if len(fresh) > 0 && ctx.Err() == nil {
			c.putCachedURLs(key, fresh, checkedAt)
		}
	}

	summary := summarizeResults(results)
	now := c.clk.Now().UTC()

	// ADR 0010: never log checked URLs or raw *url.Error strings.
	c.log.Info("link check completed",
		"project_id", projectID,
		"message_id", msgID,
		"total", summary.Total,
		"ok", summary.OK,
		"redirect", summary.Redirect,
		"broken", summary.Broken,
		"blocked", summary.Blocked,
		"error", summary.Error,
		"skipped", summary.Skipped,
		"duration_ms", now.Sub(start).Milliseconds(),
	)

	return CheckReport{
		CheckedAt: now,
		Summary:   summary,
		Results:   results,
	}
}

func (c *Checker) checkSingleBounded(overallCtx context.Context, rawURL string) (CheckResult, bool) {
	urlCtx, cancel := context.WithTimeout(overallCtx, c.timeout)
	defer cancel()

	// Pre-validate before waiting on the global semaphore so blocked URLs fail fast.
	if len(rawURL) > MaxURLBytes {
		return blockedResult(rawURL, ErrCodeInvalidURL, "URL exceeds maximum length", 0, 0), true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return blockedResult(rawURL, ErrCodeInvalidURL, "invalid URL", 0, 0), true
	}
	if err := validateTargetURL(parsed, c.allowedPorts, c.allowCIDRs); err != nil {
		return classifyErrorResult(rawURL, err, 0, 0), true
	}

	if err := c.globalSem.Acquire(urlCtx, 1); err != nil {
		// Never cache starvation/overall-timeout results where no probe was attempted.
		return classifyErrorResult(rawURL, err, 0, 0), false
	}
	defer c.globalSem.Release(1)

	res := c.checkOne(urlCtx, rawURL, parsed)
	return res, overallCtx.Err() == nil
}

func (c *Checker) checkOne(ctx context.Context, rawURL string, parsed *url.URL) CheckResult {
	started := c.clk.Now()
	redirects := 0

	client := &http.Client{
		Transport: c.transport,
		Jar:       nil, // ADR 0010: never send or persist cookies
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > MaxRedirects {
				return errTooManyRedirects
			}
			if err := validateTargetURL(req.URL, c.allowedPorts, c.allowCIDRs); err != nil {
				return err
			}
			redirects = len(via)
			req.Header.Del("Referer")
			req.Header.Set("User-Agent", c.userAgent)
			return nil
		},
	}

	statusCode, finalURL, err := c.doProbe(ctx, client, http.MethodHead, parsed.String(), false)
	if err == nil && (statusCode == http.StatusMethodNotAllowed || statusCode == http.StatusNotImplemented) {
		redirects = 0
		statusCode, finalURL, err = c.doProbe(ctx, client, http.MethodGet, parsed.String(), true)
	}

	durationMS := max(int64(0), c.clk.Now().Sub(started).Milliseconds())
	if err != nil {
		return classifyErrorResult(rawURL, err, redirects, durationMS)
	}

	code := statusCode
	var status Status
	switch {
	case code >= 200 && code <= 299:
		if redirects > 0 {
			status = StatusRedirect
		} else {
			status = StatusOK
		}
	case code >= 300 && code <= 399:
		status = StatusRedirect
	case code >= 400 && code <= 499:
		status = StatusClientError
	case code >= 500 && code <= 599:
		status = StatusServerError
	default:
		status = StatusNetworkError
	}

	var finalPtr *string
	if finalURL != "" {
		finalPtr = &finalURL
	}

	return CheckResult{
		URL:           rawURL,
		Status:        status,
		StatusCode:    &code,
		FinalURL:      finalPtr,
		RedirectCount: redirects,
		DurationMS:    durationMS,
		Cached:        false,
	}
}

func (c *Checker) doProbe(ctx context.Context, client *http.Client, method, targetURL string, withRange bool) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
	if err != nil {
		return 0, "", &policyError{code: ErrCodeInvalidURL, message: "invalid URL"}
	}
	req.Header.Set("User-Agent", c.userAgent)
	if withRange {
		req.Header.Set("Range", "bytes=0-0")
	}

	//nolint:gosec // G704: URL is pre-validated and dialed through ADR 0010 SSRF-hardened DialContext
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	if resp.Body != nil {
		_, _ = io.CopyN(io.Discard, resp.Body, maxDiscardBytes)
		_ = resp.Body.Close()
	}
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return resp.StatusCode, finalURL, nil
}

func blockedResult(rawURL, code, msg string, redirects int, durationMS int64) CheckResult {
	return CheckResult{
		URL:           rawURL,
		Status:        StatusBlocked,
		RedirectCount: redirects,
		DurationMS:    durationMS,
		ErrorCode:     &code,
		Error:         &msg,
	}
}

// classifyErrorResult maps any transport/policy error to a safe static status,
// error_code, and human message without leaking raw *url.Error strings (ADR 0010).
func classifyErrorResult(rawURL string, err error, redirects int, durationMS int64) CheckResult {
	var polErr *policyError
	if errors.As(err, &polErr) {
		return blockedResult(rawURL, polErr.code, polErr.message, redirects, durationMS)
	}
	if errors.Is(err, errTooManyRedirects) {
		code := ErrCodeTooManyRedirects
		msg := "too many redirects (max 5)"
		return CheckResult{
			URL:           rawURL,
			Status:        StatusTooManyRedirects,
			RedirectCount: redirects,
			DurationMS:    durationMS,
			ErrorCode:     &code,
			Error:         &msg,
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code := ErrCodeTimeout
		msg := "request timed out"
		return CheckResult{
			URL:           rawURL,
			Status:        StatusTimeout,
			RedirectCount: redirects,
			DurationMS:    durationMS,
			ErrorCode:     &code,
			Error:         &msg,
		}
	}

	var dnsWrap *dnsResolveError
	if errors.As(err, &dnsWrap) {
		var netErr net.Error
		if errors.As(dnsWrap.err, &netErr) && netErr.Timeout() {
			code := ErrCodeTimeout
			msg := "DNS lookup timed out"
			return CheckResult{
				URL:           rawURL,
				Status:        StatusTimeout,
				RedirectCount: redirects,
				DurationMS:    durationMS,
				ErrorCode:     &code,
				Error:         &msg,
			}
		}
		code := ErrCodeDNSError
		msg := "DNS lookup failed"
		return CheckResult{
			URL:           rawURL,
			Status:        StatusDNSError,
			RedirectCount: redirects,
			DurationMS:    durationMS,
			ErrorCode:     &code,
			Error:         &msg,
		}
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			code := ErrCodeTimeout
			msg := "DNS lookup timed out"
			return CheckResult{
				URL:           rawURL,
				Status:        StatusTimeout,
				RedirectCount: redirects,
				DurationMS:    durationMS,
				ErrorCode:     &code,
				Error:         &msg,
			}
		}
		code := ErrCodeDNSError
		msg := "DNS lookup failed"
		return CheckResult{
			URL:           rawURL,
			Status:        StatusDNSError,
			RedirectCount: redirects,
			DurationMS:    durationMS,
			ErrorCode:     &code,
			Error:         &msg,
		}
	}

	if isTLSError(err) {
		code := ErrCodeTLSError
		msg := "TLS handshake or certificate verification failed"
		return CheckResult{
			URL:           rawURL,
			Status:        StatusTLSError,
			RedirectCount: redirects,
			DurationMS:    durationMS,
			ErrorCode:     &code,
			Error:         &msg,
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		code := ErrCodeTimeout
		msg := "request timed out"
		return CheckResult{
			URL:           rawURL,
			Status:        StatusTimeout,
			RedirectCount: redirects,
			DurationMS:    durationMS,
			ErrorCode:     &code,
			Error:         &msg,
		}
	}

	code := ErrCodeNetworkError
	msg := "connection failed"
	return CheckResult{
		URL:           rawURL,
		Status:        StatusNetworkError,
		RedirectCount: redirects,
		DurationMS:    durationMS,
		ErrorCode:     &code,
		Error:         &msg,
	}
}

func isTLSError(err error) bool {
	var (
		unknownAuth x509.UnknownAuthorityError
		certInvalid x509.CertificateInvalidError
		hostErr     x509.HostnameError
		sysRoots    x509.SystemRootsError
		recHdr      tls.RecordHeaderError
		certVerify  *tls.CertificateVerificationError
		alertErr    tls.AlertError
	)
	return errors.As(err, &unknownAuth) ||
		errors.As(err, &certInvalid) ||
		errors.As(err, &hostErr) ||
		errors.As(err, &sysRoots) ||
		errors.As(err, &recHdr) ||
		errors.As(err, &certVerify) ||
		errors.As(err, &alertErr)
}

func summarizeResults(results []CheckResult) CheckSummary {
	s := CheckSummary{Total: len(results)}
	for _, r := range results {
		switch r.Status {
		case StatusOK:
			s.OK++
		case StatusRedirect:
			s.Redirect++
		case StatusClientError, StatusServerError:
			s.Broken++
		case StatusBlocked:
			s.Blocked++
		case StatusSkippedLimit:
			s.Skipped++
		default:
			s.Error++
		}
	}
	return s
}

func deduplicateOrder(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, u := range in {
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

func (c *Checker) getCachedURL(key cacheKey, targetURL string, now time.Time) (CheckResult, bool) {
	if c.cacheSize == 0 {
		return CheckResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return CheckResult{}, false
	}
	entry := elem.Value.(cacheEntry)
	rec, ok := entry.results[targetURL]
	if !ok {
		return CheckResult{}, false
	}
	if !now.Before(rec.checkedAt.Add(c.cacheTTL)) {
		delete(entry.results, targetURL)
		if len(entry.results) == 0 {
			delete(c.items, key)
			c.order.Remove(elem)
		}
		return CheckResult{}, false
	}
	c.order.MoveToFront(elem)
	return cloneCheckResult(rec.result), true
}

func (c *Checker) putCachedURLs(key cacheKey, fresh map[string]CheckResult, checkedAt time.Time) {
	if c.cacheSize == 0 || len(fresh) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		entry := elem.Value.(cacheEntry)
		// Prune expired URLs within this entry while updating.
		for u, rec := range entry.results {
			if !checkedAt.Before(rec.checkedAt.Add(c.cacheTTL)) {
				delete(entry.results, u)
			}
		}
		for u, res := range fresh {
			entry.results[u] = cachedURLResult{
				result:    cloneCheckResult(res),
				checkedAt: checkedAt,
			}
		}
		c.order.MoveToFront(elem)
		return
	}

	resMap := make(map[string]cachedURLResult, len(fresh))
	for u, res := range fresh {
		resMap[u] = cachedURLResult{
			result:    cloneCheckResult(res),
			checkedAt: checkedAt,
		}
	}
	elem := c.order.PushFront(cacheEntry{key: key, results: resMap})
	c.items[key] = elem
	for c.order.Len() > c.cacheSize {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(cacheEntry)
		delete(c.items, entry.key)
		c.order.Remove(oldest)
	}
}

func cloneCheckResult(in CheckResult) CheckResult {
	out := in
	if in.StatusCode != nil {
		v := *in.StatusCode
		out.StatusCode = &v
	}
	if in.FinalURL != nil {
		v := *in.FinalURL
		out.FinalURL = &v
	}
	if in.ErrorCode != nil {
		v := *in.ErrorCode
		out.ErrorCode = &v
	}
	if in.Error != nil {
		v := *in.Error
		out.Error = &v
	}
	return out
}
