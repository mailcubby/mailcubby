import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { HtmlCheckTab } from "@/features/message/HtmlCheckTab";
import {
  buildFilteredHtmlCheckView,
  formatPercent,
  isSafeExternalUrl,
} from "@/features/message/htmlCheck";
import { ApiError, api, type HTMLCheckClient, type HTMLCheckReport } from "@/lib/api";
import { messageHtmlCheckQueryOptions } from "@/lib/queries";

const SAMPLE_CLIENTS: HTMLCheckClient[] = [
  {
    id: "gmail.desktop-webmail",
    family: "gmail",
    family_name: "Gmail",
    name: "Gmail (Desktop Webmail)",
    platform: "Desktop Webmail",
    weight: 10,
  },
  {
    id: "outlook.windows",
    family: "outlook",
    family_name: "Outlook",
    name: "Outlook Classic (Windows)",
    platform: "Windows (Classic)",
    weight: 8,
  },
  {
    id: "outlook.outlook-com",
    family: "outlook",
    family_name: "Outlook",
    name: "Outlook.com / New Outlook",
    platform: "Web / New Windows",
    weight: 8,
  },
  {
    id: "apple-mail.macos",
    family: "apple-mail",
    family_name: "Apple Mail",
    name: "Apple Mail (macOS)",
    platform: "macOS",
    weight: 8,
  },
  {
    id: "yahoo.desktop-webmail",
    family: "yahoo",
    family_name: "Yahoo! Mail",
    name: "Yahoo! Mail (Desktop Webmail)",
    platform: "Desktop Webmail",
    weight: 5,
  },
  {
    id: "thunderbird.macos",
    family: "other",
    family_name: "Other",
    name: "Mozilla Thunderbird",
    platform: "Desktop",
    weight: 3,
  },
];

function makeSampleReport(overrides?: Partial<HTMLCheckReport>): HTMLCheckReport {
  return {
    score: 76.2,
    summary: {
      supported_pct: 66.7,
      partial_pct: 19.0,
      unsupported_pct: 14.3,
      supported: 8,
      partial: 2,
      unsupported: 2,
      unknown: 0,
      features_count: 2,
    },
    dataset: {
      source: "caniemail.com",
      license: "MIT",
      api_version: "1.2",
      last_update_date: "2026-11-27",
    },
    truncated: false,
    clients: SAMPLE_CLIENTS,
    features: [
      {
        slug: "css-display-flex",
        title: "display: flex",
        category: "css",
        url: "https://www.caniemail.com/features/css-display-flex/",
        count: 3,
        summary: { supported: 3, partial: 1, unsupported: 2, unknown: 0 },
        support: [
          {
            client_id: "gmail.desktop-webmail",
            status: "partial",
            notes: ["Only basic flex properties are supported in Gmail webmail."],
          },
          {
            client_id: "outlook.windows",
            status: "unsupported",
            notes: ["Word rendering engine ignores flexbox layouts."],
          },
          { client_id: "outlook.outlook-com", status: "supported" },
          { client_id: "apple-mail.macos", status: "supported" },
          { client_id: "yahoo.desktop-webmail", status: "unsupported" },
          { client_id: "thunderbird.macos", status: "supported" },
        ],
      },
      {
        slug: "html-table",
        title: "<table> element",
        category: "html",
        url: "https://www.caniemail.com/features/html-table/",
        count: 5,
        summary: { supported: 6, partial: 0, unsupported: 0, unknown: 0 },
        support: SAMPLE_CLIENTS.map((c) => ({ client_id: c.id, status: "supported" })),
      },
    ],
    ...overrides,
  };
}

function renderWithQuery(report: HTMLCheckReport) {
  const getSpy = vi.spyOn(api, "GET").mockResolvedValue({
    data: report,
    response: new Response(JSON.stringify(report), { status: 200 }),
  } as Awaited<ReturnType<typeof api.GET>>);

  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });

  const view = render(
    <QueryClientProvider client={client}>
      <HtmlCheckTab projectSlug="shop" messageId="01MSG" />
    </QueryClientProvider>,
  );

  return { ...view, getSpy };
}

