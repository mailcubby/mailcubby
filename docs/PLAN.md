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
| M5 v0.1.0 | 13 to 15 | Release (published 2026-11-26) |
| M6 v0.2.0 | 16a to 16e | Inline PDF preview, offline HTML compatibility check, link checker |

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

## Phase 13: v0.1.0 release checklist

**Goal**: ship v0.1.0.

Tasks:
- [x] Complete the Playwright suite (`make e2e`): local mode inbox flow, server mode setup wizard, login, invite accept as viewer, viewer restrictions, credential reveal, search, delete with undo, keyboard flows; add `@axe-core/playwright` (already pinned in `AGENTS.md`, so adding it is not a stop condition) on every screen with zero serious/critical violations (NFR-010).
- [x] Performance verification against NFR-001 to NFR-009; results recorded in `docs/benchmarks.md`.
- [x] Final spec pass: every FR with acceptance criteria has at least one test referencing it (script that greps `FR-` IDs in tests vs SPEC).
- [x] Upgrade test: data dir created by the previous tag migrates cleanly (skip with a note if no previous tag exists — skipped: no previous git tag exists yet; `00001+00002 -> 00003` forward migration with populated data verified in `internal/store/migration_projects_autoincrement_test.go`).
- [x] Security review of the diff since Phase 11; dependency updates to latest patch versions (stop condition: list them for the owner); `govulncheck` clean. Done 2026-11-26: `govulncheck@v1.10.0` clean (vuln DB 2026-11-24), `pnpm audit` clean, security review of the diff since Phase 11 (`b8079de..c6c52d6`, Phases 12 to 15) cleared with no findings; the owner-approved patch upgrades are applied (see Open questions 2026-11-26).
- [x] `CHANGELOG.md`, GitHub release notes draft.
- [x] GitHub org, GHCR namespace and `mailcubby.dev` domain registered and matching the docs; tag `v0.1.0` pushed (owner; published 2026-11-26 — GitHub release, GHCR multi-arch image, and Homebrew tap).

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
- [x] GoReleaser config publishes a Homebrew formula or cask to `mailcubby/homebrew-tap` with a service that runs local mode on :1025/:8025 with data in `$(brew --prefix)/var/mailcubby`; pre-releases do not update the tap; decision recorded in an ADR if not obvious.
- [x] README install section: macOS Homebrew first (`brew install mailcubby/tap/mailcubby`, `brew services start mailcubby`); manual LaunchAgent (macOS) and systemd user unit (Linux) for the Releases binary.
- [x] Create the `mailcubby/homebrew-tap` repository (owner; created 2026-11-25 with owner approval, README on `main`).
- [x] Create a fine-grained token with contents write on `mailcubby/homebrew-tap` and store it as the `HOMEBREW_TAP_GITHUB_TOKEN` secret of `mailcubby/mailcubby` (owner; stored 2026-11-25).

Acceptance:
- `goreleaser check` exits 0, or 2 with only the `brews` deprecation (ADR 0008); `goreleaser release --snapshot --clean` generates the formula with the service block (criterion reworded for ADR 0008, approved by the owner on 2026-11-25).
- After the first tagged release, `brew install mailcubby/tap/mailcubby && brew services start mailcubby` serves the UI on http://localhost:8025 (owner).

---

## Phase 15: visual pass

Owner-approved direction (2026-11-26): **"Studio"**. The top bar and sidebar sit on a deep ink-green chrome layer in both themes. Content panes are cool-tinted neutrals, the email preview stays neutral, and a teal-cyan accent marks actions, selection and focus. Two additions from the other directions: role badges carry a lucide icon (owner `crown`, member `pencil`, viewer `eye`), and status badges carry a leading dot so colour is never the only signal. In dark mode the chrome layer is lighter than the canvas. The message list keeps today's three-level rows (sender, subject, snippet); density is a separate decision.

Reference material (untracked, from the owner review): `docs/visual-pass/directions/b/` (tokens, spec, mockups), `docs/visual-pass/AUDITORIA.md` (audit, Portuguese), `docs/visual-pass/before/` (current screenshots).

Rules for every 15x PR:
- No new dependencies, no API, permission or security changes, and the email preview stays in the sandboxed iframe.
- All colours come from `globals.css` tokens; no raw palette classes (`emerald-*`, `amber-*`, `zinc-*`, ...) or hex values in components.
- WCAG AA in both themes, using a computed contrast table for token pairs.
- No serious or critical axe violations on changed screens.
- `make check` and `make e2e` green.
- `docs/UI.md` is updated in the same PR.
- Before/after screenshots (light and dark) are attached to the PR.

### Phase 15a: tokens and theme

Tasks:
- [x] New tokens in `web/src/styles/globals.css` (light, dark, `.dark`):
  - the Studio palette;
  - semantic `success`, `warning`, `info`, `destructive`, each with `-foreground` and `-subtle`;
  - an `--input` border at 3:1 or more;
  - chrome `--sidebar-*` tokens, with the dark chrome lighter than the canvas;
  - a secondary text token at 4.5:1 or more on `--muted`, which fixes the axe failure on the active sidebar count (audit A1);
  - an `accent`/`accent-foreground` pair at 4.5:1 or more in both themes (audit A6);
  - radius 8 px for controls and 12 px for cards.
- [x] Replace every raw palette class in `web/src` (14 files use `emerald-*`/`amber-*`) with semantic tokens; add a lint-style test that fails on raw palette classes or hex colours in `.tsx`.
- [x] `Button` and `Badge` variants: primary/secondary/outline/ghost/destructive; badge `success|warning|info|destructive|neutral|accent` with optional leading dot; role badge with lucide icon.
- [x] `docs/UI.md` section 1 (principles 1.1 and 1.6 as revised) and the token table; computed contrast table committed as `docs/ui-contrast.md` (generated by a script under `web/scripts/`).

Acceptance:
- No raw palette class or hex colour in `web/src/**/*.tsx` (test).
- Every required token pair passes AA in light and dark (generated table).
- Existing e2e and axe spec pass.

### Phase 15b: structure and navigation

Tasks:
- [x] App header and sidebar on the chrome layer: logo, admin link, preview-as-viewer, search, account menu; sentence-case group labels; project colour as switcher dot and a 2 px line on the inbox list header.
- [x] Theme choice moves from the header into the account menu (and stays in the palette and preferences); the shortcuts icon is hidden for viewers (audit V3, owner-approved flow change).
- [x] Skip link "Skip to message list" as the first focus stop (audit A4).
- [x] Viewer header shows only search and account menu; "Back to all projects" hidden when the user has exactly one project (audit V4, owner-approved).
- [x] Login, invite accept, password reset and setup wizard use the primary button for the main action (audit V5, M2); setup stepper labels fit without truncation (audit M8).

Acceptance:
- Viewer header has at most 3 controls; tab order from page load reaches the message list in 2 stops or fewer through the skip link.
- e2e covers theme switching from the account menu.

