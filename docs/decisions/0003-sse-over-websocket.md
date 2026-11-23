# ADR 0003: Server-Sent Events for realtime updates

- Status: Accepted
- Date: 2026-11-23

## Context

The UI must show new messages and counter changes in realtime. Traffic is one-directional (server to browser); user actions already go through the REST API. Mailpit uses WebSockets. Mailcubby runs behind reverse proxies (Caddy, Traefik, nginx) and must filter events per user by project membership.

Alternatives considered: WebSocket (bidirectional, which we do not need; needs a third-party library or `golang.org/x/net/websocket`; extra proxy configuration; custom auth handshake), long polling (more requests and latency).

## Decision

- Use SSE: `GET /api/v1/events` returning `text/event-stream`, implemented with `net/http` only (`http.ResponseController` to flush and lift write deadlines).
- Authentication by the normal session cookie or bearer token; no special handshake.
- An in-process event bus fans out events; each subscription filters by the subscriber's accessible project set, refreshed on membership changes.
- No event replay: on reconnect the client invalidates its queries. Heartbeat comments every 25 s keep proxies from closing idle streams.

## Consequences

- No extra dependency; works through standard HTTP proxies (nginx needs `proxy_buffering off` or honours `X-Accel-Buffering: no`, documented).
- Browsers limit concurrent HTTP/1.1 connections per origin (6); the SPA uses one shared stream per tab. HTTP/2 via the reverse proxy removes this limit.
- Client-to-server realtime messages are not possible over this channel; none are needed.
