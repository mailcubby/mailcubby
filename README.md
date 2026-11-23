# Mailcubby

> **Status: pre-alpha / in development.** Nothing here is usable yet. The documentation in `docs/` is the source of truth for what is being built. APIs, configuration and storage formats will change without notice until v1.0.

Mailcubby is a self-hosted email testing server ("mail catcher") for developers and agencies. Applications send email to Mailcubby over SMTP instead of real recipients; Mailcubby stores the messages and shows them in a fast web UI and a REST API.

It is a single static Go binary (with the web UI embedded) distributed as a small Docker image. It runs in two modes:

- **Local mode** (`MAILCUBBY_MODE=local`, default): zero configuration, no login. A drop-in replacement for Mailpit on a developer machine.
- **Server mode** (`MAILCUBBY_MODE=server`): multi-tenant. Projects, per-project SMTP credentials, users, roles and invites, so one instance can serve a whole team and its clients without anyone seeing another project's mail.

## Why

Existing open-source catchers are excellent for a single developer but stop at one shared inbox:

| | Mailpit | smtp4dev | Mailtrap (SaaS) | Mailcubby |
|---|---|---|---|---|
| Self-hosted, public source code | Yes (MIT) | Yes | No | Yes (PolyForm Shield) |
| Multiple isolated projects / inboxes | No (tags only) | Mailboxes | Yes | Yes |
| SMTP credentials per project | No | No | Yes | Yes |
| Multiple users with logins | No | No (single Basic auth) | Yes (sharing from the paid Team plan) | Yes |
| Per-project roles (owner / member / viewer) | No | No | Yes | Yes |
| Client-safe read-only view | No | No | Partly | Yes |

Mailpit's maintainer has declined multi-inbox, per-credential routing and multi-user support (issues #86, #115, #128) because it would require "a complete rewrite". Mailpit's `--tenant-id` is only a database table prefix, not isolation. Mailtrap has the right model, but it is SaaS, and Mailtrap Local is explicitly not a team tool.

Typical use case: an agency runs one Mailcubby instance for the staging environments of all its client websites. Each client site gets its own project and SMTP credentials. Developers see every project they belong to; a client gets a read-only login to their own project only.

## Features

### v1 (in development)

- SMTP server (PLAIN and LOGIN auth, STARTTLS and implicit TLS, PROXY protocol), messages up to 25 MiB by default.
- Local mode: any SMTP credentials accepted; the SMTP username auto-creates and selects a project.
- Server mode: projects, per-project SMTP credentials (copy, reveal, rotate), users, invites, roles (owner, member, viewer), instance admin.
- Recipient routing: `anything@<project>.<inbox-domain>` and `<project>+tag@<inbox-domain>`.
- Fast inbox UI: three resizable panes, keyboard-first, command palette, realtime arrival, dark and light themes.
- Message views: sandboxed HTML preview (mobile, tablet, desktop), HTML source, plain text, headers, raw source, attachments, `.eml` download.
- Full-text search with operators (`from:`, `to:`, `subject:`, `has:attachment`, `before:`, `after:`, `is:unread`, `tag:`).
- Tags, including plus-addressing tags and the `X-Tags` header (Mailpit compatible).
- REST API v1 (OpenAPI 3.1) with personal API tokens, plus a "wait for message" endpoint for automated tests.
- Realtime updates via Server-Sent Events.
- Retention by age, count and size, per project and globally.
- Integration snippets for Laravel, Symfony, Node.js (Nodemailer), WordPress, Django, Rails and swaks.
- Single static binary, SQLite storage, compressed message blobs, multi-arch Docker image of about 15 to 20 MB.

### Planned (after v1)

Release / relay to real SMTP, HTML compatibility check, spam score, webhooks, MCP endpoint for AI agents, OIDC single sign-on, PostgreSQL, S3-compatible blob storage, POP3, chaos mode (simulated SMTP failures), Prometheus metrics. See `docs/SPEC.md` section "Roadmap".

## Quick start

### Local mode (one developer)

```sh
docker run --rm -p 8025:8025 -p 1025:1025 -v mailcubby-data:/data ghcr.io/mailcubby/mailcubby:latest
```

Open http://localhost:8025 and point your application at SMTP `localhost:1025`. Any username and password work; the username becomes the project name (for example `MAIL_USERNAME=shop-staging` creates the project `shop-staging`). Without credentials, mail goes to the `default` project.

