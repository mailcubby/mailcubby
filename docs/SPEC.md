# Mailcubby: Product Specification

Version: 0.1 (draft, 2026-11-23). Status: pre-alpha.

This document is the source of truth for **what** Mailcubby does. `docs/ARCHITECTURE.md` describes **how**. `docs/PLAN.md` describes **in which order**. When documents disagree, SPEC wins; fix the other document in the same change.

Keywords MUST, MUST NOT, SHOULD and MAY are used as in RFC 2119.

---

## 1. Problem

Developers and agencies need to capture email sent by applications in development and staging so it never reaches real recipients, and inspect it (HTML rendering, headers, attachments, links).

- **Mailpit** (Go, MIT, about 10.9k GitHub stars) is the reference open-source tool. It has one shared inbox. Its maintainer declined multiple inboxes, per-credential routing and multiple users (issues #86, #115, #128) as needing "a complete rewrite". `--tenant-id` is only a table prefix. Tags are the only way to group mail, and every viewer sees every message.
- **smtp4dev** has mailboxes but a single Basic auth login for everyone.
- **Mailtrap** (SaaS) has the right model: projects, sandboxes, per-sandbox SMTP credentials, roles. Sharing starts at the Team plan (USD 42/month). Mailtrap Local (2026) is explicitly not a team tool.

An agency that runs a staging catcher for many client websites today must either run one Mailpit per client or let every client and contractor see every other client's mail.

## 2. Goals

1. One self-hosted instance serves many isolated **projects**; each project has its own SMTP credentials, members and settings.
2. A **client** can be given a read-only login that shows only their project, optionally hiding technical views.
3. **Zero-config local mode** that is a drop-in replacement for Mailpit (same default ports 1025/8025, any credentials accepted, `X-Tags` honoured).
4. **Easy install**: one Docker image of about 9 MB compressed, one volume, sane defaults, works behind a reverse proxy.
5. **Premium UI**: fast, dense, keyboard-first, realtime, safe rendering of untrusted HTML.
6. **Test-friendly API**: REST API with tokens, plus a "wait for message" endpoint for end-to-end tests.

## 3. Non-goals (v1)

- Delivering mail to real recipients (release/relay). Mailcubby never sends mail outward in v1.
- Being a production mail server, mailbox host or IMAP server.
- Horizontal scaling or high availability. One process, one SQLite database.
- Spam scoring, HTML client compatibility reports, link checking.
- Email sending of invites or password resets (links are copied manually; there is no outbound mail).
- Full Mailpit API compatibility.

See section 14 for the roadmap.

## 4. Personas

| Persona | Context | Needs |
|---|---|---|
| **Developer (local)** | Runs Mailcubby on a laptop via `docker run` or the binary, for one or several apps. | No login, no setup. Separate inboxes per app via the SMTP username. Fast search, HTML preview, keyboard navigation, API for tests. |
| **Agency team member** | Works on several client staging sites hosted by an agency. | Logs in, sees only projects they belong to, copies SMTP settings into `.env`, uses tags and search, deletes noise. |
| **Client viewer** | Employee of a client company, non-technical. | Logs in, sees only their project's mail, sees the email as recipients would. No raw source or headers unless allowed. Cannot delete. |
| **Instance admin** | Runs the server for the agency. | First-run setup, user management, project creation, global retention and quotas, audit log, health checks, backups. |

## 5. Modes

`MAILCUBBY_MODE` selects the mode at startup. It cannot change at runtime. Switching a data directory from `local` to `server` is supported (projects and messages are kept; the setup wizard runs). Switching from `server` to `local` is supported but requires an explicit `MAILCUBBY_MODE=local` and logs a warning that all projects become visible without login. If `MAILCUBBY_MODE` is not set (so it defaults to `local`) and the database has users (a server-mode data directory), startup fails with an error naming `MAILCUBBY_MODE` (set `server` to keep login, or `local` to open every project on purpose). The check runs after the database is opened (and migrated if needed) but before `app.key` is generated.

### 5.1 Local mode (`MAILCUBBY_MODE=local`, default)

- No login. Every HTTP request acts as an implicit **local operator** with owner rights on all projects. Users, members, invites, API tokens and SMTP credentials do not exist in the UI or API (their endpoints return `404`).
- SMTP accepts connections with or without AUTH. Any username/password is accepted. The SMTP username (normalised to a slug, section 7.3) selects the project and auto-creates it if it does not exist.
- Without AUTH, recipient routing applies if `MAILCUBBY_INBOX_DOMAIN` is set, else mail goes to the default project (`MAILCUBBY_DEFAULT_PROJECT`, default `default`), created at startup.
- AUTH is allowed without TLS.
- `MAILCUBBY_APP_KEY` is optional; if unset, a key is generated into `$MAILCUBBY_DATA_DIR/app.key` (mode 0600).
- On startup Mailcubby logs a warning: local mode has no authentication and must not be exposed to the internet.

### 5.2 Server mode (`MAILCUBBY_MODE=server`)

- All UI and API access requires a session or API token, except: `/healthz`, `/readyz`, `GET /api/v1/meta`, setup endpoints while setup is pending, invite preview/accept, password reset with token, and static assets.
- SMTP requires AUTH with a project SMTP credential, unless the message is accepted by recipient routing into a project that enabled it (section 7).
- Startup fails with a clear error if `MAILCUBBY_BASE_URL` or `MAILCUBBY_APP_KEY` (or `MAILCUBBY_APP_KEY_FILE`) is missing.
- Startup fails if no SMTP TLS certificate is configured and `MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH` is not `true` (AUTH is only allowed after TLS in server mode).
- First run: if no users exist, the instance is in **setup pending** state (section 6.4).

---

## 6. Functional requirements

Each requirement has an ID (FR-xxx) and acceptance criteria (AC). Tests SHOULD reference the FR ID in their name or a comment (e.g. `// FR-012`).

### 6.1 SMTP ingest

**FR-001 SMTP listener.** Mailcubby listens for SMTP on `MAILCUBBY_SMTP_ADDR` (default `:1025`). It advertises `PIPELINING`, `8BITMIME`, `ENHANCEDSTATUSCODES`, `CHUNKING` (go-smtp defaults), `SMTPUTF8` (`EnableSMTPUTF8: true`), `SIZE <max>`, `LIMITS RCPTMAX=<n>`, `AUTH PLAIN LOGIN` (when allowed), and `STARTTLS` (when a certificate is configured).
- AC: `swaks --to a@b.test --server localhost:1025` succeeds in local mode and the message appears via the API within 1 s.
- AC: EHLO response contains `SIZE 26214400` with default config.

**FR-002 Implicit TLS listener.** If `MAILCUBBY_SMTPS_ADDR` is set (e.g. `:1465`) and a certificate is configured, Mailcubby also accepts implicit TLS SMTP on that address.
- AC: `swaks --tls-on-connect --server localhost:1465` succeeds.

**FR-003 AUTH mechanisms.** Only `PLAIN` and `LOGIN` are supported. `CRAM-MD5` and others are not advertised. `LOGIN` accepts an optional initial response (`AUTH LOGIN <base64 username>`).
- AC: AUTH CRAM-MD5 returns `504 5.7.4`.

**FR-004 AUTH requires TLS in server mode.** In server mode, AUTH is advertised and accepted only on TLS connections, unless `MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH=true`.
- AC: in server mode with TLS configured, a plaintext AUTH attempt is rejected with `523 5.7.10` and no credential lookup is performed.

**FR-005 Limits.** Enforce `MAILCUBBY_SMTP_MAX_MESSAGE_BYTES` (reply `552 5.3.4`), `MAILCUBBY_SMTP_MAX_RECIPIENTS` (reply `452 4.5.3`), `MAILCUBBY_SMTP_MAX_CONNECTIONS` total and `MAILCUBBY_SMTP_MAX_CONNECTIONS_PER_IP` (reply `421 4.7.0` and close), read/write timeouts (`MAILCUBBY_SMTP_TIMEOUT`, default `60s`), maximum line length 2000 bytes.
- AC: a 26 MiB message with defaults is rejected with `552`; a 24 MiB message is accepted.
- AC: the 21st concurrent connection from one IP is refused with `421`.

**FR-006 Raw storage.** The message bytes received in DATA (after dot-unstuffing) are stored unmodified, compressed with zstd, as one blob per message per project. Mailcubby MUST NOT add or alter headers in the stored raw message. Envelope data (MAIL FROM, RCPT TO, remote IP, HELO, TLS, AUTH username) is stored in the database.
- AC: downloading `.eml` returns bytes identical (SHA-256) to what the client sent.

**FR-007 Parsing.** Messages are parsed with enmime v2. Parsing never rejects a message: malformed MIME is stored, and parser warnings are recorded (`parse_warnings` JSON) and shown in the Tech tab.
- AC: each fixture in `testdata/mime/` (including malformed ones) is accepted and its golden JSON summary matches.

**FR-008 Extracted metadata.** For each message store: subject, From (name, address), To, Cc, Reply-To lists, envelope sender and recipients, Bcc (envelope recipients absent from To/Cc), `Message-ID`, `Date` header, received time, size, snippet (first 200 characters of the text body, whitespace collapsed), presence of HTML and text parts, attachment list (filename, content type, size, content ID, disposition, MIME part path, SHA-256).
- AC: golden fixture tests cover multipart/alternative, multipart/related with inline CID images, nested message/rfc822, non-UTF-8 charsets (ISO-8859-1, Windows-1252), RFC 2047 encoded headers, missing Date, missing From.

**FR-009 Text extraction.** If a message has HTML but no text part, a text version is derived from the HTML (enmime's down-conversion) for the snippet and search index only; the UI marks it as "generated".

**FR-010 Ingest atomicity.** For each message: write the blob (temp file, fsync, rename), then insert `messages`, `messages_fts`, `attachments`, tags and counter updates in one transaction. If the transaction fails, delete the blob and reply `451 4.3.0`. A crash between blob and row leaves an orphan blob that GC removes (FR-098).
- AC: injecting a DB failure in tests leaves no row and no blob, and the client gets `451`.

**FR-011 Multiple target projects.** When recipient routing resolves recipients to several projects (no AUTH), the message is stored once per project (separate blob and row). Duplicate projects are deduplicated.

**FR-012 Realtime publish.** After commit, an event `message.created` is published to the in-process event bus (section 6.12).

Ingest throughput is specified as NFR-001 (section 13).

### 6.2 Routing

Routing rules are defined precisely in section 7.

**FR-020 Project resolution.** Every accepted message is stored in exactly the project(s) resolved by section 7, or rejected with the codes listed there.
- AC: table-driven tests cover every row of the routing table in section 7.2 in both modes.

**FR-021 Credential attribution.** Messages accepted via an SMTP credential record `smtp_credential_id`; the UI can filter by credential (useful when one project has separate credentials per environment). The list and delete-all API filter is `?credential=<credential id>`; the search operator `credential:<label>` matches by label (labels are not unique, so the UI's "Delete matching" sends the sidebar credential as `?credential=<id>`, never as `credential:<label>`).

### 6.3 Projects

**FR-030 Project attributes.** Name (1 to 80 chars), slug (section 7.3 rules, unique, editable by owner), color (one of 12 preset tokens), created time, settings (retention overrides, `recipient_routing_enabled`, `plus_address_tags`, `viewer_hide_technical`, `members_can_reveal_smtp`).

**FR-031 Create.** Server mode: instance admins and users with `can_create_projects` can create projects; the creator becomes owner. Local mode: projects are created by SMTP auto-creation or from the UI. Auto-created projects use the original SMTP username (or routed slug) truncated to 80 characters as name, color `slate`, and default settings. When `slug` is omitted on `POST /projects`, it is `slugify(name)`; a conflict returns `409`.
- AC: a user without `can_create_projects` gets `403` on `POST /api/v1/projects`.

**FR-032 Rename slug.** Changing the slug changes routing addresses; the UI warns before saving. Old slugs are not reserved.

**FR-033 Delete.** Owner deletes a project by confirming its slug. Rows are deleted in one transaction (cascades); blobs under `blobs/<project_id>/` are removed asynchronously by the GC worker; credentials stop working immediately.
- AC: after delete, SMTP AUTH with the project's credential fails with `535` and `GET /api/v1/projects/{slug}` returns `404`.

**FR-034 Counters.** Each project maintains `message_count`, `unread_count` and `size_bytes`, updated in the same transaction as inserts, deletes and seen changes.
- AC: a property test performs random inserts/deletes/seen toggles and verifies counters equal `COUNT(*)`/`SUM()` queries.

**FR-035 Projects home.** `GET /api/v1/projects` returns, for each accessible project: counters, `last_message_at`, the caller's role, and a 24-value `sparkline` (messages per hour for the last 24 h, oldest first). In server mode, a user who is neither an instance admin nor allowed to create projects (`can_create_projects`) and has exactly one accessible project skips the projects home: opening `/` (including signing in without a return URL and accepting an instance invite) replaces it with that project's inbox `/p/<slug>`, client-side. Admins, users who can create projects (the home holds "New project") and users with zero or two or more projects see the projects home; an explicit return URL after sign-in wins. Local mode always shows the projects home.
- AC: an e2e test signs in as a viewer with one project, lands on `/p/<slug>` and opens the first message with one click.

### 6.4 Users, authentication, sessions (server mode)

**FR-040 Setup.** While no user exists, the instance is in setup pending state. At startup in this state Mailcubby generates a random setup token, stores its SHA-256 in `settings`, and logs `setup token: <token>` at WARN level. `POST /api/v1/setup` with `{setup_token, email, name, password, instance_name?}` creates the first user as instance admin, stores `instance_name` in `settings` if given, marks setup done and logs the user in. A new setup token is generated at every startup while setup is pending (the previous one stops working). If `MAILCUBBY_ADMIN_EMAIL` and `MAILCUBBY_ADMIN_PASSWORD` (or `_FILE`) are set and no users exist, the admin is created at startup and setup is skipped. If they are set but users already exist, they are ignored and Mailcubby logs that at INFO level at startup (use `mailcubby admin reset-password` to recover access).
- AC: setup without a valid token returns `403`; after setup, `POST /api/v1/setup` returns `409`.

**FR-041 Users.** Attributes: email (unique, case-insensitive), name, password hash, `is_admin`, `can_create_projects`, `disabled_at`, `last_login_at`. There is no public sign-up; users are created by admin or by accepting an invite.

**FR-042 Passwords.** Hashed with argon2id, encoded PHC string, parameters m=19456 KiB, t=2, p=1 (OWASP minimum), stored with the hash so they can be raised later (rehash on login when parameters change). Length 10 to 256 characters. At most 2 concurrent hash operations (semaphore) to bound memory.
- AC: stored hashes start with `$argon2id$v=19$m=19456,t=2,p=1$`.

**FR-043 Login.** `POST /api/v1/auth/login {email, password}`. On failure return `401` with a generic message; the time taken is the same whether the user exists or not (verify against a dummy hash). Disabled users and users without a password (`password_hash` NULL, invite not yet accepted) cannot log in and take the dummy-hash path.

**FR-044 Sessions.** Server-side sessions. The cookie `mailcubby_session` holds a 32-byte random token (base64url); the DB stores only its SHA-256. Cookie flags: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when `MAILCUBBY_BASE_URL` is https. Absolute lifetime `MAILCUBBY_SESSION_TTL` (default `30d`), idle timeout `MAILCUBBY_SESSION_IDLE_TIMEOUT` (default `7d`). `last_seen_at` is updated at most once per minute. New token on every login. Logout deletes the session. Password change deletes all other sessions of the user. Disabling a user deletes all their sessions and revokes their API tokens.
- AC: a session unused for longer than the idle timeout returns `401`.

**FR-045 CSRF.** Cookie-authenticated unsafe requests (POST, PUT, PATCH, DELETE) MUST carry header `X-CSRF-Token` equal to the session's CSRF token (returned by `GET /api/v1/auth/me`), AND the `Origin` header (or `Referer` if Origin is absent) MUST match `MAILCUBBY_BASE_URL`. Bearer-token requests are exempt. Unauthenticated unsafe endpoints (login, setup, invite accept, password reset) check Origin only. Local mode has no cookies and no CSRF token; instead it rejects cross-origin unsafe requests from browsers with `403 forbidden` using Go's `http.CrossOriginProtection`: a request is rejected when `Sec-Fetch-Site` is present and is not `same-origin` or `none`, or, without `Sec-Fetch-Site`, when the `Origin` host differs from the `Host` header. `MAILCUBBY_BASE_URL`, when set, is also a trusted origin (for reverse proxies that rewrite `Host`). Requests without either header (curl, SDKs, test clients) and same-origin requests (the UI, the Vite dev proxy, which keeps `Host`) pass. There is no `Host` allowlist and no `Content-Type` requirement.
- AC (local mode): a `POST` with a foreign `Origin` or `Sec-Fetch-Site: cross-site` returns `403`; the same request without `Origin`, or same-origin, succeeds.
- AC: a POST with a valid cookie but missing `X-CSRF-Token` returns `403`.

**FR-046 Password reset.** No email. An admin generates a single-use reset link (`POST /api/v1/admin/users/{id}/password-reset`, also `mailcubby admin reset-password --email x`) valid for 1 hour; the token is stored hashed. Using it sets a new password and deletes all the user's sessions.

**FR-047 Own account.** Users can change name and password (requires current password), list their sessions and revoke them.

### 6.5 Roles and permissions

Roles are per project: `owner`, `member`, `viewer`. Instance admins (`users.is_admin`) have implicit owner rights on every project; admin access to a project they are not a member of is written to the audit log once per session per project. A project MUST always keep at least one owner (removing or demoting the last owner returns `409`).

**FR-050 Permission matrix.**

| Action | viewer | member | owner | admin |
|---|---|---|---|---|
| See project in list, read message list and message content (HTML preview, text, attachments) | yes | yes | yes | yes |
| Raw source, headers, HTML source, `.eml` download, Tech tab | yes, unless `viewer_hide_technical` | yes | yes | yes |
| Mark seen/unseen | no | yes | yes | yes |
| Add/remove tags on messages, create/edit/delete tags | no | yes | yes | yes |
| Delete messages, delete all, send test email | no | yes | yes | yes |
| See SMTP host, port, usernames | no | yes | yes | yes |
| Reveal SMTP passwords | no | if `members_can_reveal_smtp` | yes | yes |
| Create, rotate, disable, delete SMTP credentials | no | no | yes | yes |
| List members | no | yes | yes | yes |
| Invite, change roles, remove members, revoke invites | no | no | yes | yes |
| Edit project settings and retention, rename, delete project | no | no | yes | yes |
| Project audit log | no | no | yes | yes |
| Users, instance settings, all projects, instance audit log | no | no | no | yes |

- Opening a message as a viewer does NOT mark it seen (viewers are read-only). Seen state is per message, shared by all members.
- Requests for a project the caller cannot access return `404` (not `403`), so project existence is not revealed. Requests for an accessible project with insufficient role return `403`.
- AC: an authorization test matrix iterates every API route x every role (none, viewer, member, owner, admin, token-read, token-write) and asserts the expected status. A route without an entry in the matrix fails the test.

**FR-051 Leaving.** A member can leave a project (unless they are the last owner).

### 6.6 Invites

**FR-055 Create.** Owners invite by email with a role. The API returns the invite URL `<BASE_URL>/invite/<token>` once; the token (32 random bytes, base64url) is stored as SHA-256 only. Default expiry `MAILCUBBY_INVITE_TTL` (`7d`). Admins can also create **instance invites** (`project_id` and `role` NULL) to onboard a user: `POST /admin/users` and `mailcubby admin create-user` create the `users` row with `password_hash` NULL plus an instance invite for that email, and return the invite URL. Creating a project invite for an email that has a pending invite to the same project revokes the older one.
- AC: the token is not retrievable after creation; listing invites shows email, role, created_by, expires_at, status (`pending`, `accepted`, `revoked`, `expired`).

**FR-056 Accept.** `GET /api/v1/invites/{token}` previews project name (absent for instance invites), inviter name, role and email (public, rate limited). `POST /api/v1/invites/{token}/accept`:
- If the caller is logged in, the membership is added to the current user (the invite email is informative; owners see who accepted). For an instance invite, a logged-in caller gets `409 conflict` (nothing to accept).
- If not logged in, the invite is an instance invite, and a user exists with the invite email and `password_hash` is NULL (admin-created user): body `{name?, password}` sets the password (and name if given), and logs them in. A project invite never sets a password: for any existing user (with or without a password) it returns `409 login_required`.
- If not logged in and no user exists with the invite email: body `{name, password}` creates the user with that email and logs them in.
- If not logged in and a user with a password exists with that email: `409 login_required`; the UI asks the user to log in and then accepts.
- If the user is already a member, the role is upgraded if the invite role is higher, never downgraded.
Acceptance is single-use: `accepted_at` is set in the same transaction with a `WHERE accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now` guard.
- AC: two concurrent accepts of the same token produce exactly one membership; the second gets `410`.
- AC: expired or revoked tokens return `410`.

**FR-057 Revoke.** Owners revoke pending invites.

### 6.7 SMTP credentials (server mode)

**FR-060 Create.** Owners create credentials with a label (e.g. `staging`, `production-like`). Username: `<slug>-<8 random lowercase alphanumerics>` (globally unique, immutable). Password: 24 random characters from `[A-Za-z0-9]`. A project can have up to 20 credentials.

**FR-061 Encrypted storage.** Passwords are encrypted with AES-256-GCM using a subkey derived from `MAILCUBBY_APP_KEY` via HKDF-SHA256 (info `mailcubby/smtp-credential/v1`). Stored value: version byte `0x01` || 12-byte random nonce || ciphertext+tag. AAD: the credential username. Verification decrypts and compares with `crypto/subtle.ConstantTimeCompare`. Decrypted passwords are never logged.
- AC: a DB dump does not contain the plaintext password; decrypting with a different key fails.

**FR-062 Reveal, copy, rotate, disable, delete.** Reveal (`POST .../smtp-credentials/{id}/reveal`) returns the plaintext and writes an audit entry. Rotate generates a new password (username unchanged) and returns it. Disabled credentials fail AUTH with `535 5.7.8`. `last_used_at` is updated at most once per minute. If the stored password cannot be decrypted with the current `MAILCUBBY_APP_KEY` (wrong or lost key), reveal returns `409 conflict` with a message saying to restore the original key or rotate the credential (no audit entry); rotate still works and is the recovery for a lost key. In server mode, startup trial-decrypts every credential and, if any fail, logs one ERROR `N of M SMTP credentials cannot be decrypted with MAILCUBBY_APP_KEY`; it does not refuse to start.

**FR-063 Connection info.** `GET /api/v1/projects/{p}/smtp-credentials` includes the public SMTP host (`MAILCUBBY_SMTP_PUBLIC_HOST`, default the host of `MAILCUBBY_BASE_URL`), port (`MAILCUBBY_SMTP_PUBLIC_PORT`, default `587` in server mode; in local mode the port the SMTP listener actually bound from `MAILCUBBY_SMTP_ADDR`, so `MAILCUBBY_SMTP_ADDR=:2525` announces `2525`), and TLS mode (`starttls`, `tls` or `none`) so the UI can render snippets.

### 6.8 API tokens (server mode)

**FR-065 Create.** Users create personal API tokens with a name, scope `read` or `write`, optional project restriction, optional expiry. Token format: `mailcubby_` + 40 base62 characters. The first 12 characters after the prefix are stored as `prefix` (unique, shown in lists); the DB stores SHA-256 of the full token. The token is shown once.
- AC: the token works as `Authorization: Bearer <token>`; after revoke it returns `401`.

**FR-066 Effective permission.** A token's permission on a project is the minimum of its scope and the user's current role: `read` acts like viewer; `write` acts like member (never owner). `viewer_hide_technical` applies only when the user's own project role is viewer (a `read` token of a member can download raw sources). Tokens can only call message, tag, event, wait, project list/get and `auth/me` endpoints; management endpoints (members, invites, credentials, settings, tokens, admin) return `403` for tokens.

### 6.9 Messages and views

**FR-070 List.** Cursor-paginated list, newest first (ULID order), with optional search query (section 6.10), tag filter and credential filter. Default 50, max 200 per page.
- AC: with 100k messages in a project, the first page returns in under 50 ms p95 (NFR-003).

**FR-071 Detail.** Returns metadata, text body, attachment list, tags, a `render_url` for the HTML preview, and the technical fields `envelope`, `parse_warnings` and `headers`. The technical fields are omitted (not `null`) when the caller is a viewer and `viewer_hide_technical` is on.

**FR-072 Views.** The UI offers tabs: Preview (sandboxed HTML), Text, HTML source, Headers, Raw, Attachments, Tech (envelope, TLS, AUTH user, credential label, remote IP, HELO, size, parse warnings). HTML preview supports device widths (mobile 375 px, tablet 768 px, desktop 100%), rotation, zoom, and an email dark-mode toggle. While the Preview or Text tab is active, an attachment strip below the view shows one compact card per attachment (type icon, thumbnail only for the inline-allowlisted raster images of FR-074 via `?inline=1`, filename, size); a card downloads the file with `Content-Disposition: attachment`. Parts with `referenced_by_html: true` (parts the HTML preview displays through `cid:` in an image or CSS context; a `cid:` in an iframe, script, embed or media `src` does not count) are left out of the strip, which is hidden when no card remains; the Attachments tab still lists every part with full details. The strip and the Attachments tab are shown to viewers even when `viewer_hide_technical` is on. Details in `docs/UI.md`.

**FR-073 HTML rendering.** Via the render endpoint only (section 12.1). Remote images blocked by default; a per-message toggle loads them when `MAILCUBBY_RENDER_REMOTE_IMAGES=opt-in` (default). `off` removes the toggle.

**FR-074 Downloads.** `.eml` download (`message/rfc822`, attachment disposition, filename from subject, sanitised). Attachment download by attachment ID. Inline display in the UI only for raster images (png, jpeg, gif, webp) via `?inline=1`, which is ignored for any other type. PDFs and all other types are always downloaded (inline PDF viewing is on the roadmap: browser PDF viewers do not render under the attachment CSP sandbox).

**FR-075 Seen state.** Messages start unseen. Opening a message as member or above marks it seen (UI calls `PATCH` after 1 s of display, configurable off in user preferences). Bulk mark seen/unseen.

**FR-076 Delete.** Single, bulk (by IDs), and delete-all (optionally filtered by query). Delete-all is audited. Deleting removes rows in a transaction, then blobs; failures to remove blobs are left to GC.

**FR-077 Send test email.** `POST /api/v1/projects/{p}/send-test` ingests a built-in sample message (HTML + text, an inline CID image, a PDF attachment, a link) directly through the ingest pipeline (no network), so the empty-inbox guide can show a working example.

**FR-078 Wait for message.** `GET /api/v1/projects/{p}/messages/wait?query=&since=&timeout=` blocks until a message matching `query` with ID greater than `since` exists, then returns it (`200`). `since` accepts a ULID, an RFC 3339 time, or `now` (default: request time). `timeout` default `30s`, max `MAILCUBBY_API_WAIT_MAX_TIMEOUT` (`120s`). On timeout returns `204`. Implementation subscribes to the event bus before querying the DB, so no message can be missed.
- AC: a test starts `wait`, then sends via SMTP; the response arrives within 500 ms of SMTP DATA completion.
- AC: `wait` with `since=<older ULID>` returns an existing matching message immediately.

### 6.10 Search

**FR-080 Full-text search.** SQLite FTS5 table `messages_fts` with tokenizer `unicode61 remove_diacritics 2` and `prefix='2 3'`, indexing subject, from (name and address), to/cc (names and addresses), text body (first 256 KiB), attachment filenames. Search is always scoped to one project.
- AC: searching `joao` matches a subject containing `João`; `fatura` matches `Fatura` and `FATURA`.

**FR-081 Query syntax.** A query is a sequence of terms separated by whitespace. Quoted strings are phrases. A leading `-` negates a term or operator.

| Term | Meaning | SQL target |
|---|---|---|
| `word`, `"exact phrase"` | full-text match, prefix match for the last bare word (`word*`) | FTS all columns |
| `from:x` | sender address or name contains x | FTS `from_text` |
| `to:x` | To/Cc/Bcc address or name contains x | FTS `to_text` |
| `subject:x` / `subject:"a b"` | subject match | FTS `subject` |
| `has:attachment` | `attachment_count > 0` | `messages` |
| `has:html` / `has:text` | part presence | `messages` |
| `is:unread` / `is:read` | seen flag | `messages` |
| `tag:name` | has tag (case-insensitive) | `message_tags` |
| `before:2026-11-01` / `after:2026-11-01` | received before / on or after date (UTC, `YYYY-MM-DD` or RFC 3339) | `messages.received_at` |
| `credential:label` | received via credential with this label | `smtp_credentials` |

Unknown operators are treated as plain text. The parser MUST produce parameterised SQL only and escape FTS5 syntax characters in user input (each user term is quoted as an FTS5 string).
- AC: fuzz test of the parser never panics and never produces SQL containing user text outside bind parameters.

**FR-082 Search API.** `query` parameter on the list, delete-all and wait endpoints.
- Delete-all with a `query` returns `400 bad_request` and deletes nothing when the query does not narrow the selection: it parses to zero terms, every term is a negated full-text term without indexable tokens (e.g. `-"!"`), or it consists only of exclusions (negated text, `from:`, `to:`, `subject:`, `tag:`, `credential:` terms) that exclude no message in the project. A `credential=<id>` filter counts as narrowing, so delete-all with only `credential` deletes that credential's messages. To delete every message, omit `query` and `credential`. List and wait treat such no-op terms as matching everything.

### 6.11 Tags

**FR-085 Tags.** Tags are per project (`name` 1 to 40 chars, `[\p{L}\p{N}_.-]`, unique case-insensitive, color token). Messages can have many tags.

**FR-086 Automatic tags at ingest.**
- `X-Tags` header (comma-separated, Mailpit compatible): each value becomes a tag (trimmed, invalid characters replaced with `-`).
- Plus-addressing: for every envelope recipient `local+tag@domain`, `tag` becomes a tag, if the project setting `plus_address_tags` is true (default true).
- Tags are created on first use.
- AC: a message with `X-Tags: invoice, Staging` to `orders+urgent@shop.test` gets tags `invoice`, `Staging`, `urgent`.

### 6.12 Realtime

**FR-090 SSE stream.** `GET /api/v1/events` (optionally `?project=slug`) returns `text/event-stream`. Event types: `message.created`, `message.updated` (seen, tags), `message.deleted` (IDs, or `all` with project), `project.updated` (counters), `project.created`, `project.deleted`, `membership.changed`. Payload is JSON. A comment heartbeat `: ping` is sent every 25 s. There is no replay; on reconnect the client refetches.

**FR-091 Filtering.** Each subscriber receives only events for projects it can access at the time of the event. Membership changes take effect for open streams within 1 s (the subscriber's project set is refreshed on `membership.changed`). Token-authenticated subscribers are limited by token project restriction. On `project.deleted` every open stream that could access the project receives the event and then stops matching that project ID; a stream scoped to the deleted project (`?project=` or a project-restricted token) ends after delivering it (a reconnect then gets `404`).
- AC: a viewer of project A connected to `/api/v1/events` receives no event when mail arrives in project B; after being removed from A, receives no further A events.

**FR-092 Limits.** Max `MAILCUBBY_SSE_MAX_CONNECTIONS_PER_USER` (default 10) concurrent streams per user or token; the oldest is closed when exceeded. Slow consumers (buffer of 256 events full) are disconnected.

### 6.13 Retention, quotas, background workers

**FR-095 Retention policies.** Each project has `retention_max_age`, `retention_max_messages`, `retention_max_bytes`. `NULL` inherits the global default (`MAILCUBBY_RETENTION_MAX_AGE` `30d`, `MAILCUBBY_RETENTION_MAX_MESSAGES` `5000`, `MAILCUBBY_RETENTION_MAX_BYTES` `1GiB`); `0` disables that limit. The sweeper runs every `MAILCUBBY_RETENTION_INTERVAL` (`5m`) and after any insert that exceeds a count/bytes limit by more than 10% (debounced 10 s). It deletes oldest messages first, in batches of 500, each batch in its own transaction.
- AC: with `max_messages=100`, after 150 inserts and one sweep, exactly the newest 100 remain.

**FR-096 Instance quota.** `MAILCUBBY_INSTANCE_MAX_BYTES` (default `0`, unlimited): when total stored size exceeds it, SMTP replies `452 4.3.1 Insufficient system storage` until retention frees space.

**FR-097 Per-project ingest rate limit.** `MAILCUBBY_SMTP_RATE_LIMIT` (default `600/m` per project, `0` disables): excess messages get `450 4.7.1` at `MAIL FROM` (for AUTH sessions) or at `DATA` end.

**FR-098 Orphan blob GC.** Every hour: list blob files, delete any file older than 1 hour whose message row does not exist, and delete directories of deleted projects. Then run `PRAGMA incremental_vacuum(N)` if the freelist exceeds 10% of pages.
- AC: a blob written without a row is removed by the GC after its age exceeds 1 hour (test uses an injected clock).

**FR-099 Housekeeping.** Expired sessions, invites, reset tokens and audit entries older than `MAILCUBBY_AUDIT_RETENTION` (`180d`) are deleted daily.

### 6.14 Administration (server mode)

**FR-100 Users.** Admin lists, creates (returns an invite link, no password set by admin), edits (name, `is_admin`, `can_create_projects`), disables/enables, deletes users (deleting the last admin returns `409`), generates password reset links.

**FR-101 Projects.** Admin lists all projects with counters and owners.

**FR-102 Instance settings.** Admin edits instance name and default retention overrides stored in `settings` (env vars are the defaults; settings in DB override them, the UI shows which applies).

**FR-103 Stats.** Admin sees totals: projects, users, messages, stored bytes, DB size, blob size, ingest rate (last hour), version, uptime.

### 6.15 Audit log

**FR-105 Audit events.** Recorded actions (`action` values): `setup.complete`, `auth.login`, `auth.login_failed`, `auth.logout`, `user.create`, `user.update`, `user.disable`, `user.enable`, `user.delete`, `user.password_reset_link`, `user.password_change`, `project.create`, `project.update`, `project.delete`, `project.admin_access`, `member.add`, `member.update`, `member.remove`, `invite.create`, `invite.revoke`, `invite.accept`, `smtp_credential.create`, `smtp_credential.reveal`, `smtp_credential.rotate`, `smtp_credential.update`, `smtp_credential.delete`, `smtp.auth_lockout`, `api_token.create`, `api_token.revoke`, `messages.delete_all`, `settings.update`. Each entry: time, actor (user, token, system, smtp), project, target, IP, JSON metadata. Never secrets.
- AC: revealing a credential creates exactly one `smtp_credential.reveal` entry with the actor and IP.

### 6.16 Health

**FR-110** `GET /healthz` returns `200 ok` if the process is running. `GET /readyz` returns `200` if the DB is reachable (`SELECT 1` on reader and writer within 1 s), migrations are applied, the data dir is writable, and both listeners are up; else `503` with a JSON reason. Neither requires auth nor logs at INFO.

### 6.17 CLI

**FR-115** The binary provides subcommands:

| Command | Purpose |
|---|---|
| `mailcubby serve` (default) | Run SMTP + HTTP + workers. Once both listeners are bound it logs one INFO line `mailcubby ready` with `url` (`MAILCUBBY_BASE_URL`, else `http://localhost:<HTTP port>`), `smtp` (the announced host:port of FR-063) and `data_dir` (absolute path). `serve --help` prints its usage (flags and the common environment variables) to stdout and exits 0. |
| `mailcubby version` | Print version, commit, build date. |
| `mailcubby keygen` | Print a new base64 `MAILCUBBY_APP_KEY`. |
| `mailcubby healthcheck [--url]` | Exit 0 if `/readyz` returns 200 within 2 s, else exit 1 (for Docker `HEALTHCHECK` in a distroless image, no shell). Default URL: `http://127.0.0.1:<port of MAILCUBBY_HTTP_ADDR>/readyz`. |
| `mailcubby migrate status` | Show applied migrations. Migrations run automatically on `serve`. |
| `mailcubby admin create-user --email --name [--admin]` | Print an invite link for a new user. |
| `mailcubby admin reset-password --email` | Print a single-use reset link. |
| `mailcubby sendmail [-t] [-f from] [--smtp host:port] [--user u --password p] [to...]` | Read a message on stdin and submit it via SMTP (default `localhost:1025`, or `MAILCUBBY_SENDMAIL_SMTP`) as a sendmail replacement for PHP `sendmail_path`. Recipients from arguments, or from headers with `-t`. Flags commonly passed by PHP and MTAs (`-i`, `-oi`, `-odi`, `-bm`, `-F name`) are accepted and ignored. Exit code 0 on `250`, non-zero with the SMTP reply on stderr otherwise. |

Admin commands open the same SQLite database (safe with WAL while `serve` runs).

The CLI uses the stdlib `flag` package, so long options are written `--name value` or `-name value`; `sendmail` parses its arguments by hand because sendmail-style flags (`-oi`, `-t`) do not fit `flag`.

---

## 7. SMTP routing rules

### 7.1 Inputs

- `auth_user`: SMTP AUTH username, if AUTH succeeded in this session.
- `rcpts`: envelope recipients (RCPT TO).
- `INBOX_DOMAIN`: `MAILCUBBY_INBOX_DOMAIN` (lowercase, may be empty).

A recipient **routes** to slug `s` with optional tag `t` if `INBOX_DOMAIN` is non-empty and the address (domain compared case-insensitively) matches:
1. `<anything>@<s>.<INBOX_DOMAIN>` (subdomain form), or
2. `<s>@<INBOX_DOMAIN>` or `<s>+<t>@<INBOX_DOMAIN>` (local-part form).

### 7.2 Resolution, in priority order

| # | Condition | Local mode | Server mode |
|---|---|---|---|
| 1 | AUTH succeeded | Project = slugify(`auth_user`), created if missing. All RCPTs accepted. | `auth_user` looked up in `smtp_credentials` (enabled, project exists) gives the project. All RCPTs accepted. (AUTH failure already replied `535 5.7.8` and AUTH is not set.) |
| 2 | No AUTH, a RCPT routes to slug `s` | Accept RCPT; project `s` created if missing. | Accept RCPT only if project `s` exists and has `recipient_routing_enabled=true`; else reply `550 5.1.1 Mailbox unavailable` for that RCPT. |
| 3 | No AUTH, RCPT does not route | Accept RCPT into the default project (`MAILCUBBY_DEFAULT_PROJECT`). | Reply `550 5.1.1` for that RCPT. |
| 4 | No AUTH and `INBOX_DOMAIN` empty | Default project. | Reply `530 5.7.0 Authentication required` to `MAIL FROM`. |

Rules:
- Rule 1 overrides recipient routing completely (an authenticated message goes only to the credential's project, regardless of recipients).
- In rule 2/3, one message can resolve to several projects (FR-011). In local mode, a message with some routed and some non-routed RCPTs goes to each routed project plus the default project.
- If no RCPT was accepted, `DATA` is rejected with `554 5.5.1 No valid recipients`.
- Server mode applies rule 2 only for sessions without AUTH. Recipient routing in server mode lets anyone who can reach the SMTP port write into routed projects; owners enable it deliberately per project and operators SHOULD firewall the port.

### 7.3 Slug rules

- Slug: lowercase `[a-z0-9-]`, 1 to 63 characters, must start and end with `[a-z0-9]`, no `--` sequence at positions 3 to 4 (punycode prefix). Valid DNS label (needed for subdomain routing).
- `slugify(username)`: lowercase, transliterate common Latin diacritics to ASCII (`ç`->`c`, `ã`->`a`), replace every other invalid char with `-`, collapse repeats, trim `-`, truncate to 63. If empty, use the default project.
- Reserved slugs (rejected on create/rename, and slugify appends `-1`): `api`, `admin`, `assets`, `render`, `setup`, `login`, `logout`, `invite`, `reset`, `settings`, `events`, `healthz`, `readyz`, `static`, `new`.

### 7.4 Error replies summary

| Situation | Reply |
|---|---|
| AUTH bad credentials or disabled credential | `535 5.7.8 Authentication credentials invalid` |
| AUTH temporarily locked (brute force) | `454 4.7.0 Temporary authentication failure` |
| AUTH server-side failure: credential lookup error, or the stored password cannot be decrypted with `MAILCUBBY_APP_KEY` (not counted towards lockout, no failure delay) | `454 4.7.0 Temporary authentication failure` |
| AUTH without TLS in server mode | `523 5.7.10 TLS is required` (go-smtp built-in) |
| AUTH with an unsupported mechanism | `504 5.7.4` (return `smtp.ErrAuthUnknownMechanism`) |
| No AUTH required but missing (server, no inbox domain) | `530 5.7.0 Authentication required` |
| Recipient not routable (server) | `550 5.1.1 Mailbox unavailable` |
| Too many recipients | `452 4.5.3` (go-smtp built-in via `MaxRecipients`) |
| Message too large | `552 5.3.4` (go-smtp `ErrDataTooLarge`, or `MAIL FROM SIZE=` above the limit) |
| Project rate limit | `450 4.7.1 Rate limit exceeded, try again later` |
| Instance storage quota | `452 4.3.1 Insufficient system storage` |
| Internal storage error | `451 4.3.0 Temporary failure, try again later` |
| Too many connections | `421 4.7.0 Too many connections` |

Replies produced by go-smtp itself (insecure AUTH, too many recipients, message too large, line too long) keep go-smtp's wording; tests assert the reply code and enhanced code, never the text. Replies produced by Mailcubby use the texts above. Session methods MUST return a bare `*smtp.SMTPError` (go-smtp uses a type assertion, not `errors.As`; a wrapped error becomes `554 5.0.0` or `451 4.0.0`).

---

## 8. REST API v1

Base path: `/api/v1` (the UI and API are always served from the root of `MAILCUBBY_BASE_URL`; sub-path hosting is on the roadmap). JSON (UTF-8) request and response bodies; max JSON request body 1 MiB. The OpenAPI 3.1 document lives at `internal/api/openapi.yaml`, is served at `GET /api/v1/openapi.yaml`, and is the contract for generated frontend types. Handlers and the spec MUST stay in sync (a test compares registered routes with spec paths). Paths in `openapi.yaml` are absolute (`/api/v1/...`, `/render/...`, `/healthz`, `/readyz`) so the route test can compare them 1:1. `openapi.yaml` grows phase by phase: it documents exactly the routes that are registered, never planned ones.

### 8.1 Conventions

- Auth: session cookie (+ CSRF header for unsafe methods) or `Authorization: Bearer mailcubby_...`. Local mode: no auth.
- `{project}` path parameter is the project slug.
- Timestamps: RFC 3339 UTC with milliseconds. IDs: messages use ULID strings; other resources use integers.
- Pagination: `?limit=` (default 50, max 200) and `?cursor=` (opaque string from `next_cursor`). Response `{ "items": [...], "next_cursor": "..." | null }`.
- Errors: `{ "error": { "code": "snake_case_code", "message": "Human readable", "details": {} } }`. Codes: `bad_request`, `validation_failed` (details per field), `unauthorized`, `forbidden`, `not_found`, `conflict`, `login_required` (409, FR-056), `gone`, `rate_limited`, `payload_too_large`, `internal`.
- Status codes: `200`, `201` (create), `204` (no body), `400`, `401`, `403`, `404`, `409`, `410`, `413`, `422` (validation), `429` (with `Retry-After`), `500`, `503`.
- "Role" column: minimum role. `public` = no auth; `session` = any logged-in user (cookie only); `user` = any logged-in user or token; `viewer`/`member`/`owner` = project role (admin passes all); `admin` = instance admin; `viewer*` = viewer unless `viewer_hide_technical` (then member). In local mode all routes are accessible except those marked `server` (which return 404).

### 8.2 Meta, setup, auth

| Method | Path | Role | Request | Response |
|---|---|---|---|---|
| GET | `/meta` | public | | `{mode, version, instance_name, setup_required, base_url, features{remote_images, recipient_routing}, inbox_domain, smtp{host, port, tls_mode}}` (`smtp` is the public connection info of FR-063; local mode uses it for the setup guide and snippets) |
| POST | `/setup` | public (setup pending) | `{setup_token, email, name, password, instance_name?}` | `201` user; sets session cookie |
| POST | `/auth/login` | public, server | `{email, password}` | `200` user; sets cookie |
| POST | `/auth/logout` | session, server | | `204` |
| GET | `/auth/me` | user | | `{user{id,email,name,is_admin,can_create_projects}, csrf_token, memberships[{project, role}], token?{scope, project}}`; local mode returns a synthetic local operator |
| PATCH | `/auth/me` | session, server | `{name?, current_password?, new_password?}` | user |
| GET | `/auth/sessions` | session, server | | `[{id, created_at, last_seen_at, ip, user_agent, current}]` |
| DELETE | `/auth/sessions/{id}` | session, server | | `204` |
| GET | `/invites/{token}` | public, server | | `{project{name, slug}?, role, email, invited_by, expires_at}` |
| POST | `/invites/{token}/accept` | public, server | `{name?, password?}` | `200 {project?}` (`project` absent for instance invites); may set cookie |
| POST | `/password-reset/{token}` | public, server | `{password}` | `204` |

### 8.3 Projects

| Method | Path | Role | Request | Response |
|---|---|---|---|---|
| GET | `/projects` | user | | `[{id, slug, name, color, role, message_count, unread_count, size_bytes, last_message_at, sparkline[24]}]` |
| POST | `/projects` | admin or `can_create_projects` (local: any) | `{name, slug?, color?}` | `201` project |
| GET | `/projects/{project}` | viewer | | project + `settings` (owner sees all settings plus effective instance `retention_defaults`; others see `viewer_hide_technical` and `plus_address_tags`) |
| PATCH | `/projects/{project}` | owner | `{name?, slug?, color?, settings?{retention_max_age?, retention_max_messages?, retention_max_bytes?, recipient_routing_enabled?, plus_address_tags?, viewer_hide_technical?, members_can_reveal_smtp?}}` | project |
| DELETE | `/projects/{project}` | owner | `{confirm_slug}` | `204` |
| POST | `/projects/{project}/leave` | viewer, server | | `204` |
| GET | `/projects/{project}/audit` | owner, server | `?cursor&limit&action` | paginated audit entries |

### 8.4 Messages

| Method | Path | Role | Request | Response |
|---|---|---|---|---|
| GET | `/projects/{project}/messages` | viewer | `?query&tag&credential&cursor&limit` (`tag` = tag name, `credential` = credential id) | paginated `MessageSummary` |
| GET | `/projects/{project}/messages/wait` | viewer | `?query&since&timeout` | `200 MessageSummary` or `204` |
| GET | `/projects/{project}/messages/{id}` | viewer | | `MessageDetail` |
| PATCH | `/projects/{project}/messages/{id}` | member | `{seen?, tags?[names]}` | `MessageSummary` |
| DELETE | `/projects/{project}/messages/{id}` | member | | `204` |
| POST | `/projects/{project}/messages/bulk` | member | `{ids[], action: "delete"|"seen"|"unseen"|"add_tags"|"remove_tags", tags?[]}` | `{affected}` |
| DELETE | `/projects/{project}/messages` | member | `?query` (absent = all) | `{affected}` |
| GET | `/projects/{project}/messages/{id}/raw` | viewer* | | `text/plain; charset=utf-8` raw source (inline) |
| GET | `/projects/{project}/messages/{id}/eml` | viewer* | | `message/rfc822` attachment |
| GET | `/projects/{project}/messages/{id}/html` | viewer* | | `text/plain; charset=utf-8` HTML source |
| GET | `/projects/{project}/messages/{id}/headers` | viewer* | | `[{name, value}]` in original order |
| GET | `/projects/{project}/messages/{id}/attachments/{attachment_id}` | viewer | `?inline=1` (raster images only) | file (section 12.2) |
| POST | `/projects/{project}/send-test` | member | | `201 MessageSummary` |

`MessageSummary`: `{id, received_at, from{name,address}, to[], cc[], subject, snippet, size_bytes, seen, attachment_count, has_html, has_text, tags[], credential_label?}`.

`MessageDetail`: `MessageSummary` + `{reply_to[], bcc[], envelope?{mail_from, rcpt_to[], remote_ip, helo, tls, auth_user}, message_id_header, date_header, text, text_generated, text_truncated, attachments[{id, filename, content_type, size_bytes, content_id, disposition, referenced_by_html}], render_url, remote_resource_count, parse_warnings?[], headers?[]}`. `render_url` is `null` when the message has no HTML part. `text` is capped at 1 MiB (`text_truncated: true` when cut). Fields marked `?` are omitted for viewers when `viewer_hide_technical` is on (FR-071). `referenced_by_html` is `true` when the render endpoint inlines the part through a `cid:` reference in an image or CSS context (section 12.1; same Content-ID resolution as section 12.1, including case, angle-bracket and percent-encoding normalisation and the inline byte budget); it is not a technical field.

### 8.5 Tags

| Method | Path | Role | Request | Response |
|---|---|---|---|---|
| GET | `/projects/{project}/tags` | viewer | | `[{id, name, color, message_count}]` |
| POST | `/projects/{project}/tags` | member | `{name, color?}` | `201` tag |
| PATCH | `/projects/{project}/tags/{id}` | member | `{name?, color?}` | tag |
| DELETE | `/projects/{project}/tags/{id}` | member | | `204` (removes from messages) |

### 8.6 Members, invites, SMTP credentials (server mode)

| Method | Path | Role | Request | Response |
|---|---|---|---|---|
| GET | `/projects/{project}/members` | member | | `[{user{id,email,name}, role, created_at}]` |
| PATCH | `/projects/{project}/members/{user_id}` | owner | `{role}` | member |
| DELETE | `/projects/{project}/members/{user_id}` | owner | | `204` |
| GET | `/projects/{project}/invites` | owner | | `[{id, email, role, created_by, created_at, expires_at, status}]` |
| POST | `/projects/{project}/invites` | owner | `{email, role}` | `201 {invite, url}` (url shown once) |
| DELETE | `/projects/{project}/invites/{id}` | owner | | `204` |
| GET | `/projects/{project}/smtp-credentials` | member | | `{connection{host, port, tls_mode}, items[{id, label, username, created_at, last_used_at, disabled}]}` |
| POST | `/projects/{project}/smtp-credentials` | owner | `{label}` | `201 {credential, password}` |
| POST | `/projects/{project}/smtp-credentials/{id}/reveal` | owner (member if allowed) | | `{password}` |
| POST | `/projects/{project}/smtp-credentials/{id}/rotate` | owner | | `{credential, password}` |
| PATCH | `/projects/{project}/smtp-credentials/{id}` | owner | `{label?, disabled?}` | credential |
| DELETE | `/projects/{project}/smtp-credentials/{id}` | owner | | `204` |

### 8.7 API tokens (server mode)

| Method | Path | Role | Request | Response |
|---|---|---|---|---|
| GET | `/tokens` | session | | `[{id, name, prefix, scope, project?, created_at, last_used_at, expires_at}]` |
| POST | `/tokens` | session | `{name, scope, project?, expires_in_days?}` | `201 {token_info, token}` (shown once) |
| DELETE | `/tokens/{id}` | session | | `204` |

### 8.8 Admin (server mode)

| Method | Path | Role | Request | Response |
|---|---|---|---|---|
| GET | `/admin/users` | admin | `?cursor&limit&q` | paginated users with project counts |
| POST | `/admin/users` | admin | `{email, name, is_admin?, can_create_projects?}` | `201 {user, invite_url}` |
| PATCH | `/admin/users/{id}` | admin | `{name?, is_admin?, can_create_projects?, disabled?}` | user |
| DELETE | `/admin/users/{id}` | admin | | `204` |
| POST | `/admin/users/{id}/password-reset` | admin | | `{url, expires_at}` |
| GET | `/admin/projects` | admin | | all projects with owners and counters |
| GET | `/admin/settings` | admin | | `{settings, effective, source}` |
| PATCH | `/admin/settings` | admin | `{instance_name?, retention_max_age?, retention_max_messages?, retention_max_bytes?}` | settings |
| GET | `/admin/audit` | admin | `?cursor&limit&action&project&user` | paginated entries |
| GET | `/admin/stats` | admin | | stats (FR-103) |

### 8.9 Events, OpenAPI, health, render

| Method | Path | Role | Notes |
|---|---|---|---|
| GET | `/api/v1/events` | user | SSE, `?project=` optional (FR-090) |
| GET | `/api/v1/openapi.yaml` | public | OpenAPI 3.1 document |
| GET | `/healthz` | public | liveness |
| GET | `/readyz` | public | readiness |
| GET | `/render/{project}/{id}` | viewer | sandboxed HTML (section 12.1), `?images=1` |

---

## 9. Configuration

Environment variables only (no config file in v1). Variables marked (secret) also accept `<NAME>_FILE` pointing to a file whose trimmed content is the value. Durations: Go syntax (`90s`, `15m`, `24h`) plus `d` for days (`30d`). Sizes: integer bytes, or a number with `KB`, `MB`, `GB` (powers of 1000) or `KiB`, `MiB`, `GiB` (powers of 1024). Rates: `<n>/<window>` where the window is `s`, `m`, `h` or a duration (`600/m`, `10/15m`); `0` disables a rate limit where stated. Booleans: `true`/`false`/`1`/`0`. Invalid values MUST stop startup with an error naming the variable.

### 9.1 General

| Variable | Default | Description |
|---|---|---|
| `MAILCUBBY_MODE` | `local` | `local` or `server`. |
| `MAILCUBBY_DATA_DIR` | `./data` (image: `/data`) | Database, blobs, generated `app.key`. |
| `MAILCUBBY_BASE_URL` | empty | Public URL, e.g. `https://mail.example.com`. Required in server mode. Used for invite links, cookie `Secure`, Origin checks. |
| `MAILCUBBY_APP_KEY` (secret) | empty | Base64 (std or url) of 32 random bytes. Required in server mode. Local mode auto-generates `app.key`. |
| `MAILCUBBY_INSTANCE_NAME` | `Mailcubby` | Shown in the UI title. |
| `MAILCUBBY_DEFAULT_PROJECT` | `default` | Local mode default project slug. |
| `MAILCUBBY_INBOX_DOMAIN` | empty | Enables recipient routing (section 7). |
| `MAILCUBBY_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `MAILCUBBY_LOG_FORMAT` | `text` | `text` or `json` (`log/slog`). |
| `MAILCUBBY_ADMIN_EMAIL` | empty | Headless bootstrap of the first admin (server mode). |
| `MAILCUBBY_ADMIN_PASSWORD` (secret) | empty | Password for the bootstrap admin. |
| `MAILCUBBY_SENDMAIL_SMTP` | `localhost:1025` | SMTP address used by the `mailcubby sendmail` client subcommand only. |

### 9.2 HTTP

| Variable | Default | Description |
|---|---|---|
| `MAILCUBBY_HTTP_ADDR` | `:8025` | Listen address. |
| `MAILCUBBY_TRUSTED_PROXIES` | empty | Comma-separated CIDRs whose `X-Forwarded-For` / `X-Forwarded-Proto` are trusted. |
| `MAILCUBBY_SESSION_TTL` | `30d` | Absolute session lifetime. |
| `MAILCUBBY_SESSION_IDLE_TIMEOUT` | `7d` | Idle timeout. |
| `MAILCUBBY_INVITE_TTL` | `7d` | Invite expiry. |
| `MAILCUBBY_HSTS` | `true` | Send HSTS when `BASE_URL` is https. |
| `MAILCUBBY_LOGIN_RATE_LIMIT` | `10/15m` | Failed logins per IP+email before `429` (window as duration after `/`). |
| `MAILCUBBY_API_RATE_LIMIT` | `1200/m` | Requests per token or session (server mode only; local mode is not rate limited). |
| `MAILCUBBY_API_WAIT_MAX_TIMEOUT` | `120s` | Max `wait` timeout. |
| `MAILCUBBY_SSE_MAX_CONNECTIONS_PER_USER` | `10` | Concurrent SSE streams per user or token. |
| `MAILCUBBY_RENDER_REMOTE_IMAGES` | `opt-in` | `opt-in` (toggle, blocked by default) or `off`. |

### 9.3 SMTP

| Variable | Default | Description |
|---|---|---|
| `MAILCUBBY_SMTP_ADDR` | `:1025` | Listen address (STARTTLS if cert set). |
| `MAILCUBBY_SMTPS_ADDR` | empty | Implicit TLS listen address, e.g. `:1465`. |
| `MAILCUBBY_SMTP_HOSTNAME` | `mailcubby` | Name in the greeting and `Received`-style logs. |
| `MAILCUBBY_SMTP_PUBLIC_HOST` | host of `BASE_URL`, else `localhost` | Shown in connection info and snippets. |
| `MAILCUBBY_SMTP_PUBLIC_PORT` | `587` (server), the bound port of `MAILCUBBY_SMTP_ADDR` (local) | Shown in connection info and snippets. Set it in local mode only when clients reach SMTP on a different port (for example a Docker port mapping). |
| `MAILCUBBY_SMTP_TLS_CERT` / `MAILCUBBY_SMTP_TLS_KEY` | empty | PEM files. Re-read when their mtime changes (checked every 30 s). |
| `MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH` | `false` (server), `true` (local) | Allow AUTH without TLS. |
| `MAILCUBBY_SMTP_MAX_MESSAGE_BYTES` | `25MiB` | Max message size, at most `100MiB` (NFR-008). |
| `MAILCUBBY_SMTP_MAX_RECIPIENTS` | `100` | Per message. |
| `MAILCUBBY_SMTP_MAX_CONNECTIONS` | `200` | Total concurrent connections. |
| `MAILCUBBY_SMTP_MAX_CONNECTIONS_PER_IP` | `20` | Concurrent connections per client IP. |
| `MAILCUBBY_SMTP_TIMEOUT` | `60s` | Read and write timeout. |
| `MAILCUBBY_SMTP_AUTH_MAX_FAILURES` | `5` | Failures per (IP, username) within the window before that pair is locked; an IP is locked for all usernames after 5x this many. |
| `MAILCUBBY_SMTP_AUTH_LOCKOUT` | `15m` | Window and lockout duration. |
| `MAILCUBBY_SMTP_RATE_LIMIT` | `600/m` | Messages per project. `0` disables. |

### 9.4 Retention and storage

| Variable | Default | Description |
|---|---|---|
| `MAILCUBBY_RETENTION_MAX_AGE` | `30d` | Default per project. `0` = no limit. |
| `MAILCUBBY_RETENTION_MAX_MESSAGES` | `5000` | Default per project. `0` = no limit. |
| `MAILCUBBY_RETENTION_MAX_BYTES` | `1GiB` | Default per project (raw size). `0` = no limit. |
| `MAILCUBBY_RETENTION_INTERVAL` | `5m` | Sweeper interval. |
| `MAILCUBBY_INSTANCE_MAX_BYTES` | `0` | Hard cap for all stored raw bytes. `0` = unlimited. |
| `MAILCUBBY_AUDIT_RETENTION` | `180d` | Audit entries older than this are deleted. |
| `MAILCUBBY_ZSTD_LEVEL` | `3` | 1 (fastest) to 19, mapped with `zstd.EncoderLevelFromZstd` (klauspost/compress has four speed levels). |

---

## 10. Data model

SQLite, one file `app.db`. Times are `INTEGER` Unix milliseconds UTC unless noted. Booleans are `INTEGER` 0/1. Foreign keys ON. Every table that holds tenant data has a `project_id` column (denormalised where needed) so that every query can filter by it directly. Migrations are SQL files in `migrations/` (goose). `00001_init.sql` creates all v1 tables and indexes; `00002_fts.sql` creates `messages_fts` and its delete trigger (kept separate because sqlc's SQLite parser does not handle FTS5 virtual tables; it is excluded from `sqlc.yaml`). `00003_projects_autoincrement.sql` rebuilds `projects` with `id INTEGER PRIMARY KEY AUTOINCREMENT` (foreign keys off around the rebuild so no child row cascades) and starts the sequence above every project ID recorded in the audit log, so a deleted project's ID is never reused by a new project.

### users
| Column | Type | Notes |
|---|---|---|
| id | INTEGER PK | |
| email | TEXT NOT NULL | `UNIQUE COLLATE NOCASE` |
| name | TEXT NOT NULL | |
| password_hash | TEXT | NULL until set via invite/reset |
| is_admin | INTEGER NOT NULL DEFAULT 0 | |
| can_create_projects | INTEGER NOT NULL DEFAULT 0 | |
| disabled_at | INTEGER | |
| last_login_at | INTEGER | |
| created_at, updated_at | INTEGER NOT NULL | |

### sessions
| Column | Type | Notes |
|---|---|---|
| id | TEXT PK | hex SHA-256 of the cookie token |
| user_id | INTEGER NOT NULL | FK users ON DELETE CASCADE; index |
| csrf_token | TEXT NOT NULL | 32 random bytes base64url |
| ip, user_agent | TEXT | at creation |
| created_at, last_seen_at, expires_at | INTEGER NOT NULL | index `expires_at` |

### password_resets
| Column | Type | Notes |
|---|---|---|
| id | INTEGER PK | |
| user_id | INTEGER NOT NULL | FK CASCADE |
| token_hash | TEXT NOT NULL UNIQUE | SHA-256 hex |
| created_by | INTEGER | FK users SET NULL |
| created_at, expires_at | INTEGER NOT NULL | |
| used_at | INTEGER | |

### projects
| Column | Type | Notes |
|---|---|---|
| id | INTEGER PK AUTOINCREMENT | never reused after a project is deleted (`00003_projects_autoincrement.sql`) |
| slug | TEXT NOT NULL UNIQUE | |
| name | TEXT NOT NULL | |
| color | TEXT NOT NULL | token, e.g. `blue` |
| retention_max_age_s | INTEGER | NULL = inherit, 0 = unlimited |
| retention_max_messages | INTEGER | same |
| retention_max_bytes | INTEGER | same |
| recipient_routing_enabled | INTEGER NOT NULL DEFAULT 0 | |
| plus_address_tags | INTEGER NOT NULL DEFAULT 1 | |
| viewer_hide_technical | INTEGER NOT NULL DEFAULT 1 | |
| members_can_reveal_smtp | INTEGER NOT NULL DEFAULT 0 | |
| message_count, unread_count, size_bytes | INTEGER NOT NULL DEFAULT 0 | maintained counters |
| last_message_at | INTEGER | |
| created_at, updated_at | INTEGER NOT NULL | |

### project_members
| Column | Type | Notes |
|---|---|---|
| project_id | INTEGER NOT NULL | FK CASCADE |
| user_id | INTEGER NOT NULL | FK CASCADE; index |
| role | TEXT NOT NULL | `CHECK (role IN ('owner','member','viewer'))` |
| created_at | INTEGER NOT NULL | |
| | | PK (project_id, user_id) |

### invites
| Column | Type | Notes |
|---|---|---|
| id | INTEGER PK | |
| project_id | INTEGER | NULL = instance invite; FK CASCADE; index |
| email | TEXT NOT NULL | |
| role | TEXT | NULL for instance invites; `CHECK (role IN ('owner','member','viewer'))` and `CHECK ((project_id IS NULL) = (role IS NULL))` |
| token_hash | TEXT NOT NULL UNIQUE | |
| created_by | INTEGER | FK users SET NULL |
| created_at, expires_at | INTEGER NOT NULL | |
| accepted_at, accepted_by, revoked_at | INTEGER | |

### smtp_credentials
| Column | Type | Notes |
|---|---|---|
| id | INTEGER PK | |
| project_id | INTEGER NOT NULL | FK CASCADE; index |
| label | TEXT NOT NULL | |
| username | TEXT NOT NULL UNIQUE | |
| password_enc | BLOB NOT NULL | FR-061 |
| created_by | INTEGER | FK users SET NULL |
| created_at | INTEGER NOT NULL | |
| last_used_at, disabled_at | INTEGER | |

### api_tokens
| Column | Type | Notes |
|---|---|---|
| id | INTEGER PK | |
| user_id | INTEGER NOT NULL | FK CASCADE; index |
| project_id | INTEGER | NULL = all user's projects; FK CASCADE |
| name | TEXT NOT NULL | |
| prefix | TEXT NOT NULL UNIQUE | 12 chars |
| token_hash | TEXT NOT NULL | SHA-256 hex |
| scope | TEXT NOT NULL | `CHECK (scope IN ('read','write'))` |
| created_at | INTEGER NOT NULL | |
| last_used_at, expires_at, revoked_at | INTEGER | |

### messages
| Column | Type | Notes |
|---|---|---|
| pk | INTEGER PRIMARY KEY AUTOINCREMENT | rowid alias; FTS rowid. `AUTOINCREMENT` guarantees a deleted message's rowid is never reused, so a stale FTS row can never match a different message |
| id | TEXT NOT NULL UNIQUE | ULID (public ID) |
| project_id | INTEGER NOT NULL | FK CASCADE |
| smtp_credential_id | INTEGER | FK SET NULL |
| received_at | INTEGER NOT NULL | |
| subject | TEXT NOT NULL DEFAULT '' | |
| from_name, from_addr | TEXT NOT NULL DEFAULT '' | |
| to_json, cc_json, bcc_json, reply_to_json | TEXT NOT NULL DEFAULT '[]' | `[{"name","address"}]` |
| envelope_from | TEXT NOT NULL | |
| envelope_to_json | TEXT NOT NULL | |
| message_id_header | TEXT | |
| date_header | INTEGER | parsed `Date`, NULL if missing/invalid |
| snippet | TEXT NOT NULL DEFAULT '' | |
| has_html, has_text, text_generated | INTEGER NOT NULL | |
| attachment_count | INTEGER NOT NULL DEFAULT 0 | |
| size_bytes | INTEGER NOT NULL | raw size |
| blob_path | TEXT NOT NULL | relative to `blobs/` |
| seen | INTEGER NOT NULL DEFAULT 0 | |
| remote_ip, helo, auth_user | TEXT | |
| tls | INTEGER NOT NULL DEFAULT 0 | |
| parse_warnings_json | TEXT | |

Indexes: `(project_id, id DESC)`, `(project_id, received_at)`, `(project_id, seen) WHERE seen = 0`, `(project_id, smtp_credential_id)`.

### attachments
| Column | Type | Notes |
|---|---|---|
| id | INTEGER PK | |
| message_pk | INTEGER NOT NULL | FK messages(pk) CASCADE; index |
| project_id | INTEGER NOT NULL | denormalised for isolation; index |
| part_path | TEXT NOT NULL | MIME path, e.g. `1.2` |
| filename, content_type | TEXT NOT NULL | |
| size_bytes | INTEGER NOT NULL | decoded size |
| content_id | TEXT | |
| disposition | TEXT NOT NULL | `attachment` or `inline` |
| sha256 | TEXT NOT NULL | |

Attachment content is not stored separately; it is extracted from the blob on request.

### messages_fts
`CREATE VIRTUAL TABLE messages_fts USING fts5(subject, from_text, to_text, body, attachments, content='', contentless_delete=1, tokenize='unicode61 remove_diacritics 2', prefix='2 3')`. `rowid` = `messages.pk`. Contentless (the text is not needed back). `contentless_delete` needs SQLite 3.43 or later; `modernc.org/sqlite` v1.63.0 bundles SQLite 3.54.1 with FTS5 (verified 2026-11-23), and a migration test asserts it. Rows are inserted by ingest in the same transaction as `messages`. Rows are deleted by a trigger, so every delete path (single, bulk, retention, project cascade) is covered without application code:

```sql
CREATE TRIGGER messages_fts_delete AFTER DELETE ON messages BEGIN
  DELETE FROM messages_fts WHERE rowid = old.pk;
END;
```

In the goose file the trigger is wrapped in `-- +goose StatementBegin` / `-- +goose StatementEnd` (it contains semicolons). SQLite fires this trigger for rows removed by `ON DELETE CASCADE` too. A store test deletes a project and asserts `messages_fts` is empty for its former rowids.

### tags and message_tags
`tags`: `id INTEGER PK, project_id INTEGER NOT NULL FK CASCADE, name TEXT NOT NULL, color TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE(project_id, name COLLATE NOCASE)`.
`message_tags`: `message_pk INTEGER NOT NULL FK CASCADE, tag_id INTEGER NOT NULL FK CASCADE, project_id INTEGER NOT NULL` (denormalised for isolation), `PRIMARY KEY(message_pk, tag_id)`, index `(project_id, tag_id)`.

### audit_log
| Column | Type | Notes |
|---|---|---|
| id | INTEGER PK | |
| at | INTEGER NOT NULL | index |
| actor_type | TEXT NOT NULL | `user`, `token`, `system`, `smtp`, `local` |
| actor_user_id | INTEGER | FK SET NULL |
| actor_label | TEXT | email or token prefix at time of action |
| project_id | INTEGER | FK SET NULL; index `(project_id, at)` |
| action | TEXT NOT NULL | FR-105 values |
| target_type, target_id | TEXT | |
| ip | TEXT | |
| metadata_json | TEXT | never secrets |

### settings
`key TEXT PK, value TEXT NOT NULL (JSON), updated_at INTEGER NOT NULL`. Keys: `setup_token_hash`, `setup_completed_at`, `instance_name`, `retention_max_age_s`, `retention_max_messages`, `retention_max_bytes`.

---

## 11. Storage layout

```
$MAILCUBBY_DATA_DIR/
  app.db            SQLite (WAL)
  app.db-wal
  app.db-shm
  app.key           local mode only, generated if no key configured (0600)
  blobs/
    <project_id>/<yyyy>/<mm>/<ulid>.eml.zst
  tmp/              temp files for in-progress blob writes (same filesystem, cleaned at startup)
```

- `<yyyy>/<mm>` from the ULID timestamp (UTC).
- Write: create `tmp/<ulid>.tmp`, stream zstd-compressed DATA into it, fsync, rename into place, fsync the directory, then the DB transaction.
- Delete: DB transaction first, then remove the file. Missing files on delete are not errors.
- Read: open, zstd-decompress stream. Raw download streams without buffering the whole message.
- Backup: stop the container or use `sqlite3 app.db ".backup"` plus a copy of `blobs/`. Documented in README.
- File modes: directories 0700, files 0600. `app.db` is created 0600 before SQLite opens it (SQLite gives `-wal`/`-shm` the same mode); an existing `app.db` (and leftover `-wal`/`-shm`) with wider permissions is tightened to 0600 at startup, best effort (a filesystem that rejects `chmod`, such as some bind mounts, does not stop startup). No process umask is changed. The container runs as non-root UID 65532.

---

## 12. Security requirements

### 12.1 Rendering untrusted HTML

Email HTML is attacker-controlled. It MUST never execute script, reach the app's origin, or load remote resources unless the user opts in.

- HTML is served only by `GET /render/{project}/{id}`, never injected into the app DOM (`dangerouslySetInnerHTML` is forbidden for email content; enforced by lint rule).
- Response headers:
  - `Content-Type: text/html; charset=utf-8`
  - `Content-Security-Policy: sandbox allow-popups allow-popups-to-escape-sandbox; default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'; font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'`
  - With `?images=1` (and remote images enabled): `img-src data: cid: https:` instead.
  - `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cache-Control: private, no-store`, `Cross-Origin-Resource-Policy: same-origin`, `X-DNS-Prefetch-Control: off`.
- The UI embeds it in `<iframe sandbox="allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer">` (never `allow-scripts`, never `allow-same-origin`, never `allow-forms`).
- Rewriting before serving (using `golang.org/x/net/html` tokenizer, not regex):
  - inject `<meta name="referrer" content="no-referrer">` and `<base target="_blank">` at the start of `<head>` (creating `<head>` if missing); drop existing `<base>` elements;
  - drop `<meta http-equiv="refresh">` (it would navigate the iframe to a remote URL and bypass the image block) and `<link>` elements whose `rel` contains `dns-prefetch`, `preconnect`, `prefetch`, `prerender` or `preload` (not reliably covered by CSP);
  - replace `cid:` URLs in image contexts (`src` of `<img>` and `<input type=image>`, `srcset`, `background`, `poster`, SVG `<image>`/`<feImage>` `href`, and CSS `url()`, which also covers `@font-face` fonts) with `data:` URIs of the matching inline part (max 10 MiB total per render; beyond that, leave broken); `cid:` in other `src` contexts (script, iframe, embed, audio, video, source) is left unchanged, since the CSP blocks those loads;
  - remote resources (`http:`/`https:` URLs in `src`, `srcset`, `background`, CSS `url()`) are counted by the same rewriter; the count is returned as `remote_resource_count` in `MessageDetail` (the UI cannot read iframe response headers) so the UI can show "N remote images blocked";
  - no other sanitisation (the goal is to show the email as sent; isolation does the protecting). Scripts stay in the markup and are inert because of the sandbox.
- Messages without HTML: render endpoint returns `404`.

### 12.2 Attachments and raw downloads

- `Content-Disposition: attachment; filename="<ascii fallback>"; filename*=UTF-8''<percent-encoded>`.
- `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox; default-src 'none'`.
- `Content-Type`: declared type, except types that browsers can execute or render as active content (`text/html`, `application/xhtml+xml`, `image/svg+xml`, `text/xml`, `application/xml`, `application/javascript`, `text/javascript`, any `multipart/*`) are served as `application/octet-stream`.
- `?inline=1` only for `image/png`, `image/jpeg`, `image/gif`, `image/webp`; sets `Content-Disposition: inline`. Ignored for every other type (including PDF).
- Raw source and HTML source are served as `text/plain; charset=utf-8` with nosniff.

### 12.3 Application hardening

- App responses: `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`, HSTS when https.
- No inline scripts in `index.html` (theme bootstrap is a separate static file). No WebAssembly and no `eval` in the SPA: Shiki uses its JavaScript regex engine (`createJavaScriptRegexEngine`), so `script-src` needs neither `'wasm-unsafe-eval'` nor `'unsafe-eval'`.
- All SQL via sqlc-generated parameterised queries or `database/sql` with bind parameters. No string concatenation of user input into SQL (search builds SQL from fixed fragments plus bind parameters).
- Secrets (session tokens, invite tokens, reset tokens, API tokens) generated with `crypto/rand`, stored hashed (SHA-256; they have 256 bits of entropy so a slow hash is unnecessary). Passwords use argon2id (FR-042). SMTP passwords are encrypted, not hashed (FR-061), so owners can reveal them.
- Tokens and passwords never appear in logs, errors or audit metadata. Log middleware redacts `Authorization`, `Cookie`, `Set-Cookie` and `X-CSRF-Token`, never logs query strings, and replaces the token segment of invite and password-reset paths (`/api/v1/invites/{token}`, `/api/v1/password-reset/{token}`, `/invite/{token}`, `/reset/{token}`) with `[REDACTED]`.
- Constant-time comparison for all secret comparisons.

### 12.4 Brute force and rate limiting

In-memory (single process), sliding windows; state is lost on restart, which is acceptable.

- SMTP AUTH: after `MAILCUBBY_SMTP_AUTH_MAX_FAILURES` failures per (IP, username) within `MAILCUBBY_SMTP_AUTH_LOCKOUT`, further AUTH attempts for that pair get `454 4.7.0` without checking the password until the window passes; an IP is locked for all usernames after 5x that many failures. There is no global per-username lockout (usernames are not secret, and one client with a stale password must not lock out the others behind the same IP). Attempts rejected by a lockout are not counted. Server-side errors (credential lookup, or decryption, for example under a wrong `MAILCUBBY_APP_KEY`) return `454 4.7.0` and are not counted; while the key is wrong this distinguishes existing usernames from unknown ones without delay, which is acceptable because usernames are not secret. Clients such as Python `smtplib` try PLAIN and then LOGIN, so one wrong `login()` counts as two failures. Lockout state is in memory; restarting clears it. An audit entry `smtp.auth_lockout` (metadata `reason` `ip_and_username` or `ip`) is written once per lockout. After a failure the server delays its reply by 1 s (the delay goes through the injected clock so tests do not sleep).
- HTTP login: `MAILCUBBY_LOGIN_RATE_LIMIT` per (IP, email) and 5x that per IP; `429` with `Retry-After`. Only failed attempts count, but each attempt is reserved atomically before the password is verified and given back only on success, so concurrent requests cannot exceed the limit. An email longer than 254 bytes gets `400 bad_request` before rate limiting, hashing or auditing.
- Invite preview/accept and password reset: 20 requests per 15 min per IP.
- API: `MAILCUBBY_API_RATE_LIMIT` per token or session (token bucket).
- Client IP is the socket address, or `X-Forwarded-For` (rightmost untrusted hop) only when the peer is in `MAILCUBBY_TRUSTED_PROXIES`.

### 12.5 Tenant isolation

- Every store method that reads or writes tenant data takes `projectID` and includes `project_id = ?` in its SQL. Message IDs are never trusted alone.
- Authorisation happens in API middleware that resolves `{project}` to a project ID and role once per request; handlers receive the resolved project ID from context, never the raw slug.
- SSE fan-out filters by the subscriber's project set (FR-091).
- Tests (ARCHITECTURE section 8.3) enforce isolation.

### 12.6 Audit

See FR-105. Admin and owner screens show the log.

---

## 13. Non-functional requirements

| ID | Requirement | Target | How measured |
|---|---|---|---|
| NFR-001 | Ingest throughput | at least 200 messages/s sustained, 10 KiB messages, 10 parallel SMTP clients, 2 vCPU, SSD | `make bench` (`BenchmarkSMTPIngest` in `internal/smtpd`, using `net/smtp`) |
| NFR-002 | Ingest latency to SSE | p95 under 250 ms from end of DATA to SSE event | `make bench` (`BenchmarkSSELatencyNFR002` in `internal/api`) |
| NFR-003 | Message list | first page p95 under 50 ms with 100k messages in the project | Go benchmark on seeded DB |
| NFR-004 | Search | p95 under 150 ms for a two-word query with 100k messages | Go benchmark |
| NFR-005 | Memory | idle RSS under 40 MB; under 150 MB while ingesting at NFR-001 rate with 1M stored messages | manual + CI smoke |
| NFR-006 | Image size | compressed image about 9 MB (measured for v0.1.0), 25 MB hard limit per arch | CI check on `docker image inspect` |
| NFR-007 | Startup | under 1 s to ready with 1M messages (no full scans at startup) | integration test |
| NFR-008 | Max message size | default 25 MiB, configurable up to 100 MiB | config validation |
| NFR-009 | UI performance | initial JS under 250 KB gzip; inbox list scrolls 60 fps with 10k loaded rows (virtualised); interaction to next paint under 100 ms | Lighthouse / bundle size check in CI |
| NFR-010 | Accessibility | WCAG 2.1 AA (see UI.md) | axe in Playwright e2e |
| NFR-011 | Platforms | linux/amd64, linux/arm64 images; binaries for linux, macOS, Windows (amd64, arm64) | GoReleaser |
| NFR-012 | Durability | an acknowledged message (`250` after DATA) survives process crash | WAL with `synchronous=NORMAL` survives process crash; power loss may lose the last transactions (documented) |
| NFR-013 | Browser support | last 2 versions of Chrome, Firefox, Safari, Edge | |

---

## 14. Roadmap (out of scope for v1)

Listed so v1 design does not block them. Do not implement in v1.

1. **Release / relay**: forward a captured message to a real address through a configured upstream SMTP (per project allowlist).
2. **HTML compatibility check** (caniemail data) and **link checker**.
3. **Spam check** (SpamAssassin / rspamd integration).
4. **Webhooks** per project on `message.created`.
5. **MCP endpoint** so AI agents can query inboxes (list, search, read, wait).
6. **OIDC / SSO** login.
7. **PostgreSQL** store backend.
8. **S3-compatible** blob storage.
9. **POP3** access per project credential.
10. **Chaos mode**: configurable SMTP failures (per credential) for resilience testing.
11. Prometheus `/metrics`, OpenTelemetry tracing.
12. Remote image proxy (load images without exposing viewer IP).
13. App key rotation command (`mailcubby admin rotate-app-key`).
14. Invite and reset emails via an outbound SMTP setting.
15. Message links/OTP extraction endpoint for tests.
16. Per-user read state.
17. Built-in ACME certificates (certmagic) for SMTP and HTTP; v1 uses certificate files only.
18. Serving the UI and API under a sub-path (`MAILCUBBY_BASE_PATH`); v1 is served from the root of its host.
19. Inline PDF preview of attachments (needs a PDF-specific CSP that browser viewers accept).
20. PROXY protocol v1/v2 support for SMTP behind TCP load balancers (`MAILCUBBY_SMTP_PROXY_PROTOCOL` / `MAILCUBBY_SMTP_PROXY_PROTOCOL_TRUSTED`).
21. Direct HTTPS listener (`MAILCUBBY_HTTP_TLS_CERT` / `MAILCUBBY_HTTP_TLS_KEY`); v1 terminates HTTPS at a reverse proxy (Caddy, Traefik, nginx).