describe("htmlCheck helpers", () => {
  it("recomputes weighted summary and issue lists per client family", () => {
    const report = makeSampleReport();

    const outlookView = buildFilteredHtmlCheckView(report, "outlook", true);
    expect(outlookView.clients.map((c) => c.id)).toEqual([
      "outlook.windows",
      "outlook.outlook-com",
    ]);
    // 2 features x 2 outlook clients (equal weight 8): 3 supported, 1 unsupported -> 75% supported, 25% unsupported
    expect(outlookView.summary.supported_pct).toBe(75);
    expect(outlookView.summary.partial_pct).toBe(0);
    expect(outlookView.summary.unsupported_pct).toBe(25);
    expect(outlookView.score).toBe(75);
    expect(outlookView.visibleFeatures.map((f) => f.feature.slug)).toEqual(["css-display-flex"]);

    // Apple Mail supports every feature in this report -> 0 issues when onlyIssues is true.
    const appleView = buildFilteredHtmlCheckView(report, "apple-mail", true);
    expect(appleView.score).toBe(100);
    expect(appleView.summary.supported_pct).toBe(100);
    expect(appleView.visibleFeatures).toHaveLength(0);

    // Turning off onlyIssues reveals all detected features.
    const appleAll = buildFilteredHtmlCheckView(report, "apple-mail", false);
    expect(appleAll.visibleFeatures).toHaveLength(2);
  });

  it("handles unequal weights, partial 0.5 weight, unknown exclusion, rounding sum=100, and ASCII slug tie-break", () => {
    const clients: HTMLCheckClient[] = [
      {
        id: "gmail.a",
        family: "gmail",
        family_name: "Gmail",
        name: "Gmail A",
        platform: "Web",
        weight: 1,
      },
      {
        id: "gmail.b",
        family: "gmail",
        family_name: "Gmail",
        name: "Gmail B",
        platform: "iOS",
        weight: 1,
      },
      {
        id: "gmail.c",
        family: "gmail",
        family_name: "Gmail",
        name: "Gmail C",
        platform: "Android",
        weight: 1,
      },
      {
        id: "gmail.u",
        family: "gmail",
        family_name: "Gmail",
        name: "Gmail Unknown",
        platform: "Mobile Web",
        weight: 99,
      },
    ];
    const report = makeSampleReport({
      clients,
      features: [
        {
          slug: "css-aa",
          title: "CSS AA",
          category: "css",
          url: "https://www.caniemail.com/features/css-aa/",
          count: 1,
          summary: { supported: 1, partial: 1, unsupported: 1, unknown: 1 },
          support: [
            { client_id: "gmail.a", status: "supported" },
            { client_id: "gmail.b", status: "partial" },
            { client_id: "gmail.c", status: "unsupported" },
            { client_id: "gmail.u", status: "unknown" },
          ],
        },
        {
          slug: "css-a-b",
          title: "CSS A-B",
          category: "css",
          url: "https://www.caniemail.com/features/css-a-b/",
          count: 1,
          summary: { supported: 1, partial: 1, unsupported: 1, unknown: 1 },
          support: [
            { client_id: "gmail.a", status: "supported" },
            { client_id: "gmail.b", status: "partial" },
            { client_id: "gmail.c", status: "unsupported" },
            { client_id: "gmail.u", status: "unknown" },
          ],
        },
      ],
    });

    const view = buildFilteredHtmlCheckView(report, "gmail", true);
    // Equal 1/3 split rounds to 33.3 each -> diff +0.1 applied to first largest bucket (supported_pct = 33.4).
    expect(view.summary.supported_pct).toBe(33.4);
    expect(view.summary.partial_pct).toBe(33.3);
    expect(view.summary.unsupported_pct).toBe(33.3);
    expect(
      Math.round(
        (view.summary.supported_pct + view.summary.partial_pct + view.summary.unsupported_pct) * 10,
      ) / 10,
    ).toBe(100);
    // Unknown client (weight 99) excluded; score = (1 + 0.5*1) / 3 * 100 = 50%.
    expect(view.score).toBe(50);
    // ASCII byte order matches Go score.go: '-' (45) < 'a' (97), so "css-a-b" precedes "css-aa".
    expect(view.visibleFeatures.map((f) => f.feature.slug)).toEqual(["css-a-b", "css-aa"]);
  });

  it("validates external URLs and formats percentages cleanly", () => {
    expect(isSafeExternalUrl("https://www.caniemail.com/features/css-grid/")).toBe(true);
    expect(isSafeExternalUrl("http://example.test/info")).toBe(true);
    expect(isSafeExternalUrl("javascript:alert(1)")).toBe(false);
    expect(isSafeExternalUrl("data:text/html,hi")).toBe(false);
    expect(isSafeExternalUrl("not a url")).toBe(false);
    expect(formatPercent(100)).toBe("100%");
    expect(formatPercent(84.5)).toMatch(/^84[.,]5%$/);
  });

  it("skips query retries for 4xx responses and retries once for 5xx", () => {
    const opts = messageHtmlCheckQueryOptions("shop", "01MSG");
    expect(opts.staleTime).toBe(Number.POSITIVE_INFINITY);
    const retry = opts.retry as (count: number, err: unknown) => boolean;
    expect(retry(0, new ApiError(401, "unauthorized", "Unauthorized"))).toBe(false);
    expect(retry(0, new ApiError(403, "forbidden", "Forbidden"))).toBe(false);
    expect(retry(0, new ApiError(404, "not_found", "Not found"))).toBe(false);
    expect(retry(0, new ApiError(429, "rate_limited", "Too many requests"))).toBe(false);
    expect(retry(0, new ApiError(500, "internal", "Internal"))).toBe(true);
    expect(retry(1, new ApiError(500, "internal", "Internal"))).toBe(false);
  });
});

