# Mailcubby: Architecture

This document describes how Mailcubby is built. Requirements live in `docs/SPEC.md` (referenced as SPEC x.y / FR-xxx). Pinned dependency versions live in `AGENTS.md`.

## 1. Principles

1. **One process, one binary.** `CGO_ENABLED=0` static Go binary containing SMTP server, HTTP server, workers and the compiled web UI (`go:embed`). No external services.
2. **SQLite is the database; files are the blobs.** Metadata and search in SQLite (WAL, FTS5); raw messages as zstd files on disk.
3. **Stdlib first.** `net/http` routing (Go 1.22+ patterns), `log/slog`, `crypto/*`, `database/sql`. Third-party libraries only where they remove real risk or effort (SMTP protocol, MIME parsing, SQLite driver, zstd, argon2).
4. **Tenant isolation by construction.** Every tenant-scoped store method takes a `projectID`. Authorisation is resolved once per request in middleware.
5. **Explicit dependencies, no globals.** Components receive their dependencies through constructors; `internal/app` wires them.
6. **Boring and observable.** Structured logs with request/session IDs, health endpoints, clear startup errors.

## 2. Overview diagram

```
                     SMTP clients (apps, swaks, sendmail)
                                  |
                       :1025 (STARTTLS) / :1465 (TLS)
                                  |
                             [conn limits]
                                  v
+--------------------------------------------------------------------------+
| mailcubby (single process)                                                |
|                                                                          |
|  internal/smtpd  ---- session: AUTH, MAIL, RCPT, DATA ----+              |
|   (emersion/go-smtp, go-sasl)    | routing via store      |              |
|                                  v                        v              |
|                          internal/ingest  ---------> internal/blob       |
|                          (enmime v2 parse,          (zstd files under    |
|                           tags, FTS text)            /data/blobs)        |
|                                  |                                        |
|                                  v                                        |
|                          internal/store  <------ internal/search          |
|                          (modernc sqlite,        (query parser -> SQL)    |
|                           sqlc, goose)                                    |
|                             ^       |                                     |
|                             |       v                                     |
|  internal/retention ---------+   internal/events (in-process bus)         |
|  (sweeper, orphan GC,                 |                                   |
|   vacuum, housekeeping)               v                                   |
|                          internal/api (net/http)  -- SSE /api/v1/events   |
|                          internal/auth            -- REST /api/v1/...     |
|                          internal/render          -- /render/{p}/{id}     |
|                          internal/web (embed SPA) -- /, /assets/...       |
+--------------------------------------------------------------------------+
                                  ^
                          :8025 HTTP (behind Caddy / Traefik / nginx)
                                  |
                              Browsers, API clients, CI tests
```

## 3. Components

### 3.1 `cmd/mailcubby` and `internal/app`

- `cmd/mailcubby/main.go` parses the subcommand (SPEC FR-115) with the stdlib `flag` package (one `FlagSet` per subcommand) and calls into `internal/app` or `internal/cli`.
- `internal/app.Run(ctx, cfg)` opens the store, runs migrations, builds every component, and runs them under `errgroup.Group` (`golang.org/x/sync/errgroup`). `signal.NotifyContext` handles SIGINT/SIGTERM.
- Graceful shutdown order: stop accepting SMTP and HTTP connections; wait up to 30 s for in-flight SMTP DATA and HTTP requests (SSE streams are closed immediately); stop workers; close event bus; close DB (checkpoint WAL).

### 3.2 `internal/config`

- `config.Load(env func(string) (string, bool)) (Config, error)`: reads `MAILCUBBY_*` variables (SPEC section 9), applies mode-dependent defaults, supports `_FILE` for secrets, parses durations with `d`, sizes, rates. Returns all validation errors joined (`errors.Join`), each naming the variable.
- No package-level state; `Config` is a plain struct passed down. Tests call `Load` with a map-backed lookup.

### 3.3 `internal/smtpd`

- Wraps `github.com/emersion/go-smtp`. `Backend.NewSession(c *smtp.Conn)` returns a `session` struct holding: remote IP, TLS state, resolved auth (credential or local username), accepted recipients with resolved project IDs and plus tags.
- Implements `smtp.AuthSession`: `AuthMechanisms()` returns `PLAIN` and `LOGIN`; `Auth(mech)` returns `smtp.ErrAuthUnknownMechanism` (504) for anything else. go-smtp itself hides AUTH and replies `523 5.7.10` when the connection is not TLS and `Server.AllowInsecureAuth` is false, so no extra check is needed. PLAIN uses `go-sasl`'s `NewPlainServer`. `go-sasl` (pinned pseudo-version) has no LOGIN server, so `internal/smtpd/saslogin.go` implements `sasl.Server` (about 50 lines: optional initial response as username, challenges `Username:` / `Password:`, which go-smtp base64-encodes). Authenticator callbacks return `smtp.ErrAuthFailed` (`535 5.7.8`) or a `454 4.7.0` `*smtp.SMTPError` for lockout; go-smtp writes a bare `*smtp.SMTPError` as is and anything else as `454`.
- **Error contract**: go-smtp maps session errors with a type assertion (`err.(*smtp.SMTPError)`), not `errors.As`. Every `Session` method (`Mail`, `Rcpt`, `Data`) and SASL callback therefore returns a bare `*smtp.SMTPError` value: internal errors are logged at the boundary and mapped with `errors.Is/As` to the SPEC 7.4 replies (`smtp.ErrDataTooLarge` from the DATA reader is returned unchanged). A unit test asserts each SPEC 7.4 reply code end to end.
- Routing (SPEC section 7) is implemented in `internal/smtpd/routing.go` as a pure function `Resolve(mode, authResult, rcpt, inboxDomain, lookup) (Target, *smtp.SMTPError)` where `lookup` is a small interface backed by the store. Table-driven tests cover SPEC 7.2.
- Brute-force limiter (`internal/ratelimit`) consulted before credential lookup.
- Limits: `smtp.Server` fields `MaxMessageBytes`, `MaxRecipients`, `MaxLineLength` (2000), `ReadTimeout`, `WriteTimeout`, `AllowInsecureAuth`, `Domain`, `EnableSMTPUTF8: true`. `Backend.NewSession` is called at HELO/EHLO (not at connect). Connection limits are enforced by a `net.Listener` wrapper (`limitListener`) that tracks total and per-IP counts and writes `421 4.7.0 Too many connections\r\n` before closing excess connections. `limitListener` runs its own accept goroutine: each raw connection is admitted in a short per-connection goroutine (checking total and per-IP limits) and only admitted connections are handed to go-smtp's `Accept` through a channel, so one slow client can never stall the accept loop. Temporary accept errors (e.g. `EMFILE`) are retried with backoff (5 ms doubling to 1 s), never forwarded to go-smtp. The decrement happens in the wrapped `Conn.Close` (idempotent).
- Shutdown (3.1): go-smtp's `Shutdown` waits for every connection, including idle pooled ones, and its `Close` is a no-op after `Shutdown`. So `limitListener` tracks admitted connections and marks one busy from `Session.Data` until `Session.Reset` (which go-smtp calls after writing the DATA reply). `Server.Shutdown` closes non-busy connections at once, closes busy ones right after their reply, force-closes the rest when the grace period expires, and returns only after every session has logged out, so the store is closed after the last session.
- TLS: `tls.Config{GetCertificate: reloader.Get, MinVersion: tls.VersionTLS12}` where `internal/tlsutil.Reloader` re-reads cert/key when their mtime, size, or inode changes (polled every 30 s) and serves both STARTTLS (`Serve`) and implicit TLS (`ServeTLS`, FR-002). Transient reload errors keep the previous valid certificate active.
- AUTH failure delay and lockout windows use the injected `clock.Clock`; tests use the fake clock.
- `Data(r io.Reader)` hands the reader to `ingest.Ingest` with the session's envelope and targets, maps errors to SMTP replies (SPEC 7.4).

