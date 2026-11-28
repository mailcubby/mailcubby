import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LinksTab, type LinksTabProps } from "@/features/message/LinksTab";
import {
  canOpenLinkInNewTab,
  formatCheckAnnouncement,
  formatDurationMs,
  getLinkCheckStatusMeta,
  getLinkSourceLabel,
  getLinkWarningMeta,
  isSafeHttpUrl,
  summarizeCheckResults,
} from "@/features/message/links";
import {
  ApiError,
  api,
  type LinkCheckResponse,
  type LinkCheckResult,
  type MessageLinksResponse,
} from "@/lib/api";
import { messageLinksQueryOptions } from "@/lib/queries";

function makeSampleLinksResponse(overrides?: Partial<MessageLinksResponse>): MessageLinksResponse {
  return {
    total: 5,
    checkable_count: 2,
    truncated: false,
    links: [
      {
        url: "https://example.test/reset?token=abc",
        text: "Reset password",
        sources: ["html", "text"],
        count: 2,
        scheme: "https",
        checkable: true,
        warnings: [],
      },
      {
        url: "http://127.0.0.1/internal",
        text: "Internal status",
        sources: ["html"],
        count: 1,
        scheme: "http",
        checkable: true,
        warnings: ["http_insecure"],
      },
      {
        url: "mailto:support@example.test",
        text: "Contact support",
        sources: ["header"],
        count: 1,
        scheme: "mailto",
        checkable: false,
        warnings: [],
      },
      {
        url: "javascript:alert(1)",
        text: "Click me",
        sources: ["html"],
        count: 1,
        scheme: "javascript",
        checkable: false,
        warnings: ["javascript_url"],
      },
      {
        url: "",
        text: "Empty link",
        sources: ["html"],
        count: 1,
        scheme: "",
        checkable: false,
        warnings: ["missing_href"],
      },
    ],
    ...overrides,
  };
}

function makeCheckResult(overrides: Partial<LinkCheckResult> & { url: string }): LinkCheckResult {
  return {
    status: "ok",
    status_code: 200,
    final_url: overrides.url,
    redirect_count: 0,
    duration_ms: 42,
    cached: false,
    error_code: null,
    error: null,
    ...overrides,
  };
}

function renderLinksTab(
  linksResponse: MessageLinksResponse,
  props?: Partial<Omit<LinksTabProps, "projectSlug" | "messageId">>,
) {
  const getSpy = vi.spyOn(api, "GET").mockResolvedValue({
    data: linksResponse,
    response: new Response(JSON.stringify(linksResponse), { status: 200 }),
  } as Awaited<ReturnType<typeof api.GET>>);

  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });

  const mergedProps: LinksTabProps = {
    projectSlug: "shop",
    messageId: "01MSG",
    effectiveRole: "owner",
    linkCheckEnabled: true,
    mode: "local",
    ...props,
  };

  const view = render(
    <QueryClientProvider client={client}>
      <LinksTab {...mergedProps} />
    </QueryClientProvider>,
  );

  return { ...view, getSpy, client };
}