describe("HtmlCheckTab (FR-079)", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows summary bar, defaults to Only show issues, toggles all features, and filters by client family", async () => {
    renderWithQuery(makeSampleReport());

    await waitFor(() => {
      expect(screen.getByText(formatPercent(76.2))).toBeTruthy();
    });

    // Score summary legend includes text + percentages.
    expect(screen.getByText(`Supported ${formatPercent(66.7)}`)).toBeTruthy();
    expect(screen.getByText(`Partial ${formatPercent(19)}`)).toBeTruthy();
    expect(screen.getByText(`Unsupported ${formatPercent(14.3)}`)).toBeTruthy();

    // Only show issues defaults to ON: flexbox is visible, fully supported <table> is hidden.
    const onlyIssuesSwitch = screen.getByRole("switch", { name: "Only show issues" });
    expect((onlyIssuesSwitch as HTMLInputElement).checked).toBe(true);
    expect(screen.getByRole("button", { name: /display: flex/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /<table> element/ })).toBeNull();

    // Turning off "Only show issues" reveals <table> as well.
    fireEvent.click(onlyIssuesSwitch);
    expect((onlyIssuesSwitch as HTMLInputElement).checked).toBe(false);
    expect(screen.getByRole("button", { name: /<table> element/ })).toBeTruthy();

    // Turn "Only show issues" back on, then filter to Apple Mail -> EmptyState!
    fireEvent.click(onlyIssuesSwitch);
    const appleBtn = screen.getByRole("button", { name: "Apple Mail" });
    fireEvent.click(appleBtn);
    expect(appleBtn.getAttribute("aria-pressed")).toBe("true");
    expect(
      screen.getByRole("heading", { name: "All detected features are supported in Apple Mail" }),
    ).toBeTruthy();
    expect(screen.getByText("Supported 100%")).toBeTruthy();

    // Clicking "Show all 2 features" inside the EmptyState turns off the switch and reveals rows.
    fireEvent.click(screen.getByRole("button", { name: "Show all 2 features" }));
    expect((onlyIssuesSwitch as HTMLInputElement).checked).toBe(false);
    expect(screen.getByRole("button", { name: /display: flex/ })).toBeTruthy();

    // Filter to Outlook and confirm Outlook-specific score and clients.
    const outlookBtn = screen.getByRole("button", { name: "Outlook" });
    fireEvent.click(outlookBtn);
    expect(screen.getByText("Supported 75%")).toBeTruthy();
    expect(screen.getByText("Unsupported 25%")).toBeTruthy();
  });

  it("expands and collapses disclosure rows with per-client support, notes, and safe caniemail link", async () => {
    renderWithQuery(makeSampleReport());

    const rowBtn = await screen.findByRole("button", { name: /display: flex/ });
    expect(rowBtn.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText("Word rendering engine ignores flexbox layouts.")).toBeNull();

    // Expand row.
    fireEvent.click(rowBtn);
    expect(rowBtn.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByText("Outlook Classic (Windows)")).toBeTruthy();
    expect(screen.getByText("Outlook.com / New Outlook")).toBeTruthy();
    expect(screen.getByText("Word rendering engine ignores flexbox layouts.")).toBeTruthy();
    expect(
      screen.getByText("Only basic flex properties are supported in Gmail webmail."),
    ).toBeTruthy();

    const caniemailLink = screen.getByRole("link", { name: "View on caniemail.com" });
    expect(caniemailLink.getAttribute("href")).toBe(
      "https://www.caniemail.com/features/css-display-flex/",
    );
    expect(caniemailLink.getAttribute("rel")).toBe("noopener noreferrer");
    expect(caniemailLink.getAttribute("target")).toBe("_blank");

    // Filter to Gmail while expanded: only Gmail client appears in the grid.
    fireEvent.click(screen.getByRole("button", { name: "Gmail" }));
    expect(screen.getByText("Gmail (Desktop Webmail)")).toBeTruthy();
    expect(screen.queryByText("Outlook Classic (Windows)")).toBeNull();

    // Collapse row.
    fireEvent.click(rowBtn);
    expect(rowBtn.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText("Gmail (Desktop Webmail)")).toBeNull();

    // Expand all / Collapse all button toggles all visible disclosure rows.
    fireEvent.click(screen.getByRole("button", { name: "Expand all" }));
    expect(rowBtn.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "Collapse all" }));
    expect(rowBtn.getAttribute("aria-expanded")).toBe("false");
  });

  it("renders structured EmptyState when all detected features are supported and displays truncation banner when truncated", async () => {
    const allSupportedReport = makeSampleReport({
      score: 100,
      truncated: true,
      summary: {
        supported_pct: 100,
        partial_pct: 0,
        unsupported_pct: 0,
        supported: 6,
        partial: 0,
        unsupported: 0,
        unknown: 0,
        features_count: 1,
      },
      features: [
        {
          slug: "html-table",
          title: "<table> element",
          category: "html",
          url: "https://www.caniemail.com/features/html-table/",
          count: 2,
          summary: { supported: 6, partial: 0, unsupported: 0, unknown: 0 },
          support: SAMPLE_CLIENTS.map((c) => ({ client_id: c.id, status: "supported" })),
        },
      ],
    });

    renderWithQuery(allSupportedReport);

    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: "All detected features are supported" }),
      ).toBeTruthy();
    });
    expect(screen.getByRole("status").textContent).toContain("exceeded the offline HTML/CSS");
  });

  it("renders zero-feature EmptyState without Show all action, omits unsafe external URLs, and distinguishes unknown support", async () => {
    const emptyReport = makeSampleReport({
      score: 100,
      summary: {
        supported_pct: 100,
        partial_pct: 0,
        unsupported_pct: 0,
        supported: 0,
        partial: 0,
        unsupported: 0,
        unknown: 0,
        features_count: 0,
      },
      features: [],
    });

    renderWithQuery(emptyReport);
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: "All detected features are supported" }),
      ).toBeTruthy();
    });
    expect(screen.queryByRole("button", { name: /Show all/ })).toBeNull();
    cleanup();
    vi.restoreAllMocks();

    const unsafeAndUnknownReport = makeSampleReport({
      features: [
        {
          slug: "css-exotic",
          title: "Exotic CSS",
          category: "css",
          url: "javascript:alert(1)",
          count: 1,
          summary: { supported: 0, partial: 0, unsupported: 1, unknown: 5 },
          support: [
            { client_id: "gmail.desktop-webmail", status: "unsupported" },
            { client_id: "outlook.windows", status: "unknown" },
            { client_id: "outlook.outlook-com", status: "unknown" },
            { client_id: "apple-mail.macos", status: "unknown" },
            { client_id: "yahoo.desktop-webmail", status: "unknown" },
            { client_id: "thunderbird.macos", status: "unknown" },
          ],
        },
      ],
    });

    renderWithQuery(unsafeAndUnknownReport);
    const exoticBtn = await screen.findByRole("button", { name: /Exotic CSS/ });
    fireEvent.click(exoticBtn);
    expect(screen.queryByRole("link", { name: "View on caniemail.com" })).toBeNull();

    // Filtering to Yahoo where support is strictly unknown shows "No known compatibility issues in Yahoo".
    fireEvent.click(screen.getByRole("button", { name: "Yahoo" }));
    expect(
      screen.getByRole("heading", { name: "No known compatibility issues in Yahoo" }),
    ).toBeTruthy();
  });

  it("renders error EmptyState with Retry on request failure", async () => {
    const getSpy = vi.spyOn(api, "GET").mockResolvedValue({
      error: { error: { code: "internal", message: "Backend unavailable" } },
      response: new Response(null, { status: 500, statusText: "Internal Server Error" }),
    } as Awaited<ReturnType<typeof api.GET>>);

    const client = new QueryClient({
      defaultOptions: { queries: { retryDelay: 0 } },
    });

    render(
      <QueryClientProvider client={client}>
        <HtmlCheckTab projectSlug="shop" messageId="01ERR" />
      </QueryClientProvider>,
    );

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "Could not load HTML check" })).toBeTruthy();
    });
    expect(screen.getByText("Backend unavailable")).toBeTruthy();
    const callsBeforeRetry = getSpy.mock.calls.length;

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => {
      expect(getSpy.mock.calls.length).toBeGreaterThan(callsBeforeRetry);
    });
  });
});
