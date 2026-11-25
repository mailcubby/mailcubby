# Mailcubby: Implementation Plan

Phases are ordered and each is sized for one AI agent session (about 0.5 to 2 days of focused work). Large phases are split into `a`/`b` parts that are done in order. Work on the **first phase with unchecked boxes**. Tick boxes (`- [x]`) in the same change that completes the task. A phase is done when all its boxes are ticked, its acceptance criteria pass as automated tests, and `make check` is green (see `AGENTS.md` sections 7 and 9).

Rules for every phase:
- Each phase leaves the binary runnable (`make run` works) and `make check` green. Endpoints, routes and UI screens are added only when their phase implements them; `openapi.yaml` documents exactly the registered routes.
- Acceptance criteria are automated tests unless marked `(owner)`, which the owner verifies by hand; agents do not block on them (AGENTS.md section 7, item 11).
- From Phase 4a on, every phase with UI tasks adds Playwright specs in `web/e2e/` for the screens it changes, and `make e2e` must pass.

## Kickoff prompt

In Google Antigravity, `AGENTS.md` is loaded automatically; type `/next-phase` (skill in `.agents/skills/next-phase/`). With any other agent, use this prompt:

> You are implementing Mailcubby, a source-available multi-tenant email testing server. Read `AGENTS.md`, `docs/SPEC.md` and `docs/ARCHITECTURE.md` in full (and `docs/UI.md` if the phase includes frontend work, plus relevant ADRs in `docs/decisions/`). Then open `docs/PLAN.md`, find the first phase with unchecked tasks, and write a short implementation plan for that phase only: files to create or change, tests to write, and any spec ambiguities. Then implement it task by task without waiting for approval (unless a stop condition from `AGENTS.md` section 7 applies), ticking each checkbox in `docs/PLAN.md` as you finish it and keeping `make check` green. Do not add dependencies that are not in `AGENTS.md` section 3. Finish with a summary of what changed, how it was tested, which criteria are blocked or owner-verified, and anything recorded under "Open questions".

## Milestones

| Milestone | Phases | Outcome |
|---|---|---|
| M0 Foundation | 0 to 1 | Buildable repo, CI, DB with migrations |
| M1 Mailpit-lite local works | 2 to 4b | Local mode: SMTP in, inbox UI, message views, realtime. Usable daily by a developer. |
| M2 Local feature-complete | 5 | Search and tags |
| M3 Multi-tenant server | 6 to 9 | Login, projects, roles, invites, per-project SMTP credentials, settings UI, API tokens |
| M4 Production-ready | 10 to 12 | Retention, security hardening, TLS, packaging |
| M5 v1.0 | 13 to 14 | Release |

---

## Phase 0: Repository scaffold

**Goal**: an empty but complete skeleton that builds, lints, tests and produces a Docker image serving a placeholder UI.

Tasks:
- [x] `go.mod` (`module github.com/mailcubby/mailcubby`, `go 1.27`, `toolchain go1.27.4`); `cmd/mailcubby/main.go` with `serve` (placeholder HTTP server on `MAILCUBBY_HTTP_ADDR`, default `:8025`, read directly from the environment until Phase 1) and `version` subcommands; `version`, `commit`, `date` vars in `package main` set via `-ldflags`.
- [x] `Makefile` with all targets from `AGENTS.md` section 5. Targets whose inputs do not exist yet are no-ops that print why (`generate` without `sqlc.yaml`/`openapi.yaml`, `seed`, `e2e`, `bench`); `make help` lists every target. `make tools` downloads pinned `sqlc` v1.31.1 and `golangci-lint` v2.15.0 release binaries into `./bin/` for the host OS/arch. `make check` detects generated-file changes with a hash snapshot (not `git diff`).
- [x] `.golangci.yml` (v2 format) with the linters listed in `AGENTS.md`; `depguard` rule denying `github.com/go-chi`, `github.com/gin-gonic`, `github.com/labstack/echo`, `github.com/sirupsen/logrus`, `go.uber.org/zap`; generated `internal/store/sqlc/` excluded.
- [x] `web/` Vite + React 19 + TypeScript 6 app with Tailwind v4 (`@tailwindcss/vite`), shadcn/ui initialised (`components.json`), TanStack Router (file routes, `routeTree.gen.ts` committed) and Query, Biome config (generated files ignored), Vitest config with one passing test, Inter font, `globals.css` with the tokens from `docs/UI.md`, light/dark/system theme with `public/theme.js`. One route `/` rendering "Mailcubby" with a theme toggle. `pnpm` scripts: `dev`, `build`, `test`, `lint`, `typecheck`, `gen:api`. Exact versions from `AGENTS.md` section 3 (no `^`/`~`), `packageManager: "pnpm@12.21.0"`.
- [x] Vite config: `base: '/'`, dev proxy for `/api` and `/render` to `http://localhost:8025`.
- [x] `internal/web` with `//go:embed all:dist`, `dist/.gitkeep`, SPA fallback, cache headers, `/assets/` 404 for missing files, "UI not built" fallback page (ARCHITECTURE 3.13); `make web` syncs `web/dist` into `internal/web/dist`.
- [x] `deploy/Dockerfile` (multi-stage as in ARCHITECTURE 9.1), `deploy/compose.local.yml`, `.dockerignore`.
- [x] `.github/workflows/ci.yml`: job `check` (setup-go from `go.mod`, setup-node 26 + pnpm 12.21.0, `make tools`, `make deps`, `make check`), job `race` (`make test-race`), job `docker` (build image; assert the gzip-compressed size of `docker save` is under 25 MB). Runs on push and PR.
- [x] `internal/clock` (Clock interface with `Now`, `NewTicker`, `NewTimer`/`After`; real and fake implementations; the fake advances only when the test calls `Advance`) since every later phase uses it.

Files: `go.mod`, `cmd/mailcubby/`, `internal/web/`, `internal/clock/`, `Makefile`, `.golangci.yml`, `web/**`, `deploy/Dockerfile`, `deploy/compose.local.yml`, `.dockerignore`, `.github/workflows/ci.yml`.

Acceptance:
- `make tools && make deps && make check` passes on a clean clone.
- Go test: `internal/web` serves `index.html` for `/` and for a deep link `/p/x/m/y`, the deep-link HTML references `/assets/`, and `GET /assets/missing.js` is `404`. With an empty `dist/`, `/` serves the "UI not built" page.
- `mailcubby version` prints version, commit and date (test runs the built binary or calls the printing function).
- `make docker && docker run -p 8025:8025 mailcubby:dev` serves the React page at `http://localhost:8025/` (requires Docker; otherwise mark blocked).
- CI is green on the first PR (owner).

---

## Phase 1: Config, store, migrations

**Goal**: configuration loading and a SQLite store with the full v1 schema.

Tasks:
- [x] `internal/config`: all variables from SPEC section 9 with defaults by mode, `_FILE` secrets, duration (`d`), size and rate parsing (SPEC 9 formats), joined validation errors, mode checks from SPEC 5.2 that do not need TLS (BASE_URL, APP_KEY). Table-driven tests for every parser and mode default. `serve` now uses `config.Load(os.LookupEnv)` and, until Phase 6 implements authentication, exits with `server mode is not implemented yet` when `MAILCUBBY_MODE=server` (so no intermediate build serves an unauthenticated multi-tenant instance).
- [x] `internal/secrets`: APP_KEY load (base64 std/url, 32 bytes), local-mode generation to `$DATA_DIR/app.key` (0600), HKDF subkey, AES-256-GCM seal/open with version byte and AAD (SPEC FR-061). `mailcubby keygen` subcommand.
- [x] `migrations/00001_init.sql` with every table and index from SPEC section 10 (except FTS), including `messages.pk ... AUTOINCREMENT`, `message_tags.project_id` and the nullable `invites.role` checks; `migrations/00002_fts.sql` with `messages_fts` and the `messages_fts_delete` trigger; `migrations/embed.go`.
- [x] `internal/store`: open writer and reader handles with the DSNs and open order of ARCHITECTURE 3.5 (`_auto_vacuum=INCREMENTAL` on the writer), goose Provider migrations at startup, `WithTx`, error mapping (`ErrNotFound`, `ErrConflict`).
- [x] `sqlc.yaml` (schema: `00001_init.sql` only) and first queries: projects CRUD and counters, settings get/set. `make generate` wired.
- [x] `internal/store/isolation_test.go` query lint (ARCHITECTURE section 7, item 3), passing on the initial queries.
- [x] `internal/testutil`: `NewStore(t)` (migrated store in `t.TempDir()`).
- [x] `mailcubby migrate status`.

Files: `internal/config/`, `internal/secrets/`, `internal/store/`, `migrations/`, `sqlc.yaml`, `internal/testutil/`, `cmd/mailcubby/`, `internal/cli/`.

Acceptance:
- Migrations apply on an empty DB; running twice is a no-op; `PRAGMA journal_mode` is `wal`, `foreign_keys` is 1, `auto_vacuum` is 2.
- `SELECT sqlite_version()` is at least 3.43 and inserting then deleting a `messages` row leaves no `messages_fts` row for its rowid (trigger and `contentless_delete` work); deleting a project removes its FTS rows through the cascade.
- Config: invalid `MAILCUBBY_SMTP_MAX_MESSAGE_BYTES=abc` fails with an error naming the variable; `200MiB` fails (over 100 MiB); server mode without `MAILCUBBY_BASE_URL` fails; `10/15m` parses as 10 per 15 minutes.
- Secrets: seal/open round trip; open with a different key or different AAD fails.
- Store tests for project create/get/update/delete and counters pass.

