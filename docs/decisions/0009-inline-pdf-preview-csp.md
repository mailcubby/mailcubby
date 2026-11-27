# ADR 0009: Serve inline PDF previews from a dedicated `/render/` endpoint with a PDF-specific CSP

- Status: Accepted
- Date: 2026-11-27

## Context

Mailcubby v0.1 forces all PDF attachments to download (`Content-Disposition: attachment`) under the attachment response headers (`Content-Security-Policy: sandbox; default-src 'none'`, `X-Frame-Options: DENY`). As noted in `docs/SPEC.md` FR-074 and section 14.19, built-in browser PDF viewers (Chromium PDFium, Firefox PDF.js, Safari PDFKit) refuse to render a PDF when the HTTP response carries `Content-Security-Policy: sandbox` or when embedded in an `<iframe sandbox>`.

Agencies and client viewers frequently inspect transactional PDF attachments (invoices, receipts, shipping labels) and expect inline preview without downloading files to disk. Two approaches were considered:

1. **Client-side JavaScript PDF renderer (e.g. PDF.js bundled in `web/`)**: keeps download-only headers on the server, but adds a large third-party dependency (~500 KB+ gzipped, worker scripts, CMap/font assets, WebAssembly/worker CSP changes) and ongoing parser CVE maintenance.
2. **Native browser PDF viewer via a dedicated `/render/` endpoint**: adds zero dependencies and minimal UI bundle size (~1–2 KB gzip), while relying on strict server-side content validation and a tailored non-sandbox CSP that keeps HTML/script execution impossible.

## Decision

Serve inline PDF previews through a dedicated endpoint `GET /render/{project}/{id}/attachments/{attachment_id}` using the browser's native PDF viewer inside a modal `<iframe referrerPolicy="no-referrer">` (no `sandbox` attribute), with the following mandatory controls:

- **Separation of trust zones**: `GET /api/v1/projects/{project}/messages/{id}/attachments/{attachment_id}` remains unchanged (`X-Frame-Options: DENY`, `Content-Security-Policy: sandbox; default-src 'none'`, `?inline=1` restricted exclusively to allowlisted raster images). Only `/render/` endpoints may be framed by `'self'`.
- **Strict media-type and magic-byte validation**: The endpoint accepts only attachments whose coerced media type (`safeAttachmentContentType`) is `application/pdf` **and** whose decoded bytes start with the `%PDF-` signature at offset 0. Any non-PDF attachment or forged payload (such as HTML, SVG, or XML disguised as `application/pdf`, or a polyglot without `%PDF-` at byte 0) is rejected with `415 Unsupported Media Type`.
- **PDF-specific response headers**:
  - `Content-Type: application/pdf`
  - `Content-Disposition: inline; filename="<ascii fallback>"; filename*=UTF-8''<percent-encoded>`
  - `X-Content-Type-Options: nosniff`
  - `Content-Security-Policy: default-src 'none'; object-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'`
  - `Referrer-Policy: no-referrer`
  - `Cache-Control: private, no-store`
  - `Cross-Origin-Resource-Policy: same-origin`
  - `X-DNS-Prefetch-Control: off`
  - `X-Frame-Options` is omitted (matching `GET /render/{project}/{id}`; framing is governed by `frame-ancestors 'self'`).
- **SPA CSP unchanged**: The main SPA keeps `object-src 'none'` and `frame-src 'self'` because the UI embeds the PDF via `<iframe>`, never `<object>` or `<embed>`.
- **Permissions**: `PermViewer` (accessible to viewers even when `viewer_hide_technical=true`, consistent with FR-072 and FR-074).
- **Kill-switch configuration**: `MAILCUBBY_RENDER_INLINE_PDF` (boolean, default `true` in both local and server modes; exposed in `GET /api/v1/meta` as `features.inline_pdf`). When `false`, the PDF render endpoint returns `404` and the UI falls back to download-only cards.

## Consequences

- **Zero new dependencies** and negligible binary/JS bundle impact; initial JS budget (< 250 KB gzip) and Docker image size (~9 MB) are unaffected.
- **Revises `AGENTS.md` security rule 5 / SPEC 12.2** strictly for the new `/render/{project}/{id}/attachments/{attachment_id}` route; email HTML remains permanently sandboxed without `allow-scripts` or `allow-same-origin` on `GET /render/{project}/{id}`.
- **Defense in depth**: `nosniff`, byte-0 `%PDF-` validation, and `default-src 'none'` (no `script-src`) ensure a forged HTML/SVG payload can never execute script in the app origin even if a browser mishandles the MIME type.
- **Residual risk**: Inline rendering delegates PDF parsing to the browser's built-in PDF viewer (PDFium/PDF.js/PDFKit). Although browser PDF AcroForm engines are isolated from the web DOM (`window.parent`, cookies, and origin storage), high-security deployments that require strict download-only handling can set `MAILCUBBY_RENDER_INLINE_PDF=false`.
- **Mobile fallback**: Mobile browsers that do not render PDFs inside iframes are given "Open in new tab" and "Download" actions directly in the preview modal header and footer.
