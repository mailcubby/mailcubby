# Mailcubby: UI Specification

The web UI is a single-page app in `web/`, built by Vite and embedded in the Go binary. This document defines design principles, stack, screens and interaction details. Behaviour and permissions come from `docs/SPEC.md`; the API from `internal/api/openapi.yaml`.

## 1. Design principles

1. **Modern and calm.** Inspired by Resend and Linear: neutral surfaces, thin borders, little chrome, generous but dense layout. No gradients, no illustrations beyond simple empty-state icons.
2. **Dense.** Developers scan many messages. Message rows are 36 px (compact, default) or 44 px (comfortable). Base font 13 px in lists and chrome, 14 px in reading areas.
3. **Keyboard-first.** Every action reachable by keyboard; the command palette (`Cmd/Ctrl+K`) reaches every screen and action. Mouse use is fully supported but never required.
4. **Fast.** Instant navigation from cache, prefetching, optimistic updates, skeletons instead of spinners, virtualised lists. Budget: initial JS under 250 KB gzip (SPEC NFR-009).
5. **Themes.** Light, dark and system (default). Theme applied before first paint by `/theme.js` (a static file, because inline scripts are blocked by CSP).
6. **Colour carries state, not decoration.** A single accent colour is used only for: focus rings, selection, unread indicators, primary buttons, realtime arrival highlight. Project colours appear only as small dots. Destructive actions use red. Everything else is greyscale.
7. **Typography.** Inter Variable (self-hosted via `@fontsource-variable/inter`), `font-feature-settings: "cv11", "ss01"`; `tabular-nums` for times, counts and sizes. Monospace: `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace` for raw source, headers, credentials.
8. **Motion.** 120 to 180 ms ease-out for panels, popovers and arrival highlights. Disabled under `prefers-reduced-motion`.
9. **Trustworthy with untrusted content.** Email HTML only ever appears inside the sandboxed iframe (SPEC 12.1). The UI makes blocked remote content visible and opt-in.

### Design tokens (in `web/src/styles/globals.css`, Tailwind v4 `@theme`)

| Token | Light | Dark | Use |
|---|---|---|---|
| `--background` | zinc-50 | zinc-950 | app background |
| `--surface` | white | zinc-900 | panes, cards |
| `--border` | zinc-200 | zinc-800 | 1 px dividers |
| `--muted-foreground` | zinc-500 | zinc-400 | secondary text |
| `--foreground` | zinc-900 | zinc-100 | primary text |
| `--accent` | blue-600 | blue-500 | the only accent |
| `--destructive` | red-600 | red-500 | delete, errors |
| `--radius` | 6 px | 6 px | controls, cards |

Project colour tokens: `slate, red, orange, amber, lime, green, teal, cyan, blue, violet, fuchsia, pink` (12, matching SPEC FR-030).

## 2. Stack

- React 19, Vite 8, TypeScript 6 (strict), Tailwind CSS v4 (CSS-first config), shadcn/ui components (Radix-based, copied into `web/src/components/ui/`).
- TanStack Router (file-based routes, typed search params), TanStack Query (server state, SSE-driven cache updates), TanStack Virtual (message list).
- `cmdk` (command palette via shadcn `command`), `sonner` (toasts), `react-resizable-panels` (via shadcn `resizable`), `lucide-react` icons.
- Shiki (lazy-loaded, fine-grained bundle via `shiki/core` with only `html`, `css`, `json`, a custom plain theme and the JavaScript regex engine `createJavaScriptRegexEngine`; no WASM, so the app CSP needs no `wasm-unsafe-eval`) for read-only HTML source and headers highlighting. Raw sources above 512 KB are shown as virtualised plain text without highlighting. CodeMirror is not used in v1 (no editing needed).
- `openapi-fetch` typed client generated from `internal/api/openapi.yaml`.
- Exact versions: `AGENTS.md` section 3.

### Routes