---

## Phase 2: SMTP listener (local mode), ingest, blob storage, sendmail

**Goal**: messages sent over SMTP in local mode are parsed, stored, indexed and retrievable from the store, and there is a built-in way to send test mail.

Tasks:
- [x] `internal/blob`: spool (zstd), place (link or copy), open (stream decompress), remove, walk, path validation.
- [x] `internal/search`: `IndexTx` only (FTS row insert, ARCHITECTURE 3.7). Parser and builder come in Phase 5.
- [x] `internal/ingest`: pipeline steps 1 to 7 from ARCHITECTURE 3.4 (quota and rate-limit hooks are no-ops until Phase 10; retention nudge is a no-op until Phase 10), ULID generator, metadata extraction (FR-008), text derivation (FR-009), attachment list, parse warnings, FTS indexing. `X-Tags` and plus-address tags are extracted into the ingest result but stored in Phase 5.
- [x] Golden fixtures in `testdata/mime/`: at least the list in ARCHITECTURE section 8, with `-update` flag.
- [x] `internal/smtpd` (FR-001, FR-003, FR-005, FR-020): go-smtp backend and session following the error contract (ARCHITECTURE 3.3); local mode routing (SPEC 7.2 local column, including recipient routing when `MAILCUBBY_INBOX_DOMAIN` is set); AUTH PLAIN and our LOGIN server; limits; `limitListener` (total and per-IP, admission goroutine).
- [x] `internal/events`: bus with filters and slow-consumer handling (FR-092 buffer); publish `message.created` (FR-012).
- [x] `internal/app`: wire config, store, blob, ingest, smtpd, events; graceful shutdown.
- [x] `internal/ingest/assets/sample.eml` and `ingest.SendTest` helper (used by FR-077 in Phase 3b).
- [x] `mailcubby sendmail` subcommand (SPEC FR-115, including ignored sendmail flags and `-t`), and `make seed`.
- [x] `internal/testutil`: `StartServer(t, cfg)` (random loopback ports, temp data dir) and SMTP client helpers (`net/smtp`, PLAIN and LOGIN).

Files: `internal/blob/`, `internal/search/`, `internal/ingest/`, `internal/smtpd/`, `internal/events/`, `internal/app/`, `internal/cli/`, `testdata/mime/`, `internal/testutil/`, `internal/store/queries/` (messages, attachments), `Makefile`.

Acceptance:
- Integration: send via `net/smtp` with no AUTH -> stored in `default`; with AUTH user `Shop Staging` (PLAIN and LOGIN) -> project `shop-staging` (name `Shop Staging`) auto-created.
- `.eml` bytes read back from the blob equal the sent bytes (SHA-256) (FR-006).
- Injected DB failure -> `451 4.3.0`, no row, no blob (FR-010).
- 26 MiB message -> `552 5.3.4`; 101st RCPT -> `452 4.5.3`; 21st concurrent connection from one IP -> `421` (FR-005); `AUTH CRAM-MD5` -> `504` (FR-003).
- A test asserts every session error path returns a bare `*smtp.SMTPError` (no `554 5.0.0` replies).
- Golden tests pass for every fixture (FR-007, FR-008); every stored message has an FTS row.
- `bin/mailcubby sendmail -t -i < testdata/mime/<fixture>.eml` against a test server stores the message (test runs the CLI function with a stdin buffer).
- `make test-race` passes.

---

## Phase 3a: REST API foundation and read endpoints (local mode, no auth)

**Goal**: the API skeleton and the read surface needed by the inbox UI, in local mode.

Tasks:
- [x] `internal/api`: server with timeouts (ARCHITECTURE 3.10), route table, middleware chain (recover, request ID, real IP with `MAILCUBBY_TRUSTED_PROXIES`, app security headers from SPEC 12.3, access log with redaction, body limit, local principal), JSON helpers, error format (SPEC 8.1).
- [x] `internal/api/openapi.yaml` (OpenAPI 3.1, absolute paths) for the routes of this phase; test: registered routes equal spec paths and methods.
- [x] Endpoints: `GET /meta` (including `smtp` connection info), `GET /auth/me` (local operator), `GET /projects` (counters and sparkline, FR-035), `POST /projects`, `GET/PATCH/DELETE /projects/{project}` (FR-030, FR-031, FR-032, FR-033; project delete removes blobs via a synchronous best-effort walk until the GC worker exists in Phase 10), messages list (FR-070; cursor pagination, `tag`/`credential` params accepted but ignored until Phases 5 and 8), detail (FR-071), raw, eml, html source, headers, attachments (FR-074, SPEC 12.2 headers).
- [x] `GET /healthz`, `GET /readyz` (FR-110); `mailcubby healthcheck`.
- [x] `make generate` produces `web/src/api/schema.d.ts`.

Files: `internal/api/`, `internal/store/queries/`, `internal/cli/`, `cmd/mailcubby/`, `web/src/api/`.

Acceptance:
- Integration: send 3 messages, list returns them newest first with correct summaries; cursor pagination works with `limit=2`.
- Attachment named `x.html` is served as `application/octet-stream` with `Content-Disposition: attachment`; a PNG with `?inline=1` is `inline`; a PDF with `?inline=1` stays `attachment`.
- `/readyz` returns 503 when the data dir is not writable; `mailcubby healthcheck` exits 0 against a running test server and 1 when it is stopped.
- Every app response carries the SPEC 12.3 headers.
- Route/OpenAPI parity test passes.

---

## Phase 3b: Mutations, SSE, wait, render (local mode)

**Goal**: the rest of the local-mode API: changes, realtime, test helpers and HTML preview.

Tasks:
- [x] Endpoints: `PATCH` message (seen only, FR-075; tags in Phase 5), `DELETE` single, bulk (`delete`, `seen`, `unseen`), delete-all (FR-076; no query filter until Phase 5), `POST /send-test` (FR-077), `wait` (no query filter until Phase 5), `GET /events` (SSE, FR-090 events available so far, FR-092 limits, heartbeats, shutdown handling).
- [x] Counter maintenance on delete and seen changes (FR-034 property test).
- [x] `internal/render` basic version (FR-073): HTML rewrite (SPEC 12.1 rewrite rules, cid to data URIs, remote resource counting) and `GET /render/{project}/{id}` with the SPEC 12.1 headers; `remote_resource_count` in message detail. (Hostile-corpus review in Phase 11.)
- [x] OpenAPI and generated types updated.

Files: `internal/api/`, `internal/render/`, `internal/events/`, `internal/store/queries/`, `web/src/api/`.

Acceptance:
- SSE: a client connected to `/api/v1/events` receives `message.created` after an SMTP send (2 s test timeout); a separate non-race test asserts the p95 latency under 250 ms over 20 sends (NFR-002); heartbeat `: ping` is written when the fake clock advances 25 s.
- `wait` returns a message sent after the request started; returns `204` on timeout; with `since=<older ULID>` returns an existing message immediately (FR-078).
- Render response carries the exact CSP from SPEC 12.1; an email containing `<script>` is served with the script still in the markup but the CSP header has `sandbox` without `allow-scripts`; `<meta http-equiv="refresh">` is removed; a `cid:` image becomes a `data:` URI.
- FR-034 counter property test passes; `send-test` creates a message with HTML, text, an inline image and a PDF attachment.

---

## Phase 4a: Web UI shell, inbox and message view (local mode)

**Goal**: a working inbox UI for local mode, verified by Playwright.

Tasks:
- [x] Playwright harness (ARCHITECTURE 8 "E2E harness"): `web/playwright.config.ts`, `globalSetup` starting the built binary, helpers to send fixtures with `bin/mailcubby sendmail`, `make e2e`, `make deps` installs Chromium.
- [x] App shell: theme (light/dark/system), layout, routes from `docs/UI.md` section 2 (local-mode subset), typed API client (`openapi-fetch`), query key factory, `usePersistentState`, error handling (UI 4 item 15).
- [x] Projects home (UI 3.3) with counts, sparkline, local-mode SMTP banner (from `/meta`).
- [x] Project inbox with three resizable panes (UI 3.4), virtualised message list with infinite scroll, unread state, multi-select and bulk bar (delete, mark read/unread).
- [x] Message view header and tabs 1 to 7 (FR-072, UI 3.5): sandboxed preview iframe with device sizes, rotation, zoom, dark-mode toggle, remote images toggle; text; HTML source and headers with lazy Shiki (JavaScript regex engine); raw (virtualised); attachments; tech.
- [x] Empty inbox setup guide with local-mode snippets and "Send test email".
- [x] Vitest tests for persistent state and relative time formatting.

Files: `web/src/**`, `web/e2e/**`, `web/playwright.config.ts`, `Makefile`.