### 3.4 `internal/ingest`

`Ingest(ctx, env Envelope, targets []Target, r io.Reader) ([]MessageRef, error)`:

1. **Spool**: stream DATA into `tmp/<ulid>.tmp` through a zstd encoder (`klauspost/compress/zstd`, level from config) while counting bytes and computing SHA-256. One write pass.
2. **Quota check**: instance quota and per-project rate limit (SPEC FR-096, FR-097).
3. **Parse** (bounded concurrency: semaphore of `2 x GOMAXPROCS`): reopen the spool through a zstd decoder, parse with `enmime` v2 (`enmime.NewParser(...)`.`ReadEnvelope`). Extract metadata (FR-008), text (FR-009), attachment list, `X-Tags`, parse warnings (`envelope.Errors`).
4. **Build index text**: subject, from, to/cc/bcc, body (first 256 KiB), attachment names.
5. **Place blob(s)**: `fsync` spool, then for each target project `os.Link` (fallback: copy) to `blobs/<project_id>/<yyyy>/<mm>/<ulid>.eml.zst`, `fsync` dir; remove spool. Each target gets its own ULID.
6. **Commit**: one write transaction per target: insert `messages`, `attachments`, tags (`INSERT ... ON CONFLICT DO NOTHING`), `message_tags`, FTS row (`internal/search.IndexTx`, added in Phase 2 so every stored message is indexed), update project counters and `last_message_at`. On error: remove that blob, return `451`. FTS rows are removed by the `messages_fts_delete` trigger (SPEC 10), never by application code.
7. **Publish**: `events.Bus.Publish(MessageCreated{ProjectID, Summary})` after commit.
8. **Retention nudge**: if the project exceeds count/bytes limits by more than 10%, signal the sweeper (debounced).

Memory: the raw message is never fully buffered by Mailcubby itself; enmime holds the decoded parts of one message. The parse semaphore bounds peak memory.

`internal/ingest/sample.go` embeds the built-in test message (FR-077) from `internal/ingest/assets/sample.eml` via `go:embed` (not `testdata/`, which is reserved for test inputs). The sample is hand-written: multipart/related HTML + text, one small inline PNG referenced by `cid:`, a minimal valid PDF attachment (under 2 KB), and one `https://` link. `send-test` gives it a fresh `Date` and `Message-ID`.

### 3.5 `internal/store`

- Driver: `modernc.org/sqlite` (pure Go, registered as `sqlite`).
- Two `*sql.DB` handles on the same file:
  - **writer**: `SetMaxOpenConns(1)`, DSN `file:<path>?_auto_vacuum=INCREMENTAL&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=temp_store(MEMORY)&_txlock=immediate`. The `_auto_vacuum` DSN key (modernc v1.63.0) is applied before `journal_mode`, which matters because switching a new file to WAL writes page 1 and freezes the auto-vacuum mode; on an existing database it is a no-op.
  - **reader**: `SetMaxOpenConns(max(4, GOMAXPROCS))`, DSN `file:<path>?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=temp_store(MEMORY)&_pragma=query_only(1)` (no `journal_mode`: WAL is persistent in the file).
