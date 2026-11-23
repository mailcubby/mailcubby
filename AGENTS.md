# AGENTS.md

Instructions for AI coding agents (Google Antigravity, Codex, Cursor, others) and humans working on this repository. Read this file completely before changing anything. This is the only agent instruction file: there is no tool-specific copy, and any tool-specific file (such as `.agents/skills/next-phase/SKILL.md`) only points back here. Keep this file under 24,000 bytes (Antigravity truncates larger rule files).

## 1. Project summary

Mailcubby is a self-hosted email testing server ("mail catcher"), an alternative to Mailpit and Mailtrap. Applications send mail to its SMTP port; Mailcubby stores it and shows it in a web UI and REST API. Unlike Mailpit, it is multi-tenant: projects, per-project SMTP credentials, users, roles (owner, member, viewer) and invites, so an agency can share one instance with its team and clients. It also has a zero-config local mode for a single developer.

It ships as one static Go binary with the React UI embedded, in a Docker image of about 15 to 20 MB. Storage is SQLite (metadata, FTS5 search) plus zstd-compressed `.eml` files.

Naming: `mailcubby` (binary, CLI, Docker image, lowercase identifiers), `Mailcubby` (display name), `MAILCUBBY_` (env var prefix), `github.com/mailcubby/mailcubby` (Go module). Use them exactly and consistently. Do not invent other names or abbreviations for the product.

Status: pre-alpha. Copyright NOOP, PolyForm Shield 1.0.0 license (source-available: free to use, including commercially, but not to build a competing product; see `LICENSE` and ADR 0007).

## 2. Source-of-truth documents

| Document | Use it for |
|---|---|
| `docs/SPEC.md` | What to build: requirements (FR-xxx), routing rules, API, config, data model, security. Wins over every other document. |
| `docs/ARCHITECTURE.md` | How to build it: components, flows, package layout, testing, deployment. |
| `docs/UI.md` | Frontend design, screens, interactions. |
| `docs/PLAN.md` | Order of work: phases with checkboxes and acceptance criteria. |
| `docs/decisions/` | ADRs: why key choices were made. Do not reverse them without a new ADR. |

## 3. Tech stack (pinned versions, verified 2026-11-23)

### Backend

| Component | Version | Notes |
|---|---|---|
| Go | 1.27 (`go 1.27` + `toolchain go1.27.4` in `go.mod`) | `CGO_ENABLED=0` for builds |
| `github.com/emersion/go-smtp` | v0.26.0 | SMTP server |
| `github.com/emersion/go-sasl` | v0.0.0-20241020182733-b788ff22d5a6 | PLAIN server; no tagged releases. LOGIN server is ours (`internal/smtpd/saslogin.go`) |
| `github.com/jhillyerd/enmime/v2` | v2.5.0 | MIME parsing |
| `modernc.org/sqlite` | v1.63.0 | Pure Go SQLite driver (FTS5 included) |
| `github.com/pressly/goose/v3` | v3.30.1 | Migrations, embedded, Provider API |
| `sqlc` (CLI, dev tool) | v1.31.1 | Release binary installed by `make tools`, not a Go dependency |
| `github.com/klauspost/compress` | v1.20.5 | zstd |
| `github.com/oklog/ulid/v2` | v2.1.2 | Message IDs |
| `golang.org/x/crypto` | v0.59.0 | argon2id |
| `golang.org/x/net` | v0.63.0 | `html` tokenizer for render rewriting |
| `golang.org/x/sync` | v0.25.0 | `errgroup`, `semaphore` |
| `github.com/pires/go-proxyproto` | v0.16.0 | PROXY protocol for SMTP |
| `golangci-lint` (dev tool) | v2.15.0 | Release binary via `make tools` |
| GoReleaser (CI tool) | v2.20.3 | Releases and multi-arch images |
| `github.com/caddyserver/certmagic` | v0.25.6 | Optional, Phase 12 stretch only. Do not add earlier. |

HTTP routing uses the standard library `net/http.ServeMux` (Go 1.22+ method and wildcard patterns). Do not add chi, gin, echo or similar. Logging uses `log/slog`. HKDF uses stdlib `crypto/hkdf`.

### Frontend (`web/`)

