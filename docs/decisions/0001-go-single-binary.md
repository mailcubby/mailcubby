# ADR 0001: Go, single static binary with embedded UI

- Status: Accepted
- Date: 2026-11-23

## Context

Mailcubby must be trivial to install for a single developer (`docker run`) and for a small agency server, with a small image (target 15 to 20 MB), low memory use, and no external services. The reference tool, Mailpit, is a Go binary with an embedded UI, and its users expect the same operational model. The team is small and uses AI coding agents heavily, so a single language for the backend and a simple build matter.

Alternatives considered: Node.js/TypeScript backend (larger runtime image, more dependencies, weaker SMTP libraries), PHP/Laravel (needs a web server, queue worker and process manager; heavier image), Rust (excellent fit technically, slower iteration for the team and for agents on this kind of CRUD-heavy app).

## Decision

- Backend in Go 1.27, built with `CGO_ENABLED=0` into one static binary.
- The React SPA is built by Vite and embedded with `go:embed`; the binary serves SMTP, HTTP, API, SSE and background workers in one process.
- Standard library first: `net/http` with Go 1.22+ routing patterns (no router framework), `log/slog`, `database/sql`, `crypto/*`.
- Runtime image `gcr.io/distroless/static-debian13:nonroot`; multi-arch builds and releases with GoReleaser.

## Consequences

- One artifact to version, test and ship; works the same as a binary on macOS/Windows and as a container.
- CGO is unavailable, which constrains the SQLite driver choice (see ADR 0002) and rules out some C libraries.
- The UI must be built before `go build` for release binaries (`make build-all`); a placeholder page is served when it is not.
- Horizontal scaling is out of scope; one process owns the data directory.