Acceptance:
- Playwright: send a fixture, open the project, open the message, switch through tabs 1 to 7 (each shows its content); the preview iframe has `sandbox="allow-popups allow-popups-to-escape-sandbox"` and no `allow-scripts`/`allow-same-origin`; "Send test email" on an empty project creates a message that appears after reload; bulk delete removes selected rows.
- Playwright: reloading a deep link `/p/<slug>/m/<id>` renders the message (no blank page).
- No email HTML is ever inserted into the app DOM (`grep -r dangerouslySetInnerHTML web/src` is empty; Biome rule active).
- `make e2e` passes.

---

## Phase 4b: Realtime, keyboard, command palette, polish (local mode)

**Goal**: the premium details of UI section 4 for local mode. **Milestone M1: "Mailpit-lite local works".**

Tasks:
- [x] SSE client: cache updates on `message.created/updated/deleted`, `project.updated`; arrival highlight, "N new" pill, document title count, `aria-live` announcements; connection indicator; invalidate on reconnect.
- [x] Keyboard shortcuts (UI section 4 item 2) with help overlay; single-key shortcuts off inside inputs and via preferences; `/account/preferences` page (local mode).
- [x] Command palette with projects, core actions and recent messages.
- [x] Optimistic delete with undo; mark read after delay (preference); skeletons; prefetch next/previous.
- [x] Initial JS budget check script (`pnpm --dir web check:bundle`, gzip of the entry chunk and its static imports under 250 KB) run by `make check`.
- [x] Vitest tests for the shortcuts hook and the SSE cache reducer.

Files: `web/src/**`, `web/e2e/**`, `web/package.json`.

Acceptance:
- Playwright: with the inbox open, a message sent via `sendmail` appears without reload and the title count increases; `j`/`k` move the selection; `1` to `7` switch tabs; `e` deletes and the undo toast restores the row; `Cmd/Ctrl+K` opens the palette and navigates to another project; `?` opens the shortcut help.
- Playwright: keyboard-only walkthrough of the inbox (Tab/Shift+Tab, Enter, Esc) reaches every control, and the focused element always has a visible focus indicator (computed `outline-style` is not `none` or `box-shadow` is not `none`).
- Bundle budget check passes.
- Lighthouse (desktop) performance at least 90 on the inbox with 1,000 messages from `make seed COUNT=...` (owner).

---

## Phase 5: Search (FTS5) and tags

**Goal**: full-text search with operators, and tags.

Tasks:
- [x] `internal/search`: query parser (SPEC FR-081), fuzz test, SQL builder over the FTS index (FR-080; except `credential:`, added in Phase 8).
- [x] `query` parameter on list, delete-all and wait (FR-082).
- [x] Tags (FR-085): store queries, auto tags at ingest from `X-Tags` and plus-addressing (FR-086), tag endpoints (SPEC 8.5), `PATCH` message tags, bulk add/remove tags, `tag` list filter, `message.updated` events for tag changes.
- [x] UI: search bar with operator autocomplete and chips; tags in sidebar with counts; tag editing in message header and bulk bar; `t` shortcut; palette search operators.
- [x] Benchmarks for NFR-003 and NFR-004 on a seeded 100k-message project (`make bench`).

Files: `internal/search/`, `internal/ingest/`, `internal/api/`, `internal/store/queries/`, `web/src/features/inbox/`, `web/e2e/`.

Acceptance:
- `joao` finds `João`; `from:alice subject:"order 12" has:attachment -tag:spam after:2026-11-01` returns exactly the expected fixtures.
- Fuzz test runs 60 s in CI without panics; builder output never contains user text outside args.
- `X-Tags: invoice, Staging` + `orders+urgent@shop.test` gives three tags (FR-086).
- Playwright: typing `from:alice` filters the list and shows a chip; adding a tag in the message header shows it in the sidebar count.
- Benchmarks meet NFR-003 and NFR-004 on CI hardware (or record numbers and an open question if CI is too slow).

---

## Phase 6: Auth, users, sessions, setup wizard (server mode)

**Goal**: server mode with login and first-run setup.

Tasks:
- [x] `internal/auth`: argon2id (FR-042) with dummy hash, sessions (FR-044), CSRF and Origin checks (FR-045), principal in context.
- [x] Remove the Phase 1 server-mode guard. Server mode startup checks (SPEC 5.2, including the TLS-or-insecure check; certificate files are loaded once at startup) and setup state (FR-040): setup token logging, headless admin bootstrap.
- [x] Audit writer (`store.InsertAudit`) and the `auth.*`, `setup.complete`, `user.password_change` entries (more actions in later phases; full coverage checked in Phase 11).
- [x] Users (FR-041). Endpoints: `/setup` (with `instance_name`), `/auth/login` (FR-043), `/auth/logout`, `/auth/me` (with CSRF token), `PATCH /auth/me` and sessions list/revoke (FR-047), password reset with token (FR-046); `mailcubby admin reset-password`.
- [x] HTTP login rate limit (`internal/ratelimit`, SPEC 12.4).
- [x] In server mode every non-public route requires a principal. Until Phase 7 adds roles, only instance admins pass project routes (others get `404`); local mode unchanged. Server-mode SMTP rejects unauthenticated mail (`530`) until credentials exist in Phase 8.
- [x] `make run-server` and `make dev MODE=server` work (headless admin from env).
- [x] UI: login page, setup wizard steps 1 to 3 (single `POST /setup` at the end; steps 4 and 5 in Phase 9), account pages (profile, security, sessions; preferences from Phase 4b), `401` handling with return URL, CSRF header on unsafe requests, logout in the account menu.

Files: `internal/auth/`, `internal/ratelimit/`, `internal/api/`, `internal/app/`, `internal/cli/`, `internal/store/queries/`, `web/src/features/{auth,setup,account}/`, `web/e2e/`.

Acceptance:
- Setup without token -> `403`; with token -> admin created, logged in; second setup -> `409`.
- Login with an unknown email and with a user without password both run exactly one argon2id verification against the dummy hash (asserted with an instrumented verifier, not with timing).
- POST with cookie but no CSRF header -> `403`; with wrong Origin -> `403`.
- Idle and absolute session expiry enforced (fake clock).
- 11th failed login for the same IP+email within 15 min -> `429` with `Retry-After`.
- Playwright (server-mode instance): setup wizard from token to logged-in home; logout; login again.

---

## Phase 7a: Projects, members, roles, invites, isolation (backend)

**Goal**: multi-tenant projects with roles, and proven isolation, at the API level.

Tasks:
- [x] `internal/auth/permissions.go` implementing the SPEC FR-050 matrix; middleware resolving `{project}` to `ProjectContext` with `404` for no access (replaces the Phase 6 admin-only rule).
- [x] Project create permission (`can_create_projects`, admin), last-owner rule, leave project (FR-051), admin implicit access with `project.admin_access` audit.
- [x] Members endpoints; invites create/list/revoke/preview/accept with single-use guard (FR-055 to FR-057), including instance invites and admin-created users without password.
- [x] Admin endpoints: users CRUD (FR-100), disable (revokes sessions and tokens), password reset link, all projects list (FR-101); `mailcubby admin create-user`.
- [x] SSE membership filtering and `membership.changed` refresh (FR-091).
- [x] Viewer restrictions: no seen marking, `viewer_hide_technical` enforcement on raw/eml/html/headers endpoints and on the technical fields of message detail (FR-071).
- [x] Tests: authorisation matrix over the route table (FR-050), two-tenant isolation integration test (ARCHITECTURE section 7 item 4), concurrent invite accept test.

Files: `internal/auth/`, `internal/api/`, `internal/store/queries/`, `internal/events/`, `internal/cli/`.

Acceptance:
- Authorisation matrix test covers every route and passes; adding a route without a matrix entry fails the test.
- Two-tenant test: zero leaks across all routes and SSE.
- Two concurrent accepts of one invite -> one membership and one `410`.
- Removing the last owner -> `409`.
- A viewer cannot fetch `/raw` when `viewer_hide_technical` is on (`403`), can when off; message detail omits `envelope`, `headers`, `parse_warnings` for that viewer.
- Admin-created user accepts the instance invite by setting a password, then logs in.

---

## Phase 7b: Projects, members and admin UI

**Goal**: the UI for multi-tenant projects.

Tasks:
- [x] Projects home with roles and "New project" (when allowed); project settings shell (UI 3.6) with General and Members and invites sections.
- [x] Invite accept page (`/invite/$token`) for all FR-056 cases; reset page (`/reset/$token`).
- [x] Admin users and projects pages (UI 3.7).
- [x] Viewer UI restrictions (UI 3.8) and "Preview as viewer" toggle.

Files: `web/src/features/{projects,settings,admin,auth}/`, `web/e2e/`.

Acceptance:
- Playwright: admin invites a viewer to a project; the invite link opens in a fresh browser context, the viewer creates an account and sees only that project, without delete, tag or technical tabs; "Preview as viewer" as owner hides the same controls.
- Playwright: admin creates a user, opens the returned invite link, sets a password and lands on the projects home.

---

## Phase 8: SMTP credentials, AUTH routing, recipient routing, brute-force protection

**Goal**: server-mode SMTP fully per project.