| Component | Version | Notes |
|---|---|---|
| Node.js | 26 LTS | build only |
| pnpm | 12.21.0 | `packageManager` field in `web/package.json` |
| React / React DOM | 19.3.1 | |
| Vite | 8.3.8 | with `@vitejs/plugin-react` 6.3.0 |
| TypeScript | 6.0.3 | Stay on 6.0.x; TypeScript 7 (native) is a later upgrade with its own ADR |
| Tailwind CSS | 4.3.5 | via `@tailwindcss/vite` 4.3.5, CSS-first config |
| shadcn/ui CLI | 4.23.0 | `pnpm dlx shadcn@4.23.0 add <component>` |
| `@tanstack/react-router` | 1.170.46 | file-based routes via `@tanstack/router-plugin` 1.169.0 |
| `@tanstack/react-query` | 5.108.3 | all server state |
| `@tanstack/react-virtual` | 3.14.28 | message list virtualisation |
| `cmdk` | 1.1.1 | command palette (via shadcn `command`) |
| `sonner` | 2.0.8 | toasts (via shadcn `sonner`) |
| `react-resizable-panels` | 4.24.1 | panes (via shadcn `resizable`) |
| `lucide-react` | 1.78.0 | icons |
| `@fontsource-variable/inter` | 5.3.0 | self-hosted Inter (no Google Fonts requests) |
| `shiki` | 4.6.0 | read-only syntax highlighting, lazy-loaded, `shiki/core` + JavaScript regex engine (no WASM) |
| `openapi-typescript` / `openapi-fetch` | 7.13.0 / 0.17.0 | types and typed client from `internal/api/openapi.yaml` |
| `@biomejs/biome` | 2.6.8 | lint + format (no ESLint, no Prettier) |
| Vitest | 5.1.1 | with `@testing-library/react` 16.3.3, `jsdom` 31.0.1 |
| `@playwright/test` | 1.65.1 | e2e from Phase 4a (Chromium only); `@axe-core/playwright` 4.14.0 added in Phase 13 |

Pin exact versions in `go.mod` and `web/package.json` (no `^` or `~` for runtime dependencies). Commit `go.sum` and `web/pnpm-lock.yaml`.

## 4. Repository layout

See `docs/ARCHITECTURE.md` section 5 for the full tree. Key paths:

- `cmd/mailcubby/`: entry point and subcommands.
- `internal/<pkg>/`: all Go code (`app`, `cli`, `config`, `smtpd`, `ingest`, `blob`, `store`, `search`, `auth`, `secrets`, `ratelimit`, `api`, `events`, `render`, `retention`, `tlsutil`, `clock`, `web`, `testutil`).
- `internal/store/queries/*.sql`: sqlc input. `internal/store/sqlc/`: generated, do not edit.
- `internal/api/openapi.yaml`: API contract. `web/src/api/schema.d.ts`: generated, do not edit.
- `migrations/`: goose SQL migrations (sequential `00001_init.sql`, `00002_...`).
- `testdata/mime/`: MIME fixtures and golden files. `testdata/hostile/`: hostile HTML emails (Phase 11).
- `web/`: Vite React app. `web/src/routeTree.gen.ts`: generated by the TanStack Router Vite plugin, committed, do not edit. `web/e2e/`: Playwright specs.
- `deploy/`: Dockerfiles, compose files, Caddyfile.
- `.agents/skills/next-phase/SKILL.md`: Antigravity skill (`/next-phase`) that runs section 7 for the next PLAN phase.

## 5. Commands

All through the `Makefile`. Run from the repo root.

