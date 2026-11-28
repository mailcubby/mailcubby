import type { BadgeVariant } from "@/components/ui/badge";
import type {
  HTMLCheckClient,
  HTMLCheckClientFamily,
  HTMLCheckClientSupport,
  HTMLCheckFeature,
  HTMLCheckFeatureCategory,
  HTMLCheckFeatureSummary,
  HTMLCheckReport,
  HTMLCheckSummary,
  HTMLCheckSupportStatus,
} from "@/lib/api";

export type ClientFamilyFilter = "all" | HTMLCheckClientFamily;

export interface ClientFamilyOption {
  value: ClientFamilyFilter;
  label: string;
}

export const CLIENT_FAMILY_OPTIONS: ReadonlyArray<ClientFamilyOption> = [
  { value: "all", label: "All" },
  { value: "gmail", label: "Gmail" },
  { value: "outlook", label: "Outlook" },
  { value: "apple-mail", label: "Apple Mail" },
  { value: "yahoo", label: "Yahoo" },
  { value: "other", label: "Other" },
];

export interface FilteredFeatureEntry {
  feature: HTMLCheckFeature;
  summary: HTMLCheckFeatureSummary;
  support: ReadonlyArray<{
    client: HTMLCheckClient;
    status: HTMLCheckSupportStatus;
    notes: ReadonlyArray<string>;
  }>;
  hasIssues: boolean;
}

export interface FilteredHtmlCheckView {
  clients: ReadonlyArray<HTMLCheckClient>;
  score: number;
  summary: HTMLCheckSummary;
  allFeatures: ReadonlyArray<FilteredFeatureEntry>;
  visibleFeatures: ReadonlyArray<FilteredFeatureEntry>;
  issueCount: number;
}

const pctFormatter = new Intl.NumberFormat(undefined, {
  maximumFractionDigits: 1,
});

function round1(value: number): number {
  return Math.round(value * 10) / 10;
}

export function formatPercent(value: number): string {
  return `${pctFormatter.format(value)}%`;
}

export function getClientFamilyLabel(family: ClientFamilyFilter): string {
  return CLIENT_FAMILY_OPTIONS.find((opt) => opt.value === family)?.label ?? "All";
}

const CATEGORY_LABELS: Record<HTMLCheckFeatureCategory, string> = {
  html: "HTML",
  css: "CSS",
  image: "Image",
  others: "Other",
};

const SUPPORT_STATUS_META: Record<
  HTMLCheckSupportStatus,
  { label: string; variant: BadgeVariant }
> = {
  supported: { label: "Supported", variant: "success" },
  partial: { label: "Partial", variant: "warning" },
  unsupported: { label: "Unsupported", variant: "destructive" },
  unknown: { label: "Unknown", variant: "neutral" },
};

export function getCategoryLabel(category: HTMLCheckFeatureCategory): string {
  return CATEGORY_LABELS[category] ?? "Other";
}

export function getSupportStatusMeta(status: HTMLCheckSupportStatus): {
  label: string;
  variant: BadgeVariant;
} {
  return SUPPORT_STATUS_META[status] ?? { label: "Unknown", variant: "neutral" };
}

export function isSafeExternalUrl(rawUrl: string): boolean {
  try {
    const parsed = new URL(rawUrl);
    return parsed.protocol === "https:" || parsed.protocol === "http:";
  } catch {
    return false;
  }
}

/**
 * Filters an HTMLCheckReport by email client family and "only show issues",
 * recomputing weighted compatibility percentages for the active client subset
 * using the same formula as internal/compat/score.go.
 */
