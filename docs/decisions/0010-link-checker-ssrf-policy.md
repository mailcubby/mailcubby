# ADR 0010: Opt-in server link checker with DialContext SSRF protection

- Status: Accepted
- Date: 2026-11-27

## Context

Developers and agencies testing HTML emails want to inspect all hyperlinks in a captured message and verify that links are not broken (404/5xx/unreachable) before shipping campaigns or transactional templates (SPEC 14.2).

However, Mailcubby v0.1 makes zero outbound HTTP requests. Introducing server-initiated HTTP checks on URLs extracted from untrusted emails creates three distinct risks:

1. **Server-Side Request Forgery (SSRF) and DNS rebinding**: An attacker-controlled email could contain links targeting loopback (`127.0.0.1`, `::1`), private RFC 1918 networks, cloud instance metadata endpoints (`169.254.169.254`), IPv4-mapped IPv6 addresses (`::ffff:169.254.169.254`), or a domain that resolves to a public IP during pre-validation and switches to a private IP when connected.
2. **Side effects on single-use staging links**: Captured emails commonly contain password-reset links, magic login links, email verification links, and one-click unsubscribe URLs. Automatically probing links on ingest or on `GET` would consume or trigger those links unexpectedly.
3. **Unexpected outbound network profile in multi-tenant servers**: Operators deploying `MAILCUBBY_MODE=server` behind firewalls or on agency networks must consciously opt in before Mailcubby initiates outbound HTTP requests on behalf of tenant users.

## Decision

Split link inspection into two distinct operations with mode-appropriate defaults and defense-in-depth SSRF controls:

1. **Always-on offline extraction (`GET /api/v1/projects/{project}/messages/{id}/links`)**:
   - Extracts and deduplicates links from HTML (`<a href>`, `<area href>`), plain text, and `List-Unsubscribe` headers entirely offline from the stored blob.
   - Reports anchor text, sources, occurrence counts, scheme, `checkable` flag (`http`/`https` only), and static warnings (`javascript_url`, `data_url`, `credentials_in_url`, `http_insecure`, `missing_href`).
   - Permission: `PermViewerTech` (`viewer*`, hidden when `viewer_hide_technical=true`).

2. **Explicit user-triggered active checking (`POST /api/v1/projects/{project}/messages/{id}/links/check`)**:
   - **Never runs automatically** on ingest or on `GET`. Runs only on explicit `POST` (which enforces CSRF + Origin checks in server mode and `http.CrossOriginProtection` in local mode).
   - **Mode-dependent default**: Controlled by `MAILCUBBY_LINK_CHECK_ENABLED`, defaulting to **`true` in `local` mode** (zero-config developer ergonomics on a laptop, matching `MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH`) and **`false` in `server` mode** (explicit opt-in per instance for multi-tenant deployments). Exposed in `GET /api/v1/meta` as `features.link_check`. When disabled, `POST .../links/check` returns `403` (`code: "feature_disabled"`).
   - **Strict permission**: `PermMember` only (member, owner, admin, `write` API token). Viewers and `read` API tokens can never trigger outbound HTTP requests.
   - **Message-bound URL allowlist**: Optional request body `{ "urls": [...] }` allows checking a single link or subset (so users can skip single-use token links), and every supplied URL **must** exist in that message's extracted link set; arbitrary caller-supplied URLs return `400 bad_request`.

3. **SSRF and DNS-rebinding defenses (`internal/linkcheck`)**:
   - **URL pre-validation**: Only `http` and `https` schemes; reject embedded userinfo (`user:pass@host`), empty hosts, disallowed ports (configurable via `MAILCUBBY_LINK_CHECK_ALLOWED_PORTS`, default `80,443`), and obvious local names after stripping trailing dots (`localhost`, `*.localhost`, `*.local`, `*.internal`) unless resolved into an explicitly allowlisted CIDR.
   - **Custom `http.Transport.DialContext` (no TOCTOU window)**:
     - `Proxy: nil` (ignores `HTTP_PROXY`/`HTTPS_PROXY` environment variables).
     - Resolves the target hostname inside `DialContext` via `net.Resolver.LookupNetIP`.
     - Rejects any IP with a non-empty zone ID (`ip.Zone() != ""`) and unmaps every IP (`ip.Unmap()`) to defeat IPv4-mapped IPv6 bypasses.
     - Rejects the request if **any** resolved IP falls outside allowed ranges (unless matched by `MAILCUBBY_LINK_CHECK_ALLOW_CIDRS`, default empty, intended only for deliberate staging subnets). Blocked prefixes cover: unspecified (`0.0.0.0/8`, `::/128`), loopback (`127.0.0.0/8`, `::1/128`), RFC 1918 private (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), link-local/IMDS (`169.254.0.0/16`, `fe80::/10`), deprecated site-local (`fec0::/10`), IPv6 ULA (`fc00::/7`), CGNAT (`100.64.0.0/10`), multicast/broadcast/reserved/documentation/benchmark ranges, and IPv4-embedding transition/legacy prefixes (NAT64 `64:ff9b::/96`, `64:ff9b:1::/48`, 6to4 `2002::/16`, Teredo `2001::/32`, IPv4-compatible `::/96`, IPv4-translated `::ffff:0:0:0/96`).
     - Dials the validated literal IP directly (`net.JoinHostPort(ip.String(), port)`), letting `net/http` preserve the original hostname for `Host` header and TLS SNI (`InsecureSkipVerify: false`).
   - **Redirect handling**: At most 5 redirects (`CheckRedirect`); every redirect target re-runs URL pre-validation and passes through the same `DialContext`.
   - **Method and response bounds**: Sends `HEAD` with `User-Agent: Mailcubby-LinkChecker/<version>`, no cookies (`Jar: nil`), and no `Referer`. Falls back to `GET` with `Range: bytes=0-0` only on `405` or `501`, discarding at most 1 KiB of response body and never returning body bytes to callers.
   - **Resource & abuse limits**: Per-URL timeout `MAILCUBBY_LINK_CHECK_TIMEOUT` (default `5s`, max `30s`), overall request timeout `30s`, max unique URLs per check `MAILCUBBY_LINK_CHECK_MAX_LINKS` (default `50`, max `200`; remainder reported as `skipped_limit`), concurrency capped at 5 per request (`errgroup.SetLimit(5)`) and 16 globally (`semaphore.NewWeighted(16)`), tenant-scoped in-memory LRU cache (64 entries keyed by `{projectID, msgID}`, 5-minute TTL), and a dedicated server-mode rate limit (`10/m` per session/token).
   - **Log privacy**: Full checked URLs (and raw `*url.Error` strings, which embed the full URL including query tokens) are never written to structured logs; logs record only `project_id`, `message_id`, counts, classified error codes, and duration.

## Consequences

- Local-mode developers get working link checking out of the box while still protected against accidental probing of local/private services (and can still disable it with `MAILCUBBY_LINK_CHECK_ENABLED=false`).
- Multi-tenant server deployments keep zero outbound HTTP traffic by default until an administrator explicitly enables `MAILCUBBY_LINK_CHECK_ENABLED=true`.
- Users can inspect all extracted links safely offline and choose to check individual URLs or all links, with a clear UI caution about single-use token links.
- No new dependencies and no database schema changes are required.
- Residual risk (documented in `docs/ARCHITECTURE.md` section 10.4): when enabled, checking external links reveals the Mailcubby server's egress IP to target hosts and may trigger stateful URLs if a user checks single-use links; internal services exposed on public IPs behind IP allowlists remain reachable unless blocked by egress firewall rules.