Local mode has no authentication. Do not expose it to the internet.

### Server mode (team)

`deploy/compose.server.yml` (abbreviated):

```yaml
services:
  mailcubby:
    image: ghcr.io/mailcubby/mailcubby:latest
    restart: unless-stopped
    environment:
      MAILCUBBY_MODE: server
      MAILCUBBY_BASE_URL: https://mail.example.com
      MAILCUBBY_APP_KEY: ${MAILCUBBY_APP_KEY}      # generate with: docker run --rm ghcr.io/mailcubby/mailcubby keygen
      MAILCUBBY_INBOX_DOMAIN: inbox.example.com
      MAILCUBBY_SMTP_TLS_CERT: /caddy/caddy/certificates/acme-v02.api.letsencrypt.org-directory/mail.example.com/mail.example.com.crt
      MAILCUBBY_SMTP_TLS_KEY: /caddy/caddy/certificates/acme-v02.api.letsencrypt.org-directory/mail.example.com/mail.example.com.key
      MAILCUBBY_TRUSTED_PROXIES: 172.16.0.0/12
    ports:
      - "587:1025"                               # SMTP with STARTTLS, exposed directly
    volumes:
      - mailcubby-data:/data
      - caddy-data:/caddy:ro
  caddy:
    image: caddy:2
    restart: unless-stopped
    ports: ["80:80", "443:443"]
    command: caddy reverse-proxy --from mail.example.com --to mailcubby:8025
    volumes:
      - caddy-data:/data
volumes:
  mailcubby-data:
  caddy-data:
```

The Caddy certificate files must be readable by the Mailcubby container user (UID 65532); the final `deploy/compose.server.yml` takes care of this and documents how.

```sh
docker compose -f deploy/compose.server.yml up -d
docker compose logs mailcubby | grep "setup token"
```

Open https://mail.example.com, enter the setup token from the log, and create the first admin account. Then create a project, copy its SMTP credentials and invite your team.

## Configuration

All configuration is via environment variables prefixed with `MAILCUBBY_`. The full table is in `docs/SPEC.md` section 9. The most common ones:

| Variable | Default | Description |
|---|---|---|
| `MAILCUBBY_MODE` | `local` | `local` (no login) or `server` (multi-tenant with login). |
| `MAILCUBBY_DATA_DIR` | `/data` in Docker, `./data` otherwise | SQLite database and message blobs. |
| `MAILCUBBY_HTTP_ADDR` | `:8025` | Web UI and API listen address. |
| `MAILCUBBY_SMTP_ADDR` | `:1025` | SMTP listen address. |
| `MAILCUBBY_BASE_URL` | (empty) | Public URL of the UI. Required in server mode. |
| `MAILCUBBY_APP_KEY` | (empty) | 32-byte base64 key encrypting SMTP secrets. Required in server mode. |
| `MAILCUBBY_INBOX_DOMAIN` | (empty) | Enables recipient routing, e.g. `inbox.example.com`. |
| `MAILCUBBY_SMTP_TLS_CERT` / `_KEY` | (empty) | PEM files for STARTTLS; reloaded automatically when they change. |
| `MAILCUBBY_SMTP_MAX_MESSAGE_BYTES` | `25MiB` | Maximum accepted message size. |
| `MAILCUBBY_RETENTION_MAX_AGE` | `30d` | Default retention per project (`0` disables). |

See `.env.example` for a commented template.

## Development

Requirements: Go 1.27.4, Node.js 26 LTS, pnpm 12.21. See `AGENTS.md` for conventions and `docs/PLAN.md` for the implementation plan.

```sh
make tools      # install pinned dev tools (sqlc, golangci-lint)
make deps       # download Go modules, web packages and Playwright Chromium (needs network once)
make dev        # Go server on :8025/:1025 + Vite dev server on :5173
make seed       # send the sample emails in testdata/mime/ to the running instance
make check      # generate, lint, test, build: must pass before every commit
```

## Documentation

- `docs/SPEC.md`: product specification (requirements, API, data model, security).
- `docs/ARCHITECTURE.md`: components, flows, package layout, deployment.
- `docs/UI.md`: design principles and screens.
- `docs/PLAN.md`: phased implementation plan.
- `docs/decisions/`: architecture decision records.

## License

PolyForm Shield 1.0.0. Copyright (c) 2026 NOOP. See `LICENSE`.