- Open order: writer, run migrations, then reader. `store.Open` returns an error if `PRAGMA journal_mode` is not `wal` or `PRAGMA auto_vacuum` is not `2` after migrations on a database it created.
- Migrations: `github.com/pressly/goose/v3` with SQL files in `migrations/`, embedded via `migrations/embed.go` (`//go:embed *.sql`), applied at startup with the goose Provider API. Migrations are forward-only in production; each file still has a `-- +goose Down` section for development.
- Queries: `sqlc` (`engine: sqlite`, `database/sql`) generates code into `internal/store/sqlc/` from `internal/store/queries/*.sql`. sqlc understands goose files (it ignores `-- +goose Down` sections). `sqlc.yaml` lists migration files explicitly as schema; `00002_fts.sql` (FTS5 virtual table and trigger) is excluded because sqlc's SQLite parser cannot handle it, and all FTS statements are hand-written in `internal/search`. Every new migration is added to the `sqlc.yaml` schema list unless it only contains statements sqlc cannot parse. If sqlc rejects a statement (for example a virtual table, trigger or unusual constraint syntax), move that statement into an excluded migration and record it under Open questions in `docs/PLAN.md`; never hand-edit generated code.
- Public API: `store.Store` with repository-style methods grouped by file (`projects.go`, `messages.go`, `members.go`, ...). Tenant-scoped methods take `projectID int64` explicitly, e.g. `GetMessage(ctx, projectID int64, id ulid.ULID)`. Write methods take or open a transaction via `store.WithTx(ctx, func(tx *store.Tx) error)`.
- Errors: `store.ErrNotFound`, `store.ErrConflict` (unique violation), wrapped with `%w`; API maps them to `404`/`409`.

### 3.6 `internal/blob`

- `blob.Store` with `Create(ctx) (*Spool, error)`, `Place(spool, projectID, id) (relPath, error)`, `Open(relPath) (io.ReadCloser, error)` (decompressing), `Remove(relPath)`, `Walk(fn)` for GC, `RemoveProject(projectID)`.
- zstd: one shared `*zstd.Encoder` configuration per store (level from `MAILCUBBY_ZSTD_LEVEL` via `zstd.EncoderLevelFromZstd`); streaming decoders created with `zstd.WithDecoderConcurrency(1)` and reused via a `sync.Pool` (`Reset` per use). `DecodeAll` is not used, so a 25 MiB message is never fully buffered.
- Paths are built only from integer project IDs and ULIDs; any other input is rejected (no path traversal possible).

### 3.7 `internal/search`

- `Parse(q string) (Query, error)`: tokenizer for terms, quoted phrases, `-` negation, operators (SPEC FR-081). Fuzz-tested.
- `Build(projectID, Query, filters, cursor, limit) (sql string, args []any)`: composes fixed SQL fragments; user text only in args. FTS terms are emitted as quoted FTS5 strings (`"` doubled), joined with column filters (`subject : "..."`).
- `IndexTx(ctx, tx, pk, doc)` inserts the `messages_fts` row (Phase 2). Deletes are handled by the SQL trigger, so there is no `DeleteTx`.
- Search SQL joins `messages_fts` to `messages` on `rowid = pk` and always filters `messages.project_id = ?`.

### 3.8 `internal/events`

- `Bus` with `Publish(Event)` and `Subscribe(filter Filter) (*Subscription, func())`. Each subscription has a buffered channel (256). Publish never blocks: if a buffer is full the subscription is closed with reason `slow_consumer`.
- `Filter` is a function `func(projectID int64) bool` backed by an `atomic.Pointer` to the subscriber's current project set; `membership.changed` events trigger a refresh from the store. The SSE handler subscribes before loading the initial set, always queues `project.deleted`, and on it removes that project ID from the set (closing streams scoped to it), so a later project reusing the ID is never matched.
- Used by: SSE handler, `wait` endpoint, retention nudges, counters cache invalidation.

### 3.9 `internal/auth`

- `password.go`: argon2id hash/verify/needsRehash (`golang.org/x/crypto/argon2`), PHC encoding, semaphore of 2.
- `session.go`: create, lookup (SHA-256 of cookie), touch (rate-limited `last_seen_at` update), revoke.
- `token.go`: API token generation/parse (`mailcubby_` prefix), lookup by prefix, constant-time hash compare.
- `csrf.go`: CSRF token check + Origin check.
- `permissions.go`: `type Role`, `type Action`, and the single source-of-truth table of SPEC FR-050: `func Allowed(role Role, a Action, p ProjectSettings) bool`.
- `principal.go`: `Principal{Kind: Local|User|Token, UserID, IsAdmin, TokenScope, TokenProjectID}` stored in request context.
- `internal/secrets`: APP_KEY loading/generation, HKDF subkeys (`crypto/hkdf`), AES-256-GCM seal/open for SMTP credentials.

### 3.10 `internal/api`

- Routing with `http.ServeMux` method patterns (`GET /api/v1/projects/{project}/messages/{id}`). No router dependency.
- Routes are declared in one table (`routes.go`): `{Method, Pattern, Need: Permission, Handler}`. `Need` is one of `Public`, `Session`, `User`, `Admin`, or a project `Action`. The authorisation matrix test (SPEC FR-050) iterates this table.
- Middleware chain (outer to inner): recover, request ID, real IP (trusted proxies), security headers, access log (`slog`, redacted), body size limit, rate limit, authenticate (session cookie / bearer token / local principal), CSRF (server mode: cookie + unsafe method; local mode: `http.CrossOriginProtection` on unsafe methods), then per-route: resolve `{project}` slug to project ID and role (`404` if no access), check `Need`, put `ProjectContext{ID, Slug, Role, Settings}` into the request context.
- Handlers decode JSON with `DisallowUnknownFields`, validate, call store/services, encode with a shared `writeJSON` / `writeError` helper. Handlers never read `{project}` themselves.
- `openapi.yaml` is embedded and served; `routes_test.go` checks every route has a matching OpenAPI path+method and vice versa.
- SSE (`events.go`): sets `Content-Type: text/event-stream`, `Cache-Control: no-store`, `X-Accel-Buffering: no`; writes `retry: 3000` and flushes immediately (so proxies forward the headers at once); uses `http.NewResponseController(w).SetWriteDeadline(time.Time{})` to lift the server write timeout; writes events and 25 s heartbeats (`: ping`) through the injected clock's ticker; ends on client disconnect, server shutdown or slow-consumer close. `http.Server.Shutdown` waits for active handlers, so SSE and `wait` handlers also select on an app-level shutdown channel that `internal/app` closes before calling `Shutdown`. No response compression middleware exists (it would buffer SSE).
- HTTP server timeouts: `ReadHeaderTimeout 10s`, `ReadTimeout 60s`, `WriteTimeout 60s` (lifted per request for SSE, `wait`, downloads), `IdleTimeout 120s`, `MaxHeaderBytes 64 KiB`.