### Phase 15c: inbox and message view

Tasks:
- [x] Message list rows with the new tokens (selected, unread, hover, realtime arrival), keeping the three-level row; tag pills as soft pills.
- [x] Reading pane header hierarchy (subject 18 px, sender avatar initials, credential chip), tabs and preview toolbar visually subordinate to the tabs; attachment strip cards.
- [x] Empty states: one structured empty state per pane (icon on a tinted circle, title, one line, one action); empty inbox shows "No messages yet" once (audit M6); viewer reading pane shows plain text, not shortcuts (audit V2).
- [x] After sign-in, a user with exactly one project and no admin rights lands on `/p/<slug>` (audit V1, owner-approved flow change; SPEC and e2e updated).

Acceptance:
- Viewer reaches the first email in at most 1 action after sign-in when they have one project (e2e).
- Axe clean on inbox and message view in both themes.

### Phase 15d: settings and admin

Tasks:
- [x] Settings layout and cards with the new hierarchy; SMTP credentials table without overflow at 1024 px and above (fixed layout, icon actions with labels) (audit M1).
- [x] Members and invites: role badges with icons, invite status badges (pending = warning, accepted = success, expired/revoked = neutral) (audit M3), role select styled like inputs (audit M11), subtitle text per role (audit M5), "Invite people" action in the page header (audit M7).
- [x] Invites table shows pending invites by default with a "Show accepted" toggle (audit M4, owner-approved flow change).
- [x] Admin screens (users, projects, settings, audit, stats) and account screens on the same components.

Acceptance:
- No horizontal overflow on settings pages at 1024 px (e2e check on SMTP).
- Axe clean on SMTP, members (with invite dialog) and admin users in both themes.

### Phase 15e: UX fixes from the audit and final pass

Tasks:
- [x] Remaining audit items: viewer "Settings unavailable" and other error/empty screens in the new style (audit V7), login shows the instance name (audit V6), setup step 5 success state on the success token (audit M9).
- [x] Final review of every screen in light and dark for owner, member, viewer and local mode; fix spacing and consistency issues found.
- [x] Axe e2e spec extended to every changed screen in both themes.

Acceptance:
- Axe shows no serious or critical violations on changed screens (e2e).
- Local mode needs no new step compared to v1.0.0.

### Phase 15 follow-ups

Small leftovers from the Phase 15 final review (frontend only).

Tasks:
- [x] Invite accept page: the signed-in links out ("Back to projects" on an instance invite, "Go to projects" on an unavailable invite) follow the single-project rule and open `/p/<slug>` as "Back to inbox" / "Go to inbox".
- [x] Project switcher: "All projects" is hidden for the users who have no "Back to all projects" (audit V4 rule).
- [x] A non-404 load error on a single-project user's only project offers "Try again" as the primary action instead of a back link to `/`.
- [x] Setup wizard card keeps one width across all steps without clipping the step 5 snippets.
- [x] Projects home while the V1 redirect decides: skeleton or documented empty pane (UI.md 4.6).

Acceptance:
- Vitest or Playwright covers each item; `make check` and `make e2e` green.

---

## Phase 16: v0.2 message inspection features

**Goal**: add inline PDF preview, offline HTML client compatibility checks (`caniemail`), and an SSRF-hardened link checker with zero new Go/npm dependencies and zero SQLite schema migrations. Each part is sized for one autonomous agent session and one PR.

### Phase 16a: Inline PDF preview (backend + frontend + e2e)

Implements SPEC 14.19 under ADR 0009.

