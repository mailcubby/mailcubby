# Mailcubby

> **Pre-release.** Mailcubby is new and still a pre-release: expect rough edges, and back up your data before upgrading. See `docs/SPEC.md` for the complete specification.

Mailcubby is a self-hosted email testing server ("mail catcher") for developers and agencies. Applications send email to Mailcubby over SMTP instead of real recipients; Mailcubby stores the messages and shows them in a fast web UI and REST API.

It ships as a single static Go binary (with the React web UI embedded) and a multi-arch Docker image (~9 MB compressed). It runs in two modes:

- **Local mode** (`MAILCUBBY_MODE=local`, default): zero configuration, no login. A drop-in replacement for Mailpit on a developer machine.
- **Server mode** (`MAILCUBBY_MODE=server`): multi-tenant. Projects, per-project SMTP credentials, users, roles (owner, member, viewer), invites and API tokens, so one instance can serve a whole team and its clients without anyone seeing another project's mail.

## Why

Existing open-source catchers are excellent for a single developer but stop at one shared inbox:

| | Mailpit | smtp4dev | Mailtrap (SaaS) | Mailcubby |
|---|---|---|---|---|
| Self-hosted, public source code | Yes (MIT) | Yes | No | Yes (PolyForm Shield) |
| Multiple isolated projects / inboxes | No (tags only) | Mailboxes | Yes | Yes |
| SMTP credentials per project | No | No | Yes | Yes |
| Multiple users with logins | No | No (single Basic auth) | Yes (Team plan+) | Yes |
| Per-project roles (owner / member / viewer) | No | No | Yes | Yes |
| Client-safe read-only view | No | No | Partly | Yes |

Typical use case: an agency runs one Mailcubby instance for the staging environments of all its client websites. Each client site gets its own project and SMTP credentials. Developers see every project they belong to; a client gets a read-only login to their own project only, with optional hiding of raw/header technical views.

## Features

- **SMTP server**: PLAIN and LOGIN auth, STARTTLS (`:1025` / `:587`) and optional implicit TLS (`MAILCUBBY_SMTPS_ADDR=:1465`) with automatic certificate hot-reload every 30 s, configurable message size up to 100 MiB (25 MiB default), connection and brute-force limits.
- **Local mode**: any SMTP credentials accepted; the SMTP username auto-creates and selects a project.
- **Server mode**: projects, per-project encrypted SMTP credentials (copy, reveal, rotate, disable), users, invites, roles (`owner`, `member`, `viewer`), audit log, instance admin.
- **Recipient routing**: `anything@<project>.<inbox-domain>` and `<project>+tag@<inbox-domain>`.
- **Fast inbox UI**: three resizable panes, virtualised message list, keyboard-first shortcuts, command palette (`Cmd/Ctrl+K`), realtime updates via SSE, dark and light themes.
- **Safe message views**: strictly sandboxed HTML preview (mobile/tablet/desktop, rotation, zoom, dark mode, opt-in remote images), plain text, syntax-highlighted HTML source and headers, raw source, attachments, `.eml` download, envelope/TLS tech details.
- **Full-text search (FTS5)**: diacritic-insensitive search with operators (`from:`, `to:`, `subject:`, `has:attachment`, `has:html`, `has:text`, `before:`, `after:`, `is:unread`, `is:read`, `tag:`, `credential:`, `-` negation).
- **Tags**: manual tags plus automatic tags from the `X-Tags` header (Mailpit compatible) and plus-addressing (`local+tag@domain`).
- **REST API v1 (OpenAPI 3.1)**: personal API tokens (`read`/`write`, optional project scope) and a blocking `/messages/wait` endpoint for automated tests.
- **Retention & quotas**: age, count and size policies per project and globally, instance byte cap, per-project ingest rate limit, automatic orphan blob GC and incremental SQLite vacuum.
- **Integration snippets**: Laravel, Symfony, Node.js (Nodemailer), WordPress (WP Mail SMTP), Django, Rails, `swaks`, and PHP `sendmail_path`.