### 3.11 `internal/render`

- `Render(ctx, projectID, msgID, opts) (Result, error)`: loads the blob, finds the HTML part and inline parts via enmime, rewrites with the `golang.org/x/net/html` tokenizer (SPEC 12.1: inject referrer meta and `<base target>`, drop `<base>`, `<meta http-equiv=refresh>` and prefetch-style `<link>`, inline `cid:` parts), counts remote resources (`http:`/`https:` in `src`, `srcset`, `background`, CSS `url()`), returns `Result{HTML []byte, RemoteResourceCount int}`. The message detail handler uses the same function (cached) to fill `remote_resource_count`.
- The HTTP handler (`GET /render/{project}/{id}`) is registered in `internal/api` but uses its own header set (SPEC 12.1) instead of the app CSP.
- Small LRU cache (32 entries, keyed by project, message ID and options) because the UI re-requests when switching device sizes.

### 3.12 `internal/retention` (workers)

- `Sweeper`: runs every `MAILCUBBY_RETENTION_INTERVAL` and on nudges; per project computes effective limits (project override, instance setting, env default) and deletes oldest messages in batches of 500 (one transaction each), then removes blobs and publishes `message.deleted`.
- `OrphanGC`: hourly; walks `blobs/`, deletes files older than 1 h with no row, removes directories of deleted projects; cleans `tmp/` entries older than 1 h.
- `Vacuum`: after GC, `PRAGMA incremental_vacuum(1000)` while `freelist_count / page_count > 0.1`.
- `Housekeeping`: daily; expired sessions, invites, resets, old audit entries.
- All workers take a `clock.Clock` interface for testability and stop on context cancel.

### 3.13 `internal/web`

- `//go:embed all:dist` of the Vite build (copied from `web/dist` by `make web`; `dist/.gitkeep` keeps the embed valid before the first build).
- Serves `/assets/*` with `Cache-Control: public, max-age=31536000, immutable`; `index.html` with `no-cache`; other root files (`/theme.js`, `/favicon.svg`) with `no-cache`; SPA fallback to `index.html` for any GET not under `/api/`, `/render/`, `/assets/`, `/healthz`, `/readyz` (a missing `/assets/x.js` is a `404`, never `index.html`, so a stale chunk fails loudly).
- The Vite build uses absolute asset URLs (`base: '/'`). Relative URLs (`base: './'`) would break deep links such as `/p/shop/m/01J...` after a reload, because the browser would resolve `./assets/...` against the nested path. A Go test requests a deep link and asserts the returned HTML references `/assets/`.
- If `dist/index.html` is missing (Go-only dev build), serves a plain page "UI not built: run `make web`".

## 4. Request flows

### 4.1 SMTP ingest (server mode, authenticated)

```
App            limitListener         smtpd.session         ratelimit   store        ingest/blob          events    SSE clients
 |-- TCP connect --->|                    |                    |          |              |                  |           |
 |                   |-- accept, count -->|                    |          |              |                  |           |
 |<----------------------- 220 greeting --|                    |          |              |                  |           |
 |-- EHLO ---------------------------------->| 250 STARTTLS ... |          |              |                  |           |
 |-- STARTTLS / TLS handshake ---------------->|                |          |              |                  |           |
 |-- AUTH PLAIN <b64> ------------------------>|-- locked? ---->|          |              |                  |           |
 |                                             |-- credential by username ->|            |                  |           |
 |                                             |   decrypt + constant-time compare        |                  |           |
 |<-------------------------- 235 Auth OK -----|                |          |              |                  |           |
 |-- MAIL FROM / RCPT TO ---------------------->| rate limit, recipient count            |                  |           |
 |-- DATA + body + . -------------------------->|-- Ingest(env, targets, r) ------------>|                  |           |
 |                                             |                |          |  spool zstd, parse, link blob      |           |
 |                                             |                |          |<- tx insert -|                  |           |
 |                                             |                |          |              |-- Publish ------>|-- filter ->|
 |<-------------------------- 250 OK <id> -----|                |          |              |                  |  event    |
```

The `250` reply is sent only after the transaction commits (FR-010).

### 4.2 Login (server mode)

1. `POST /api/v1/auth/login` with JSON; middleware checks Origin and login rate limit.
2. Lookup user by email (reader). If missing or disabled, verify against a dummy hash (constant time) and return `401`.
3. Verify argon2id; on success rehash if parameters changed; create session (random token, store SHA-256, CSRF token); set cookie; audit `auth.login`; return user.
4. SPA calls `GET /api/v1/auth/me` to obtain `csrf_token` and memberships, stores CSRF token in memory (not localStorage), sends it as `X-CSRF-Token` on unsafe requests.

### 4.3 SSE

1. Browser opens `EventSource('/api/v1/events')` (cookie sent automatically; GET so no CSRF needed).
2. Middleware authenticates; handler loads the principal's project set, subscribes to the bus with a filter, flushes headers.
3. Ingest publishes `message.created`; the bus delivers only to subscribers whose filter accepts the project; the handler writes `event: message.created\ndata: {...}\n\n` and flushes.
4. The SPA updates TanStack Query caches (prepends to the inbox list, bumps counters) without refetching.
5. On `membership.changed` for the user, the handler refreshes the project set; on removal from all projects, the stream stays open with no events.
6. On disconnect, `EventSource` reconnects (retry 3 s, sent via `retry:` field) and the SPA invalidates queries.

### 4.4 Wait for message

1. Handler subscribes to the bus (filter: this project) **before** querying.
2. Queries the store for the newest message matching `query` with ID greater than `since`; if found, returns it.
3. Otherwise, for each `message.created` event in the project, re-runs the query restricted to that message ID; first match returns `200`.
4. Timeout returns `204`; client disconnect cancels.