Tasks:
- [x] Config: add `MAILCUBBY_RENDER_INLINE_PDF` (bool, default `true` in local and server modes) in `internal/config` and expose `features.inline_pdf` in `GET /api/v1/meta`.
- [x] Backend: add `GET /render/{project}/{id}/attachments/{attachment_id}` (`PermViewer`, accessible to viewers even when `viewer_hide_technical=true`) in `internal/api/routes.go` and `internal/api/handlers_render.go` (or dedicated attachment render handler). Return `404` when `MAILCUBBY_RENDER_INLINE_PDF=false`; validate coerced media type is `application/pdf` **and** decoded bytes start with `%PDF-` at offset 0 (otherwise `415 Unsupported Media Type`). Serve with ADR 0009 headers (`Content-Disposition: inline`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; object-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'`, `Referrer-Policy: no-referrer`, `Cache-Control: private, no-store`, `Cross-Origin-Resource-Policy: same-origin`, `X-DNS-Prefetch-Control: off`, no `X-Frame-Options`). Keep `/api/v1/.../attachments/{attachment_id}` unchanged.
- [x] Update `internal/api/openapi.yaml`, regenerate `web/src/api/schema.d.ts`, update `TestAuthorizationMatrix`, two-tenant `isolation_test.go`, and `TestRouteClassSecurityHeaders` (`headers_test.go`).
- [x] Frontend: add lazy-loaded `PdfPreviewDialog` (native `<Dialog>` + `<iframe referrerPolicy="no-referrer">` without `sandbox`, header actions for "Open in new tab" and "Download", mobile fallback note); add "Preview" action for PDF rows in `AttachmentsTab`; turn PDF cards in `AttachmentStrip` into split cards (preview button + sibling download link, no nested interactive elements) when `meta.features.inline_pdf` is `true`.
- [x] Docs: update `docs/SPEC.md` (FR-072, FR-074, sections 8.1, 8.2, 8.9, 9.2, 12.2, 14.19), `docs/ARCHITECTURE.md` (sections 3.11 and 10.3), `docs/UI.md`, and `AGENTS.md` security rule 5 cross-referencing ADR 0009.
- [x] Tests: Go unit/integration tests (valid PDF, forged HTML/SVG as `application/pdf` -> `415`, non-PDF -> `415`, feature disabled -> `404`, tenant isolation, authz matrix, exact headers); Vitest tests for `AttachmentStrip` / `PdfPreviewDialog`; Playwright e2e (previewing `send-test` PDF as local operator and as viewer with `viewer_hide_technical=true`, hostile HTML-disguised-as-PDF executes zero script, axe WCAG 2.1 AA in light and dark).

Files: `internal/config/`, `internal/api/`, `testdata/hostile/`, `web/src/features/message/`, `web/src/api/`, `web/e2e/`, `docs/SPEC.md`, `docs/ARCHITECTURE.md`, `docs/UI.md`, `AGENTS.md`.

Acceptance:
- `GET /render/{project}/{id}/attachments/{attachment_id}` serves a valid PDF inline with the exact ADR 0009 headers, returns `415` for HTML/SVG disguised as `application/pdf`, and returns `404` when `MAILCUBBY_RENDER_INLINE_PDF=false`.
- `TestAuthorizationMatrix`, `isolation_test.go`, `headers_test.go`, `make check`, and `make e2e` pass (including axe clean in light and dark on the PDF preview dialog).

### Phase 16b: HTML compatibility check — backend (`internal/compat`)

Implements the offline `caniemail` analysis engine (SPEC 14.2).

Tasks:
- [x] Dataset & generator: commit a compacted, zstd-compressed snapshot of `caniemail.com/api/data.json` (MIT license with attribution notice; generating the initial snapshot requires one-time network access, after which builds and tests run fully offline) under `internal/compat/data/caniemail.json.zst` (~15–30 KB compressed, curated ~18 current email clients including Outlook Classic vs New) plus a maintainer update script (`scripts/update-caniemail` or `internal/compat/cmd/gen`) and a test asserting the compressed dataset stays under 100 KB.
- [x] `internal/compat`: implement `Checker` (no mutable globals; decompressed immutable dataset + 64-entry LRU cache keyed by `{projectID, msgID}`), bounded `golang.org/x/net/html` walker (ignoring MSO conditional comments `<!--[if mso]>...<![endif]-->`), linear-time O(N) hand-written CSS tokenizer/parser (zero backtracking regexes, strict byte/node/declaration caps), declarative rule registry mapping HTML elements/attributes/image formats and CSS at-rules/selectors/properties/values/units to `caniemail` slugs, and weighted compatibility scoring.
- [x] API: add `GET /api/v1/projects/{project}/messages/{id}/html-check` (`PermViewerTech` / `viewer*`; returns `404` when message has no HTML part), update `internal/api/openapi.yaml`, regenerate `web/src/api/schema.d.ts`, update `TestAuthorizationMatrix` and two-tenant `isolation_test.go`.
- [x] Docs: update `docs/SPEC.md` (new FR, section 8.4) and `docs/ARCHITECTURE.md` (new `internal/compat` component and package tree).
- [x] Tests: unit test verifying every rule slug exists in the embedded dataset; fixture tests for common email HTML/CSS patterns (flexbox, grid, `@media`, `prefers-color-scheme`, MSO conditionals, `border-radius`, SVG, WebP); fuzz test on the HTML/CSS analyzer (`FuzzAnalyzeHTML`, no panics, bounded runtime); API authz & isolation tests.

Files: `internal/compat/**`, `internal/api/`, `web/src/api/`, `docs/SPEC.md`, `docs/ARCHITECTURE.md`.

Acceptance:
- Compressed dataset is under 100 KB; every rule slug resolves in the dataset; fuzz test runs cleanly without panics.
- `GET /api/v1/projects/{project}/messages/{id}/html-check` returns deterministic compatibility scores and per-client support, returns `404` for text-only messages, enforces `viewer_hide_technical`, and passes `make check` + `make test-race`.

### Phase 16c: HTML compatibility check — frontend (`HtmlCheckTab`)

Implements the UI for HTML compatibility checks (SPEC 14.2).

Tasks:
- [x] Add `queryKeys.messageHtmlCheck(slug, id)` (`staleTime: Infinity`) and lazy-loaded `HtmlCheckTab` in `web/src/features/message/` wired as tab **HTML check** (shortcut key `9`, URL `?tab=check`, hidden when `!message.has_html` or `hideTechnical`).
- [x] UI: score summary bar (Supported / Partial / Unsupported using semantic Studio tokens with text + percentage, never color-only), client family filter pills (All / Gmail / Outlook / Apple Mail / Yahoo / Other), "Only show issues" toggle (default on), expandable feature rows (accessible disclosure with `aria-expanded`, category badge, occurrence count, external `caniemail.com` link with `rel="noopener noreferrer"`, and per-client support grid with notes), and structured `EmptyState` when all detected features are supported.
- [x] Update keyboard shortcuts help overlay (`1`–`9` or tab list) and `docs/UI.md`.
- [x] Tests: Vitest tests for filtering, "Only show issues", and disclosure expansion; Playwright e2e verifying tab `9`, filtering, hidden for viewers with `viewer_hide_technical=true`, zero `dangerouslySetInnerHTML`, bundle budget (`check:bundle` < 250 KB gzip), and axe WCAG 2.1 AA in light and dark.

Files: `web/src/features/message/`, `web/src/routes/p.$project.m.$messageId.tsx`, `web/src/lib/queryKeys.ts`, `web/src/lib/shortcuts.ts`, `web/e2e/`, `docs/UI.md`.

Acceptance:
- Playwright opens the `HTML check` tab (and switches with key `9`), sees compatibility results and client filters, confirms it is hidden for restricted viewers, and passes axe in both themes; `make check` and `make e2e` pass.

### Phase 16d: Link checker — backend (`internal/linkcheck`)

Implements offline link extraction and SSRF-hardened active link checking (SPEC 14.2) under ADR 0010.

Tasks:
- [ ] Config: add `MAILCUBBY_LINK_CHECK_ENABLED` (default `true` in local mode, `false` in server mode), `MAILCUBBY_LINK_CHECK_TIMEOUT` (`5s`, max `30s`), `MAILCUBBY_LINK_CHECK_MAX_LINKS` (`50`, max `200`), `MAILCUBBY_LINK_CHECK_ALLOWED_PORTS` (`80,443`), `MAILCUBBY_LINK_CHECK_ALLOW_CIDRS` (empty) in `internal/config`; expose `features.link_check` in `GET /api/v1/meta`.
- [ ] `internal/linkcheck`: implement offline extractor (HTML `<a href>`/`<area href>`, plain-text URLs, `List-Unsubscribe` header, deduplication, static warnings) and active checker enforcing all ADR 0010 SSRF/DNS-rebinding defenses (`Proxy: nil`, custom `DialContext` with `LookupNetIP`, trailing-dot hostname normalization, IPv6 zone-ID rejection, `ip.Unmap()`, full private/loopback/link-local/IMDS/site-local/ULA/CGNAT/NAT64/6to4/Teredo/IPv4-compatible/translated denylist, `ALLOW_CIDRS` override, direct literal IP dial preserving TLS SNI/`Host`, `CheckRedirect` max 5, `HEAD` + `GET Range: bytes=0-0` fallback on `405`/`501`, 1 KiB discard cap, `errgroup.SetLimit(5)` + global semaphore 16, tenant-scoped 64-entry 5-minute TTL LRU cache keyed by `{projectID, msgID}` via injected `clock.Clock`, zero logging of checked URLs or raw `*url.Error` strings).
- [ ] API: add `GET /api/v1/projects/{project}/messages/{id}/links` (`PermViewerTech`) and `POST /api/v1/projects/{project}/messages/{id}/links/check` (`PermMember`, dedicated `10/m` server-mode rate limit, optional `{ urls?: string[] }` validated against extracted message links -> `400` for arbitrary URLs, `403 feature_disabled` when disabled). Update `internal/api/openapi.yaml`, regenerate `web/src/api/schema.d.ts`, update `TestAuthorizationMatrix`, two-tenant `isolation_test.go`, and `TestIntegrationLogRedactionNoSecrets`.
- [ ] Docs: update `docs/SPEC.md` (new FR, FR-050 permission matrix, sections 3, 8.1, 8.2, 8.4, 9.2, 12.4, 14.2) and `docs/ARCHITECTURE.md` (`internal/linkcheck` component, package tree, threat model sections 10.3/10.4).
- [ ] Tests: comprehensive unit tests for IP blocklist (IPv4, IPv6, IPv4-mapped IPv6, NAT64, 6to4, Teredo, IMDS), simulated DNS rebinding via fake resolver, redirect-to-loopback/private IP blocking, `ALLOW_CIDRS` allowlist, `HEAD` -> `GET` fallback on `405`, arbitrary-URL `400` rejection, `feature_disabled` `403`, rate limit `429`, log redaction (no checked URLs in logs), authz matrix (`viewer` and `token-read` get `403` on `POST .../links/check`), and tenant isolation.

Files: `internal/config/`, `internal/linkcheck/**`, `internal/api/`, `internal/app/`, `web/src/api/`, `docs/SPEC.md`, `docs/ARCHITECTURE.md`.

Acceptance:
- Every blocked IP range, IPv4-mapped IPv6 bypass, DNS rebinding scenario, and private redirect is rejected as `blocked`; arbitrary caller-supplied URLs return `400`; viewers and `read` tokens receive `403` on `POST .../links/check`; zero checked URLs appear in captured logs; `make check` and `make test-race` pass.

### Phase 16e: Link checker — frontend (`LinksTab`) and v0.2 polish

Implements the UI for link extraction and checking (SPEC 14.2) and closes v0.2.

Tasks:
- [ ] Add `queryKeys.messageLinks(slug, id)` and lazy-loaded `LinksTab` in `web/src/features/message/` wired as tab **Links** (shortcut key `8`, URL `?tab=links`, hidden when `hideTechnical`).
- [ ] UI: offline extracted link table (URL, anchor text, source badges, count, static warnings, copy URL, safe "Open in new tab" disabled for non-`http(s)` schemes); toolbar with "Check all links" + per-row check button (visible only when `meta.features.link_check === true` and `effectiveRole !== "viewer"`), concise single-use-token caution note, quiet info banner when disabled on server instances, semantic status badges (`200 OK`, redirect chain, `4xx`/`5xx`, `Blocked (private IP)`, timeout/DNS/TLS error), and `aria-live="polite"` summary announcement.
- [ ] Update `docs/UI.md`, `README.md` (v0.2 features & new env vars), and `CHANGELOG.md` (`[Unreleased]` / v0.2 notes).
- [ ] Tests: Vitest tests for `LinksTab` states (offline list, enabled vs disabled, viewer vs member, per-row check, static warnings); Playwright e2e (local mode + server mode with `MAILCUBBY_LINK_CHECK_ENABLED=true` and local test target via `MAILCUBBY_LINK_CHECK_ALLOW_CIDRS` and `MAILCUBBY_LINK_CHECK_ALLOWED_PORTS`, verifying offline extraction, active check, blocked private target, viewer cannot trigger check, and axe WCAG 2.1 AA in light and dark).
- [ ] Final verification: `make check`, `make test-race`, `make e2e`, `./scripts/check-spec-frs.sh`, and `pnpm --dir web check:bundle` (< 250 KB gzip).

Files: `web/src/features/message/`, `web/src/routes/p.$project.m.$messageId.tsx`, `web/src/lib/queryKeys.ts`, `web/e2e/`, `docs/UI.md`, `README.md`, `CHANGELOG.md`.

Acceptance:
- Playwright verifies offline link listing, explicit active checking, SSRF-blocked status badge, disabled state in default server mode, viewer restrictions, and axe clean in light and dark; `make check`, `make test-race`, and `make e2e` are green.

---

## Backlog (after v0.2.0)

Not scheduled. When an item is picked up it becomes a phase above. Items marked "SPEC 14.n" are in the SPEC roadmap. (All v0.1.x backlog items — audit V4, V6, V8, S5–S9, L4–L5, I7 — and the SSE latency benchmark fix shipped after v0.1.0 in PRs #16 to #24.)

### v0.2+ / Next (features)

1. Webhooks on `message.created` and the MCP endpoint for AI agents. SPEC 14.4, 14.5.

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
- 2026-11-25, Phase 14d: the Homebrew install uses a formula (GoReleaser `brews`, deprecated in favour of `homebrew_casks`) because only formulae support `brew services` and casks quarantine unsigned binaries (ADR 0008). `goreleaser check` therefore exits 2 with only the `brews` deprecation; `goreleaser release` just warns. Any future CI `check` step must accept exit 2.
- 2026-11-25, Phase 14d: the Homebrew service listens on all interfaces like the plain binary and Mailpit (audit decision S2); loopback-only is opt-in through `~/.homebrew/services/mailcubby.env` (Homebrew 6.0.15 or later), documented in the README and the formula caveats. The formula `post_install` creates `var/mailcubby` and `var/log` so the service working directory exists. Release archives (Windows zip included) now carry `deploy/launchd/dev.mailcubby.plist` and `deploy/systemd/mailcubby.service`.
- 2026-11-26, Phase 15 (owner decision): visual direction "Studio" approved with role-badge icons and status-badge dots; flow changes approved: single-project users land in their inbox after sign-in (V1), theme moves into the account menu and shortcuts icon is hidden for viewers (V3), "Back to all projects" is hidden with one project (V4), invites table shows pending by default (M4). UI.md principles 1.1 and 1.6 are revised in Phase 15a.
- 2026-11-26, Phase 15 audit: the Laravel snippet comment says `MAIL_ENCRYPTION=null` while `docs/UI.md` 3.6.2 says `tls`; out of scope for the visual pass, to be checked against `snippets.ts` golden tests.
- 2026-11-26, Phase 15a: the dark chrome layer uses `--sidebar` `#1c3538` (active `#2a4d4f`, muted text `#abc2c0`) instead of direction B's `#0f2022`, so it is visibly lighter than the canvas (luminance ratio 1.45 against `--background`, 1.34 against `--surface`; the contrast script requires 1.25 or more). Badge mappings follow the owner decision over the B mockup: expired, revoked and disabled are neutral (B had revoked as destructive), status badges carry a leading dot rather than B's clock/check icons, and the admin "Pending invite" badge is warning. API token scope `write` is an accent badge (was solid foreground). Outline buttons and form fields use the `--input` border (3:1), as in B. Out of scope for the guard test: `web/src/features/message/shiki.ts` keeps its own hex syntax colours (a `.ts` theme for Shiki, not component styling). Hand-rolled primary buttons on the auth pages keep `hover:opacity-90` until they move to `Button` in Phase 15b; the native role select (M11) is restyled in 15d.
- 2026-11-26, Phase 15b: the theme moved into the header menu. Local mode has no accounts, so its header menu is a "Preferences menu" (Preferences link and the theme radios); server mode's account menu gains the same Theme group. The sign-in, setup, invite and reset pages have no account menu and keep the small Light / Dark / System toggle in their own top bar (`ThemeToggle`).
- 2026-11-26, Phase 15b: "viewer" for the header means the effective role in the open project (so "Preview as viewer" also hides the shortcuts button), or, outside a project, a non-admin in server mode whose every project role is viewer. Local mode is never a viewer. "Back to all projects" (audit V4) is hidden only in server mode, for non-admins whose project list has exactly one project; local mode keeps it, and the logo always links to the projects home.
- 2026-11-26, Phase 15b: the skip link is a `<button>` that moves focus to the message listbox (or the list pane when it is empty) rather than an `<a href="#...">`: Biome's `useValidAnchor` rejects an anchor with a click handler, and a hash link would go through the router's history. It renders only on the inbox route.
- 2026-11-26, Phase 15b: two chrome tokens were added, `--sidebar-hover` (hover fill between the rail and the active item) and `--sidebar-warning` (Reconnecting dot; the content `--warning` is below 3:1 on the light chrome), with their pairs in `web/scripts/contrast.mjs`. The top bar is 48 px (was 44 px), per direction B. The account-menu trigger takes its accessible name from its visible text ("<name> — Account menu", the suffix screen-reader only) because axe's `label-content-name-mismatch` flagged an `aria-label` that did not contain the avatar initial.
- 2026-11-26, Phase 15b: audit M2 is applied to the invite dialog ("Create invite link") and the sidebar "Create tag" dialog as well as the auth and setup pages. On setup step 5, "Send test email" stays primary until the first message arrives and then "Go to inbox" becomes the primary button, so the step never shows two primaries. `ApiSettingsSection` still has a hand-rolled primary link with `hover:opacity-90`; it is settings content and moves to `buttonClassName` in Phase 15d.
- 2026-11-26, Phase 15c: audit V1 lives in the projects home route (client-side `Navigate` with `replace`), so every way of reaching `/` is covered: sign-in without a return URL, instance-invite accept, the logo and `g p`. For a non-admin with one project the logo therefore opens the inbox, which supersedes the 15b note that the logo always links to the projects home. A non-admin with `can_create_projects` and one project is redirected too ("no admin rights"); "New project" stays in the command palette. The projects home is still reachable for admins, users with zero or two or more projects, and local mode. "Back to all projects" now renders only once the user and project list have loaded (no flash before the V4 rule applies).
- 2026-11-26, Phase 15c: `EmptyState` draws its icon on a neutral `--secondary` circle, not direction B's accent-tinted one, and the sender avatar is neutral too: UI.md 1.6 reserves the accent for focus, selection, unread, primary buttons and arrivals. Empty inbox (audit M6): "No messages yet" lives in the reading pane with the setup guide and the list says "Waiting for mail"; below 768 px the reading pane is absent, so the list shows "No messages yet" itself with "Send test email" for members and owners. The "No unread messages" state was unreachable (the Unread filter counted as an active search) and now shows; a reading pane whose filtered list is empty shows "Nothing to show" instead of "choose from the list". Viewer reading pane (audit V2) applies to "Preview as viewer" as well. UI.md 1.2 no longer claims 36/44 px rows: the three-level rows are about 70/80 px, and single-line rows stay a separate decision. The preview toolbar's rotate and "Email dark" toggles became ghost buttons (secondary fill when on); the attachment card's trailing download icon changed one e2e assertion (`attachment-strip.spec.ts`: two SVGs per PDF card).
- 2026-11-26, Phase 15c: the open message row is marked by the `--accent-subtle` tint only, without a side bar (the craft floor rejects one-sided bars, and B's mockup has none); unread rows show the relative time in medium-weight foreground while read rows keep it muted (B keeps every time muted). In a narrow reading pane (below about 450 px) the header's action icons wrap under the subject instead of truncating it. A single-project non-admin may briefly see the projects page skeleton before the V1 redirect, because the project list is not prefetched at sign-in.
- 2026-11-26, Phase 15d: the invites table shows only `pending` invites by default (audit M4, client-side filter of the unchanged list response). The **Show accepted** checkbox shows every invite the API returns, so it also reveals `revoked` and `expired` history, not only `accepted` (expired invites are purged daily by FR-099 anyway). With nothing pending the card reads "No pending invites.".
- 2026-11-26, Phase 15d: the members page's invite trigger moved from the Members card ("Invite member") to the page header as **Invite people** (owners only, audit M7); `server-projects-admin.spec.ts` and `accessibility.spec.ts` select the new name. The SMTP credentials table drops the separate "Last used" column: it shows under the status badge ("Used 3m", "Never used"), and "Created" shows under the label; the actions column header is screen-reader only. The card-header create buttons ("New credential", "New API token") stay visible with an empty list but turn outline there, so the empty state's "Create first …" is the only primary.
- 2026-11-26, Phase 15d: new owned UI components `Card`, the table primitives, `NativeSelect` (a styled native `<select>`; no shadcn select exists in the repo), `Switch` (a native checkbox with `role="switch"`) and the page frame `SettingsShell`, used by project settings, account and admin. The two settings empty states (no SMTP credentials, no API tokens) use inline tinted-circle markup rather than Phase 15c's `EmptyState`, which was being written in parallel; Phase 15e should move them onto it. `IntegrationSnippetsPanel` is unchanged because the inbox empty state and the setup wizard share it.
- 2026-11-26, Phase 15e: the single-project landing (audit V1, SPEC FR-035) now also excludes users with `can_create_projects`: they keep the projects home and its "New project", and so they keep "Back to all projects" (V4 uses the same helper). This supersedes the 15c note that such users were redirected. `server-projects-admin.spec.ts` changed one assertion on purpose: `dev@agency.test` (created with "Can create projects") now lands on `/` after the password reset, not on `/p/dev-sandbox`. While the redirect is still possible the projects home renders an empty `main` (`aria-busy`) instead of its skeleton, which removes the flash 15c recorded; admins, creators and local mode render as before.
- 2026-11-26, Phase 15e: audit V6 needed only a footer: the sign-in title already was the instance name. "Powered by Mailcubby" (muted, under the card) shows only when the instance name is not "Mailcubby", so the word never appears twice. Audit M9: the step 5 waiting card is neutral (it was accent-tinted, which UI.md 1.6 does not allow) and the arrival is a `--success-subtle` card; "First message received!" is unchanged for the e2e.
- 2026-11-26, Phase 15e: audit V7 and the 15d leftovers: every error, not-found and empty screen uses `EmptyState`, which gained an `h1` level for full-page states. New `web/src/components/PageStates.tsx` holds the centred page frame, the shared project 404/load-error state (it was duplicated in the inbox and settings layouts) and `NotFoundPage`, wired as the router's `defaultNotFoundComponent` (unknown addresses showed TanStack's plain "Not Found" before). Also moved: projects home empty, filter and error states, SMTP and API token empty states, the API token load error, the project audit "unavailable" card, the Attachments tab and the invalid-invite card.
- 2026-11-26, Phase 15e: the skip link renders only while the message list is on screen (`messageListVisible` on the active inbox context), so a phone with a message open has no control without a target; the e2e checks both cases at 375 px. `Switch` keeps the native checkbox as the track and draws the knob as a sibling span (it was a `::before` on the input); the routing card's `aria-labelledby` now points at its label text instead of the input.
- 2026-11-26, Phase 15e final review (screens in `docs/visual-pass/after/15e/`), fixed: the "Color" legend touched the selected swatch ring (flex gap does not apply to a `legend`; project general settings, create project and create tag dialogs); the projects home title was 18 px while every other page title is 20 px; the project audit log had Refresh inside its filter card while admin audit and stats have it in the page header; profile, change password and instance settings ended their forms with inline buttons while general and retention used `CardFooter` (all now use `CardFooter`; instance settings "Discard" is now "Reset" like the others; the profile card gained the title "Account details"); card stacks used 16, 20 and 24 px gaps (now 16 px everywhere); form error alerts mixed `bg-destructive/10` with `bg-destructive-subtle` (now the token everywhere). Left as is: the setup card widens on step 5 for the snippets. (The accent line beside the list in the after-sign-in shots is the resize handle's hover state: the pointer is still where "Sign in" was.)
- 2026-11-26, Phase 15e: `accessibility.spec.ts` now runs every screen in light and dark: local mode (one test), and a serial server-mode group on one fresh instance (setup wizard, owner and admin, viewer, public pages). Public pages switch theme with their own toggle, the rest through the header menu (`chooseTheme`), and both assert the `dark` class. It also covers the empty inbox, no results, no unread, SMTP without credentials, preview as viewer, the invite dialog, page and project not found, settings unavailable, the invalid invite and the sign-in error. UI.md 1.6 now lists the soft accent badge (owner, Admin, current session, `write` scope, overridden setting) among the accent's uses, which matches what shipped since 15a.
- 2026-11-26, Phase 13 security and dependency check (supersedes the dependency part of the 2026-11-25 Phase 13 entry):
  - `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` resolved to v1.10.0 (the version pinned in `ci.yml`), vuln DB updated 2026-11-24, Go 1.27.4: **no vulnerabilities**, at symbol or module level, in the 40 scanned modules or the standard library. `pnpm --dir web audit`: no known vulnerabilities. GitHub advisories list nothing for `pnpm@12.21.0` or `golang.org/x/net@0.62.0`.
  - Security review of `b8079de..c6c52d6` (end of Phase 11 to Phase 15e), cleared with no findings, per AGENTS.md section 8 rule: (1) both preview iframes in `PreviewTab.tsx` keep `sandbox="allow-popups allow-popups-to-escape-sandbox"` and `referrerPolicy="no-referrer"`; `web/src` has no `dangerouslySetInnerHTML`, `innerHTML`, `insertAdjacentHTML`, `srcdoc` or `eval`; text-tab links are `http(s)` only; the attachment strip links to the attachment endpoint, whose `?inline=1` is allowlisted server side; the render rewrite now skips `cid:` in non-image `src`, which only narrows what is inlined. (2) The one new query, `ListSMTPCredentialSecrets`, is `-- isolation: global` and called only from the startup key check in `internal/app/app.go`; no handler reaches it. (3) No route was added; `openapi.yaml` only gains a `409` on credential reveal and `referenced_by_html`. (4) Secrets: the startup key check logs counts only; the reveal `409` carries no secret; `app.db*` are now 0600. (5) Attachment and raw responses are unchanged (the strip and its thumbnails use the existing endpoint and headers). (6) Local mode gained `http.CrossOriginProtection` (Phase 14a); server-mode Origin and CSRF checks are unchanged. (7) Input and limits: SMTPS shares the total and per-IP connection limits; SMTP AUTH lockout keyed by (IP, username) with a 5x per-IP ceiling bounds username-variation attempts per IP. (8) SMTP AUTH over implicit TLS is detected through `TLSConnectionState`, so the TLS-only rule holds on both listeners. (9) Blob paths are unchanged; only zstd encoder pooling changed. Deploy and CI: `release.yml` runs only on `v*` tag pushes and is the only workflow that sees `HOMEBREW_TAP_GITHUB_TOKEN`; no `github.event.*` or `head_ref` is interpolated into `run:` steps; Caddy 2 replaces untrusted client `X-Forwarded-For`, so the private-range `MAILCUBBY_TRUSTED_PROXIES` default in `compose.server.yml` cannot be spoofed through Caddy.
  - **Owner** notes, no change made: `ci.yml` declares no `permissions:` block (the repository default token is `read`, checked 2026-11-26; an explicit `permissions: contents: read` would keep that if the default changes). Third-party actions are pinned by tag (`@v4`, `@v6`), not by commit SHA. `modernc.org/libc v1.80.2` comes from goose (v3.30.1, and still v3.30.2) while sqlite v1.63.0 was built against v1.80.1 (same minor; tests pass); keep libc and memory on whatever the sqlite release requires.
  - Upgrades need the owner's approval (AGENTS.md section 7 stop condition). None is a security fix. The owner approved the "safe patch" rows on 2026-11-26 ("Sim aplica os novos patch") and they are applied: goose v3.30.2 and x/net v0.63.0 (`go mod tidy` dropped `sethvargo/go-retry` and listed `golang.org/x/crypto` v0.59.0 as a direct requirement, since `internal/auth` imports it; same version), `@tanstack/react-query` 5.108.4, `react-resizable-panels` 4.24.2, `@axe-core/playwright` 4.14.1, `@biomejs/biome` 2.6.9 (`biome.json` `$schema` migrated, no new findings), `vitest` 5.1.2, `@types/node` 26.6.10, `@types/react` 19.3.3. `react-resizable-panels` 4.24.3 and `@types/node` 26.6.11 were published less than a day earlier, inside pnpm's 1-day `minimumReleaseAge` quarantine, so the previous patches were taken instead of adding `minimumReleaseAgeExclude` entries; take them in the next patch round. Every other row waits.

  Go (`go.mod`, every line; "latest minor" means the newest release with the same major version):

  | Module | Kind | Current | Latest patch | Latest minor | Recommendation |
  |---|---|---|---|---|---|
  | `github.com/emersion/go-sasl` | direct | v0.0.0-20241020182733-b788ff22d5a6 | no tagged releases (current is the latest commit) | - | none |
  | `github.com/emersion/go-smtp` | direct | v0.26.0 | v0.26.0 | v0.26.0 | none |
  | `github.com/jhillyerd/enmime/v2` | direct | v2.5.0 | v2.5.0 | v2.5.0 | none |
  | `github.com/klauspost/compress` | direct | v1.20.5 | v1.20.5 | v1.20.5 | none |
  | `github.com/oklog/ulid/v2` | direct | v2.1.2 | v2.1.2 | v2.1.2 | none |
  | `github.com/pressly/goose/v3` | direct | v3.30.1 | v3.30.2 | v3.30.2 | safe patch (replaces `sethvargo/go-retry` with an internal package; drops that indirect dependency) |
  | `golang.org/x/net` | direct | v0.62.0 | v0.62.0 | v0.63.0 | safe patch in practice (v0.x minor; only `http2` changed, we use `x/net/html`; matches the AGENTS.md pin; no other `go.mod` change) |
  | `golang.org/x/sync` | direct | v0.25.0 | v0.25.0 | v0.25.0 | none |
  | `modernc.org/sqlite` | direct | v1.63.0 | v1.63.0 | v1.63.0 | none |
  | `golang.org/x/crypto` | direct (argon2id in `internal/auth`; was marked indirect until `go mod tidy` in this change) | v0.59.0 | v0.59.0 | v0.59.0 | none |
  | `golang.org/x/sys` | indirect | v0.50.0 | v0.50.0 | v0.50.0 | none |
  | `golang.org/x/text` | indirect | v0.44.0 | v0.44.0 | v0.44.0 | none |
  | `modernc.org/libc` | indirect | v1.80.2 | v1.80.2 | v1.82.13 | skip, follow sqlite |
  | `modernc.org/memory` | indirect | v1.12.1 | v1.12.1 | v1.13.1 | skip, follow sqlite |
  | `modernc.org/mathutil` | indirect | v1.7.1 | v1.7.1 | v1.7.1 | none |
  | `github.com/mattn/go-colorable` | indirect | v0.1.14 | v0.1.16 | v0.1.16 | skip, moves with its direct dependency |
  | `github.com/mattn/go-runewidth` | indirect | v0.0.23 | v0.0.31 | v0.0.31 | skip, moves with its direct dependency |
  | `github.com/olekukonko/ll` | indirect | v0.1.8 | v0.1.9 | v0.1.9 | skip, moves with its direct dependency |
  | `github.com/olekukonko/tablewriter` | indirect | v1.1.4 | v1.1.5 | v1.1.5 | skip, moves with its direct dependency |
  | `github.com/goccy/go-json` | indirect | v0.10.6 | v0.10.6 | v0.12.0 | skip, moves with its direct dependency |
  | `github.com/sethvargo/go-retry` | indirect | v0.5.0 | v0.5.0 | v0.7.0 | skip (goose v3.30.2 removes it) |
  | `github.com/cespare/xxhash/v2` | indirect | v2.3.0 | v2.3.0 | v2.3.0 | none |
  | `github.com/clipperhouse/displaywidth` | indirect | v0.11.0 | v0.11.0 | v0.11.0 | none |
  | `github.com/clipperhouse/uax29/v2` | indirect | v2.7.0 | v2.7.0 | v2.7.0 | none |
  | `github.com/dustin/go-humanize` | indirect | v1.1.0 | v1.1.0 | v1.1.0 | none |
  | `github.com/fatih/color` | indirect | v1.19.0 | v1.19.0 | v1.19.0 | none |
  | `github.com/google/uuid` | indirect | v1.6.0 | v1.6.0 | v1.6.0 | none |
  | `github.com/inbucket/html2text` | indirect | v1.0.0 | v1.0.0 | v1.0.0 | none |
  | `github.com/mattn/go-isatty` | indirect | v0.0.24 | v0.0.24 | v0.0.24 | none |
  | `github.com/mfridman/interpolate` | indirect | v0.2.0 | v0.2.0 | v0.2.0 | none |
  | `github.com/ncruces/go-strftime` | indirect | v1.1.0 | v1.1.0 | v1.1.0 | none |
  | `github.com/olekukonko/errors` | indirect | v1.3.0 | v1.3.0 | v1.3.0 | none |
  | `github.com/pkg/errors` | indirect | v0.9.1 | v0.9.1 | v0.9.1 | none |
  | `go.uber.org/multierr` | indirect | v1.11.0 | v1.11.0 | v1.11.0 | none |
  | `github.com/cention-sany/utf7`, `github.com/gogs/chardet`, `github.com/olekukonko/cat`, `github.com/remyoudompheng/bigfft`, `github.com/ssor/bom` | indirect | pseudo-versions | no tagged releases | - | none |
  | Go toolchain | tool | go1.27.4 | go1.27.4 | go1.27.4 | none |
  | `sqlc` / `golangci-lint` / GoReleaser / `govulncheck` | tools | v1.31.1 / v2.15.0 / v2.20.3 / v1.10.0 | same | same | none |

  Web (`web/package.json`; latest major in brackets when newer):

  | Package | Kind | Current | Latest patch | Latest minor | Recommendation |
  |---|---|---|---|---|---|
  | `@tanstack/react-query` | runtime | 5.108.3 | 5.108.4 | 5.108.4 | safe patch |
  | `react-resizable-panels` | runtime | 4.24.1 | 4.24.3 | 4.24.3 | safe patch |
  | `lucide-react` | runtime | 1.78.0 | 1.78.0 | 1.81.0 | skip (minor, icons only) |
  | `@fontsource-variable/inter` | runtime | 5.3.0 | 5.3.0 | 5.3.0 | none |
  | `@tanstack/react-router` | runtime | 1.170.46 | 1.170.46 | 1.170.46 | none |
  | `@tanstack/react-virtual` | runtime | 3.14.28 | 3.14.28 | 3.14.28 | none |
  | `clsx` | runtime | 2.1.1 | 2.1.1 | 2.1.1 | none |
  | `openapi-fetch` | runtime | 0.17.0 | 0.17.0 | 0.17.0 | none |
  | `react` / `react-dom` | runtime | 19.3.1 | 19.3.1 | 19.3.1 | none |
  | `shiki` | runtime | 4.6.0 | 4.6.0 | 4.6.0 | none |
  | `sonner` | runtime | 2.0.8 | 2.0.8 | 2.0.8 | none |
  | `tailwind-merge` | runtime | 3.7.1 | 3.7.1 | 3.7.1 | none |
  | `@axe-core/playwright` | dev | 4.14.0 | 4.14.1 | 4.14.1 | safe patch |
  | `@biomejs/biome` | dev | 2.6.8 | 2.6.9 | 2.6.9 | safe patch (may add lint findings; run `make lint`) |
  | `vitest` | dev | 5.1.1 | 5.1.2 | 5.1.2 | safe patch |
  | `@types/node` | dev | 26.6.6 | 26.6.11 | 26.6.11 | safe patch |
  | `@types/react` | dev | 19.3.0 | 19.3.3 | 19.3.3 | safe patch |
  | `vite` | dev (build) | 8.3.8 | 8.3.8 | 8.4.0 | skip (minor) |
  | `typescript` | dev | 6.0.3 | 6.0.3 | 6.0.3 [7.0.2] | skip (TypeScript 7 needs its own ADR, AGENTS.md section 3) |
  | `@playwright/test` | dev | 1.65.1 | 1.65.1 | 1.65.1 | none |
  | `@tailwindcss/vite` / `tailwindcss` | dev | 4.3.5 | 4.3.5 | 4.3.5 | none |
  | `@tanstack/router-plugin` | dev | 1.169.0 | 1.169.0 | 1.169.0 | none |
  | `@testing-library/react` | dev | 16.3.3 | 16.3.3 | 16.3.3 | none |
  | `@types/react-dom` | dev | 19.3.0 | 19.3.0 | 19.3.0 | none |
  | `@vitejs/plugin-react` | dev | 6.3.0 | 6.3.0 | 6.3.0 | none |
  | `jsdom` | dev | 31.0.1 | 31.0.1 | 31.0.1 | none |
  | `openapi-typescript` | dev | 7.13.0 | 7.13.0 | 7.13.0 | none |
  | `pnpm` (`packageManager`, CI) | tool | 12.21.0 | 12.21.0 | 12.24.0 | skip (minor) |
- 2026-11-26, Phase 15 follow-ups: the setup wizard card is `max-w-2xl` (672 px) on every step, which supersedes the 15e final-review note that left it widening on step 5. `max-w-xl` (576 px) was measured first and truncated the generated SMTP username on step 5 at 1280 px; `server-onboarding-settings-tokens.spec.ts` now asserts the same card width on steps 1 and 5 and that no connection value is truncated (default and WordPress tabs). Long snippet lines still scroll inside the focusable code block, as before.
- 2026-11-26, Phase 15 follow-ups: the projects home keeps the empty `aria-busy` pane (no skeleton) while the single-project redirect decides; UI.md 4.6 now names it as the one exception to "skeletons, not spinners". A project card skeleton there is exactly the projects-page flash that 15c recorded and 15e removed, and the pane lasts one request. A Vitest case pins it (no skeleton during the first load, then the redirect). Also in the follow-ups: the invite page's signed-in "Go to projects" on an unavailable invite follows the same rule as "Back to projects" ("Go to inbox"), and a non-404 failure on a single-project user's only project shows only a primary "Try again" (with `RefreshCw`, like the other refresh actions); other load errors keep the back link and "Retry", so no existing selector changed.
- 2026-11-26, release (owner decision): the first release is **v0.1.0**, not v1.0.0, because the project is pre-alpha with no users. v1.0.0 will be tagged once the REST API, configuration and data format are declared stable; until then a 0.x minor version may change them, with upgrade steps in `CHANGELOG.md`. The `v1.0.0-rc.1` rehearsal (2026-11-25) published a GitHub pre-release with its tag and archives, and (per `.goreleaser.yaml`) only the GHCR tag `1.0.0-rc.1` (pre-releases get no `1.0` or `latest`). The owner deleted the rc.1 GitHub release and its tag on 2026-11-26 (`gh release delete v1.0.0-rc.1 --cleanup-tag`); the GHCR `1.0.0-rc.1` image tags remain and the owner will remove them (needs a token with `delete:packages`). No Homebrew formula was ever published (`skip_upload: auto` skips pre-releases; `mailcubby/homebrew-tap` holds only its README), so nobody needs `brew reinstall`, and v0.1.0 is the first real run of the tap upload and the `HOMEBREW_TAP_GITHUB_TOKEN` check in `release.yml`. `release.yml` derives the notes file from the tag, so `docs/release-notes-v0.1.0.md` is picked up without a workflow change.
- 2026-11-27, v0.2 planning (owner decisions): approved Phases 16a to 16e with zero new Go/npm dependencies and zero SQLite schema migrations: (1) inline PDF preview via dedicated `GET /render/{project}/{id}/attachments/{attachment_id}` with PDF-specific non-sandbox CSP, byte-0 `%PDF-` magic-byte check, `nosniff`, `default-src 'none'`, and `MAILCUBBY_RENDER_INLINE_PDF=true` default (ADR 0009); (2) offline HTML compatibility check using a compacted zstd-compressed `caniemail.com` snapshot (MIT license, ~15–30 KB compressed, manual maintainer update script) embedded in `internal/compat/data/`; (3) link checker with offline extraction (`GET .../links`, `PermViewerTech`) always on, active `POST .../links/check` (`PermMember`, DialContext SSRF/DNS-rebinding defenses, ADR 0010) controlled by `MAILCUBBY_LINK_CHECK_ENABLED` defaulting to `true` in local mode and `false` in server mode; (4) two separate lazy-loaded message view tabs: `Links` (`8`) and `HTML check` (`9`).
- 2026-11-27, Phase 16b: `internal/compat/data/caniemail.json.zst` embeds all 308 `caniemail.com` features across 18 curated email clients (4 Gmail, 5 Outlook distinguishing `outlook.windows` Classic from `outlook.outlook-com` Web / New Outlook, 2 Apple Mail, 3 Yahoo, 4 Other: Thunderbird, Samsung Email, ProtonMail, Fastmail) at 18,890 bytes zstd-compressed (`NOTICE` carries the MIT attribution). The generator preserves JSON insertion order when reading `stats[family][platform]` so the chronologically latest tested version is always selected (e.g. `16.80` after `2016` on Outlook macOS). Weighted scoring evaluates each unique detected feature once across all known client cells (`supported` = 1.0, `partial` = 0.5, `unsupported` = 0.0; `unknown` excluded) using per-client tier weights, and returns `supported_pct`/`partial_pct`/`unsupported_pct` summing to 100.0 alongside overall `score` and ordered `features` (most unsupported first). `FuzzAnalyzeHTML` was also added to `.github/workflows/ci.yml`'s `fuzz` job (30s).
- 2026-11-27, Phase 16c: `HtmlCheckTab` is lazy-loaded as its own Vite chunk (~4 KB gzip; initial JS bundle remains unchanged at ~149 KB gzip, well under the 250 KB budget) and wired as tab `HTML check` (`?tab=check`, shortcut key `9`, leaving `8` reserved for Phase 16e `Links`). Client family filters (`All`, `Gmail`, `Outlook`, `Apple Mail`, `Yahoo`, `Other`) recompute weighted compatibility percentages and issue ordering for the selected client subset using the same formula as `internal/compat/score.go`, while `"All"` displays the backend summary directly. External `caniemail.com` links are validated to `http:`/`https:` schemes and rendered with `target="_blank" rel="noopener noreferrer"` inside the disclosure panel (never nested inside the disclosure button).