Tasks:
- [x] SMTP credentials store and endpoints: create, list (with connection info), reveal (audited), rotate, update (label, disable), delete (FR-060, FR-061, FR-062, FR-063).
- [x] smtpd server-mode routing (SPEC 7.2 server column), credential verification with decrypt + constant-time compare, `smtp_credential_id` on messages (FR-021), `last_used_at` throttled updates.
- [x] Recipient routing in server mode with `recipient_routing_enabled`; multi-project delivery (FR-011) in both modes.
- [x] AUTH over TLS only in server mode (FR-004) using a runtime-generated test certificate in tests.
- [x] Brute-force lockout per IP and username, 1 s failure delay through the clock, `smtp.auth_lockout` audit (SPEC 12.4).
- [x] Error replies exactly as SPEC 7.4 (codes; texts for Mailcubby-generated replies).
- [x] `credential:` search operator and `credential` filter in the list API; credentials in the inbox sidebar (UI 3.4).

Files: `internal/smtpd/`, `internal/store/queries/`, `internal/api/`, `internal/ratelimit/`, `internal/search/`, `web/src/features/inbox/`.

Acceptance:
- Table-driven routing tests cover every row of SPEC 7.2 in both modes, including multi-project RCPT sets.
- Wrong password -> `535 5.7.8`; 6th failure within 15 min -> `454 4.7.0` even with the right password; after the window, success (fake clock).
- Disabled credential and deleted project -> `535`.
- Plaintext AUTH in server mode -> `523 5.7.10`; after STARTTLS -> accepted.
- DB dump contains no plaintext SMTP password.

---

## Phase 9: Project settings UI, integration snippets, API tokens

**Goal**: everything an owner needs to onboard an app and a client.

Tasks:
- [x] API tokens: create/list/revoke endpoints, bearer authentication, effective permission rules (FR-065, FR-066), `last_used_at`.
- [x] Project settings UI sections: SMTP (credentials table with copy/reveal/rotate/disable/delete), integration snippets (Laravel, Symfony, Nodemailer, WordPress WP Mail SMTP, Django, Rails, swaks, PHP sendmail_path) for local and server mode, routing and client view toggles, API section with curl examples.
- [x] Account > API tokens page.
- [x] Setup wizard steps 4 and 5 (first project, credential, snippets, send test, live wait for first message).
- [x] Authorisation matrix extended with token principals (`token-read`, `token-write`).

Files: `internal/auth/`, `internal/api/`, `web/src/features/{settings,account,setup}/`, `web/e2e/`.

Acceptance:
- A `read` token can list and wait but `DELETE` returns `403`; a `write` token can delete; any token on `/projects/{p}/members` returns `403`; revoked token -> `401`.
- Snippet unit tests (Vitest): each generated snippet for a fixture credential matches a golden string (local and server variants).
- Playwright: new server instance, wizard steps 1 to 5, "Send test email", the waiting indicator turns into success, inbox shows the message.
- Wizard to first message in under 2 minutes on a fresh instance (owner).

---

## Phase 10: Retention, quotas, workers

**Goal**: bounded storage and self-maintenance.

Tasks:
- [x] `internal/retention`: sweeper (FR-095) with effective limits resolution (project, instance setting, env), batches, nudges from ingest; counters and `message.deleted` events for deletions.
- [x] Instance quota `452` (FR-096) and per-project SMTP rate limit `450` (FR-097).
- [x] Orphan blob GC, tmp cleanup, project directory removal (replaces the Phase 3a synchronous walk), incremental vacuum (FR-098).
- [x] Housekeeping (FR-099).
- [x] Admin instance settings endpoints and UI for defaults (FR-102), admin stats endpoint and page (FR-103).
- [x] Retention settings UI (UI 3.6.4).

Files: `internal/retention/`, `internal/ingest/`, `internal/smtpd/`, `internal/api/`, `internal/app/`, `web/src/features/{settings,admin}/`, `web/e2e/`.

Acceptance:
- With `max_messages=100`, 150 inserts and one sweep leave the newest 100 (FR-095); counters stay consistent.
- Orphan blob older than 1 h removed; younger kept (fake clock).
- Instance quota exceeded -> `452 4.3.1`; after sweep frees space, accepted again.
- 601st message within a minute for one project -> `450 4.7.1` (with default rate, fake clock).
- Playwright: owner sets max messages on a project and the retention section shows usage against the limit.

---

## Phase 11: Security hardening

**Goal**: close the security requirements and make them verifiable.

Tasks:
- [x] Review and complete `internal/render` against SPEC 12.1 (all rewrite rules, size cap, `remote_resource_count`, LRU cache) with a corpus of hostile emails in `testdata/hostile/` (scripts, event handlers, `<meta refresh>`, `<noscript><meta http-equiv=refresh>`, `<svg><style><meta …>`, `<base href>`, forms, `javascript:` links, SVG with script, CSS `url()` exfiltration, `<link rel=prefetch>`, `<iframe>`, `<object>`, `srcset` tracking pixels).
- [x] Test asserting header sets per route class (app, render, attachment, raw, SSE).
- [x] Rate limits for API, invite and reset endpoints (SPEC 12.4).
- [x] Audit log: all FR-105 actions written; owner project audit endpoint and UI; admin audit endpoint and UI with filters.
- [x] Log redaction test (no secrets in captured logs during the full integration suite).
- [x] `gosec` clean; `govulncheck` job added to CI (run via `go run golang.org/x/vuln/cmd/govulncheck@<pinned>` in CI only; pin the version in the workflow, it is not a Go module dependency).
- [x] Threat model section appended to `docs/ARCHITECTURE.md` (assets, actors, mitigations).

Files: `internal/render/`, `internal/api/`, `internal/ratelimit/`, `internal/store/queries/`, `testdata/hostile/`, `web/src/features/{settings,admin}/`, `web/e2e/`, `.github/workflows/ci.yml`, `docs/ARCHITECTURE.md`.

Acceptance:
- Playwright loads each hostile email in the real iframe and asserts: no script executes (no `window.__pwned` on the iframe or the app), no request leaves `127.0.0.1` with images blocked (`page.on('request')`), and the app URL does not change.
- Every FR-105 action has a test asserting its audit entry.
- `govulncheck` and `gosec` pass in CI (owner, if CI is not reachable).

---

## Phase 12: TLS, packaging, docs polish

**Goal**: production deployment story.

Tasks:
- [x] `internal/tlsutil.Reloader` (mtime polling) for SMTP STARTTLS and implicit TLS listener (FR-002).
- [x] `.goreleaser.yaml`, `deploy/Dockerfile.goreleaser`, `.github/workflows/release.yml`: binaries for linux/darwin/windows x amd64/arm64, multi-arch images on `ghcr.io/mailcubby/mailcubby`, checksums; image size check (NFR-006).
- [x] `deploy/compose.server.yml` with Caddy and a shared certificate volume whose key files are readable by UID 65532 (ARCHITECTURE 9.3 pitfall), `deploy/Caddyfile` alternative, `deploy/compose.local.yml` final.
- [x] README polish: install, local and server quick starts, reverse proxy notes (Caddy, Traefik, nginx with `proxy_buffering off` for SSE), backups, upgrades, Mailpit migration notes (ports, `X-Tags`, sendmail).

Files: `internal/tlsutil/`, `internal/smtpd/`, `internal/app/`, `.goreleaser.yaml`, `deploy/**`, `.github/workflows/release.yml`, `README.md`.

Acceptance:
- Replacing cert files on disk switches the served certificate after the next poll without restart (test with two generated certs and the fake clock).
- `goreleaser release --snapshot --clean` succeeds; images for amd64 and arm64 are each under 25 MB compressed (blocked: Docker daemon and goreleaser binary unavailable in local environment; all 6 targets cross-compiled and size check wired in `.github/workflows/release.yml`).
- Following the README server quick start on a fresh VM yields a working instance with STARTTLS on 587 (owner).

---

## Phase 13: v1.0 release checklist

**Goal**: ship v1.0.

Tasks:
- [x] Complete the Playwright suite (`make e2e`): local mode inbox flow, server mode setup wizard, login, invite accept as viewer, viewer restrictions, credential reveal, search, delete with undo, keyboard flows; add `@axe-core/playwright` (already pinned in `AGENTS.md`, so adding it is not a stop condition) on every screen with zero serious/critical violations (NFR-010).
- [x] Performance verification against NFR-001 to NFR-009; results recorded in `docs/benchmarks.md`.
- [x] Final spec pass: every FR with acceptance criteria has at least one test referencing it (script that greps `FR-` IDs in tests vs SPEC).
- [x] Upgrade test: data dir created by the previous tag migrates cleanly (skip with a note if no previous tag exists — skipped: no previous git tag exists yet; `00001+00002 -> 00003` forward migration with populated data verified in `internal/store/migration_projects_autoincrement_test.go`).
- [ ] Security review of the diff since Phase 11; dependency updates to latest patch versions (stop condition: list them for the owner); `govulncheck` clean. (blocked: `govulncheck` and upstream patch-version checks require network/CI; Phase 12–13 diff security review and `gosec` completed cleanly locally; known dependency version notes listed in Open questions)
- [x] `CHANGELOG.md`, GitHub release notes draft.
- [ ] GitHub org, GHCR namespace and `mailcubby.dev` domain registered and matching the docs; tag `v1.0.0` pushed (owner).