describe("links helpers (FR-120)", () => {
  it("validates safe http(s) URLs strictly and rejects unsafe, credential-bearing, or scheme-relative bypasses", () => {
    expect(isSafeHttpUrl("https://example.test/path?q=1")).toBe(true);
    expect(isSafeHttpUrl("http://example.test")).toBe(true);
    expect(isSafeHttpUrl("javascript:alert(1)")).toBe(false);
    expect(isSafeHttpUrl("data:text/html,<script>alert(1)</script>")).toBe(false);
    expect(isSafeHttpUrl("mailto:user@example.test")).toBe(false);
    expect(isSafeHttpUrl("tel:+351123456789")).toBe(false);
    expect(isSafeHttpUrl("")).toBe(false);
    expect(isSafeHttpUrl("/relative/path")).toBe(false);
    // Reject scheme-relative-without-//, control characters, backslashes, and embedded credentials.
    expect(isSafeHttpUrl("https:/api/v1/meta")).toBe(false);
    expect(isSafeHttpUrl("http:foo")).toBe(false);
    expect(isSafeHttpUrl("https:///path")).toBe(false);
    expect(isSafeHttpUrl("https://\\evil.test")).toBe(false);
    expect(isSafeHttpUrl("ht\ttps://example.test")).toBe(false);
    expect(isSafeHttpUrl(" https://example.test")).toBe(false);
    expect(isSafeHttpUrl("https://bank.com@evil.test/login")).toBe(false);
    expect(isSafeHttpUrl("http://user:pass@example.test")).toBe(false);

    expect(canOpenLinkInNewTab({ url: "https://example.test", scheme: "https" })).toBe(true);
    expect(canOpenLinkInNewTab({ url: "http://example.test", scheme: "http" })).toBe(true);
    expect(canOpenLinkInNewTab({ url: "https:/api/v1/meta", scheme: "https" })).toBe(false);
    expect(canOpenLinkInNewTab({ url: "https://user:pass@example.test", scheme: "https" })).toBe(
      false,
    );
    expect(canOpenLinkInNewTab({ url: "mailto:a@b.test", scheme: "mailto" })).toBe(false);
    expect(canOpenLinkInNewTab({ url: "javascript:alert(1)", scheme: "javascript" })).toBe(false);
    expect(canOpenLinkInNewTab({ url: "https://example.test", scheme: "mailto" })).toBe(false);
  });

  it("maps source labels, static warnings, durations, and every check status semantically", () => {
    expect(getLinkSourceLabel("html")).toBe("HTML");
    expect(getLinkSourceLabel("text")).toBe("Text");
    expect(getLinkSourceLabel("header")).toBe("Header");

    expect(getLinkWarningMeta("javascript_url").variant).toBe("destructive");
    expect(getLinkWarningMeta("data_url").variant).toBe("destructive");
    expect(getLinkWarningMeta("credentials_in_url").variant).toBe("destructive");
    expect(getLinkWarningMeta("http_insecure").variant).toBe("warning");
    expect(getLinkWarningMeta("missing_href").variant).toBe("warning");

    expect(
      getLinkCheckStatusMeta(makeCheckResult({ url: "https://a.test", status: "ok" })),
    ).toEqual({ label: "200 OK", variant: "success", detail: null });

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "https://a.test/old",
          status: "redirect",
          status_code: 200,
          redirect_count: 2,
          final_url: "https://a.test/new",
        }),
      ),
    ).toEqual({
      label: "200 (2 redirects)",
      variant: "info",
      detail: "https://a.test/new",
    });

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "https://a.test/missing",
          status: "client_error",
          status_code: 404,
        }),
      ),
    ).toEqual({ label: "404 Not Found", variant: "destructive", detail: null });

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "https://a.test/boom",
          status: "server_error",
          status_code: 503,
        }),
      ),
    ).toEqual({ label: "503 Unavailable", variant: "destructive", detail: null });

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "http://127.0.0.1/secret",
          status: "blocked",
          status_code: null,
          final_url: null,
          error_code: "blocked_ip",
          error: "target IP address is blocked",
        }),
      ),
    ).toEqual({
      label: "Blocked (private IP)",
      variant: "warning",
      detail: "target IP address is blocked",
    });

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "http://localhost/admin",
          status: "blocked",
          status_code: null,
          final_url: null,
          error_code: "blocked_host",
          error: "local hostname is blocked",
        }),
      ).label,
    ).toBe("Blocked (local host)");

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "https://slow.test",
          status: "timeout",
          status_code: null,
          final_url: null,
          error_code: "timeout",
          error: "request timed out",
        }),
      ),
    ).toEqual({ label: "Timeout", variant: "warning", detail: "request timed out" });

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "https://nx.test",
          status: "dns_error",
          status_code: null,
          final_url: null,
          error_code: "dns_error",
          error: "DNS lookup failed",
        }),
      ),
    ).toEqual({ label: "DNS error", variant: "destructive", detail: "DNS lookup failed" });

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "https://badtls.test",
          status: "tls_error",
          status_code: null,
          final_url: null,
          error_code: "tls_error",
          error: "TLS handshake failed",
        }),
      ),
    ).toEqual({ label: "TLS error", variant: "destructive", detail: "TLS handshake failed" });

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "https://loop.test",
          status: "too_many_redirects",
          status_code: null,
          final_url: null,
          error_code: "too_many_redirects",
          error: "too many redirects (max 5)",
        }),
      ).label,
    ).toBe("Too many redirects");

    expect(
      getLinkCheckStatusMeta(
        makeCheckResult({
          url: "https://extra.test",
          status: "skipped_limit",
          status_code: null,
          final_url: null,
          error_code: "skipped_limit",
          error: "exceeded maximum links per check",
        }),
      ).label,
    ).toBe("Skipped (limit)");

    expect(formatDurationMs(0)).toBe("< 1 ms");
    expect(formatDurationMs(85)).toBe("85 ms");
    expect(formatDurationMs(1250)).toMatch(/^1[.,]3 s$/);
  });

  it("summarizes results and formats polite live announcements", () => {
    const results: LinkCheckResult[] = [
      makeCheckResult({ url: "https://ok.test", status: "ok", status_code: 200 }),
      makeCheckResult({
        url: "https://redir.test",
        status: "redirect",
        status_code: 200,
        redirect_count: 1,
        final_url: "https://redir.test/final",
      }),
      makeCheckResult({
        url: "https://broken.test",
        status: "client_error",
        status_code: 404,
      }),
      makeCheckResult({
        url: "http://127.0.0.1/",
        status: "blocked",
        status_code: null,
        final_url: null,
        error_code: "blocked_ip",
        error: "target IP address is blocked",
      }),
      makeCheckResult({
        url: "https://err.test",
        status: "dns_error",
        status_code: null,
        final_url: null,
        error_code: "dns_error",
        error: "DNS lookup failed",
      }),
    ];

    expect(summarizeCheckResults(results)).toEqual({
      total: 5,
      ok: 1,
      redirect: 1,
      broken: 1,
      blocked: 1,
      error: 1,
      skipped: 0,
    });

    expect(formatCheckAnnouncement(results, "all")).toBe(
      "Checked 5 links: 1 OK, 1 redirect, 1 broken, 1 blocked, 1 error.",
    );
    expect(formatCheckAnnouncement([results[3] as LinkCheckResult], "single")).toBe(
      "Link check complete: Blocked (private IP).",
    );
  });

  it("skips retries for 4xx responses and retries once for 5xx in query options", () => {
    const opts = messageLinksQueryOptions("shop", "01MSG");
    expect(opts.staleTime).toBe(Number.POSITIVE_INFINITY);
    const retry = opts.retry as (count: number, err: unknown) => boolean;
    expect(retry(0, new ApiError(403, "forbidden", "Forbidden"))).toBe(false);
    expect(retry(0, new ApiError(404, "not_found", "Not found"))).toBe(false);
    expect(retry(0, new ApiError(500, "internal", "Internal"))).toBe(true);
    expect(retry(1, new ApiError(500, "internal", "Internal"))).toBe(false);
  });
});

