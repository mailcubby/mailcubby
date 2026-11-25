# Mailcubby v1.0.0

We are excited to release **Mailcubby v1.0.0** — a self-hosted, multi-tenant email testing server built for developers and agencies.

Mailcubby ships as a single static Go binary (~10–11 MB compressed container image) with an embedded React UI, SQLite (WAL + FTS5) metadata/search, and zstd-compressed `.eml` blob storage.

## Highlights

- **Zero-config local mode (Mailpit drop-in)**: listens on `:1025` (SMTP) and `:8025` (HTTP), accepts any SMTP credentials, auto-creates a project per SMTP username, honours `X-Tags` and plus-addressing, and includes `mailcubby sendmail` for PHP `sendmail_path`.
- **Multi-tenant server mode**: isolated projects, per-project SMTP credentials (encrypted at rest with AES-256-GCM), `owner`/`member`/`viewer` roles, single-use invite links, client-safe viewer mode (with optional hidden technical tabs and "Preview as viewer"), personal API tokens, and a full audit log.
- **Safe untrusted HTML preview**: email HTML is never inserted into the app DOM; it renders exclusively through `/render/{project}/{id}` inside a strict sandboxed `<iframe>` with remote images blocked by default and opt-in per message.
- **Fast search & realtime UI**: FTS5 search with operators (`from:`, `to:`, `subject:`, `has:`, `is:`, `tag:`, `before:`, `after:`, `credential:`), Server-Sent Events live inbox updates, virtualised lists, keyboard navigation (`j`/`k`, `1`–`7`, `e` with undo, `Cmd/Ctrl+K`), framework integration snippets, and WCAG 2.1 AA accessibility.
- **Test-friendly API**: OpenAPI 3.1 REST API with cursor pagination, `POST /send-test`, and `GET /messages/wait` for CI and end-to-end test suites.
- **Production ready**: STARTTLS and implicit TLS (`:1465`) with live 30 s certificate reloading, bounded retention/quotas/GC, rate limiting & brute-force protection, and a ready-to-run Docker Compose + Caddy stack (`deploy/compose.server.yml`).

## Quick Start

### Local mode (Docker)

```sh
docker run --rm \
  -p 8025:8025 \
  -p 1025:1025 \
  -v mailcubby-data:/data \
  ghcr.io/mailcubby/mailcubby:v1.0.0
```

Open `http://localhost:8025` and point your app's SMTP host to `localhost:1025`.

### Server mode (Docker Compose + Caddy)

```sh
export MAILCUBBY_DOMAIN=mail.example.com
export MAILCUBBY_APP_KEY=$(docker run --rm ghcr.io/mailcubby/mailcubby:v1.0.0 keygen)
docker compose -f deploy/compose.server.yml up -d
```

See [README.md](README.md) for reverse-proxy setup (Caddy, Traefik, nginx), backups, and Mailpit migration notes.

## Downloads & Checksums

- **Container images**: `ghcr.io/mailcubby/mailcubby:1.0.0`, `:1.0`, `:latest` (`linux/amd64`, `linux/arm64`)
- **Binaries**: `linux`, `darwin`, and `windows` (`amd64` and `arm64`) archives plus `checksums.txt` attached to this release.

## Verification

- 68/68 functional requirements in `docs/SPEC.md` covered by automated Go, Vitest, and Playwright tests.
- WCAG 2.1 AA accessibility verified across all local-mode and server-mode screens via `@axe-core/playwright` (zero serious or critical violations).
- Performance benchmark results recorded in `docs/benchmarks.md`.

**Full Changelog**: See [CHANGELOG.md](CHANGELOG.md).