export function buildFilteredHtmlCheckView(
  report: HTMLCheckReport,
  family: ClientFamilyFilter,
  onlyIssues: boolean,
): FilteredHtmlCheckView {
  const clients =
    family === "all" ? report.clients : report.clients.filter((c) => c.family === family);

  let totalSupported = 0;
  let totalPartial = 0;
  let totalUnsupported = 0;
  let totalUnknown = 0;

  let weightedSupported = 0;
  let weightedPartial = 0;
  let weightedUnsupported = 0;
  let totalWeight = 0;

  const allFeatures: FilteredFeatureEntry[] = report.features.map((feature) => {
    const supportByClientId = new Map<string, HTMLCheckClientSupport>();
    for (const item of feature.support) {
      supportByClientId.set(item.client_id, item);
    }

    let supported = 0;
    let partial = 0;
    let unsupported = 0;
    let unknown = 0;

    const support = clients.map((client) => {
      const entry = supportByClientId.get(client.id);
      const status: HTMLCheckSupportStatus = entry?.status ?? "unknown";
      const notes: ReadonlyArray<string> = entry?.notes ?? [];
      const weight = client.weight > 0 ? client.weight : 1;

      switch (status) {
        case "supported":
          supported += 1;
          totalSupported += 1;
          weightedSupported += weight;
          totalWeight += weight;
          break;
        case "partial":
          partial += 1;
          totalPartial += 1;
          weightedPartial += weight;
          totalWeight += weight;
          break;
        case "unsupported":
          unsupported += 1;
          totalUnsupported += 1;
          weightedUnsupported += weight;
          totalWeight += weight;
          break;
        default:
          unknown += 1;
          totalUnknown += 1;
          break;
      }

      return { client, status, notes };
    });

    const summary: HTMLCheckFeatureSummary = {
      supported,
      partial,
      unsupported,
      unknown,
    };

    return {
      feature,
      summary,
      support,
      hasIssues: unsupported > 0 || partial > 0,
    };
  });

  // Deterministic ordering matching backend score.go: most unsupported first,
  // then most partial, then slug alphabetically.
  allFeatures.sort((a, b) => {
    if (a.summary.unsupported !== b.summary.unsupported) {
      return b.summary.unsupported - a.summary.unsupported;
    }
    if (a.summary.partial !== b.summary.partial) {
      return b.summary.partial - a.summary.partial;
    }
    if (a.feature.slug < b.feature.slug) {
      return -1;
    }
    if (a.feature.slug > b.feature.slug) {
      return 1;
    }
    return 0;
  });

  let supportedPct = 100;
  let partialPct = 0;
  let unsupportedPct = 0;
  let score = 100;

  if (family === "all") {
    supportedPct = report.summary.supported_pct;
    partialPct = report.summary.partial_pct;
    unsupportedPct = report.summary.unsupported_pct;
    score = report.score;
  } else if (totalWeight > 0) {
    supportedPct = round1((weightedSupported / totalWeight) * 100);
    partialPct = round1((weightedPartial / totalWeight) * 100);
    unsupportedPct = round1((weightedUnsupported / totalWeight) * 100);
    const diff = round1(100 - (supportedPct + partialPct + unsupportedPct));
    if (diff !== 0) {
      if (supportedPct >= partialPct && supportedPct >= unsupportedPct) {
        supportedPct = round1(supportedPct + diff);
      } else if (partialPct >= unsupportedPct) {
        partialPct = round1(partialPct + diff);
      } else {
        unsupportedPct = round1(unsupportedPct + diff);
      }
    }
    score = round1(((weightedSupported + 0.5 * weightedPartial) / totalWeight) * 100);
  }

  const summary: HTMLCheckSummary =
    family === "all"
      ? report.summary
      : {
          supported_pct: supportedPct,
          partial_pct: partialPct,
          unsupported_pct: unsupportedPct,
          supported: totalSupported,
          partial: totalPartial,
          unsupported: totalUnsupported,
          unknown: totalUnknown,
          features_count: report.features.length,
        };

  const issueCount = allFeatures.reduce((acc, item) => acc + (item.hasIssues ? 1 : 0), 0);
  const visibleFeatures = onlyIssues ? allFeatures.filter((item) => item.hasIssues) : allFeatures;

  return {
    clients,
    score,
    summary,
    allFeatures,
    visibleFeatures,
    issueCount,
  };
}
