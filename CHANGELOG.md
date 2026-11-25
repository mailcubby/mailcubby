# Changelog

All notable changes to Mailcubby are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Mailcubby adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-11-25

Initial production release of **Mailcubby**, a self-hosted, multi-tenant email testing server shipped as a single static Go binary with an embedded React UI.

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