| Path | Screen |
|---|---|
| `/login` | Login (server mode) |
| `/setup` | First-run wizard (server mode, setup pending) |
| `/invite/$token` | Accept invite |
| `/reset/$token` | Set new password |
| `/` | Projects home |
| `/p/$project` | Project inbox (list + empty reading pane) |
| `/p/$project/m/$messageId` | Inbox with message open (`?tab=preview|text|source|headers|raw|attachments|tech`) |
| `/p/$project/settings/$section` | Project settings (`general`, `smtp`, `members`, `retention`, `routing`, `api`) |
| `/account/$section` | Account (`profile`, `security`, `tokens`, `preferences`) |
| `/admin/$section` | Admin (`users`, `projects`, `settings`, `audit`, `stats`) |

Inbox search state lives in URL search params (`?q=from:alice is:unread&tag=invoice`) so views are shareable and the back button works.

## 3. Screens

### 3.1 Login

Centered card: instance name, email, password, "Sign in". Errors inline under the form (generic "Email or password is incorrect"). Rate limit shows "Too many attempts, try again in N minutes". No sign-up link (invites only). Local mode never shows this screen.

### 3.2 Setup wizard (first run, server mode)

Steps with a progress indicator; each step is a short form. Steps 1 to 3 are collected client-side and submitted together as one `POST /api/v1/setup` at the end of step 3 (a wrong token sends the user back to step 1 with the error).

1. **Setup token**: paste the token from the server log. The step lists where to find it, each command with a copy button: Docker Compose (`docker compose -f deploy/compose.server.yml logs mailcubby | grep "setup token" | tail -1`), Docker (`docker logs <container> 2>&1 | grep "setup token" | tail -1`) and the binary (the `setup token:` line in the terminal or log of `mailcubby serve`). A restart prints a new token, so the latest one wins. The "Paste the setup token" error shows only after a submit attempt and clears when the user types.
2. **Admin account**: name, email, password (strength hint, minimum 10 characters).
3. **Instance**: instance name (sent as `instance_name`); read-only checks with green/amber status, all from `GET /api/v1/meta`: configured `base_url` vs `window.location.origin`, SMTP public host, port and TLS mode (`none` is amber), inbox domain (if set).
4. **First project**: name (slug auto-derived, editable). Creates the project and its first SMTP credential.
5. **Connect your app**: connection details and integration snippets (3.6.2), "Send test email" button, and a live "waiting for first message" indicator that turns into a success state when any message arrives (SSE). "Go to inbox".

### 3.3 Projects home

- Header: instance name, command palette hint (`Cmd K`), account menu, "New project" (if allowed).
- Filter input ("Filter projects", `/` focuses it).
- Project list as cards in a responsive grid (or compact list, toggle remembered). Each card: colour dot, name, slug (mono, muted), role badge (owner/member/viewer), unread count (accent badge), total messages, size, "last message 3m ago", 24 h sparkline (inline SVG, 24 bars, muted colour; accent for the latest hour if non-zero).
- Realtime: counts and sparklines update via SSE; a card briefly highlights when mail arrives.
- Keyboard: arrows/`j`/`k` move focus, `Enter` opens, `n` new project.
- Local mode: a banner with "SMTP `localhost:1025`, any username creates a project" and copy buttons.
- Empty state (no projects): explains what a project is and offers "New project" (or, for users without permission, "Ask an admin to add you to a project").

### 3.4 Project inbox (three resizable panes)

```
+------------+-------------------------------+----------------------------------+
| Sidebar    | Search [from:alice is:unread] | Subject                          |
| (o) shop   |-------------------------------| From  To  Date   [tags] [actions]|
|  Inbox  12 | * Alice   Order #123   2m  [] |----------------------------------|
|  Unread  3 |   Bob     Reset pwd    1h     | Preview Text Source Headers Raw  |
| Tags       |   ...                         | Attachments Tech  [device][dark] |
|  invoice 4 |                               |                                  |
|  urgent  1 |                               |   sandboxed iframe               |
| Credentials|                               |                                  |
|  staging   |                               |                                  |
| Settings   |                               |                                  |
+------------+-------------------------------+----------------------------------+
```

