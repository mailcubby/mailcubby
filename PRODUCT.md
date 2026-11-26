# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

- **Developers and agency staff (owner, member)** who send test mail from their apps and need to find, inspect and debug captured messages fast, often many per minute, mostly with the keyboard.
- **Agency clients (viewer)** who are invited to one project to review the emails their app sends: sign in, read, download attachments. Not technical; occasional use.
- **Solo developers (local mode)** who run one binary or container with zero configuration.

## Product Purpose

A self-hosted email testing server (mail catcher): apps send mail to its SMTP port, Mailcubby stores it and shows it in a web UI and REST API, without delivering anything. Success: a developer sees a new message in under a second and can trust what it shows; a client can review emails without help.

## Positioning

Multi-tenant where Mailpit is single-user: projects, per-project SMTP credentials, roles (owner, member, viewer) and invites, so an agency can share one instance with its team and clients. One static Go binary with the UI embedded, about 9 MB image.

## Operating Context

Runs on a developer laptop (local mode) or a shared server behind a reverse proxy (server mode). Used next to an IDE and terminal while testing app flows (sign-up, password reset, invoices). Clients use it from an invite link in a browser.

## Capabilities and Constraints

- Email HTML is untrusted and only rendered in a sandboxed iframe (AGENTS.md section 8).
- Keyboard-first, dense, fast (initial JS under 250 KB gzip).
- Stack and dependencies pinned (AGENTS.md section 3); no new dependencies without owner approval.
- WCAG 2.1 AA in light and dark.

## Brand Commitments

Name `Mailcubby` (display) / `mailcubby` (identifiers). Inter, lucide icons. No other brand assets yet.

## Evidence on Hand

MIME fixtures in `testdata/mime/`. No customers, testimonials or metrics to cite.

## Product Principles

1. The task comes first: density and speed over decoration.
2. Untrusted content is never trusted.
3. Clients get fewer decisions, not a different product.
4. Local mode stays zero-config.
