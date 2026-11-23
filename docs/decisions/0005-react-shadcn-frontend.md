# ADR 0005: React, Vite, Tailwind and shadcn/ui for the frontend

- Status: Accepted
- Date: 2026-11-23

## Context

A polished, dense, keyboard-first UI is a core differentiator (see `docs/UI.md`). The UI is a client-rendered SPA embedded in the Go binary, so no server-side rendering is needed. The team and AI coding agents are most productive with mainstream, well-documented tools, and accessible primitives (dialogs, menus, popovers, comboboxes) must be correct.

Alternatives considered: Vue (Mailpit's choice; smaller ecosystem for the command palette, virtualisation and component primitives we want), Svelte (smaller bundles, fewer agents trained on current Svelte 5 idioms), server-rendered HTML with htmx (hard to deliver the realtime, keyboard-heavy three-pane experience), Next.js (SSR not needed; does not embed cleanly in a Go binary).

## Decision

- React 19 + TypeScript 6 (strict) + Vite 8.
- Tailwind CSS v4 (CSS-first config) and shadcn/ui components copied into the repo (Radix primitives, owned code).
- TanStack Router (typed file routes, search params in the URL), TanStack Query (server state, SSE cache updates), TanStack Virtual (lists), cmdk (palette), sonner (toasts), react-resizable-panels (panes), lucide-react (icons), Shiki lazy-loaded for read-only code views.
- Biome for lint and format; Vitest for unit tests; Playwright for e2e.
- API types generated from the OpenAPI document (`openapi-typescript`, `openapi-fetch`).

## Consequences

- Larger baseline bundle than Svelte; mitigated with route-level code splitting, lazy Shiki, and a CI budget (initial JS under 250 KB gzip).
- shadcn components are owned code: we can adapt them freely but must update them manually.
- Node.js is needed only at build time; the runtime image contains only static files inside the Go binary.