- **Sidebar** (collapsible with `[`): project switcher (popover with search, also `Cmd+K`), Inbox (count), Unread (count), tags with counts (click filters; `+` creates tag for members), credentials (filter by credential, owner/member only), Settings link, connection quick-copy (SMTP host/port/username) for members.
- **Message list** (virtualised): search bar with operator autocomplete (typing `from:` suggests recent senders; `tag:` suggests tags; `is:` suggests `unread`/`read`; `has:` suggests `attachment`) and parsed operators shown as removable chips. Row content: unread dot, sender name (bold if unread), subject, snippet (muted, single line), attachment icon, tag pills (max 2 + "+N"), relative time. Multi-select with `x` or checkbox/shift-click; bulk bar shows delete, mark read/unread, tag. Infinite scroll with cursor pagination. Header shows total and "Delete all" (member) with confirmation that states the count and active filter.
- **Reading pane**: message header and tabbed views (3.5). When nothing is selected: keyboard hints, or the setup guide when the project has no messages (3.4.1).
- Pane widths persist per user (localStorage). Below 1024 px the layout becomes two panes (sidebar collapses into a drawer); below 768 px it becomes a single stacked view with back navigation.

#### 3.4.1 Empty inbox = setup guide

For a project with zero messages, the list area shows: "No messages yet", connection details (host, port, TLS, username; password reveal for allowed roles), the integration snippet tabs (3.6.2), recipient routing addresses if enabled, and a primary "Send test email" button (member+; calls `POST /send-test`). Viewers see "No messages yet" only.

### 3.5 Message view

**Header**: subject (selectable, copy on click), from (name + address; click copies address), to/cc/bcc (collapsed after 3 with "+N"), received time (relative, exact on hover), size, credential label, tags (editable inline for members with autocomplete), actions: delete, mark unread, download `.eml`, copy link to message, open preview in new window (opens `/render/...` directly, still sandboxed by CSP), previous/next.

**Tabs** (number keys `1` to `7`; `8` is reserved for a future Links tab):