| Command | What it does |
|---|---|
| `make help` | List targets. |
| `make tools` | Download pinned `sqlc` and `golangci-lint` binaries into `./bin/` (network). |
| `make deps` | `go mod download`, `pnpm --dir web install --frozen-lockfile`, and (once `web/playwright.config.ts` exists) `pnpm --dir web exec playwright install chromium` (network). After `make tools` and `make deps`, every other target works offline. |
| `make generate` | `sqlc generate` (skipped while `sqlc.yaml` does not exist) and OpenAPI TypeScript types (`pnpm --dir web gen:api`, skipped while `internal/api/openapi.yaml` does not exist). |
| `make web` | `pnpm install --frozen-lockfile && pnpm build` in `web/`, then sync `web/dist/` into `internal/web/dist/` (keeps `.gitkeep`). |
| `make build` | `CGO_ENABLED=0 go build -trimpath` to `bin/mailcubby` (embeds whatever is in `internal/web/dist`). |
| `make build-all` | `make web` then `make build`. |
| `make run` | Local mode: `go run ./cmd/mailcubby serve` with `MAILCUBBY_DATA_DIR=./data`. |
| `make run-server` | Server mode for development: dev app key, `MAILCUBBY_BASE_URL=http://localhost:8025`, `MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH=true`, data in `./data-server`. |
| `make dev` | `make run` plus Vite dev server on :5173 (proxies `/api`, `/render` to :8025). `make dev MODE=server` runs `run-server` instead, with `MAILCUBBY_BASE_URL=http://localhost:5173` so Origin/CSRF checks pass through the Vite proxy. Long-running: start it in the background. |
| `make test` | `go test ./...` |
| `make test-race` | `CGO_ENABLED=1 go test -race ./...` |
| `make test-web` | `pnpm --dir web test` (Vitest, run mode). |
| `make lint` | `golangci-lint run`, `biome check web`, `tsc --noEmit` in `web/`. |
| `make fmt` | `golangci-lint fmt` (gofmt, goimports) and `biome format --write`. |
| `make check` | `generate` and fail if that run changed any generated file (hash snapshot before/after, not `git diff`, so uncommitted work is fine), then `lint`, `test`, `test-web`, `build-all`. Must pass before you finish any task. |
| `make bench` | Go benchmarks (store, search, ingest). |
| `make e2e` | `build-all`, then Playwright tests (Chromium) against the fresh binary on free ports with temp data dirs (from Phase 4a). |
| `make seed` | Send every `testdata/mime/*.eml` to a running instance with `bin/mailcubby sendmail` (`SMTP=localhost:1025 USER=<project> COUNT=1`). From Phase 2. |
| `make docker` | `docker build -f deploy/Dockerfile -t mailcubby:dev .` |
| `make migration name=add_x` | Create the next sequential goose SQL file in `migrations/` (plain shell, no goose CLI). Add it to the `sqlc.yaml` schema list unless sqlc cannot parse it (ARCHITECTURE 3.5). |
| `make clean` | Remove `bin/`, `web/dist/`, `internal/web/dist/*` except `.gitkeep`. |

## 6. Coding conventions

### Go

- **Stdlib first.** Use the standard library unless a dependency from the table above is the right tool. `net/http`, `log/slog`, `database/sql`, `crypto/*`, `encoding/json`, `flag`.
- **Small packages** with one responsibility, named by what they provide (`blob`, `search`), lowercase singular, no `util`/`common`/`helpers`. Keep files under about 500 lines.
- **No globals.** No mutable package-level variables, no `init()` with side effects, no singletons. Allowed at package level: constants, sentinel errors, immutable lookup tables, compiled regexps, and the `version`/`commit`/`date` vars in `package main` set by `-ldflags`. Dependencies are passed into constructors (`func NewSweeper(st *store.Store, blobs *blob.Store, clk clock.Clock, log *slog.Logger, cfg Config) *Sweeper`).
- **Context.** `ctx context.Context` is the first parameter of anything that does I/O or may block. Never store a context in a struct. Respect cancellation in loops.
- **Errors.** Wrap with context: `fmt.Errorf("store: get message %s: %w", id, err)`. Messages lowercase, no trailing punctuation. Use `errors.Is/As`, sentinel errors (`store.ErrNotFound`) and typed errors where callers branch. Either handle or return an error, never both log and return. Log at the boundary (HTTP handler, SMTP reply, worker loop). No `panic` outside `main` and impossible-state assertions. Exception: go-smtp `Session` methods and SASL callbacks return a bare `*smtp.SMTPError` (go-smtp does not unwrap; ARCHITECTURE 3.3).
- **Interfaces** are declared by the consumer, small (1 to 3 methods), and only when there are two implementations or a test needs a fake. Constructors return concrete types.
- **Time** comes from an injected `clock.Clock`; never call `time.Now()` in logic that tests need to control.
- **Logging** with an injected `*slog.Logger`; structured keys in `snake_case` (`project_id`, `message_id`, `remote_ip`). Never log secrets, passwords, tokens, cookie values or message bodies.
- **SQL** lives in `internal/store/queries/*.sql` (sqlc). Hand-written SQL is allowed only in `internal/search` (dynamic search and FTS) and must use bind parameters. Every tenant query filters by `project_id` (see section 8).
- **Concurrency**: prefer `errgroup` and channels with clear ownership; every goroutine has a documented exit condition; no goroutine leaks (tests use `goleak` only if added via ADR, otherwise careful review).
- **Tests**: table-driven with `t.Run(tc.name, ...)`, `t.Parallel()` where safe, `t.TempDir()` for files, real SQLite (no DB mocks), fake clocks, no `time.Sleep` for synchronisation, no external network. Reference requirement IDs (`// FR-021`). Golden files updated only with `-update` and reviewed.
- **Docs**: every exported identifier has a doc comment starting with its name.
- Format and imports by `golangci-lint fmt`. Lint config in `.golangci.yml` (govet, staticcheck, errcheck, errorlint, gosec, revive, bodyclose, noctx, rowserrcheck, sqlclosecheck, unparam, misspell, gocritic, depguard).

