# Changelog

All notable changes to Mailcubby are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Mailcubby adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-11-28

Adds inline PDF preview, offline HTML/CSS email-client compatibility checks, and an SSRF-hardened link checker, plus online SQLite backups, admin CLI improvements, and security hardening. Ships with zero new Go/npm dependencies and zero SQLite schema migrations — existing `v0.1.0` data directories upgrade directly without migration steps.

### Added

#### Message Inspection
- **Inline PDF preview** (ADR 0009, #26): dedicated `GET /render/{project}/{id}/attachments/{attachment_id}` endpoint with coerced `application/pdf` + byte-0 `%PDF-` magic validation (`415` on forged or polyglot attachments), PDF-specific non-sandbox CSP, `MAILCUBBY_RENDER_INLINE_PDF` kill switch (default `true`), lazy-loaded `PdfPreviewDialog`, and split PDF cards in the attachment strip and Attachments tab (available to viewers even when `viewer_hide_technical` is enabled).
- **Offline HTML/CSS compatibility check** (FR-079, #27, #28): `internal/compat` analyzer powered by a compacted zstd-compressed `caniemail.com` MIT dataset (~19 KB, 18 curated email clients distinguishing Outlook Classic from Outlook.com / New Outlook), `GET /api/v1/projects/{project}/messages/{id}/html-check`, and lazy-loaded **HTML check** tab (shortcut `9`, `?tab=check`) with weighted compatibility breakdown, client family filters, "Only show issues" toggle, and expandable feature disclosures.
- **Link checker** (FR-120, ADR 0010, #29, #30): always-on offline link extraction (`GET /api/v1/projects/{project}/messages/{id}/links`) across HTML, genuine plain text, and `List-Unsubscribe` headers with static warnings (`javascript_url`, `data_url`, `credentials_in_url`, `http_insecure`, `missing_href`); opt-in SSRF-hardened active HTTP link checking (`POST /api/v1/projects/{project}/messages/{id}/links/check`, `member+`, `10/m` server-mode rate limit, custom `DialContext` blocking private/loopback/IMDS/NAT64/6to4/Teredo ranges and DNS rebinding); configuration variables `MAILCUBBY_LINK_CHECK_ENABLED` (`true` in local mode, `false` in server mode), `MAILCUBBY_LINK_CHECK_TIMEOUT`, `MAILCUBBY_LINK_CHECK_MAX_LINKS`, `MAILCUBBY_LINK_CHECK_ALLOWED_PORTS`, and `MAILCUBBY_LINK_CHECK_ALLOW_CIDRS`; and lazy-loaded **Links** tab (shortcut `8`, `?tab=links`) with per-row and bulk checks, single-use-token caution note, semantic status badges, safe external link handling, and `aria-live="polite"` announcements.

#### CLI, Operations & UX
- **Online SQLite backup** (`mailcubby backup --out <path|->`, #23): consistent WAL-safe backups via `VACUUM INTO` that work inside the distroless image without `sqlite3` or a shell.
- **Admin CLI & startup warnings** (#24): `mailcubby admin list-users` and `mailcubby admin enable-user` subcommands, plus startup warnings when `MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH=true` or `MAILCUBBY_BASE_URL` uses `http://`.
- **Server-mode systemd unit** (`deploy/systemd/mailcubby-server.service`, #20) and hardened Dockerfiles with a pinned Caddy image (#19).
- **Invite email mismatch warning** (#17): warns signed-in users on the invite accept page when the invite email differs from their signed-in account.

### Changed
- `mailcubby sendmail` enforces a 30 s dial/command timeout and static asset requests log quietly (#22).
- Documented password-reset personal API token scope, `/messages/wait` concurrency limits, and `MAILCUBBY_TRUSTED_PROXIES` narrowing (#21).
- Moved the SSE latency NFR-002 check from a flaky unit test to `BenchmarkSSELatencyNFR002` (#16).

### Security
- Coerce XML (`*/xml`, `*+xml`) and empty/malformed attachment `Content-Type` headers to `application/octet-stream` on attachment downloads (#18).

## [0.1.0] - 2026-11-26

First release of **Mailcubby**, a self-hosted, multi-tenant email testing server shipped as a single static Go binary with an embedded React UI.

This is an early 0.x release: until 1.0.0, configuration, the REST API and the data format may change between minor versions, and each such change will be listed here with its upgrade steps. Container images tagged `1.0.0-rc.1` came from a withdrawn release rehearsal; data directories created with them work with 0.1.0 unchanged.

### Added

#### Core SMTP & Storage
- **Dual-mode operation**: zero-config **local mode** (`MAILCUBBY_MODE=local`, drop-in replacement for Mailpit on `:1025` / `:8025` with automatic project creation from SMTP usernames) and authenticated multi-tenant **server mode** (`MAILCUBBY_MODE=server`).
- **SMTP listener** (`:1025`) with `PIPELINING`, `8BITMIME`, `ENHANCEDSTATUSCODES`, `CHUNKING`, `SMTPUTF8`, `SIZE`, `LIMITS RCPTMAX`, `AUTH PLAIN`/`LOGIN`, `STARTTLS`, plus optional **implicit TLS** (`MAILCUBBY_SMTPS_ADDR`, e.g. `:1465`) with live 30-second certificate reloading (`internal/tlsutil.Reloader`).
- **Routing rules**: per-credential routing in server mode, auto-creation by SMTP username in local mode, and optional recipient routing (`MAILCUBBY_INBOX_DOMAIN`) with subdomain (`*@slug.domain`) and local-part (`slug+tag@domain`) forms and multi-project delivery.
- **Durable storage**: pure-Go SQLite (`modernc.org/sqlite`) in WAL mode with embedded `goose` migrations and `AUTOINCREMENT` project/message IDs, paired with zstd-compressed `.eml` blobs on disk (unmodified raw RFC 5322 bytes, pooled encoders/decoders).
- **MIME parsing**: resilient `enmime` v2 parsing (never rejects malformed MIME; records `parse_warnings`), automatic HTML-to-text fallback marked `text_generated`, and automatic tagging from `X-Tags` headers and plus-addressing (capped at 50 auto-tags per message).
- **Full-text search**: SQLite FTS5 (`unicode61 remove_diacritics 2`, `prefix='2 3'`) with query operators (`from:`, `to:`, `subject:`, `has:attachment|html|text`, `is:read|unread`, `tag:`, `before:`, `after:`, `credential:`, quoted phrases, `-` negation) and fuzz-tested parameterised SQL generation.

#### Multi-Tenancy, Auth & Security
- **First-run setup wizard** (5 steps: setup token, admin account, instance checks, first project, live SSE test delivery) and headless bootstrap via `MAILCUBBY_ADMIN_EMAIL` / `MAILCUBBY_ADMIN_PASSWORD`.
- **Users, sessions & invites**: Argon2id password hashing (`m=19456, t=2, p=1`, dummy-hash constant-time login path), `HttpOnly`/`SameSite=Lax`/`Secure` server-side sessions with idle and absolute expiry, CSRF + Origin verification, single-use project and instance invite links, and admin-generated password reset links.
- **Role-based access control**: per-project `owner`, `member`, and `viewer` roles with strict tenant isolation (`404` on inaccessible projects), last-owner and last-admin protections, and optional `viewer_hide_technical` and `members_can_reveal_smtp` controls.
- **Per-project SMTP credentials**: up to 20 credentials per project with AES-256-GCM encryption (HKDF-SHA256 subkey derived from `MAILCUBBY_APP_KEY`, username AAD), reveal (audited), rotate, disable, and delete.
- **Personal API tokens**: `mailcubby_` bearer tokens with `read`/`write` scopes and optional project/expiry restrictions.
- **Sandboxed HTML preview**: dedicated `/render/{project}/{id}` endpoint with strict CSP (`sandbox allow-popups allow-popups-to-escape-sandbox; default-src 'none'`), recursive HTML/raw-text/foreign-element rewriting (`<base>`, `<meta http-equiv="refresh">`, prefetch `<link>`, `cid:` inlining), opt-in remote image loading, and hostile-email corpus verification.
- **Hardening & audit**: sliding-window and token-bucket rate limiting (SMTP AUTH lockout with `454 4.7.0`, HTTP login `429`, invite/reset `20/15m`, API rate limits), active-content attachment coercion, secret redaction in logs, and full FR-105 audit log (admin and project-owner views).

#### REST API, Realtime & Operations
- **OpenAPI 3.1 REST API** (`/api/v1/*`, `/api/v1/openapi.yaml`) with cursor pagination, bulk operations, `POST /send-test`, and `GET /messages/wait` for automated E2E tests.
- **Server-Sent Events** (`/api/v1/events`) with live membership filtering, deleted-project isolation, heartbeats, connection caps, and exponential-backoff client recovery.
- **Retention & background workers**: per-project and instance-default retention sweeper (age, message count, raw bytes), instance storage quota (`452 4.3.1`), per-project SMTP rate limit (`450 4.7.1`), hourly orphan blob GC + incremental vacuum, and daily housekeeping.
- **CLI subcommands**: `serve`, `version`, `keygen`, `healthcheck`, `migrate status`, `admin create-user`, `admin reset-password`, and `sendmail` (drop-in PHP `sendmail_path` replacement with STARTTLS support).
- **Packaging**: multi-arch distroless Docker images (`linux/amd64`, `linux/arm64`), GoReleaser v2 release workflow for Linux/macOS/Windows binaries, and production Docker Compose + Caddy stack.

#### Web UI
- **Keyboard-first React 19 UI**: light/dark/system themes, three-pane resizable inbox with virtualised message list, 7 message inspection tabs (Preview with device/rotation/zoom/dark-mode/remote-images, Text, lazy Shiki HTML source, Headers, virtualised Raw, Attachments, Tech), command palette (`Cmd/Ctrl+K`), keyboard shortcuts with help overlay (`?`), optimistic single-message delete with undo, "Preview as viewer" mode, framework integration snippets (Laravel, Symfony, Nodemailer, WordPress WP Mail SMTP, Django, Rails, swaks, PHP `sendmail_path`), and WCAG 2.1 AA accessibility verified via `@axe-core/playwright`.
- **Visual pass "Studio"** (#6 to #10, follow-ups in #12): new design tokens with a deep ink-green chrome layer for the header and sidebar in both themes, semantic `success`/`warning`/`info`/`destructive` colours and a teal-cyan accent, all token pairs checked for WCAG AA in a generated contrast table (`docs/ui-contrast.md`); role badges with icons and status badges with a leading dot; restyled message list, reading pane, attachment strip, empty, error and not-found states; settings, members, invites (pending by default), SMTP credentials and admin screens on shared card and table components; a "Skip to message list" link; theme choice in the account menu; users with exactly one project who are neither admins nor allowed to create projects land in its inbox after sign-in; axe checks on every screen in light and dark.
- **Credits** (#14): public pages (sign-in, invite, reset, setup) show "Made by Fábio Ferreira at NOOP", and Account > Preferences gains an "About Mailcubby" card with the version, the credit and the licence link.

[0.2.0]: https://github.com/mailcubby/mailcubby/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/mailcubby/mailcubby/releases/tag/v0.1.0