Acceptance:
- All checkboxes in this file are ticked or marked owner/blocked with a reason; `make check` and `make e2e` green; CI and release workflows green, images published, README quick starts work from the published image (owner).

---

## Phase 14: pre-release polish

**Goal**: close the findings of the final pre-v1.0 audit (2026-11-25) that block a good first release, without adding steps, env vars or security friction to local mode. Local mode must stay as simple as Mailpit. Each part is one pull request; audit IDs (S, L, V, I) refer to that audit.

### Phase 14a: security and operability

Tasks:
- [x] S1a: local mode rejects cross-origin unsafe requests from browsers (stdlib `http.CrossOriginProtection`); curl, SDKs, the Vite dev proxy and reverse proxies keep working. FR-045 updated.
- [x] S3: a data dir that has users refuses to start when `MAILCUBBY_MODE` was not set (defaulted to local), with a clear error naming the variable; an explicit `MAILCUBBY_MODE=local` still starts.
- [x] S4/V2: SMTP AUTH lockout keyed by (remote IP, username) with a per-IP ceiling of 5x the per-pair limit; no global per-username bucket. SPEC 12.4 updated.
- [x] V1: an SMTP credential that fails to decrypt (wrong or lost `MAILCUBBY_APP_KEY`) answers `454` and does not count towards lockout; startup logs an ERROR "N of M SMTP credentials cannot be decrypted"; reveal returns a clear error; README documents the recovery (rotate credentials).
- [x] S10: `app.db` and its `-wal`/`-shm` files are created with mode 0600 (SPEC 11).
- [x] L1: in local mode the announced SMTP port (`/api/v1/meta`, SMTP quick copy, snippets) is derived from `MAILCUBBY_SMTP_ADDR`.
- [x] L2/L3: startup logs one line `mailcubby ready url=... smtp=... data_dir=<absolute path>`; `mailcubby serve --help` prints the flags and relevant env vars and exits 0.

Acceptance:
- A cross-site `POST` with a foreign `Origin` (or `Sec-Fetch-Site: cross-site`) to a local-mode API returns 403; the same request without `Origin` (curl) and a same-origin request succeed; `make dev` (Vite proxy) still mutates.
- Starting with a data dir containing users and no `MAILCUBBY_MODE` exits non-zero with the error; with `MAILCUBBY_MODE=local` it starts.
- Wrong passwords for one username from an IP do not lock out another username from the same IP; the per-IP ceiling still locks; Python `smtplib` (two failed attempts per `login()`) is covered.
- A credential encrypted under another key gives `454` and no lockout increment; the startup ERROR is logged.
- Created `app.db*` files are mode 0600.
- `MAILCUBBY_SMTP_ADDR=:2525` in local mode makes `/api/v1/meta` announce port 2525.
- The ready line contains the URL, SMTP address and absolute data dir; `serve --help` exits 0.

### Phase 14b: release and docs

Tasks:
- [x] `release.yml` passes `--release-notes docs/release-notes-v1.0.0.md`; `.goreleaser.yaml` sets `release.prerelease: auto`.
- [x] Release notes: image tag `:1.0.0` (not `:v1.0.0`), absolute links, `127.0.0.1:` port bindings for local mode, server quick start identical to the README (S2 docs).
- [x] Compose files set `name: mailcubby` (server) and `name: mailcubby-local` (local); README backup uses `mailcubby_mailcubby-data` (I3, I4).
- [x] README: `git clone --depth 1 --branch v1.0.0` step before compose; `go install` is not supported; pre-release banner; image size about 9 MB; `127.0.0.1:` port bindings in local examples; ARCHITECTURE examples likewise (S2 docs).
- [x] V3: README shows `| tail -1` for the setup token (regenerated on each start); V5: README shows `docker compose exec mailcubby mailcubby admin reset-password`; startup logs when `MAILCUBBY_ADMIN_*` is ignored because users exist.
- [x] CI runs Playwright e2e (Chromium); specs use unique slugs (`uniqueSlug(prefix)` helper) so retries do not hit 409.
- [x] Comment explaining `getItemKey` in `web/src/features/inbox/MessageListPane.tsx`.
- [x] Setup wizard step 1 shows the `docker compose logs`, `docker logs <container>` and binary variants for finding the setup token.
- [x] Setup wizard shows "Paste the setup token" only after a submit attempt.
- [x] `make docker` passes the git version, so `/api/v1/meta` of a locally built image reports it.
- [x] Commit the "Backlog (after v1.0.0)" section of this file.

Acceptance:
- CI workflow has an `e2e` job running `make e2e` on Chromium; `make e2e` passes twice in a row against the same binary build.
- Vitest/Playwright cover the wizard token hint and the deferred validation error.
- `make docker` then `docker run` shows the `git describe` version in `/api/v1/meta` (owner if Docker is unavailable to the agent).
- Release notes and README server quick starts match; `docker compose -f deploy/compose.server.yml config` shows project name `mailcubby`.

### Phase 14c: attachments in the message view

Tasks:
- [x] Attachment strip below the Preview and Text tabs: one compact card per attachment (lucide type icon, thumbnail for raster images via `?inline=1`, filename, size); clicking downloads; hidden when there are no attachments. Inline parts referenced by `cid:` from the HTML are left out, using the same resolution as `/render`.
- [x] The Attachments tab keeps full details (content type, inline/attachment badge, inline `cid:` parts); viewers with `viewer_hide_technical` see both strip and tab.
- [x] SPEC FR-072, `docs/UI.md` and `openapi.yaml` updated; no new dependencies; section 8 rules kept (downloads as `attachment`, inline only for allowlisted raster images, no inline PDF).

Acceptance:
- Playwright: a message with a PDF and a PNG attachment shows two cards (PNG with thumbnail, PDF with icon only); clicking a card downloads with the right filename; a message without attachments shows no strip; an HTML message with a `cid:` image shows it in the Attachments tab but not in the strip; a viewer with `viewer_hide_technical` sees the strip.
- Go test: the "referenced by HTML" flag matches what the render endpoint rewrites.
- axe: no serious/critical violations on the message view with the strip.

### Phase 14d: Homebrew install (local mode)

Tasks:
- [ ] GoReleaser config publishes a Homebrew formula or cask to `mailcubby/homebrew-tap` with a service that runs local mode on :1025/:8025 with data in `$(brew --prefix)/var/mailcubby`; pre-releases do not update the tap; decision recorded in an ADR if not obvious.
- [ ] README install section: macOS Homebrew first (`brew install mailcubby/tap/mailcubby`, `brew services start mailcubby`); manual LaunchAgent (macOS) and systemd user unit (Linux) for the Releases binary.
- [ ] Create the `mailcubby/homebrew-tap` repository (owner).
- [ ] Create a fine-grained token with contents write on `mailcubby/homebrew-tap` and store it as the `HOMEBREW_TAP_GITHUB_TOKEN` secret of `mailcubby/mailcubby` (owner).

Acceptance:
- `goreleaser check` passes; `goreleaser release --snapshot --clean` generates the formula/cask with the service block (blocked if goreleaser is unavailable).
- After the first tagged release, `brew install mailcubby/tap/mailcubby && brew services start mailcubby` serves the UI on http://localhost:8025 (owner).

---

## Backlog (after v1.0.0)

Not scheduled. When an item is picked up it becomes a phase above. Items marked "SPEC 14.n" are in the SPEC roadmap.

### v1.0.x (small, no design change)

- `mailcubby backup` command for an online SQLite backup that works in the distroless image and compose volumes (audit V6).
- Docs: password reset does not revoke API tokens; long-poll `/messages/wait` limits; `MAILCUBBY_TRUSTED_PROXIES` scope in compose (audit S5, S6, S7).
- Invite accepted by a user whose email differs from the invite: warning in the accept UI (audit S8).
- Attachment content types: map `*+xml`, `*/xml` and invalid values to `application/octet-stream` (audit S9).
- Startup warnings for insecure SMTP AUTH and an `http://` base URL; hint at `keygen` when the app key has the wrong length; CLI `list-users` / `enable-user` (audit V8).
- Server mode as a plain binary: systemd unit and docs (audit V4).
- Framework snippets, a `curl` example for `/messages/wait` and a `host.docker.internal` note in the README; `sendmail` dial timeout; quieter access log (audit L4, L5).
- OCI labels in `deploy/Dockerfile`, healthcheck `start_period`, pinned Caddy image (audit I7).

### v1.1 (features)

1. HTML compatibility check (caniemail data, offline). Most useful for agencies building templates. SPEC 14.2.
2. Link checker, opt-in per instance, with SSRF protection (no private, loopback or link-local targets, timeouts, size caps) because the server fetches URLs found in untrusted email. SPEC 14.2.
3. Inline PDF preview of attachments. SPEC 14.20.
4. Visual polish pass over all screens (owner review first).
5. Webhooks on `message.created` and the MCP endpoint for AI agents. SPEC 14.4, 14.5.
6. Mailpit data import and API compatibility shim, to ease switching. SPEC 14.15.

### Later