Planned after v1 (out of scope for v1): outbound release/relay, HTML compatibility/link checks, spam scoring, webhooks, MCP endpoint, OIDC/SSO, PostgreSQL, S3 blob storage, POP3, chaos mode, Prometheus metrics. See `docs/SPEC.md` section 14.

---

## Install

### Homebrew (macOS and Linux, local mode)

```sh
brew install mailcubby/tap/mailcubby
brew services start mailcubby
```

The service runs local mode: web UI on http://localhost:8025, SMTP on `localhost:1025`, data in `$(brew --prefix)/var/mailcubby`, log in `$(brew --prefix)/var/log/mailcubby.log`. It starts again at login. Stop Mailpit (`brew services stop mailpit`) or anything else on ports 1025 and 8025 first.

Like the plain binary, the service listens on all interfaces so Docker containers and VMs can reach it, and local mode has no authentication. To listen on loopback only (or change any other setting), create `~/.homebrew/services/mailcubby.env` (`$XDG_CONFIG_HOME/homebrew/services/mailcubby.env` if `XDG_CONFIG_HOME` is set) and restart. This needs Homebrew 6.0.15 or later; the file is kept across upgrades and must not be group or world writable:

```sh
dir="${XDG_CONFIG_HOME:+$XDG_CONFIG_HOME/homebrew}"; dir="${dir:-$HOME/.homebrew}/services"
mkdir -p "$dir"
(umask 077; cat > "$dir/mailcubby.env" <<'EOF'
MAILCUBBY_HTTP_ADDR=127.0.0.1:8025
MAILCUBBY_SMTP_ADDR=127.0.0.1:1025
EOF
)
brew services restart mailcubby
```

On older Homebrew the service ignores that file: run `MAILCUBBY_HTTP_ADDR=127.0.0.1:8025 MAILCUBBY_SMTP_ADDR=127.0.0.1:1025 mailcubby serve` yourself, or use the LaunchAgent or systemd unit below with those variables added.

Stop with `brew services stop mailcubby`; upgrade with `brew upgrade mailcubby && brew services restart mailcubby`. Homebrew only loads formulae from trusted taps: the fully qualified `brew install mailcubby/tap/mailcubby` trusts the formula for you; otherwise run `brew trust --tap mailcubby/tap` first. Mailcubby is not in homebrew-core (see `docs/decisions/0008-homebrew-formula.md`).

### Docker

Multi-arch images (`linux/amd64`, `linux/arm64`) are published to GHCR:

```sh
docker pull ghcr.io/mailcubby/mailcubby:latest
```

### Prebuilt binaries