| Key | Tab | Content |
|---|---|---|
| 1 | Preview | Sandboxed iframe of `/render/{project}/{id}`. Toolbar: device (desktop fluid, tablet 768 x 1024, mobile 375 x 667), rotate (swaps width/height for tablet/mobile), zoom (50 to 200%, fit), email dark-mode toggle (sets `color-scheme: dark` on the iframe element so the email's `prefers-color-scheme: dark` rules apply), remote images toggle with "N remote images blocked" notice (when `opt-in`). Device frame is a subtle rounded border with dimensions label. Hidden if the message has no HTML. |
| 2 | Text | Text part in a readable column (max 80ch), URLs auto-linked as React elements (`target=_blank rel="noopener noreferrer"`). Shows "Generated from HTML" note if `text_generated`. |
| 3 | HTML source | Highlighted (Shiki, lazy) with line numbers, wrap toggle, copy, search within (`Cmd+F` handled by browser). |
| 4 | Headers | Table of headers in original order, monospace values, copy per row, filter box. |
| 5 | Raw | Full raw source (`/raw`), virtualised plain text for large messages, copy, download `.eml`. |
| 6 | Attachments | List with icon by type, filename, size, content type, inline/attachment badge, `cid:` Content-ID, a "shown in preview" badge for parts the HTML preview inlines (`referenced_by_html`), download; thumbnails for raster images (`?inline=1`); every other type, including PDF, is downloaded. Count badge on tab (counts every part). |
| 7 | Tech | Envelope (MAIL FROM, RCPT TO), remote IP, HELO, TLS yes/no, AUTH user, credential, Message-ID, Date header vs received time, raw size, parse warnings. |

Viewers with `viewer_hide_technical` see only Preview, Text and Attachments. The last selected tab and device are remembered.

**Attachment strip** (FR-072): while Preview or Text is active, a compact strip sits below the view (a non-scrolling footer of the reading pane, so it stays visible under the full-height preview iframe without scrolling the email). Header "N attachments" with a paperclip icon, then one card per attachment: thumbnail for the inline-allowlisted raster images (png, jpeg, gif, webp via `?inline=1`; never SVG), otherwise a lucide icon by type (image, PDF, calendar, archive, audio, video, text, generic file); filename (truncated, full name on hover) and size (`Intl`). Each card is a link that downloads the file (`Content-Disposition: attachment`) with an accessible name "Download <filename>, <size>"; PDFs are never rendered inline. Cards wrap and the strip is capped at about two rows with its own scroll, so many attachments cannot squeeze the preview. Parts with `referenced_by_html` (inline images the preview already shows through `cid:`) are left out; the strip is hidden when no card remains. The strip is not shown on the technical tabs or on Attachments, which keeps the full details. Viewers with `viewer_hide_technical` see the strip and the Attachments tab.

### 3.6 Project settings

Left nav of sections; each section saves independently with optimistic UI and toast confirmation. Owner-only sections are hidden for other roles.

#### 3.6.1 General
Name, slug (with a warning that recipient routing addresses change; shows old and new addresses), colour picker (12 tokens). Danger zone: delete project (type the slug to confirm).

#### 3.6.2 SMTP
- Connection card: host, port, TLS mode, each with copy button.
- Credentials table: label, username (mono, copy), password (masked; **Reveal** (owner, or member if allowed) fetches and shows for 30 s then re-masks; **Copy** reveals and copies without displaying), created, last used, status. Row actions: rotate (confirm: "Apps using the old password will fail"), disable/enable, delete. "New credential" asks for a label and shows the password once with copy.
- **Integration snippets** (tabs; values filled with the selected credential; password shown as `********` until revealed, copy button copies the real value after reveal):
  - Laravel `.env`:
    ```
    MAIL_MAILER=smtp
    MAIL_SCHEME=smtp            # Laravel 10 and older: MAIL_ENCRYPTION=tls
    MAIL_HOST=mail.example.com
    MAIL_PORT=587
    MAIL_USERNAME=shop-a1b2c3d4
    MAIL_PASSWORD=********
    ```
  - Symfony: `MAILER_DSN=smtp://shop-a1b2c3d4:********@mail.example.com:587`
  - Node.js Nodemailer: `nodemailer.createTransport({ host: "mail.example.com", port: 587, secure: false, requireTLS: true, auth: { user: "shop-a1b2c3d4", pass: "********" } })`
  - WordPress (WP Mail SMTP plugin): settings table (Mailer "Other SMTP", host, encryption TLS, port 587, auto TLS on, authentication on, username, password) and the `wp-config.php` constants alternative (`WPMS_ON`, `WPMS_MAILER`, `WPMS_SMTP_HOST`, `WPMS_SMTP_PORT`, `WPMS_SSL`, `WPMS_SMTP_AUTH`, `WPMS_SMTP_USER`, `WPMS_SMTP_PASS`, `WPMS_SMTP_AUTOTLS`).
  - Django `settings.py`: `EMAIL_BACKEND`, `EMAIL_HOST`, `EMAIL_PORT = 587`, `EMAIL_HOST_USER`, `EMAIL_HOST_PASSWORD`, `EMAIL_USE_TLS = True`.
  - Rails: `config.action_mailer.delivery_method = :smtp` and `smtp_settings` with `address`, `port: 587`, `user_name`, `password`, `authentication: :plain`, `enable_starttls_auto: true`.
  - swaks: `swaks --to test@example.com --server mail.example.com:587 --tls --auth PLAIN --auth-user shop-a1b2c3d4 --auth-password '********'`
  - PHP `sendmail_path`: `sendmail_path = "/usr/local/bin/mailcubby sendmail -t"` (local mode).
  In local mode snippets use the `smtp` host and port from `GET /api/v1/meta` (defaults `localhost`, `1025`), no TLS, and the project slug as username with a placeholder password. Snippet generation is a pure function (`web/src/features/settings/snippets.ts`) so it can be golden-tested.

#### 3.6.3 Members and invites
Members table (name, email, role select for owners, remove). Pending invites (email, role, expires, revoke). "Invite" dialog: email + role; result shows the invite link with copy button and "Link shown once" note. "Preview as viewer" toggle explained here (3.8).

#### 3.6.4 Retention
Max age, max messages, max size: each with "Inherit instance default (30 days)" checkbox or a value. Shows current usage (messages, size) against limits with a simple bar.

#### 3.6.5 Routing and client view
Recipient routing toggle (server mode) with the resulting addresses (`*@shop.inbox.example.com`, `shop+tag@inbox.example.com`) and a warning that unauthenticated senders can then write into this project. Plus-addressing tags toggle. "Hide technical views from viewers" toggle. "Members can reveal SMTP passwords" toggle.

#### 3.6.6 API
Explains API tokens (created in Account > API tokens, optionally restricted to this project), with a "Create token for this project" shortcut and copyable `curl` examples for list, wait and delete-all using the project slug.

### 3.7 Account and admin

- **Account**: profile (name, email read-only), security (change password, active sessions with revoke), API tokens (create with name, scope, project, expiry; token shown once), preferences (theme, density, mark-as-read delay 0/1/3 s/never, single-key shortcuts on/off, 24 h clock). Preferences are per browser (`usePersistentState`, localStorage), not stored on the server. In local mode only `/account/preferences` exists (reachable from the header menu and the palette); the other account sections are server-mode only.
- **Admin**: Users (table, search, invite user, toggle admin / can-create-projects, disable, reset link), Projects (all projects with owners, counters, open), Settings (instance name, default retention with "from env" vs "overridden" indicator), Audit log (filterable table: time, actor, action, project, target, IP; JSON metadata on expand), Stats (cards: messages, storage, DB size, ingest rate, version, uptime).

### 3.8 Client view mode

Viewers get a simplified UI: no delete or tag actions, no SMTP or settings sections, technical tabs hidden when `viewer_hide_technical` is on, and a clean header. Owners can toggle **Preview as viewer** (eye icon in the header, also in the palette) to see exactly what viewers see; a persistent banner shows while active.

## 4. Premium details (required for v1)

1. **Command palette** (`Cmd/Ctrl+K`): fuzzy search over projects, tags, actions ("Delete all in shop", "Copy SMTP password", "Toggle theme", "Go to settings", "Send test email"), recent messages by subject, and search operators. Grouped results, keyboard only, shows shortcuts next to actions, remembers recent commands. Follows the Superhuman command palette guidance (reference 3).
2. **Keyboard shortcuts** (single-key shortcuts are disabled while focus is in an input, and can be turned off in preferences, WCAG 2.1.4):

   | Key | Action |
   |---|---|
   | `j` / `k` | next / previous message (opens it in the reading pane) |
   | `Enter` / `o` | open focused message / focus reading pane |
   | `Esc` | close dialog, clear selection, back to list |
   | `e` or `#` or `Delete` | delete message (with undo) |
   | `u` | toggle read / unread |
   | `x` | select message; `Shift+j/k` extends selection |
   | `/` | focus search |
   | `t` | edit tags |
   | `1` to `7` | switch message tab (`8` reserved) |
   | `d` | cycle device (desktop, tablet, mobile) |
   | `i` | toggle remote images |
   | `[` | toggle sidebar |
   | `g p` / `g s` / `g a` | go to projects / project settings / account |
   | `Cmd/Ctrl+K` | command palette |
   | `?` | shortcuts help overlay |
3. **Realtime arrival**: new messages slide into the list with a 1.5 s accent background fade; if the list is scrolled down, a "N new" pill appears at the top (click scrolls up); the document title shows the unread count (`(3) shop - Mailcubby`); an `aria-live="polite"` region announces "New message from Alice: Order #123" (throttled to one announcement per 5 s).
4. **Empty inbox as setup guide** with "Send test email" (3.4.1).
5. **Optimistic delete with undo**: the row disappears immediately; a toast "Message deleted. Undo" stays 5 s; the API call is sent when the toast expires (or immediately on `pagehide` using `fetch(..., { keepalive: true })`). Bulk deletes and delete-all use a confirmation dialog instead of undo.
6. **Skeletons** for list rows, message header and project cards; no full-page spinners. Content appears from cache instantly when available.
7. **Prefetch**: when a message opens, prefetch the next and previous message details; prefetch on 100 ms row hover; prefetch the render URL of the next message only on fast connections (`navigator.connection.saveData` false).
8. **Device preview** with rotation, zoom and email dark-mode toggle (3.5).
9. **Remote image toggle** with a blocked-count notice; per message, not persisted (privacy).
10. **Copy on click** for addresses, Message-ID, SMTP fields, slugs and snippets, with a short "Copied" toast.
11. **Relative timestamps** (`now`, `3m`, `2h`, `Yesterday 14:03`, `12 Nov`) via `Intl.RelativeTimeFormat` / `Intl.DateTimeFormat`, updating every minute; exact local and UTC time in a tooltip on hover/focus.
12. **Client view mode** (3.8).
13. **Remembered layout**: pane widths, sidebar collapsed state, selected tab, device, density, theme (localStorage, namespaced `mailcubby:`).
14. **Connection state**: a small indicator in the sidebar footer shows realtime status (connected, reconnecting); on reconnect the app invalidates queries.
15. **Error handling**: API errors show toasts with the server message; `401` redirects to login preserving the return URL; `404` on a project shows "Project not found or you do not have access".

## 5. Accessibility (WCAG 2.1 AA)

- Contrast: text 4.5:1, large text and UI components 3:1, in both themes (verified with axe in e2e and manually for the accent).
- Full keyboard operation; visible focus ring (2 px accent outline with offset) on every interactive element; logical tab order; focus is trapped in dialogs and restored on close.
- Message list uses `role="listbox"` (or `grid` when bulk selection is active) with `aria-selected`, `aria-activedescendant` and row labels ("Unread. From Alice. Order #123. 2 minutes ago. 1 attachment").
- The preview iframe has `title="Email preview: <subject>"`.
- Single-key shortcuts can be turned off (2.1.4); shortcuts never fire inside text inputs.
- `aria-live` announcements for new mail and for toasts (sonner supports this).
- Respect `prefers-reduced-motion` and `prefers-contrast`; support 200% zoom and 320 px width without loss of content.
- Icons that convey meaning have labels; colour is never the only signal (unread uses dot + bold weight; project colours accompany names).
- Forms: labels bound to inputs, errors linked via `aria-describedby`, required fields marked.

## 6. References

1. Resend design: https://resend.com/design.md
2. Linear UI redesign: https://linear.app/now/how-we-redesigned-the-linear-ui
3. Superhuman command palette: https://blog.superhuman.com/how-to-build-a-remarkable-command-palette/
4. Mailtrap Email Sandbox overview (projects, inboxes, sharing): https://docs.mailtrap.io/email-sandbox/overview
5. Mailpit (feature baseline, `X-Tags`, UI conventions): https://github.com/axllent/mailpit
6. Rendering untrusted HTML email safely (Close): https://making.close.com/posts/rendering-untrusted-html-email-safely