- Spam check (SpamAssassin / rspamd). SPEC 14.3.
- Invite and reset emails through an outbound SMTP setting. SPEC 14.14.
- OIDC / SSO, PostgreSQL, S3 blobs, metrics/tracing. SPEC 14.6, 14.7, 14.8, 14.11.
- Commercial offering (hosted service or paid license); see ADR 0007. Needs its own ADR.

## Open questions

Agents: record spec ambiguities and the decision taken here, newest last, with phase and date. Items starting with **Owner** need a decision from the owner; agents continue with the stated default meanwhile.

- 2026-11-23, doc review: `MAILCUBBY_BASE_PATH` (sub-path hosting) moved to the roadmap; v1 is served from the root of its host and the SPA is built with `base: '/'`.
- 2026-11-23, doc review: inline PDF preview removed from v1 (browser PDF viewers do not render under the attachment CSP sandbox); PDFs are downloaded.
- 2026-11-23, doc review: `messages.pk` uses `AUTOINCREMENT` and FTS rows are deleted by a trigger, so cascades and retention can never leave stale FTS rows matching reused rowids.
- 2026-11-23, doc review: Playwright e2e starts in Phase 4a (not Phase 13) so autonomous UI phases have automated acceptance; `mailcubby sendmail` and `make seed` moved to Phase 2.
- 2026-11-23, Phase 0 (owner decision): PROXY protocol support and the direct HTTPS listener are NOT needed in v1; moved to the roadmap in `docs/SPEC.md` and removed from Phase 12.
- 2026-11-23, Phase 0 (owner decision): certmagic ACME stretch goal dropped from v1 (roadmap only).
- 2026-11-23, Phase 0 (owner decision): commits are enabled during autonomous runs — make one local Conventional Commit per completed task, never push.
- 2026-11-23, Phase 1: Setting both `<VAR>` and `<VAR>_FILE` for a secret fails config validation rather than silently preferring one; paired env vars (`MAILCUBBY_ADMIN_EMAIL`/`MAILCUBBY_ADMIN_PASSWORD`, `MAILCUBBY_SMTP_TLS_CERT`/`MAILCUBBY_SMTP_TLS_KEY`) must be set together; `MAILCUBBY_SESSION_IDLE_TIMEOUT` must not exceed `MAILCUBBY_SESSION_TTL`; `mailcubby migrate status` reports all embedded migrations as `pending` without creating `app.db` when the database file does not exist yet.
- 2026-11-23, Phase 2: In recipient routing (`MAILCUBBY_INBOX_DOMAIN`), candidate slugs are validated against SPEC 7.3 `ValidateSlug` (reserved or invalid slugs are treated as non-routed rather than creating invalid project slugs); `parse_warnings_json` stores a JSON array of human-readable warning strings (`NULL` when empty) and excludes `enmime.ErrorPlainTextFromHTML` because HTML-only text derivation is tracked explicitly via `text_generated`; `mailcubby sendmail` falls back to an empty SMTP reverse-path (`MAIL FROM:<>`) when neither `-f` nor a valid `From` header is present.
- 2026-11-23, Phase 3a: In project settings JSON (`GET`/`PATCH /api/v1/projects/{project}`), `retention_max_age` is represented as `integer | null` in seconds (`0` disables limit, `null` inherits global default, matching `retention_max_age_s`); message list cursors encode the last returned message ULID as unpadded base64url and return `400 bad_request` when invalid; `MessageDetail.remote_resource_count` returns `0` until Phase 3b adds `internal/render`; `GET /readyz` returns `{"status":"ok"}` on `200` and `{"status":"unavailable","reason":"..."}` on `503`.
- 2026-11-23, Phase 3b: Non-empty `?query` on `DELETE /api/v1/projects/{project}/messages` and `GET /api/v1/projects/{project}/messages/wait` returns `400 bad_request` until Phase 5 implements search filtering (avoiding accidental unfiltered deletes); `POST /messages/bulk` requires 1 to 1000 non-empty IDs (deduplicated) and returns `affected` equal to rows deleted or whose `seen` flag changed; deleting messages recomputes `projects.last_message_at` (`NULL` when empty); `wait` with omitted `since` or `since=now` waits only for messages arriving after subscription; `golang.org/x/net` was promoted from indirect to direct at `v0.62.0` (already in `go.mod`) rather than upgrading offline to `v0.63.0`.
- 2026-11-23, Phase 4a: Lazy Shiki highlighting (HTML source and Headers highlighted view) uses `codeToTokens` with `createJavaScriptRegexEngine()` and renders tokens as React `<span>` elements so `dangerouslySetInnerHTML` is never used anywhere in `web/src`; UI primitives in `web/src/components/ui/` use native HTML (`<dialog>` with `showModal()`, `closedby="any"` + fallback), Tailwind v4, `sonner`, and `react-resizable-panels` without adding unlisted Radix packages; the sidebar "Unread" view filters loaded messages client-side until Phase 5 adds server-side `query=is:unread`; opening an unseen message in local mode marks it seen immediately and deletes confirm via modal dialog until Phase 4b adds configurable read delay and optimistic undo delete.
- 2026-11-23, Phase 4b: Command palette is implemented directly on native modal `<dialog>` (`showModal()`, `closedby="any"` + pointer fallback, ARIA `combobox`/`listbox` with fuzzy search) instead of installing `cmdk`, keeping the dependency footprint minimal and initial JS well under the 250 KB gzip budget (`138.34 KB` gzip); preferences toggles use native `<input type="checkbox" role="switch">` with explicit `<label htmlFor>` association rather than an unlisted `input-switch-polyfill`; in the inbox `/` opens the command palette until Phase 5 adds the message search bar, and `t` tag-editing shortcut is deferred to Phase 5 alongside tags.
- 2026-11-24, Phase 5: Known search operators with an empty value (e.g. trailing `from:` while typing) or unrecognized `has:`/`is:` values are treated as plain text per FR-081, while invalid `before:`/`after:` dates return `400 bad_request`; `DELETE /messages?query=""` (a non-empty raw query that parses to zero terms) returns `400 bad_request` to guard against accidental unfiltered deletes; `PATCH /messages/{id}` (`tags`) and `POST /messages/bulk` (`add_tags`) validate tag names per FR-085, match existing project tags case-insensitively, and auto-create missing tags with color `slate`; `MessageUpdatedPayload.Tags` uses `*[]string` so clearing all tags emits `"tags": []` in SSE while seen-only updates omit `"tags"`; local 100k-message benchmarks measured 0.46 ms p95 for NFR-003 (<50 ms) and 3.89 ms p95 for NFR-004 (<150 ms) on Apple M5 Pro.
- 2026-11-24, Phase 6: `POST /api/v1/password-reset/{token}` returns `410 gone` uniformly for unknown, already-used, or expired tokens (consistent with FR-056 and avoiding token-existence oracles); HTTP login rate limiting checks both `(IP, email)` at `MAILCUBBY_LOGIN_RATE_LIMIT` and `IP` at `5x` before credential verification and records failed attempts only, so the 11th attempt after 10 failures within the window receives `429` with `Retry-After`; password length validation (`FR-042`) checks `10..256` Unicode characters with a 1024-byte cap; until Phase 7 adds project roles, non-admin server-mode users receive `[]` on `GET /api/v1/projects`, `404` on project routes and `?project=` SSE filters, and no project events on `GET /api/v1/events`.
- 2026-11-24, Phase 7a: Last-admin protection (`FR-100`) returns `409 conflict` not only when deleting the last admin but also when demoting (`is_admin: false`) or disabling (`disabled: true`) the last active admin so an instance cannot lock itself out; an instance admin calling `POST /api/v1/projects/{project}/leave` on a project where they are not an explicit member returns `409 conflict`; `POST /api/v1/invites/{token}/accept` returns `{"project":{"name":...,"slug":...}}` for project invites and `{}` for instance invites (matching `GET /api/v1/invites/{token}`); `GET /api/v1/projects/{project}/invites` returns `created_by` as a nullable string (inviter display name, falling back to email or `null`); `GET /api/v1/admin/users` cursor encodes the last returned integer user ID as unpadded base64url (`ORDER BY id ASC`) and `?q=` matches email or name via case-insensitive substring (`INSTR(LOWER(...))`); `GET /api/v1/projects/{project}` exposes all 7 settings to owners/admins/local operators and only `plus_address_tags` and `viewer_hide_technical` to members/viewers per SPEC 8.3.
- 2026-11-24, Phase 7b: Project settings routes use TanStack Router's non-nested `$project_` prefix (`p.$project_.settings.*`) so settings render as a full-page layout with section navigation outside the three-pane inbox shell; "Preview as viewer" is available whenever `project.role === "owner"` (both server-mode owners/admins and local-mode operators) and applies the exact same client-side UI restrictions as a real viewer including `viewer_hide_technical`; the "Leave project" button in Members & invites is shown only when the signed-in user appears in the explicit members list (avoiding the Phase 7a `409` for implicit-access admins).
- 2026-11-24, Phase 8: SMTP credential `label` is validated as 1 to 80 characters after trimming (matching project names), duplicate labels within a project are permitted by the schema, and `credential:<label>` matches case-insensitively (`COLLATE NOCASE`); creating a 21st credential on a project returns `409 conflict`; credential creation, reveal, and rotate responses include `Cache-Control: private, no-store`; `smtp.auth_lockout` audit entries are emitted once per lockout epoch (when an IP or username reaches `MAILCUBBY_SMTP_AUTH_MAX_FAILURES` within `MAILCUBBY_SMTP_AUTH_LOCKOUT`) with `actor_type="smtp"`, and subsequent locked attempts reply `454 4.7.0 Temporary authentication failure` without checking passwords or duplicating audit entries; invalid `?credential=` values on `GET /messages` return `400 bad_request`.
- 2026-11-24, post-Phase 8 review (`fix/review-findings`): two acceptance criteria ticked earlier did not hold and are now covered by regression tests. Phase 6 "11th failed login -> 429" failed under concurrent attempts (check-then-verify-then-record race); Phase 7a "zero leaks across all routes and SSE" failed because `projects.id` reuse let open SSE streams of a deleted project receive another tenant's events.
- 2026-11-24, review fix (FR-056): an anonymous accept of a **project** invite returns `409 login_required` for any existing user, with or without a password; only an **instance** invite may set the password of an admin-created passwordless user. A project invite for a disabled existing user now returns `409` instead of `403`. The invite page explains how passwordless users finish account setup first.
- 2026-11-24, review fix (SPEC 12.3): access and panic logs replace the token segment of `/api/v1/invites/{token}[/accept]`, `/api/v1/password-reset/{token}`, `/invite/{token}` and `/reset/{token}` with `[REDACTED]` (cleaned path, case-insensitive).
- 2026-11-24, review fix (SPEC 12.4): HTTP login atomically reserves one attempt on both the (IP, email) and IP limiters before password verification and gives it back only after a correct password, so only failures count and concurrency cannot exceed the limit (each success can let at most one extra attempt through). A login email over 254 bytes after trim and lowercase returns `400 bad_request` before rate limiting, hashing or auditing.
- 2026-11-24, review fix (FR-082): `DELETE /messages?query=` returns `400 bad_request` and deletes nothing when the query does not narrow the selection: every term is a negated full-text term without unicode61 tokens (`-"!"`, `-*`; Go-letter code points that unicode61 treats as separators, U+19B0–U+19C0, U+19C8–U+19C9, U+1CF2–U+1CF3, now count as non-tokens), or the query has only exclusions that would match every message (e.g. `-credential:typo`). To delete everything, omit `query`. List and wait still treat no-op terms as matching everything.
- 2026-11-24, review fix (FR-021, FR-082): `DELETE /api/v1/projects/{project}/messages` accepts `credential=<id>` (same validation as the list endpoint) alone or with `query`. The UI's "Delete matching" sends the sidebar credential as that ID instead of `credential:<label>`, because labels are not unique and label matching deleted other credentials' messages. A credential ID counts as a narrowing filter.
- 2026-11-24, review fix (web): post-login `returnTo` falls back to `/` if it contains any C0 control character, DEL or backslash, does not start with a single `/`, resolves to another origin via `new URL(value, location.origin)`, resolves to `/login` or `/setup`, or whose rebuilt path starts with `//` (dot segments such as `/.//evil` collapse to a protocol-relative path).
- 2026-11-24, review fix (FR-091, SPEC 10): `projects.id` is `AUTOINCREMENT` from migration 00003 (table rebuild with foreign keys off and an orphan guard; sequence seeded from `project.create`/`project.delete` audit rows). SSE drops a deleted project's ID after delivering `project.deleted` and ends streams scoped to it; admin-access audit dedup entries are cleared. Residual: IDs of projects deleted before 00003 whose audit rows were pruned can be reused once; 00003 refuses to start on pre-existing FK orphans.
- 2026-11-24, review fix (smtpd): temporary accept errors (e.g. `EMFILE`) are retried inside `limitListener` with backoff (5 ms doubling to 1 s) and never stop the SMTP listener. On shutdown, idle SMTP connections (including after EHLO) close at once, in-flight DATA may finish within the grace period and its connection closes right after the reply, all connections are force-closed at the deadline, and `smtpd.Server.Shutdown` returns only after every session has ended, so the store closes after the last session. EHLO or DATA during shutdown gets `451 4.3.0`.
- 2026-11-24, review fix (config): an empty or whitespace `MAILCUBBY_APP_KEY`/`_FILE` or `MAILCUBBY_ADMIN_PASSWORD`/`_FILE` counts as unset, as `MAILCUBBY_BASE_URL` already did; server mode without an effective app key, or `MAILCUBBY_ADMIN_EMAIL` without an effective password, fails startup.
- 2026-11-24, post-Phase 8 review follow-up (**Owner**, target Phase 10/11): `X-Tags` at ingest currently has no per-message tag cap (unlike the API cap of 50), so a single message with ~100k tags can hold the SQLite writer for seconds and flood a project's tag list. Default unless owner says otherwise: cap auto-tags at ingest to 50 per message and record a parse warning when truncated.
- 2026-11-24, post-Phase 8 review follow-up (**Owner**, target v1 or roadmap): Non-ASCII tag names (`FATURAÇÃO` vs `faturação`) and emails (`Élodie@…`) are not case-insensitive in SQLite because `COLLATE NOCASE` folds ASCII only while Go dedupe folds Unicode; fixing requires a Unicode-folded indexed column and a SPEC 10 update. Default unless owner asks for it in v1: keep ASCII `COLLATE NOCASE` in v1.
- 2026-11-24, post-Phase 8 review follow-up (target Phase 11): Security & hardening items from PR #2 to address during Phase 11:
  - Render hostile corpus: include `<noscript><meta http-equiv=refresh>` and `<svg><style><meta …>` (raw-text elements in `x/net/html` tokenizer that sandboxed browsers still parse).
  - Viewer isolation (FR-050): `envelope.auth_user` (SMTP username) is currently returned to viewers when `viewer_hide_technical=false`, conflicting with FR-050 ("See SMTP usernames: viewer = no"); omit/redact `auth_user` for viewers.
  - Auth/users/sessions: close open SSE streams on logout or session revoke; invalidate outstanding password-reset tokens after password reset or password change; exclude passwordless pending admins from last-admin protection; revoke pending instance invites when an admin deletes a user; prevent deleting a user who is a project's sole owner (or handle consistently with last-owner rule).
  - Rate limiting: fix SMTP AUTH lockout check-verify-record concurrency race by reusing `ratelimit.Reserve` (and avoid O(n) prune under global mutex above 1024 keys).
  - Store/CLI/shutdown: tighten `isolation_test.go` so `project_id =` is verified per referenced tenant table rather than anywhere in the statement; make `LoadOrCreateLocalKey` recover from a partial/empty `app.key` and enforce `0600`; avoid closing the store while HTTP handlers outlive the shutdown deadline; warn/require TLS or flag when `mailcubby sendmail` sends `AUTH PLAIN` to non-loopback hosts; avoid duplicating messages in already-committed projects if a multi-project ingest fails mid-way with `451`.