Download the archive for your OS and architecture (`linux`, `darwin`, `windows` on `amd64` and `arm64`) from [GitHub Releases](https://github.com/mailcubby/mailcubby/releases), verify against `checksums.txt`, and place `mailcubby` on your `PATH`:

```sh
tar -xzf mailcubby_<version>_linux_amd64.tar.gz
./mailcubby version
```

### Run the Releases binary as a service

Without Homebrew, put the binary at `~/.local/bin/mailcubby` and run it in local mode at login with the service files that ship in the release archive under `deploy/` (data in `~/.local/share/mailcubby`). Run these commands in the directory where you extracted the archive.

macOS LaunchAgent (launchd needs absolute paths, so `sed` fills in your home directory):

```sh
mkdir -p ~/.local/bin && install -m 0755 mailcubby ~/.local/bin/mailcubby
xattr -d com.apple.quarantine ~/.local/bin/mailcubby 2>/dev/null || true  # downloaded with a browser
mkdir -p ~/Library/LaunchAgents
sed "s|__HOME__|$HOME|g" deploy/launchd/dev.mailcubby.plist > ~/Library/LaunchAgents/dev.mailcubby.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.mailcubby.plist
```

The binary is not signed or notarized, so Gatekeeper blocks it while it carries the quarantine attribute that browsers add (`curl` does not). Logs go to `~/Library/Logs/mailcubby.log`. After replacing the binary, restart with `launchctl kickstart -k gui/$(id -u)/dev.mailcubby`. Stop and remove with `launchctl bootout gui/$(id -u)/dev.mailcubby`, then delete `~/Library/LaunchAgents/dev.mailcubby.plist` so it does not start again at login.

Linux systemd user unit:

```sh
mkdir -p ~/.local/bin ~/.config/systemd/user && install -m 0755 mailcubby ~/.local/bin/mailcubby
cp deploy/systemd/mailcubby.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now mailcubby
```

Logs: `journalctl --user -u mailcubby`. To keep it running while you are logged out, run `loginctl enable-linger "$USER"`. Both files keep the default listen addresses; add `MAILCUBBY_HTTP_ADDR=127.0.0.1:8025` and `MAILCUBBY_SMTP_ADDR=127.0.0.1:1025` to the environment for loopback only.

### Build from source

Requires Go 1.27.4, Node.js 26 LTS, and pnpm 12.21.0:

```sh
make tools
make deps
make build-all
./bin/mailcubby version
```

`go install` is not supported: the web UI has to be built and embedded first, so use `make build-all`.

---

## Quick start

### Local mode (single developer)

Run with Docker (bound to localhost only — local mode has no authentication):

```sh
docker run --rm \
  -p 127.0.0.1:8025:8025 \
  -p 127.0.0.1:1025:1025 \
  -v mailcubby-data:/data \
  ghcr.io/mailcubby/mailcubby:latest
```

Or with Docker Compose:

```sh
git clone --depth 1 --branch v1.0.0 https://github.com/mailcubby/mailcubby
cd mailcubby
docker compose -f deploy/compose.local.yml up -d
```

Or with the standalone binary:

```sh
mailcubby serve
```

It stores data in `./data` (relative to the current directory) and logs one line with everything you need, for example `msg="mailcubby ready" url=http://localhost:8025 smtp=localhost:1025 data_dir=/home/me/project/data`. `mailcubby serve --help` lists the common environment variables.

Open http://localhost:8025 and point your application's SMTP settings at `localhost:1025`:

- Any username and password are accepted.
- The SMTP username becomes the project slug and auto-creates the project on first message (for example `MAIL_USERNAME=shop-staging` routes to project `shop-staging`).
- Without SMTP credentials, mail goes to the `default` project (or routes by recipient when `MAILCUBBY_INBOX_DOMAIN` is set).

> **Warning:** Local mode has no authentication. Never expose it to the internet.

Local mode rejects cross-origin `POST`/`PUT`/`PATCH`/`DELETE` requests from browsers (`403`), so a web page you visit cannot change your inbox. curl, SDKs, test clients, the Vite dev proxy and reverse proxies are not affected; behind a proxy that rewrites `Host`, set `MAILCUBBY_BASE_URL` to the URL you open.

### Server mode (team / agency)

Server mode requires HTTPS (terminated by a reverse proxy), a 32-byte `MAILCUBBY_APP_KEY`, and a TLS certificate for SMTP STARTTLS.

#### 1. Configure DNS and firewall

- Point an `A`/`AAAA` record for your instance hostname (e.g. `mail.example.com`) to your server.
- Optional (for recipient routing): point `inbox.example.com` and `*.inbox.example.com` to the server.
- Open TCP ports `80` and `443` (HTTPS reverse proxy) and `587` (SMTP STARTTLS; plus `465` if enabling implicit TLS).

#### 2. Get the compose files and create `deploy/.env`

```sh
git clone --depth 1 --branch v1.0.0 https://github.com/mailcubby/mailcubby
cd mailcubby
```

Generate an application encryption key:

```sh
docker run --rm ghcr.io/mailcubby/mailcubby:latest keygen
```

Create `deploy/.env`:

```sh
MAILCUBBY_DOMAIN=mail.example.com
MAILCUBBY_APP_KEY=<output-from-keygen>
# Optional: enables recipient routing (*@<project>.inbox.example.com)
# MAILCUBBY_INBOX_DOMAIN=inbox.example.com
# Optional: pin the image version (default: latest)
# MAILCUBBY_VERSION=1.0.0
```

#### 3. Start Caddy and Mailcubby

```sh
# 1. Start Caddy first so it obtains the Let's Encrypt certificate for MAILCUBBY_DOMAIN
docker compose -f deploy/compose.server.yml up -d caddy

# 2. Wait a few seconds for certificate issuance, then start Mailcubby
docker compose -f deploy/compose.server.yml up -d mailcubby
```

**Why `caddy-init` and `user: "65532:65532"` exist in `deploy/compose.server.yml`:**
Mailcubby runs inside a distroless container as non-root UID `65532`, and Caddy writes ACME private keys with mode `0600`. In `deploy/compose.server.yml`, a one-shot `caddy-init` container prepares `caddy-data` and `caddy-config` owned by `65532:65532`, and Caddy runs as `65532:65532` (listening internally on `8080`/`8443` mapped from host `80`/`443`). Mailcubby mounts `caddy-data` read-only at `/caddy`, so it can read the `0600` key directly and hot-reload renewals every 30 s without restarts or relaxed permissions.

`deploy/Caddyfile` pins `acme_ca https://acme-v02.api.letsencrypt.org/directory` so the certificate path under `/caddy/caddy/certificates/acme-v02.api.letsencrypt.org-directory/` is deterministic. If you switch ACME issuers (for example ZeroSSL, which uses `acme.zerossl.com-v2-dv90`), update `MAILCUBBY_SMTP_TLS_CERT` and `MAILCUBBY_SMTP_TLS_KEY` accordingly.

#### 4. Complete first-run setup

Read the one-time setup token from the logs (a new token is printed on every start and only the latest one works, hence `tail -1`):

```sh
docker compose -f deploy/compose.server.yml logs mailcubby | grep "setup token" | tail -1
```

Open `https://mail.example.com/setup`, paste the setup token, create the first admin account, and create your first project + SMTP credential in the wizard.

To bootstrap an admin headlessly instead of using the setup wizard, set `MAILCUBBY_ADMIN_EMAIL` and `MAILCUBBY_ADMIN_PASSWORD` on first start (with Compose, add them to `deploy/.env`; outside Compose, `MAILCUBBY_ADMIN_PASSWORD_FILE` also works). Once users exist these variables are ignored (the startup log says so). To regain access to an account, print a one-time password reset link:

```sh
docker compose -f deploy/compose.server.yml exec mailcubby /mailcubby admin reset-password --email admin@example.com
```

#### 5. Verify STARTTLS on port 587

Using a credential created in your project's SMTP settings:

```sh
swaks --to test@example.test \
  --from app@example.test \
  --server mail.example.com:587 \
  --tls \
  --auth-user "<smtp-username>" \
  --auth-password "<smtp-password>"
```

To also accept **implicit TLS (SMTPS)** on port 465, uncomment `MAILCUBBY_SMTPS_ADDR: :1465` and the `"465:1465"` port mapping in `deploy/compose.server.yml` (the same certificate reloader serves both STARTTLS and implicit TLS).

---

## Reverse proxy notes

Mailcubby terminates plain HTTP on `MAILCUBBY_HTTP_ADDR` (default `:8025`). Put Caddy, Traefik, or nginx in front of it for HTTPS, and set `MAILCUBBY_TRUSTED_PROXIES` to the CIDR(s) of your proxy so client IPs from `X-Forwarded-For` are recorded accurately in rate limits and audit logs.

SMTP (`:1025` / `:587` and optional `:1465` / `:465`) is exposed directly by Mailcubby, not proxied through HTTP.

### Caddy

See `deploy/Caddyfile`. For a standalone Caddy instance running directly on the host:

```caddyfile
mail.example.com {
	reverse_proxy 127.0.0.1:8025 {
		flush_interval -1
	}
}
```

### Traefik

Add Docker labels to the `mailcubby` service (ensure Mailcubby shares the Traefik network and `MAILCUBBY_TRUSTED_PROXIES` includes that network's CIDR):

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.mailcubby.rule=Host(`mail.example.com`)"
  - "traefik.http.routers.mailcubby.entrypoints=websecure"
  - "traefik.http.routers.mailcubby.tls.certresolver=letsencrypt"
  - "traefik.http.services.mailcubby.loadbalancer.server.port=8025"
  # Disable response buffering for SSE streams:
  - "traefik.http.services.mailcubby.loadbalancer.responseforwarding.flushinterval=1ms"
```

Traefik stores certificates inside `acme.json` rather than separate PEM files; to share that certificate with Mailcubby's SMTP STARTTLS listener, export PEM files with a sidecar (e.g. `traefik-certs-dumper`) into a volume readable by UID `65532` and point `MAILCUBBY_SMTP_TLS_CERT`/`_KEY` at them. Mailcubby picks up updates automatically every 30 s.

### nginx

SSE (`/api/v1/events`) and `/messages/wait` require **proxy buffering disabled** and a long read timeout:

```nginx
server {
    listen 443 ssl http2;
    server_name mail.example.com;

    ssl_certificate     /etc/letsencrypt/live/mail.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mail.example.com/privkey.pem;

    client_max_body_size 2m;

    location / {
        proxy_pass http://127.0.0.1:8025;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Connection "";

        # Required for Server-Sent Events (/api/v1/events) and /messages/wait:
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
    }
}
```

Set `MAILCUBBY_TRUSTED_PROXIES=127.0.0.1/32` (or the nginx container's CIDR). Mailcubby also emits `X-Accel-Buffering: no` on SSE responses, and does not enable HTTP response compression.

---

## Configuration

All configuration is via `MAILCUBBY_*` environment variables (see `.env.example` for a commented template and `docs/SPEC.md` section 9 for the full reference). Secret variables also accept `<NAME>_FILE`.

| Variable | Default | Description |
|---|---|---|
| `MAILCUBBY_MODE` | `local` | `local` (no login) or `server` (multi-tenant with login). A data directory that has users (used in server mode) refuses to start unless `MAILCUBBY_MODE` is set explicitly. |
| `MAILCUBBY_DATA_DIR` | `/data` (Docker), `./data` (binary) | SQLite database (`app.db`), blobs, local `app.key`. |
| `MAILCUBBY_BASE_URL` | (empty) | Public URL of the web UI. Required in server mode. |
| `MAILCUBBY_APP_KEY` | (empty) | 32-byte base64 key encrypting SMTP secrets (`_FILE` supported). Required in server mode. |
| `MAILCUBBY_HTTP_ADDR` | `:8025` | Web UI and REST API listen address. |
| `MAILCUBBY_SMTP_ADDR` | `:1025` | SMTP listen address (STARTTLS if cert configured). In local mode the UI snippets and `/api/v1/meta` announce this port. |
| `MAILCUBBY_SMTPS_ADDR` | (empty) | Optional implicit TLS listen address (e.g. `:1465`). Requires TLS cert and key. |
| `MAILCUBBY_SMTP_TLS_CERT` / `_KEY` | (empty) | PEM files for STARTTLS and SMTPS; hot-reloaded every 30 s when modified. |
| `MAILCUBBY_INBOX_DOMAIN` | (empty) | Enables recipient routing, e.g. `inbox.example.com`. |
| `MAILCUBBY_TRUSTED_PROXIES` | (empty) | Comma-separated proxy CIDRs trusted for `X-Forwarded-For`. |
| `MAILCUBBY_SMTP_MAX_MESSAGE_BYTES` | `25MiB` | Max accepted message size (up to `100MiB`). |
| `MAILCUBBY_SMTP_AUTH_MAX_FAILURES` | `5` | Server mode: failed AUTH attempts per (client IP, username) within `MAILCUBBY_SMTP_AUTH_LOCKOUT` before that pair gets `454` until the window passes; an IP is locked for all usernames after 5x this many. Restarting clears lockouts. |
| `MAILCUBBY_SMTP_AUTH_LOCKOUT` | `15m` | Lockout window and duration. |
| `MAILCUBBY_RETENTION_MAX_AGE` | `30d` | Default per-project age retention (`0` disables). |
| `MAILCUBBY_RETENTION_MAX_MESSAGES` | `5000` | Default per-project message cap (`0` disables). |
| `MAILCUBBY_RETENTION_MAX_BYTES` | `1GiB` | Default per-project raw size cap (`0` disables). |

---

## Backups and restore

All persistent state lives inside `MAILCUBBY_DATA_DIR` (`/data` in Docker):

- `app.db` (plus `app.db-wal` and `app.db-shm`): SQLite database (users, projects, message metadata, FTS5 index, encrypted SMTP credentials, audit log).
- `blobs/`: zstd-compressed raw `.eml.zst` files.
- `app.key` (local mode only): auto-generated encryption key.

### Offline backup (simplest)

Stop Mailcubby and archive the data directory (Compose names the volume `mailcubby_mailcubby-data` after the project name `mailcubby` set in `deploy/compose.server.yml`):

```sh
docker compose -f deploy/compose.server.yml stop mailcubby
docker run --rm -v mailcubby_mailcubby-data:/data:ro -v "$PWD:/backup" alpine:3.21 \
  tar -czf "/backup/mailcubby-$(date -u +%Y%m%dT%H%M%SZ).tar.gz" -C /data .
docker compose -f deploy/compose.server.yml start mailcubby
```

### Online backup (without downtime)

SQLite runs in WAL mode, so never copy `app.db` with `cp` while Mailcubby is running. Use SQLite's online `.backup` command and then sync `blobs/`:

```sh
mkdir -p /backups/mailcubby
sqlite3 /path/to/data/app.db ".backup '/backups/mailcubby/app.db'"
rsync -a /path/to/data/blobs/ /backups/mailcubby/blobs/
```

To restore, stop Mailcubby, replace `app.db` (remove any old `app.db-wal`/`app.db-shm`) and `blobs/` with permissions owned by UID `65532` (in Docker), and start Mailcubby again.

> **Important:** Back up `MAILCUBBY_APP_KEY` separately from `app.db` (for example in your password manager or secret store). Without the original `MAILCUBBY_APP_KEY`, existing encrypted SMTP credentials cannot be decrypted after a restore.

### Wrong or lost `MAILCUBBY_APP_KEY`

`MAILCUBBY_APP_KEY` encrypts only the SMTP credential passwords. If Mailcubby starts with a different key (a typo in `.env`, a regenerated key, a volume moved to another host), it still starts, but:

- the startup log shows `ERROR "N of M SMTP credentials cannot be decrypted with MAILCUBBY_APP_KEY"`;
- SMTP AUTH with those credentials gets `454 4.7.0` (temporary), so well-behaved senders queue and retry instead of dropping mail, and these attempts do not count towards the brute-force lockout;
- revealing or copying such a password in the UI shows a clear error (`409 conflict`) instead of the password.

To recover:

1. **Restore the original key** if you still have it, and restart. Everything works again, including mail queued by senders.
2. **Only if the original key is lost**, rotate each affected credential (project settings, SMTP credentials, Rotate) and update the new password in the applications that use it. Rotation encrypts with the current key. Do not rotate while the original key may still turn up: credentials rotated under the new key stop working if you later restore the old one.

Keep the key in your secret store next to the backup of `app.db`.

> **Durability note:** SQLite uses `WAL` with `synchronous=NORMAL`. Every SMTP `250 OK` message survives an application crash, though a sudden host power loss may lose the most recently committed transactions.

---

## Upgrades

Database migrations are embedded in the binary and run automatically on startup.

1. Back up `MAILCUBBY_DATA_DIR` before upgrading.
2. Pull the new image (or replace the binary) and restart. If you pinned `MAILCUBBY_VERSION` in `deploy/.env`, change it first:

   ```sh
   docker compose -f deploy/compose.server.yml pull mailcubby
   docker compose -f deploy/compose.server.yml up -d mailcubby
   ```

3. Inspect migration state at any time without starting the server:

   ```sh
   docker compose -f deploy/compose.server.yml exec mailcubby /mailcubby migrate status
   ```

Downgrades across schema versions are not supported; restore from the pre-upgrade backup if you need to roll back.

---

## Migrating from Mailpit

Mailcubby's local mode is designed as a drop-in replacement for Mailpit with multi-project support:

| Area | Mailpit | Mailcubby |
|---|---|---|
| **Default ports** | SMTP `1025`, HTTP `8025` | Identical: SMTP `1025`, HTTP `8025`. |
| **Tags (`X-Tags`)** | Comma-separated `X-Tags` header | Supported identically, plus automatic plus-addressing tags (`orders+urgent@domain`). |
| **Multiple projects** | Single inbox (tags only) | In local mode, pass any SMTP username (`MAIL_USERNAME=shop-staging`) to route into an isolated, auto-created project. Mail without AUTH goes to `default`. |
| **PHP `sendmail_path`** | `mailpit sendmail -S localhost:1025` | `mailcubby sendmail -t --smtp localhost:1025` (or set `MAILCUBBY_SENDMAIL_SMTP`). Accepts and ignores standard sendmail flags (`-i`, `-oi`, `-odi`, `-bm`, `-F`). |
| **Team / client staging** | Single Basic auth for everyone | Switch to `MAILCUBBY_MODE=server` for isolated projects, per-project SMTP credentials, users, invites, and read-only client viewers. |
| **REST API** | Mailpit `/api/v1/*` | Mailcubby's own OpenAPI 3.1 API (`/api/v1/openapi.yaml`), including `GET /api/v1/projects/{project}/messages/wait` for tests. |

Example PHP `php.ini` migration:

```ini
; Before (Mailpit):
; sendmail_path = "/usr/local/bin/mailpit sendmail -S localhost:1025"

; After (Mailcubby, routing to project "shop-staging" in local mode):
sendmail_path = "/usr/local/bin/mailcubby sendmail -t --smtp localhost:1025 --user shop-staging --password local"
```

---

## CLI subcommands

| Command | Description |
|---|---|
| `mailcubby serve` | Run SMTP, HTTP and background workers (default subcommand). |
| `mailcubby version` | Print version, commit and build date. |
| `mailcubby keygen` | Generate a new 32-byte base64 `MAILCUBBY_APP_KEY`. |
| `mailcubby healthcheck [--url]` | Exit 0 if `/readyz` returns 200 within 2 s (used by Docker `HEALTHCHECK`). |
| `mailcubby migrate status` | Display applied and pending database migrations. |
| `mailcubby admin create-user --email --name [--admin]` | Create a user and print a one-time invite link. |
| `mailcubby admin reset-password --email` | Print a one-time 1-hour password reset link. |
| `mailcubby sendmail [-t] [-f from] [--smtp host:port] [--user u --password p] [to...]` | Submit an email from stdin over SMTP. |

---

## Development

Requirements: Go 1.27.4, Node.js 26 LTS, pnpm 12.21.0. See `AGENTS.md` for coding conventions and `docs/PLAN.md` for the implementation plan.

```sh
make tools      # install pinned dev tools (sqlc, golangci-lint)
make deps       # download Go modules, web packages and Playwright Chromium
make dev        # Go server on :8025/:1025 + Vite dev server on :5173 (MODE=local|server)
make seed       # send sample emails in testdata/mime/ to the running instance
make check      # generate check, lint, Go tests, Vitest, build-all, bundle budget
make test-race  # Go tests with -race (CGO_ENABLED=1)
make e2e        # Playwright end-to-end tests against the built binary
```

## Documentation

- `docs/SPEC.md`: product specification (requirements, API, data model, security).
- `docs/ARCHITECTURE.md`: components, flows, package layout, deployment, threat model.
- `docs/UI.md`: design principles and screens.
- `docs/PLAN.md`: phased implementation plan.
- `docs/decisions/`: architecture decision records.

## Credits

Mailcubby is designed and developed by [Fábio Ferreira](https://x.com/fabiomlferreira) at [NOOP](https://noop.pt).

## License

PolyForm Shield 1.0.0. Copyright (c) 2026 NOOP. See `LICENSE`.

Mailcubby is source-available, not OSI open source. You may use it for free, including commercially (for example to test your clients' or your company's email), run it for your team or clients, read and modify the code, and fork it. You may not use the code to offer a product or service that competes with Mailcubby, such as reselling it or a modified version as an email testing product or hosted service. For other uses, contact NOOP.
