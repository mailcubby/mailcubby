# Mailcubby v0.2.0

**Mailcubby v0.2.0** adds three major message inspection features — **inline PDF preview**, **offline HTML/CSS email-client compatibility checks**, and an **SSRF-hardened link checker** — alongside **online SQLite backups**, new admin CLI commands, a server-mode Linux `systemd` unit, and security hardening.

This release ships with **zero new Go or npm dependencies** and **zero SQLite schema migrations**: existing `v0.1.0` data directories upgrade directly without migration steps. Back up your data directory before upgrading (see [Upgrading](#upgrading)).

Mailcubby is source-available under the [PolyForm Shield License 1.0.0](https://polyformproject.org/licenses/shield/1.0.0): free to use, including commercially, but not to build a competing product.

## Highlights

### Message Inspection

- **Inline PDF preview ([ADR 0009](https://github.com/mailcubby/mailcubby/blob/v0.2.0/docs/decisions/0009-inline-pdf-preview.md), #26)**:
  - Dedicated `GET /render/{project}/{id}/attachments/{attachment_id}` endpoint with coerced `application/pdf` plus byte-0 `%PDF-` magic validation (`415` on forged or polyglot attachments), a PDF-specific CSP (`default-src 'none'; object-src 'self'; frame-ancestors 'self'`), and `MAILCUBBY_RENDER_INLINE_PDF` kill switch (default `true`).
  - Lazy-loaded `PdfPreviewDialog` modal plus split PDF cards (Preview + Download) in the attachment strip and Attachments tab — available to viewers even when `viewer_hide_technical` is enabled.
- **Offline HTML/CSS compatibility check (FR-079, #27, #28)**:
  - `internal/compat` analyzer backed by a compacted zstd-compressed `caniemail.com` MIT snapshot (~19 KB, 18 curated email clients distinguishing Outlook Classic on Windows from Outlook.com / New Outlook).
  - `GET /api/v1/projects/{project}/messages/{id}/html-check` and a lazy-loaded **HTML check** tab (shortcut `9`, `?tab=check`) with weighted compatibility score breakdown, client family filters (`All`, `Gmail`, `Outlook`, `Apple Mail`, `Yahoo`, `Other`), "Only show issues" toggle, and expandable per-client disclosures.
- **Link checker ([ADR 0010](https://github.com/mailcubby/mailcubby/blob/v0.2.0/docs/decisions/0010-link-checker-ssrf-policy.md), FR-120, #29, #30)**:
  - Always-on offline link extraction (`GET /api/v1/projects/{project}/messages/{id}/links`) across HTML, genuine plain text, and `List-Unsubscribe` headers with static warnings (`javascript_url`, `data_url`, `credentials_in_url`, `http_insecure`, `missing_href`).
  - Opt-in SSRF-hardened active link checking (`POST /api/v1/projects/{project}/messages/{id}/links/check`, `member+`, `10/m` server-mode rate limit) using a custom `DialContext` that blocks private/loopback/IMDS/CGNAT/ULA/NAT64/6to4/Teredo ranges and DNS rebinding, never logs checked URLs, and defaults to `MAILCUBBY_LINK_CHECK_ENABLED=true` in local mode and `false` in server mode.
  - Lazy-loaded **Links** tab (shortcut `8`, `?tab=links`) with offline link table, safe "Open in new tab" (`rel="noopener noreferrer"`, disabled for non-`http(s)://` and credential-bearing URLs), per-row and bulk checks, single-use-token caution note, semantic status badges, and `aria-live="polite"` announcements.

### Operations, CLI & Security

- **Online SQLite backup (`mailcubby backup --out <path|->`, #23)**: WAL-safe online backups via `VACUUM INTO` that run directly inside the distroless container image without `sqlite3` or a shell.
- **Admin CLI & startup warnings (#24)**: added `mailcubby admin list-users` and `mailcubby admin enable-user`, plus startup warnings when `MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH=true` or `MAILCUBBY_BASE_URL` uses `http://`.
- **Server-mode `systemd` unit & hardened Dockerfiles (#19, #20)**: added `deploy/systemd/mailcubby-server.service` for standalone Linux server deployments and pinned the Caddy image in `deploy/compose.server.yml`.
- **Attachment Content-Type hardening (#18)**: coerces XML (`*/xml`, `*+xml`) and empty/malformed attachment `Content-Type` headers to `application/octet-stream` on downloads.
- **Invite UX & `sendmail` timeout (#17, #22)**: warns signed-in users when an invite email differs from their signed-in account, enforces a 30 s dial/command timeout in `mailcubby sendmail`, and quiets static asset access logs.

## Upgrading

1. Back up your data directory first (online via `mailcubby backup --out <path|->` plus `blobs/`, or offline while stopped).
2. Pull the new container image (`ghcr.io/mailcubby/mailcubby:0.2.0` or `:latest`), upgrade via Homebrew (`brew upgrade mailcubby && brew services restart mailcubby`), or replace the standalone binary.
3. Restart Mailcubby. No schema migrations or configuration changes are required.
4. To opt in to active HTTP link checking in **server mode**, set `MAILCUBBY_LINK_CHECK_ENABLED=true` (and optionally `MAILCUBBY_LINK_CHECK_TIMEOUT`, `MAILCUBBY_LINK_CHECK_MAX_LINKS`, `MAILCUBBY_LINK_CHECK_ALLOWED_PORTS`, `MAILCUBBY_LINK_CHECK_ALLOW_CIDRS`).

## Quick Start

### Local mode (Docker)

Local mode has no authentication, so bind its ports to `127.0.0.1`:

```sh
docker run --rm \
  -p 127.0.0.1:8025:8025 \
  -p 127.0.0.1:1025:1025 \
  -v mailcubby-data:/data \
  ghcr.io/mailcubby/mailcubby:0.2.0
```

Open `http://localhost:8025` and point your app's SMTP host to `localhost:1025`.

### Server mode (Docker Compose + Caddy)

Point a DNS record for your hostname (e.g. `mail.example.com`) to the server and open TCP ports `80`, `443` and `587`. Then get the compose files:

```sh
git clone --depth 1 --branch v0.2.0 https://github.com/mailcubby/mailcubby
cd mailcubby
```

Generate an application encryption key:

```sh
docker run --rm ghcr.io/mailcubby/mailcubby:0.2.0 keygen
```

Create `deploy/.env`:

```sh
MAILCUBBY_DOMAIN=mail.example.com
MAILCUBBY_APP_KEY=<output-from-keygen>
# Optional: enables recipient routing (*@<project>.inbox.example.com)
# MAILCUBBY_INBOX_DOMAIN=inbox.example.com
# Optional: pin the image version (default: latest)
# MAILCUBBY_VERSION=0.2.0
```

Start Caddy and Mailcubby:

```sh
# 1. Start Caddy first so it obtains the Let's Encrypt certificate for MAILCUBBY_DOMAIN
docker compose -f deploy/compose.server.yml up -d caddy

# 2. Wait a few seconds for certificate issuance, then start Mailcubby
docker compose -f deploy/compose.server.yml up -d mailcubby
```

Read the one-time setup token from the logs (a new token is printed on every start and only the latest one works, hence `tail -1`):

```sh
docker compose -f deploy/compose.server.yml logs mailcubby | grep "setup token" | tail -1
```

Open `https://mail.example.com/setup`, paste the setup token, create the first admin account, and create your first project + SMTP credential in the wizard.

See the [README](https://github.com/mailcubby/mailcubby/blob/v0.2.0/README.md#quick-start) for reverse-proxy setup (Caddy, Traefik, nginx), online backups, `systemd` deployment, and Mailpit migration notes.

## Downloads & Checksums

- **Homebrew** (macOS and Linux, local mode): `brew install mailcubby/tap/mailcubby && brew services start mailcubby`
- **Container images**: `ghcr.io/mailcubby/mailcubby:0.2.0`, `:0.2`, `:latest` (`linux/amd64`, `linux/arm64`)
- **Binaries**: `linux`, `darwin`, and `windows` (`amd64` and `arm64`) archives plus `checksums.txt` attached to this release.

## Verification

- 70/70 functional requirements in `docs/SPEC.md` covered by automated Go, Vitest, and Playwright tests ([`docs/SPEC.md`](https://github.com/mailcubby/mailcubby/blob/v0.2.0/docs/SPEC.md)).
- WCAG 2.1 AA accessibility verified across all local-mode and server-mode screens in Light and Dark themes via `@axe-core/playwright` (zero serious or critical violations).
- Initial JS bundle kept at ~149.4 KB gzip (budget: 250 KB gzip), with `PdfPreviewDialog`, `HtmlCheckTab`, and `LinksTab` lazy-loaded in dedicated chunks.
- Zero new Go or npm dependencies and zero SQLite schema migrations compared to `v0.1.0`.

**Full Changelog**: https://github.com/mailcubby/mailcubby/compare/v0.1.0...v0.2.0