### Frontend

- TypeScript `strict` plus `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`. No `any` (Biome `noExplicitAny` as error); use `unknown` and narrow.
- Function components and hooks only. Components in `PascalCase.tsx`, hooks `useThing.ts`, colocated tests `*.test.tsx`.
- **Server state only in TanStack Query.** No Redux, Zustand or global stores. Query keys from one factory (`web/src/lib/queryKeys.ts`). SSE events update the cache via `queryClient.setQueryData`, not refetch storms.
- **API access only through the typed `openapi-fetch` client** (`web/src/lib/api.ts`) using generated types. Never hand-write API response types.
- Routing with TanStack Router file routes; search params validated with `validateSearch`. Route-level code splitting for settings, admin, and source/raw viewers.
- UI components from shadcn/ui in `web/src/components/ui/` (owned code, edit as needed). Styling only with Tailwind classes and the CSS variables in `web/src/styles/globals.css`; merge classes with `cn()`. No CSS-in-JS, no CSS modules, no inline style objects except computed dimensions.
- Icons from `lucide-react` only. Font: Inter via `@fontsource-variable/inter`.
- Persisted UI preferences (pane sizes, theme, selected tab, device) through `usePersistentState` (localStorage, namespaced `mailcubby:`). Never store tokens or CSRF tokens in localStorage.
- Dates and numbers via `Intl` APIs; no date libraries.
- Accessibility is part of done: keyboard reachable, visible focus, labelled controls, `aria-live` for realtime announcements (see `docs/UI.md`).
- Biome is the only linter/formatter. `noDangerouslySetInnerHtml` is an error and must never be suppressed.

## 7. How to work

1. Read `docs/SPEC.md` and `docs/ARCHITECTURE.md` fully at the start of every session. Read `docs/UI.md` for any frontend work and the ADRs in `docs/decisions/` that touch your area.
2. Open `docs/PLAN.md`, find the **first phase with unchecked tasks** (phases may be split, e.g. `3a`, `3b`), and work only on that phase. Do not start the next phase in the same session unless the current one is complete and the owner asks.
3. Before coding, write a short plan (files to create or change, tests to add, spec ambiguities). The project is built autonomously: **proceed without waiting for approval** unless a stop condition (below) applies or the owner asked to review plans.
4. Implement in small, reviewable steps. After each task: run the relevant tests, then tick its checkbox (`- [x]`) in `docs/PLAN.md` in the same change.
5. Keep `make check` green. Never finish with failing tests, lint errors or a dirty generated-code diff. Never delete or skip a test to make it pass; fix the code or, if the test is wrong, explain why in the summary.
6. **Dependencies**: use only what is in section 3. Adding a dependency is a stop condition. Prefer 50 lines of our own code over a small dependency.
7. **Spec gaps**: if the spec is ambiguous or wrong, choose the most conservative, secure option, implement it, and record it under "Open questions" at the end of `docs/PLAN.md` (and fix the spec text if the answer is obvious). Do not silently diverge.
8. **Docs move with code**: a behaviour change updates `docs/SPEC.md`, `internal/api/openapi.yaml` and, if structural, `docs/ARCHITECTURE.md` in the same change. Architectural reversals need a new ADR.
9. Do not edit generated files (`internal/store/sqlc/`, `web/src/api/schema.d.ts`, `web/src/routeTree.gen.ts`); change the source and run `make generate`.
10. Commits follow Conventional Commits (`feat(smtpd): ...`, `fix(api): ...`, `docs: ...`, `test: ...`, `chore: ...`), one logical change each. Do not commit or push unless the owner asks; when commits are enabled, commit at least once per completed task.
11. **Blocked criteria**: if an acceptance criterion needs something unavailable in your environment (Docker, network, a GitHub CI run, a human), do everything else, leave that box unticked with `(blocked: <reason>)` appended, record it in Open questions, and say so in the summary. Criteria marked `(owner)` in PLAN are verified by the owner, not by you; do not block on them.