### 4.5 Render

1. UI sets `<iframe sandbox="allow-popups allow-popups-to-escape-sandbox" src="/render/<slug>/<id>">` (`?images=1` when the user loads remote images).
2. Middleware authenticates by cookie (same-site), resolves project, checks `read` permission.
3. `render.Render` rewrites HTML; handler sets the render CSP and headers; returns.

## 5. Repository and Go package layout

```
.
|-- AGENTS.md, README.md, CONTRIBUTING.md, LICENSE
|-- .agents/skills/next-phase/SKILL.md   Antigravity skill: run the next PLAN phase (defers to AGENTS.md)
|-- Makefile
|-- go.mod, go.sum
|-- .golangci.yml
|-- .goreleaser.yaml
|-- sqlc.yaml
|-- .github/workflows/        ci.yml, release.yml
|-- cmd/mailcubby/             main.go, subcommands
|-- internal/
|   |-- app/                  wiring, lifecycle, errgroup
|   |-- cli/                  admin/keygen/healthcheck/sendmail subcommands
|   |-- config/               env parsing, validation
|   |-- smtpd/                go-smtp backend, session, routing, LOGIN sasl, limitListener
|   |-- ingest/               spool, parse (enmime), metadata, tags, sample message
|   |-- blob/                 zstd blob files, spool, GC walk
|   |-- store/                Store, Tx, repositories
|   |   |-- queries/          *.sql for sqlc
|   |   `-- sqlc/             generated (do not edit)
|   |-- search/               query parser, SQL builder, FTS index maintenance
|   |-- auth/                 passwords, sessions, tokens, csrf, permissions, principal
|   |-- secrets/              APP_KEY, HKDF, AES-GCM
|   |-- ratelimit/            sliding window and token bucket limiters
|   |-- api/                  routes, middleware, handlers, SSE, openapi.yaml
|   |-- events/               in-process bus
|   |-- render/               HTML rewrite for sandboxed preview
|   |-- retention/            sweeper, orphan GC, vacuum, housekeeping
|   |-- tlsutil/              certificate reloader
|   |-- clock/                Clock interface + fake
|   |-- web/                  go:embed of built SPA (dist/)
|   `-- testutil/             StartServer, SMTP helpers, seeded stores, golden helpers
|-- migrations/               goose SQL files + embed.go
|-- testdata/mime/            *.eml fixtures and *.golden.json
|-- testdata/hostile/         hostile HTML emails for render tests (Phase 11)
|-- web/                      Vite + React app (see docs/UI.md)
|   |-- package.json, pnpm-lock.yaml, vite.config.ts, tsconfig.json, biome.json, components.json, playwright.config.ts
|   |-- e2e/                Playwright specs and helpers (from Phase 4a)
|   `-- src/
|       |-- main.tsx
|       |-- routes/           TanStack Router file routes
|       |-- features/         inbox/, message/, projects/, settings/, admin/, auth/, account/, setup/
|       |-- components/ui/    shadcn/ui components (generated, then owned)
|       |-- components/       shared app components
|       |-- lib/              api client, sse, query client, shortcuts, format
|       |-- api/schema.d.ts   generated from internal/api/openapi.yaml
|       `-- styles/           globals.css (Tailwind v4, theme tokens)
|-- deploy/
|   |-- Dockerfile            multi-stage (node build, go build, distroless)
|   |-- Dockerfile.goreleaser runtime-only image for GoReleaser
|   |-- compose.local.yml
|   |-- compose.server.yml    with Caddy
|   `-- Caddyfile
`-- docs/                     SPEC, ARCHITECTURE, UI, PLAN, decisions/
```

Dependency direction (enforced by review, optionally by `depguard`): `cmd -> app -> {smtpd, api, retention} -> {ingest, search, auth, render, events} -> {store, blob, secrets} -> stdlib/sqlc`. `store` never imports `api`; `api` never touches `*sql.DB` directly.

## 6. Data and storage

See SPEC sections 10 and 11. Additional implementation notes:

- ULIDs via `github.com/oklog/ulid/v2` with a monotonic entropy source guarded by a mutex (one generator in `internal/ingest`).
- Message list query: `SELECT ... FROM messages WHERE project_id = ? AND id < ? ORDER BY id DESC LIMIT ?` uses index `(project_id, id DESC)`.
- Counters are updated with `UPDATE projects SET message_count = message_count + 1, ...` inside the ingest/delete/seen transactions.
- Sparkline: `SELECT (received_at / 3600000) AS h, COUNT(*) FROM messages WHERE project_id = ? AND received_at >= ? GROUP BY h`, cached 30 s per project, invalidated on `message.created`.

## 7. Tenant isolation strategy