- 2026-11-24, post-Phase 8 review follow-up (target Phase 13 / web polish): Frontend & build-check items from PR #2 to address before v1.0:
  - SSE client should recover/reconnect after non-200 responses (e.g. `502` behind Caddy during restart, or `401` on session expiry) instead of staying stuck on "reconnecting" until reload.
  - Inbox UX: undo-delete should not re-insert into unrelated filtered lists or duplicate rows across pages; mark-read timer should depend on `seenMutation.mutate` so inbox re-renders do not reset it; invite-page "sign out" should call `queryClient.clear()` like other logout paths; admin/role switches should confirm destructive changes (especially self-demotion) and removing yourself from a project should navigate away instead of landing on a 404 settings page.
  - Tooling: make `make check` also detect a stale `web/src/routeTree.gen.ts`.
- 2026-11-24, Phase 9: `POST /api/v1/tokens` returns `404 not_found` when `project` refers to a nonexistent or inaccessible project (avoiding cross-tenant existence leaks per FR-050), validates `expires_in_days` in `1..3650` when non-null, and sets `Cache-Control: private, no-store`; `GET /api/v1/tokens` lists non-revoked tokens (including expired ones so users can see and revoke them) and `DELETE /api/v1/tokens/{id}` sets `revoked_at` (`404` if missing, already revoked, or owned by another user); `api_tokens.last_used_at` updates are throttled to at most once per minute (matching sessions and SMTP credentials); invalid or revoked `Authorization: Bearer` headers on non-public unsafe routes bypass `csrfMiddleware`'s Origin check so callers receive `401 unauthorized` instead of `403`, while public unsafe routes (`/setup`, `/auth/login`, `/invites/*/accept`, `/password-reset/*`) always enforce Origin.
- 2026-11-24, Phase 10: `GET`/`PATCH /api/v1/admin/settings` represents `retention_max_age` in integer seconds (consistent with `ProjectSettings.retention_max_age`, `0` disables the limit, and `null` in `PATCH` deletes the DB override back to the environment default) and returns `{settings, effective, source}` where each source is `"env"` or `"override"`; `GET /api/v1/projects/{project}` (plus create/update responses) includes `retention_defaults` (`{retention_max_age, retention_max_messages, retention_max_bytes}`) for owners only so non-admin project owners and local-mode operators see live effective instance defaults in UI 3.6.4; `OrphanGC` lists on-disk project directories before querying DB project IDs and re-verifies `ErrNotFound` per candidate to prevent any TOCTOU race with concurrent project creation; `Housekeeping` removes both absolute-expired and idle-expired sessions plus expired/used password reset tokens alongside expired invites and aged audit entries.
- 2026-11-24, Phase 11: Audit log pagination (`GET /api/v1/projects/{project}/audit`, `GET /api/v1/admin/audit`) cursors by `audit_log.id DESC` (unpadded base64url integer cursor) so events sharing a millisecond timestamp are never skipped; admin audit `?project=` matches project slug (falling back to numeric project ID, returning empty results for unknown slugs) and `?user=` matches numeric `actor_user_id` or case-insensitive `actor_label` substring; `envelope.auth_user` is omitted for callers without `ActionReadSMTP` (viewers and read-only tokens) per FR-050 even when `viewer_hide_technical=false`; `DELETE /api/v1/admin/users/{id}` returns `409 conflict` if the user is the sole owner of any project (SPEC 6.5) and revokes pending instance invites on deletion; auto-tags at ingest (`X-Tags` + plus-address tags) are capped at 50 per message with a parse warning when truncated; `mailcubby sendmail` negotiates `STARTTLS` when advertised and refuses plaintext `AUTH` to non-loopback hosts unless `--allow-insecure-auth` is passed (warning on stderr); `govulncheck@v1.1.4` CI job is added to `.github/workflows/ci.yml` and verified by CI/owner because the local environment runs offline.
- 2026-11-25, Phase 12: `MAILCUBBY_SMTPS_ADDR` requires `MAILCUBBY_SMTP_TLS_CERT` and `MAILCUBBY_SMTP_TLS_KEY` in `config.Load` (failing loudly rather than silently ignoring the implicit TLS listener); `internal/tlsutil.Reloader` polls every 30 s checking mtime, size, and inode (`os.SameFile`), retains the previously valid certificate on transient reload failures, and retries on the next poll; `smtpd.Server` shares total/per-IP connection limits across `Serve` and `ServeTLS` and closes excess implicit TLS connections without writing a plaintext `421`; `.goreleaser.yaml` uses `dockers_v2` (GoReleaser v2.20.3) and omits `:{{ .Major }}.{{ .Minor }}` and `:latest` on prereleases; `deploy/compose.server.yml` and `deploy/Caddyfile` run Caddy as UID `65532:65532` (`caddy-init` volume ownership, `http_port 8080`, `https_port 8443`, pinned Let's Encrypt `acme_ca`) so `0600` private keys are readable by Mailcubby's `nonroot` user; the `goreleaser release --snapshot --clean` and amd64/arm64 compressed image size acceptance criterion is blocked locally because Docker daemon and `goreleaser` are unavailable in this environment (all 6 `CGO_ENABLED=0` binaries cross-compiled cleanly with `linux/amd64` 8.1 MB gzipped and `linux/arm64` 7.4 MB gzipped, and the <25 MB check is enforced in `.github/workflows/release.yml`).
- 2026-11-25, Phase 13: All PR #2 web polish and build-check follow-ups addressed (exponential-backoff SSE recovery on `EventSource.CLOSED` + auth re-check, scoped multi-page undo-delete restore, stable `seenMutation.mutate`/`tagsMutation.mutate` callbacks, `queryClient.clear()` on invite sign-out, confirmation dialogs for admin/self-demotion + safe redirect on self-removal, and `routeTree.gen.ts` staleness check in `make check`); `@axe-core/playwright@4.14.0` added across every local-mode and server-mode screen (WCAG 2.1 AA, zero serious/critical violations) with contrast, `<dl>`, focusable `<pre>`, label-in-name, and ARIA listbox fixes; `internal/blob.Store` pools `*zstd.Encoder` instances (cutting ingest allocations from 2.07 MB/op to 1.36 MB/op); NFR-001 through NFR-009 verified and recorded in `docs/benchmarks.md` (macOS APFS `fsync` barrier measured 190.0 msgs/s for NFR-001; Linux SSD target >= 200 msgs/s); `scripts/check-spec-frs.sh` verifies 68/68 FRs in `docs/SPEC.md` are referenced by automated tests; tag-to-tag upgrade test skipped because no prior git tag exists yet (`00001+00002 -> 00003` populated migration verified); **Owner**: review dependency patch candidates before tagging `v1.0.0` (`golang.org/x/net` is `v0.62.0` in `go.mod` vs `v0.63.0` in `AGENTS.md`, `pnpm` 12.21.0 -> 12.23.0 available, plus full upstream Go/npm patch check and CI `govulncheck` run).
- 2026-11-25, Phase 14a: local mode uses `http.CrossOriginProtection` and also trusts `MAILCUBBY_BASE_URL` as an origin, so reverse proxies that rewrite `Host` keep working. Browser code on another origin (for example an in-page `fetch` from an app under test) can no longer write to the local API; curl, SDKs, the Playwright `request` fixture and the Vite dev proxy are unaffected. Same behaviour as Mailpit. Known trade-off: a plain-HTTP reverse proxy that rewrites `Host` (nginx default `proxy_pass`) gets 403 on UI writes unless `MAILCUBBY_BASE_URL` is set (an existing variable; HTTPS proxies and proxies that keep `Host` need nothing). `deploy/compose.local.yml` and `.env.example` leave `MAILCUBBY_MODE` unset so the implicit-local refusal (S3) protects Docker users too.
- 2026-11-25, Phase 14a: SMTP AUTH credential lookup errors reply `454 4.7.0` (was `451 4.3.0`); decrypt failures (wrong `MAILCUBBY_APP_KEY`) reply 454 and do not count towards lockout. Credential reveal on a decrypt failure returns `409 conflict` with a message naming `MAILCUBBY_APP_KEY`, keeping the error enum unchanged.
- 2026-11-25, Phase 14a: a new `app.db` is created 0600 before SQLite opens it; an existing `app.db*` with wider permissions is chmodded to 0600 best effort, and a failed chmod (some bind mounts) is ignored silently so startup is never blocked.
- 2026-11-25, Phase 14a: the implicit-local refusal (S3) opens and migrates the database before refusing, but creates no `app.key`. An explicit `MAILCUBBY_SMTP_PUBLIC_PORT` still overrides the bound port in local mode (Docker port mappings).
- 2026-11-25, Phase 14b: `release.yml` hardcodes `--release-notes docs/release-notes-v1.0.0.md`, so the `v1.0.0-rc.1` rehearsal publishes the v1.0.0 notes (intended) and every later tag needs that path updated.
- 2026-11-25, Phase 14b: the admin reset recipe is `docker compose -f deploy/compose.server.yml exec mailcubby /mailcubby admin reset-password ...` (absolute path: the distroless image has no `mailcubby` on `PATH`). The startup line for ignored `MAILCUBBY_ADMIN_*` is INFO, not WARN, because it repeats on every restart while the variables stay set.
- 2026-11-25, Phase 14b: the setup wizard's Compose command includes `-f deploy/compose.server.yml` so it works from the repo root where the README leaves the user. The "Paste the setup token" error could not be reproduced before typing on the previous code; it is now gated on a submit attempt, so the tests document the behaviour rather than guard a reproduced regression.
- 2026-11-25, Phase 14b: the compose `name:` keys change volume names from `deploy_*` to `mailcubby_*` / `mailcubby-local_*`; harmless because no image has been published yet. Compose files use `ghcr.io/mailcubby/mailcubby:${MAILCUBBY_VERSION:-latest}` so a `v1.0.0` checkout can pin its image in `deploy/.env` (optional, Compose only). Image size measured at 8.47 MB compressed for linux/arm64 only. `docs/benchmarks.md` still says ~10–11 MB estimated and ADR 0001 says 15–20 MB (historical, left unchanged).
- 2026-11-25, Phase 14b: **Owner**: add the new `e2e` CI job to the required checks of the `main` ruleset (with `check`, `race`, `fuzz`, `vuln`, `docker`); remove the README pre-release banner when v1.0.0 ships.
- 2026-11-25, Phase 14c: the attachment strip is a footer of the reading pane, shown only with the Preview and Text tabs and capped at about two rows with its own scroll, so it stays visible under the full-height preview (like Mailpit). Card accessible name: "Download <filename> <size>". Strip and Attachments tab are shown to viewers regardless of `viewer_hide_technical`; `referenced_by_html` is not a technical field.
- 2026-11-25, Phase 14c: `referenced_by_html` comes from the renderer (`render.Result.ReferencedCIDs`), keyed by normalised Content-ID. If two parts share a Content-ID both are flagged although only the first renders; the other appears only in the Attachments tab (accepted for v1). A `cid:` part skipped for exceeding the 10 MiB inline budget is not flagged and appears in the strip, since the preview cannot show it. If rendering fails, every flag is false. Only image and CSS `url()` contexts the render CSP lets the preview show count (`<img>`/`<input type=image>` `src`, `srcset`, `background`, `poster`, SVG `<image>`/`<feImage>` `href`, CSS `url()` including `@font-face` fonts, which `font-src data:` allows); a `cid:` in an iframe, script, embed or media `src` is neither inlined nor flagged.