**Stop conditions** (stop, explain, and ask the owner): adding or upgrading a dependency; reversing an ADR or a section 8 security rule; an acceptance criterion that contradicts the spec or cannot be met by any reasonable implementation; a phase that turns out to be much larger than one session (propose a split in PLAN instead of rushing); anything that needs credentials or publishing (GitHub, GHCR, domains).

**UI verification**: UI behaviour is verified by Playwright specs in `web/e2e/` (`make e2e`), which are the acceptance evidence. An agent with a browser (for example Antigravity's browser subagent) may additionally open the running app (`make run` in the background, then `http://localhost:8025`), take screenshots of the screens it changed and attach them to its summary; this complements but never replaces the Playwright specs.

**Terminal hygiene**: run long-lived processes (`make run`, `make dev`, `make run-server`) in the background and stop them when done; never leave a foreground command waiting for input; use free ports or the defaults; keep dev data in `./data*` (gitignored); tests use `t.TempDir()`.

## 8. Security rules (non-negotiable)

1. **Never render email HTML unsandboxed.** Email HTML is shown only through `GET /render/{project}/{id}` inside `<iframe sandbox="allow-popups allow-popups-to-escape-sandbox">` with the CSP from SPEC 12.1. Never `allow-scripts`, never `allow-same-origin`, never `dangerouslySetInnerHTML`, never `innerHTML` with message content anywhere in the app.
2. **Tenant isolation in every query.** Every store method on tenant data takes `projectID` and its SQL filters by `project_id`. Never look up a message, attachment, tag, credential, member or invite by ID alone. Handlers use the project resolved by middleware, never the raw slug. Inaccessible projects return `404`.
3. **Every new route** goes into the route table with a permission and into `openapi.yaml`; the authorisation matrix test must cover it.
4. Secrets: generate with `crypto/rand`; store tokens as SHA-256, passwords as argon2id, SMTP passwords encrypted with AES-GCM under `MAILCUBBY_APP_KEY`. Compare with `crypto/subtle`. Never log or return secrets except the one-time creation/reveal responses defined in the spec.
5. Attachments and raw sources are served with `Content-Disposition: attachment` (except the allowlisted inline types), `X-Content-Type-Options: nosniff` and the attachment CSP. Never serve message content as `text/html` outside the render endpoint.
6. Cookie-authenticated unsafe requests require the CSRF header and Origin check.
7. Parameterised SQL only. Validate all input at the API boundary (lengths, enums, slugs). Limit request body sizes.
8. SMTP AUTH only over TLS in server mode (unless explicitly configured otherwise), with brute-force lockout.
9. File paths for blobs are built only from integer IDs and ULIDs.
10. If you find a security issue, fix it with a regression test and mention it explicitly in your summary.

## 9. Definition of done (per task and per phase)

- [ ] All acceptance criteria of the task/phase in `docs/PLAN.md` are met and covered by automated tests.
- [ ] `make check` passes locally (and `make test-race` for concurrency changes; `make e2e` for any phase with UI tasks).
- [ ] New or changed endpoints are in `openapi.yaml`, the route table and the authorisation matrix.
- [ ] Tenant isolation tests updated for any new tenant data.
- [ ] Docs updated (`SPEC`, `ARCHITECTURE`, `UI`, ADR) where behaviour or structure changed.
- [ ] Checkboxes ticked in `docs/PLAN.md`; open questions recorded.
- [ ] No new lint warnings, no skipped tests, no `TODO` without a phase reference (`TODO(phase-10): ...`).
- [ ] Summary to the user lists what changed, how it was tested, and any deviations from the spec.

## 10. Environment notes

- Toolchain: Go 1.27.4 exactly (matches `toolchain` in `go.mod`, so no toolchain download), Node.js 26 LTS, pnpm 12.21.0 (`npm install -g pnpm@12.21.0`; Corepack is not bundled with Node.js 26). `make test-race` needs a C compiler (`CGO_ENABLED=1`).
- Network is needed only for `make tools`, `make deps`, `make docker` and when adding a dependency (a stop condition). Agent sandboxes that block network by default (Antigravity's terminal sandbox does) need the owner to approve these commands once; everything else, including `make check` and `make e2e`, runs offline.
- Antigravity loads this file automatically as an always-on rule (workspace `AGENTS.md`). Type `/next-phase` to run the next phase via `.agents/skills/next-phase/SKILL.md`. Other agents: start with the kickoff prompt in `docs/PLAN.md`.