1. **Store signatures**: every method touching `messages`, `attachments`, `tags`, `message_tags`, `smtp_credentials`, `invites`, `project_members` takes `projectID` and filters by it. Joins through `messages` always include `messages.project_id = ?`.
2. **Single resolution point**: `{project}` is resolved to an ID and role only in middleware. Handlers read `ProjectContext` from context.
3. **Query lint test**: `internal/store/isolation_test.go` parses every file in `internal/store/queries/` and fails if a query on a tenant table lacks `project_id`, unless the query carries the annotation `-- isolation: global (<reason>)` (allowed for admin listings, retention, GC, credential lookup by username).
4. **Two-tenant integration test**: seeds projects A and B with messages, tags, attachments, credentials, members and invites; as a user of A only, calls every route with B's IDs substituted into A's paths (and B's slug) and asserts `404`, and asserts no B data appears in list responses or SSE.
5. **Authorisation matrix test**: route table x principal kinds (SPEC FR-050).
6. **SMTP**: credential lookup returns the project ID; nothing in the SMTP session accepts a project from client input except recipient routing, which checks `recipient_routing_enabled` in server mode.

## 8. Testing strategy

| Level | Tooling | Scope |
|---|---|---|
| Unit | `go test`, table-driven, `t.Parallel()` | config parsing, slugify, routing table, search parser (plus `go test -fuzz`), permissions, render rewriting, token formats, ratelimit |
| Store | real SQLite in `t.TempDir()` via `testutil.NewStore(t)` | every query, counters, cascades, migrations up from empty |
| Golden MIME | `testdata/mime/*.eml` -> `*.golden.json` | ingest metadata; `go test ./internal/ingest -update` regenerates; diffs reviewed |
| Integration | `testutil.StartServer(t, cfg)` (random ports on 127.0.0.1) + `net/smtp` client (PLAIN AUTH, STARTTLS with a runtime-generated self-signed cert) + `net/http` client | end-to-end ingest, routing in both modes, API, SSE, wait, isolation, authz matrix |
| Benchmarks | `go test -bench` | NFR-001, NFR-003, NFR-004 |
| Frontend unit | Vitest + Testing Library | hooks (shortcuts, SSE cache updates), query parser helpers, components with logic |
| E2E (from Phase 4a) | Playwright (Chromium only) via `make e2e` | every UI phase adds specs for its screens; Phase 11 adds hostile-email checks; Phase 13 adds `@axe-core/playwright` on every screen and cross-checks the whole suite |

Rules: no sleeping in tests (use fake clocks and event channels); no network beyond loopback; tests must pass with `-race` (`make test-race`, CGO enabled in CI only for the race job). Latency targets (NFR-002, FR-078 500 ms) are asserted in benchmarks or dedicated non-race tests; functional tests wait with generous timeouts (2 s) so they do not flake on slow CI.

**E2E harness** (`web/e2e/`, Phase 4a): `make e2e` builds the binary with the UI (`make build-all`), and Playwright's `globalSetup` starts `bin/mailcubby serve` on free ports with a fresh temp data dir (local mode, plus a server-mode instance with `MAILCUBBY_ADMIN_EMAIL`/`_PASSWORD` and `MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH=true` and `MAILCUBBY_BASE_URL` equal to its own `http://127.0.0.1:<port>` so Origin checks pass, from Phase 6). Specs create mail through `POST /send-test` or by running `bin/mailcubby sendmail` on files from `testdata/mime/` (`child_process.execFile`), and wait on UI state with Playwright's auto-waiting assertions, never fixed sleeps. Chromium is installed by `make deps` (`pnpm --dir web exec playwright install chromium`).

**Seeding a dev instance**: `make seed` sends every `testdata/mime/*.eml` to a running instance via `bin/mailcubby sendmail` (`SMTP=localhost:1025`, `USER=<project>`, `COUNT=<n>` repeats the set, useful for 1,000-message UI checks). Benchmarks seed their own databases through the store, not SMTP.

**Fixtures** are small, hand-written `.eml` files (no real personal data, example.com/example.test addresses, each under 50 KB except the size-limit tests, which generate large bodies in code). Binary parts (PNG, PDF) are minimal valid files encoded in the fixture. Golden JSON files are produced with `-update` and then reviewed for plausibility before committing.

Golden fixture set (minimum): plain text; multipart/alternative; HTML-only; multipart/related with CID images; attachments (PDF, ZIP, filename with UTF-8 and RFC 2231 encoding); nested `message/rfc822`; ISO-8859-1 and Windows-1252; RFC 2047 encoded subject; very long header lines; missing Date and From; malformed boundary; 8-bit body without charset; calendar invite (`text/calendar`); `X-Tags` header.

## 9. Deployment

### 9.1 Dockerfile (`deploy/Dockerfile`)

```dockerfile
# Stage 1: web
FROM node:26-alpine AS web
WORKDIR /src/web
RUN npm install -g pnpm@12.21.0
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# Stage 2: go
FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/ internal/web/dist/
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /out/mailcubby ./cmd/mailcubby \
 && mkdir -p /out/data

# Stage 3: runtime
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/mailcubby /mailcubby
COPY --from=build --chown=65532:65532 /out/data /data
ENV MAILCUBBY_DATA_DIR=/data
VOLUME /data
EXPOSE 1025 8025
HEALTHCHECK --interval=30s --timeout=3s CMD ["/mailcubby", "healthcheck"]
ENTRYPOINT ["/mailcubby"]
CMD ["serve"]
```

distroless `static` (not `scratch`) is used because it ships CA certificates, `/etc/passwd` with the `nonroot` user, and tzdata, at about 2 MB. It has no shell, so `HEALTHCHECK` uses the exec form and the binary's own `healthcheck` subcommand (never `curl` or `wget`). The `golang` tag matches the `toolchain` line in `go.mod` so the build does not download another toolchain. `.dockerignore` excludes `node_modules`, `web/dist`, `bin`, `data*` and `.git`.

### 9.2 Releases

- GoReleaser v2 (`.goreleaser.yaml`): `before.hooks: [make web]`; builds `linux/darwin/windows` x `amd64/arm64` with `CGO_ENABLED=0`; archives + checksums; multi-arch images `ghcr.io/mailcubby/mailcubby:{{ .Version }}`, `:{{ .Major }}.{{ .Minor }}`, `:latest` from `deploy/Dockerfile.goreleaser` (runtime stage only, copies the prebuilt binary). Use the multi-platform Docker configuration documented for the pinned GoReleaser version. SBOMs via syft and keyless cosign signing are optional.
- Triggered by `release.yml` on tags `v*`.

### 9.3 Running

Local:

```sh
docker run --rm -p 127.0.0.1:8025:8025 -p 127.0.0.1:1025:1025 -v mailcubby-data:/data ghcr.io/mailcubby/mailcubby:latest
```

Local mode has no authentication and listens on all interfaces inside the container, so examples publish its ports on `127.0.0.1` only.

Server: `deploy/compose.server.yml` (see README). Topology:

```
Internet --443--> Caddy (auto HTTPS) --http--> mailcubby:8025
Internet --587--> mailcubby:1025 (STARTTLS, cert files from Caddy's volume, read-only)
```

- SMTP is exposed directly (not through Caddy). The certificate obtained by Caddy for the UI host is reused by mounting Caddy's data volume read-only and pointing `MAILCUBBY_SMTP_TLS_CERT/KEY` at the files; `internal/tlsutil.Reloader` picks up renewals every 30 s. Because Caddy writes key files `0600` owned by its own user and Mailcubby runs as UID 65532, `deploy/compose.server.yml` runs a one-shot `caddy-init` container to `chown 65532:65532` the Caddy volumes and runs Caddy as `user: "65532:65532"` (listening internally on `http_port 8080` / `https_port 8443` mapped from host `80`/`443`), while `deploy/Caddyfile` pins `acme_ca` to Let's Encrypt so the issuer directory (`acme-v02.api.letsencrypt.org-directory`) is deterministic.
- If the configured certificate files cannot be read at startup, Mailcubby exits with an error naming the variable and the file (never starts silently without STARTTLS).
- `MAILCUBBY_TRUSTED_PROXIES` must include the Docker network so client IPs are correct for rate limiting and audit.

### 9.4 Operations

- Logs: `log/slog` to stdout, `text` or `json`. Each HTTP request logs method, route pattern, status, duration, request ID, principal kind; each SMTP session logs remote IP, auth user, recipients count, result code, message IDs.
- Backups: documented in README (SQLite `.backup` + blobs copy, or stop and copy the volume).
- Upgrades: migrations run automatically; downgrades are not supported (restore from backup).

## 10. Threat model

Mailcubby is designed to capture untrusted email from staging/development applications and serve it safely to multi-tenant teams and external client viewers. This section summarizes the security assets, actors, primary threats, implemented mitigations, and residual risks.

### 10.1 Assets

| Asset | Sensitivity | Storage |
|---|---|---|
| Captured messages & attachments | High (often contain PII, password-reset links, magic links, invoices) | SQLite metadata + zstd `.eml` blobs (`0600`) |
| User passwords & sessions | Critical | Argon2id PHC hashes; session tokens stored as SHA-256 only |
| Invite & password-reset tokens | Critical | SHA-256 hashes only; single-use with expiry |
| Personal API tokens | High | SHA-256 hashes only (`mailcubby_` + 40 base62 chars) |
| Project SMTP credentials | High | AES-256-GCM encrypted under HKDF subkey of `MAILCUBBY_APP_KEY` with username AAD |
| `MAILCUBBY_APP_KEY` | Critical | Env var / `_FILE` (server mode) or `$DATA_DIR/app.key` (`0600`, local mode) |
| Audit trail (`audit_log`) | Medium | SQLite (retention `MAILCUBBY_AUDIT_RETENTION`, never holds secrets) |

### 10.2 Actors and trust boundaries

1. **Unauthenticated network attacker**: can reach public HTTP endpoints (`/healthz`, `/readyz`, `/api/v1/meta`, `/setup`, `/auth/login`, `/invites/{token}`, `/password-reset/{token}`) and the SMTP listener (`:1025` / `:1465`).
2. **Untrusted email sender / hostile email payload**: controls raw RFC 5322 bytes, HTML, CSS, headers (`X-Tags`, `Subject`, `From`), and attachment filenames/MIME types.
3. **Client viewer**: authenticated lowest-privilege tenant user scoped to assigned project(s); must be read-only, isolated from other projects, prevented from seeing SMTP usernames/credentials, and blocked from technical views when `viewer_hide_technical` is enabled.
4. **Project member / owner**: authenticated tenant user; members can manage messages/tags and read SMTP info (revealing passwords only if `members_can_reveal_smtp` is enabled); owners manage project settings, members, invites, credentials, and project audit log; neither can access other projects or admin routes.
5. **API token bearer**: automated CI/test client restricted to `read` or `write` scope (never owner/admin/management endpoints) and optionally pinned to one project.
6. **Instance administrator**: trusted for user/instance management; implicit access to non-member projects is audited (`project.admin_access`).

### 10.3 Threats and mitigations

| Threat | Mitigation | Verification |
|---|---|---|
| **XSS / script execution / origin escape via email HTML** | Email HTML is never inserted into the app DOM (`noDangerouslySetInnerHtml` Biome error). Served exclusively from `GET /render/{project}/{id}` inside `<iframe sandbox="allow-popups allow-popups-to-escape-sandbox">` with SPEC 12.1 CSP (`sandbox` without `allow-scripts`/`allow-same-origin`/`allow-forms`, `default-src 'none'`, `form-action 'none'`, `base-uri 'none'`). `internal/render` rewrites via `golang.org/x/net/html` (injects `no-referrer` + `<base target="_blank">`, drops `<base>`, `<meta http-equiv="refresh">`, prefetch/preload `<link>`, and recursively rewrites `<noscript>`, `<svg>`/`<math>` raw-text elements, and `iframe srcdoc`). | `internal/render/render_test.go`, `testdata/hostile/` corpus, `web/e2e/hostile-render.spec.ts` |
| **Remote tracking pixel / CSS exfiltration** | Render CSP defaults to `img-src data: cid:` (blocking external images, fonts, stylesheets, frames, objects). Remote image loading is per-message opt-in (`?images=1`, disabled when `MAILCUBBY_RENDER_REMOTE_IMAGES=off`). `Referrer-Policy: no-referrer` and `X-DNS-Prefetch-Control: off` prevent referrer and DNS leaks. | `internal/api/headers_test.go`, `web/e2e/hostile-render.spec.ts` |
| **Active content execution via downloads / attachments** | Attachments and `.eml` downloads force `Content-Disposition: attachment` (inline only for raster PNG/JPEG/GIF/WebP with `?inline=1`), `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox; default-src 'none'`, and coerce HTML/XHTML/SVG/XML/JS/multipart to `application/octet-stream`. Raw/HTML sources are served as `text/plain; charset=utf-8` with `nosniff` and attachment CSP. | `internal/api/headers_test.go`, `internal/api/api_test.go` |
| **Cross-tenant data leakage** | Every tenant store method takes `projectID` explicitly and filters every referenced tenant table (`isolation_test.go` SQL lint). Middleware resolves `{project}` once per request and returns `404` on inaccessible projects. `projects.id` is `AUTOINCREMENT` (migration 00003) so deleted IDs are never reused. SSE filters by live membership set, drops deleted project IDs immediately, and closes streams on logout/revocation/disable/delete. Viewers never see `envelope.auth_user`. | `internal/store/isolation_test.go`, `internal/api/isolation_test.go`, `internal/api/project_id_reuse_test.go`, `internal/api/viewer_test.go` |
| **Privilege escalation & broken access control** | Single route table (`internal/api/routes.go`) mapped to `internal/auth/permissions.go` (`Allowed`, `EffectiveTokenRole`, `CanReadTechnical`). Last active admin (with password) and sole project owner cannot be removed/deleted (`409`). Project invites cannot set passwords on existing accounts (`409 login_required`). | `internal/api/authz_matrix_test.go`, `internal/api/members_invites_test.go`, `internal/api/log_redaction_test.go` |
| **CSRF & session hijacking** | `HttpOnly`, `SameSite=Lax`, `Secure` (on `https://`) session cookies; idle and absolute expiry enforced; unsafe cookie requests require both matching `Origin`/`Referer` and `X-CSRF-Token` (kept in memory, never `localStorage`). Local mode (no cookies) rejects cross-origin unsafe browser requests with stdlib `http.CrossOriginProtection` (`Sec-Fetch-Site`, else `Origin` vs `Host`), so a web page cannot create or delete data on a developer's instance; DNS rebinding is out of scope, as in Mailpit (bind to `127.0.0.1`). Password change/reset revokes other/all sessions, outstanding reset tokens, and open SSE streams. Post-login `returnTo` rejects external origins, backslashes, control chars, and protocol-relative paths. | `internal/api/auth_test.go`, `internal/api/cross_origin_test.go`, `internal/api/log_redaction_test.go`, `web/src/features/auth/auth.test.tsx` |
| **Credential brute force & enumeration** | Argon2id (`m=19456, t=2, p=1`, semaphore of 2) with dummy-hash verification for unknown/disabled/passwordless users. Atomic `Reserve`/`Cancel` rate limits on HTTP login (`MAILCUBBY_LOGIN_RATE_LIMIT` per IP+email and 5x per IP) and SMTP AUTH (`MAILCUBBY_SMTP_AUTH_MAX_FAILURES` per (IP, username) and 5x per IP, 1 s failure delay, `smtp.auth_lockout` audit). `20/15m` per-IP limits on invite and password-reset endpoints; `MAILCUBBY_API_RATE_LIMIT` token bucket per session/token in server mode. Server-mode SMTP requires TLS before AUTH unless `MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH=true`. | `internal/ratelimit/*_test.go`, `internal/api/rate_limit_test.go`, `internal/smtpd/server_mode_test.go`, `internal/smtpd/auth_lockout_test.go` |
| **Resource exhaustion / DoS** | SMTP total and per-IP connection limits (`limitListener`), `MaxMessageBytes` (25 MiB default, max 100 MiB), `MaxRecipients` (100), bounded parse semaphore (`2 * GOMAXPROCS`), 50 auto-tag cap per message, 10 MiB `cid:` inline render cap, per-project SMTP rate limit (`450`), instance storage quota (`452`), retention sweeper and orphan blob GC, 1 MiB JSON request body cap, HTTP server timeouts, SSE per-user connection cap (10) and slow-consumer disconnect (256 buffer). | `internal/smtpd/limit_listener_test.go`, `internal/ingest/ingest_test.go`, `internal/app/retention_quota_test.go`, `internal/retention/*_test.go` |
| **SQL injection & path traversal** | All SQL is sqlc-generated parameterized queries or `internal/search` fixed fragments + bind parameters (fuzz-tested). Blob paths are built solely from integer project IDs and ULIDs with strict validation (`internal/blob`). | `internal/search/search_test.go` (fuzz), `internal/blob/blob_test.go` |
| **Secret leakage in logs or storage** | Access and panic logs never log query strings, redact `Authorization`/`Cookie`/`Set-Cookie`/`X-CSRF-Token`, and replace token segments in `/api/v1/invites/*`, `/api/v1/password-reset/*`, `/invite/*`, `/reset/*` with `[REDACTED]`. Decrypted SMTP passwords are never logged or stored in plaintext. `app.key` is created atomically with `0600`. Static analysis (`gosec`) and CI vulnerability scanning (`govulncheck`). | `internal/api/log_redaction_test.go`, `internal/secrets/secrets_test.go`, `.github/workflows/ci.yml` |

### 10.4 Residual risks and operator responsibilities

- **Local mode has no authentication**: `MAILCUBBY_MODE=local` is strictly for local development and must never be exposed to untrusted networks.
- **In-memory rate limiting**: sliding-window and token-bucket counters live in process memory and reset on restart.
- **Reverse proxy configuration**: operators running behind Caddy/Traefik/nginx must set `MAILCUBBY_TRUSTED_PROXIES` accurately so client IP extraction for rate limiting and audit logs cannot be spoofed via `X-Forwarded-For`, and must terminate HTTPS with `MAILCUBBY_BASE_URL=https://...` so `Secure` cookies and HSTS apply.
- **Server-mode recipient routing**: enabling `recipient_routing_enabled` on a project permits unauthenticated SMTP delivery to routed addresses; operators enabling it should firewall SMTP access to trusted networks.
- **Database host security**: compromise of both `MAILCUBBY_APP_KEY` and `app.db` allows decrypting stored SMTP credentials; keep `MAILCUBBY_APP_KEY` outside backups or in a secret manager.
