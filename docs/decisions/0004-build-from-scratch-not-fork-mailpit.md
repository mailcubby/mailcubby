# ADR 0004: Build from scratch instead of forking Mailpit

- Status: Accepted
- Date: 2026-11-23

## Context

Mailpit (Go, MIT, about 10.9k stars) is the best open-source mail catcher and the obvious starting point. Its maintainer declined multiple inboxes, per-credential routing and multiple users (issues #86, #115, #128), stating it would need "a complete rewrite". Its `--tenant-id` is only a table prefix. Its storage keeps raw messages inside SQLite, its UI is Vue, and its SMTP server is a fork of an older smtpd package that has had security advisories (header injection, long-line DoS).

Our core requirement, isolation by project with per-project credentials and roles, touches every layer: SMTP session, storage schema, every query, API authorisation, realtime fan-out and UI.

## Decision

- Write Mailcubby from scratch, using Mailpit as a feature and UX baseline, not as a code base.
- Keep compatibility where it helps migration: default ports 1025/8025, `X-Tags` header, plus-address tags, a `sendmail` subcommand.
- Use maintained libraries for the risky parts: `emersion/go-smtp` (SMTP protocol), `jhillyerd/enmime` v2 (MIME parsing).
- No Mailpit code is copied. If a future contributor ports a specific piece of MIT code, keep its copyright notice.

## Consequences

- More initial work, but a schema and API designed for tenancy from day one, and no long-lived fork to rebase.
- We lose Mailpit's mature extras (HTML check, link check, spam check, POP3, chaos, webhooks) in v1; they are on the roadmap.
- Users migrating from Mailpit change only the image; existing SMTP settings keep working in local mode.