describe("LinksTab (FR-120)", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders offline extracted links with source badges, static warnings, copy, and safe open links", async () => {
    renderLinksTab(makeSampleLinksResponse());

    await waitFor(() => {
      expect(screen.getByText("https://example.test/reset?token=abc")).toBeTruthy();
    });

    expect(screen.getByText("5 links · 2 checkable")).toBeTruthy();
    expect(screen.getByText("Reset password")).toBeTruthy();
    expect(screen.getByText("(missing href)")).toBeTruthy();

    // Source badges.
    expect(screen.getAllByText("HTML").length).toBeGreaterThanOrEqual(4);
    expect(screen.getByText("Text")).toBeTruthy();
    expect(screen.getByText("Header")).toBeTruthy();

    // Static warnings.
    expect(screen.getByText("Insecure HTTP")).toBeTruthy();
    expect(screen.getByText("javascript: URL")).toBeTruthy();
    expect(screen.getByText("Missing href")).toBeTruthy();

    // Single-use token caution note is shown when active checking is enabled.
    expect(screen.getByText(/single-use links/i)).toBeTruthy();

    // Safe http(s) links open in new tab with rel="noopener noreferrer".
    const safeLink = screen.getByRole("link", {
      name: "Open https://example.test/reset?token=abc in new tab",
    });
    expect(safeLink.getAttribute("href")).toBe("https://example.test/reset?token=abc");
    expect(safeLink.getAttribute("target")).toBe("_blank");
    expect(safeLink.getAttribute("rel")).toBe("noopener noreferrer");

    // Non-http(s) links render a disabled button and NEVER an <a> element.
    expect(screen.queryByRole("link", { name: /javascript:alert/ })).toBeNull();
    expect(screen.queryByRole("link", { name: /mailto:support/ })).toBeNull();
    const disabledJsOpen = screen.getByRole("button", {
      name: "Open in new tab unavailable for javascript link",
    });
    expect((disabledJsOpen as HTMLButtonElement).disabled).toBe(true);
    const disabledMailtoOpen = screen.getByRole("button", {
      name: "Open in new tab unavailable for mailto link",
    });
    expect((disabledMailtoOpen as HTMLButtonElement).disabled).toBe(true);
    const disabledCopyMissing = screen.getByRole("button", { name: "Copy URL unavailable" });
    expect((disabledCopyMissing as HTMLButtonElement).disabled).toBe(true);
  });

  it("checks all links and individual rows via POST /links/check and updates aria-live summary", async () => {
    const checkAllResp: LinkCheckResponse = {
      checked_at: "2026-11-28T11:00:00.000Z",
      summary: { total: 2, ok: 1, redirect: 0, broken: 0, blocked: 1, error: 0, skipped: 0 },
      results: [
        makeCheckResult({
          url: "https://example.test/reset?token=abc",
          status: "ok",
          status_code: 200,
          duration_ms: 35,
        }),
        makeCheckResult({
          url: "http://127.0.0.1/internal",
          status: "blocked",
          status_code: null,
          final_url: null,
          duration_ms: 2,
          error_code: "blocked_ip",
          error: "target IP address is blocked",
        }),
      ],
    };

    const singleRecheckResp: LinkCheckResponse = {
      checked_at: "2026-11-28T11:01:00.000Z",
      summary: { total: 1, ok: 0, redirect: 1, broken: 0, blocked: 0, error: 0, skipped: 0 },
      results: [
        makeCheckResult({
          url: "https://example.test/reset?token=abc",
          status: "redirect",
          status_code: 200,
          redirect_count: 1,
          final_url: "https://example.test/login",
          duration_ms: 60,
          cached: true,
        }),
      ],
    };

    const postSpy = vi
      .spyOn(api, "POST")
      .mockResolvedValueOnce({
        data: checkAllResp,
        response: new Response(JSON.stringify(checkAllResp), { status: 200 }),
      } as Awaited<ReturnType<typeof api.POST>>)
      .mockResolvedValueOnce({
        data: singleRecheckResp,
        response: new Response(JSON.stringify(singleRecheckResp), { status: 200 }),
      } as Awaited<ReturnType<typeof api.POST>>);

    const { container } = renderLinksTab(makeSampleLinksResponse());

    const checkAllBtn = await screen.findByRole("button", { name: "Check all links" });
    fireEvent.click(checkAllBtn);

    await waitFor(() => {
      expect(screen.getByText("200 OK")).toBeTruthy();
      expect(screen.getByText("Blocked (private IP)")).toBeTruthy();
    });

    // Check-all POST omits urls filter.
    expect(postSpy).toHaveBeenNthCalledWith(
      1,
      "/api/v1/projects/{project}/messages/{id}/links/check",
      {
        params: { path: { project: "shop", id: "01MSG" } },
      },
    );

    const liveRegion = container.querySelector('[aria-live="polite"]');
    expect(liveRegion?.textContent).toBe("Checked 2 links: 1 OK, 1 blocked.");

    // Per-row recheck uses matching "Recheck <url>" accessible name (WCAG 2.5.3),
    // sends only that URL, and preserves the blocked result on the other row.
    const rowCheckBtn = screen.getByRole("button", {
      name: "Recheck https://example.test/reset?token=abc",
    });
    expect(rowCheckBtn.textContent).toBe("Recheck");
    fireEvent.click(rowCheckBtn);

    await waitFor(() => {
      expect(screen.getByText("200 (1 redirect)")).toBeTruthy();
      expect(screen.getByText("→ https://example.test/login")).toBeTruthy();
    });

    expect(postSpy).toHaveBeenNthCalledWith(
      2,
      "/api/v1/projects/{project}/messages/{id}/links/check",
      {
        params: { path: { project: "shop", id: "01MSG" } },
        body: { urls: ["https://example.test/reset?token=abc"] },
      },
    );
    expect(screen.getByText("Blocked (private IP)")).toBeTruthy();
    expect(liveRegion?.textContent).toBe("Link check complete: 200 (1 redirect).");
  });

  it("shows quiet server info banner and hides check buttons when disabled in server mode", async () => {
    renderLinksTab(makeSampleLinksResponse(), {
      effectiveRole: "member",
      linkCheckEnabled: false,
      mode: "server",
    });

    await waitFor(() => {
      expect(screen.getByText("https://example.test/reset?token=abc")).toBeTruthy();
    });

    const banner = screen.getByRole("status");
    expect(banner.textContent).toContain("Active link checking is disabled on this server");
    expect(banner.textContent).toContain("MAILCUBBY_LINK_CHECK_ENABLED=true");

    expect(screen.queryByRole("button", { name: "Check all links" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Check https?:/ })).toBeNull();
    expect(screen.queryByText(/single-use links/i)).toBeNull();
  });

  it("hides active check controls and server config banner for viewers even when link checking is enabled", async () => {
    renderLinksTab(makeSampleLinksResponse(), {
      effectiveRole: "viewer",
      linkCheckEnabled: true,
      mode: "server",
    });

    await waitFor(() => {
      expect(screen.getByText("https://example.test/reset?token=abc")).toBeTruthy();
    });

    expect(screen.queryByRole("button", { name: "Check all links" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Check https?:/ })).toBeNull();
    expect(screen.queryByText(/single-use links/i)).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();

    // Viewers still have offline table columns (no Status column) and copy/open actions.
    const table = screen.getByRole("table");
    expect(within(table).queryByRole("columnheader", { name: "Status" })).toBeNull();
    expect(
      screen.getByRole("link", {
        name: "Open https://example.test/reset?token=abc in new tab",
      }),
    ).toBeTruthy();
  });

  it("renders EmptyState for messages with zero links and truncation notice when truncated", async () => {
    renderLinksTab({
      links: [],
      total: 0,
      checkable_count: 0,
      truncated: false,
    });

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "No links found" })).toBeTruthy();
    });
    expect(screen.queryByRole("button", { name: "Check all links" })).toBeNull();

    cleanup();
    vi.restoreAllMocks();

    renderLinksTab(makeSampleLinksResponse({ truncated: true }));
    await waitFor(() => {
      expect(screen.getByRole("status").textContent).toContain(
        "exceeded the offline link extraction limit",
      );
    });
  });

  it("avoids prototype-property collisions, disables opening credential URLs, and fails closed when meta is undefined", async () => {
    renderLinksTab(
      {
        total: 3,
        checkable_count: 0,
        truncated: false,
        links: [
          {
            url: "toString",
            text: "Relative prototype name",
            sources: ["html"],
            count: 1,
            scheme: "",
            checkable: false,
            warnings: [],
          },
          {
            url: "constructor",
            text: "Another prototype name",
            sources: ["html"],
            count: 1,
            scheme: "",
            checkable: false,
            warnings: [],
          },
          {
            url: "https://user:pass@example.test/login",
            text: "Credentialed link",
            sources: ["html"],
            count: 1,
            scheme: "https",
            checkable: false,
            warnings: ["credentials_in_url"],
          },
        ],
      },
      {
        effectiveRole: "owner",
        linkCheckEnabled: undefined,
        mode: undefined,
      },
    );

    await waitFor(() => {
      expect(screen.getByText("toString")).toBeTruthy();
    });

    // Prototype property names must never produce spurious check results.
    expect(screen.queryByText("Network error")).toBeNull();
    // Undefined meta fails closed (no active check controls) without flashing the disabled banner.
    expect(screen.queryByRole("button", { name: "Check all links" })).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();
    // Credential-bearing URL cannot be opened in a new tab.
    expect(screen.queryByRole("link", { name: /user:pass/ })).toBeNull();
    const disabledCredOpen = screen.getByRole("button", {
      name: "Open in new tab unavailable for link with embedded credentials",
    });
    expect((disabledCredOpen as HTMLButtonElement).disabled).toBe(true);
  });

  it("renders error EmptyState with Retry when GET /links fails", async () => {
    const getSpy = vi.spyOn(api, "GET").mockResolvedValue({
      error: { error: { code: "internal", message: "Blob read failed" } },
      response: new Response(null, { status: 500, statusText: "Internal Server Error" }),
    } as Awaited<ReturnType<typeof api.GET>>);

    const client = new QueryClient({
      defaultOptions: { queries: { retryDelay: 0 } },
    });

    render(
      <QueryClientProvider client={client}>
        <LinksTab
          projectSlug="shop"
          messageId="01ERR"
          effectiveRole="owner"
          linkCheckEnabled={true}
          mode="local"
        />
      </QueryClientProvider>,
    );

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "Could not load links" })).toBeTruthy();
    });
    expect(screen.getByText("Blob read failed")).toBeTruthy();
    const callsBefore = getSpy.mock.calls.length;

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => {
      expect(getSpy.mock.calls.length).toBeGreaterThan(callsBefore);
    });
  });
});
