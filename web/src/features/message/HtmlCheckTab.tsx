import { useQuery } from "@tanstack/react-query";
import { AlertCircle, AlertTriangle, CheckCircle2, ChevronRight, ExternalLink } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { EmptyState } from "@/components/EmptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  buildFilteredHtmlCheckView,
  CLIENT_FAMILY_OPTIONS,
  type ClientFamilyFilter,
  type FilteredFeatureEntry,
  formatPercent,
  getCategoryLabel,
  getClientFamilyLabel,
  getSupportStatusMeta,
  isSafeExternalUrl,
} from "@/features/message/htmlCheck";
import { getApiErrorMessage } from "@/lib/api";
import { formatCount } from "@/lib/format";
import { messageHtmlCheckQueryOptions } from "@/lib/queries";
import { cn } from "@/lib/utils";

export interface HtmlCheckTabProps {
  projectSlug: string;
  messageId: string;
}

function FeatureRow({
  entry,
  expanded,
  onToggle,
  idPrefix,
}: {
  entry: FilteredFeatureEntry;
  expanded: boolean;
  onToggle: () => void;
  idPrefix: string;
}) {
  const { feature, summary, support } = entry;
  const panelId = `${idPrefix}-panel-${feature.slug}`;
  const hasSafeUrl = isSafeExternalUrl(feature.url);

  return (
    <li>
      <button
        type="button"
        aria-expanded={expanded}
        aria-controls={panelId}
        onClick={onToggle}
        className="flex w-full cursor-pointer items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/60 focus-visible:-outline-offset-2"
      >
        <div className="flex min-w-0 flex-1 items-center gap-2.5">
          <ChevronRight
            className={cn(
              "size-4 shrink-0 text-muted-foreground transition-transform duration-150 motion-reduce:transition-none",
              expanded && "rotate-90",
            )}
            aria-hidden="true"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-[13px] font-medium text-foreground">{feature.title}</span>
            <Badge variant="outline">{getCategoryLabel(feature.category)}</Badge>
            <span className="text-xs text-muted-foreground tabular-nums">
              {formatCount(feature.count)}×
              <span className="sr-only"> {feature.count === 1 ? "occurrence" : "occurrences"}</span>
            </span>
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
          {summary.unsupported > 0 ? (
            <Badge variant="destructive" dot>
              {formatCount(summary.unsupported)} unsupported
            </Badge>
          ) : null}
          {summary.partial > 0 ? (
            <Badge variant="warning" dot>
              {formatCount(summary.partial)} partial
            </Badge>
          ) : null}
          {summary.unsupported === 0 && summary.partial === 0 ? (
            <Badge variant={summary.supported > 0 ? "success" : "neutral"} dot>
              {summary.supported > 0 ? "Supported" : "Unknown"}
            </Badge>
          ) : null}
        </div>
      </button>

      {expanded ? (
        <div id={panelId} className="border-t border-border bg-background px-4 py-3">
          <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
            <span className="font-mono text-[11px] text-muted-foreground">{feature.slug}</span>
            {hasSafeUrl ? (
              <a
                href={feature.url}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 font-medium text-accent hover:underline"
              >
                <span>View on caniemail.com</span>
                <ExternalLink className="size-3" aria-hidden="true" />
              </a>
            ) : null}
          </div>

          {support.length === 0 ? (
            <p className="mt-2.5 text-xs text-muted-foreground">
              No tracked clients match this filter.
            </p>
          ) : (
            <ul className="mt-2.5 grid grid-cols-1 gap-2 sm:grid-cols-2">
              {support.map(({ client, status, notes }) => {
                const statusMeta = getSupportStatusMeta(status);
                const seenNotes = new Map<string, number>();
                const keyedNotes = notes.map((note) => {
                  const occ = (seenNotes.get(note) ?? 0) + 1;
                  seenNotes.set(note, occ);
                  return { key: `${client.id}-${note}-${occ}`, text: note };
                });
                return (
                  <li key={client.id} className="rounded-md border border-border bg-surface p-2.5">
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0">
                        <p className="truncate text-xs font-medium text-foreground">
                          {client.name}
                        </p>
                        <p className="truncate text-[11px] text-muted-foreground">
                          {client.platform}
                        </p>
                      </div>
                      <Badge variant={statusMeta.variant} dot className="shrink-0">
                        {statusMeta.label}
                      </Badge>
                    </div>
                    {keyedNotes.length > 0 ? (
                      <ul className="mt-2 space-y-1 border-t border-border/70 pt-1.5 text-[11.5px] leading-snug text-muted-foreground">
                        {keyedNotes.map((item) => (
                          <li key={item.key}>{item.text}</li>
                        ))}
                      </ul>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      ) : null}
    </li>
  );
}

export function HtmlCheckTab({ projectSlug, messageId }: HtmlCheckTabProps) {
  const [family, setFamily] = useState<ClientFamilyFilter>("all");
  const [onlyIssues, setOnlyIssues] = useState(true);
  const [expandedSlugs, setExpandedSlugs] = useState<ReadonlySet<string>>(() => new Set());

  const idPrefix = useId();
  const summaryHeadingId = `${idPrefix}-summary`;
  const featuresHeadingId = `${idPrefix}-features`;
  const onlyIssuesId = `${idPrefix}-only-issues`;

  const {
    data: report,
    isPending,
    error,
    refetch,
  } = useQuery(messageHtmlCheckQueryOptions(projectSlug, messageId));

  const view = useMemo(
    () => (report ? buildFilteredHtmlCheckView(report, family, onlyIssues) : null),
    [report, family, onlyIssues],
  );

  if (isPending) {
    return (
      <div aria-busy="true" className="h-full overflow-auto bg-background p-4">
        <div className="mx-auto flex max-w-3xl flex-col gap-4">
          <Skeleton className="h-28 w-full rounded-lg" />
          <Skeleton className="h-9 w-full rounded-md" />
          <Skeleton className="h-44 w-full rounded-lg" />
        </div>
      </div>
    );
  }

  if (error || !report || !view) {
    return (
      <div className="flex h-full items-center justify-center bg-background">
        <EmptyState
          icon={AlertCircle}
          headingLevel={3}
          title="Could not load HTML check"
          description={getApiErrorMessage(error)}
          action={
            <Button variant="outline" size="xs" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      </div>
    );
  }

  const familyLabel = getClientFamilyLabel(family);
  const visibleSlugs = view.visibleFeatures.map((entry) => entry.feature.slug);
  const allExpanded =
    visibleSlugs.length > 0 && visibleSlugs.every((slug) => expandedSlugs.has(slug));

  const toggleFeature = (slug: string) => {
    setExpandedSlugs((prev) => {
      const next = new Set(prev);
      if (next.has(slug)) {
        next.delete(slug);
      } else {
        next.add(slug);
      }
      return next;
    });
  };

  const toggleExpandAll = () => {
    if (allExpanded) {
      setExpandedSlugs(new Set());
      return;
    }
    setExpandedSlugs(new Set(visibleSlugs));
  };

  const supportedLabel = `Supported ${formatPercent(view.summary.supported_pct)}`;
  const partialLabel = `Partial ${formatPercent(view.summary.partial_pct)}`;
  const unsupportedLabel = `Unsupported ${formatPercent(view.summary.unsupported_pct)}`;

  const allUnknownInScope =
    view.clients.length === 0 || (view.summary.supported === 0 && view.summary.unknown > 0);
  const scopePhrase = family === "all" ? "all tracked email clients" : familyLabel;

  let emptyTitle = "All detected features are supported";
  let emptyDescription = `All ${formatCount(report.features.length)} detected ${report.features.length === 1 ? "feature is" : "features are"} supported across ${scopePhrase}.`;
  if (allUnknownInScope) {
    emptyTitle =
      family === "all"
        ? "No known compatibility issues"
        : `No known compatibility issues in ${familyLabel}`;
    emptyDescription =
      view.clients.length === 0
        ? `No tracked email clients match ${familyLabel}.`
        : `Support data is unknown for the ${formatCount(report.features.length)} detected ${report.features.length === 1 ? "feature" : "features"} in ${scopePhrase}.`;
  } else if (family !== "all") {
    emptyTitle = `All detected features are supported in ${familyLabel}`;
  }

  let liveAnnouncement: string;
  if (report.features.length === 0) {
    liveAnnouncement = "No tracked HTML or CSS features detected.";
  } else if (view.visibleFeatures.length === 0) {
    liveAnnouncement = emptyTitle;
  } else {
    liveAnnouncement = `Showing ${formatCount(view.visibleFeatures.length)} of ${formatCount(report.features.length)} detected features for ${scopePhrase}.`;
  }

  return (
    <div className="h-full overflow-auto bg-background p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <p className="sr-only" aria-live="polite">
          {liveAnnouncement}
        </p>
        {report.truncated ? (
          <div
            role="status"
            className="flex items-start gap-2.5 rounded-lg border border-warning/30 bg-warning-subtle px-3.5 py-2.5 text-xs text-warning-foreground"
          >
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden="true" />
            <p>
              This message exceeded the offline HTML/CSS analysis limit. Results reflect the
              analyzed portion of the markup.
            </p>
          </div>
        ) : null}

        <section
          aria-labelledby={summaryHeadingId}
          className="rounded-lg border border-border bg-surface p-4"
        >
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <div className="flex flex-wrap items-baseline gap-2.5">
              <h3
                id={summaryHeadingId}
                className="text-xs font-semibold uppercase tracking-wider text-muted-foreground"
              >
                HTML &amp; CSS Compatibility
              </h3>
              <span className="text-xl font-semibold text-foreground tabular-nums">
                {formatPercent(view.score)}
              </span>
            </div>
            <p className="text-xs text-muted-foreground tabular-nums">
              {formatCount(report.features.length)}{" "}
              {report.features.length === 1 ? "feature" : "features"} ·{" "}
              {formatCount(view.clients.length)} {view.clients.length === 1 ? "client" : "clients"}
              {family !== "all" ? ` (${familyLabel})` : ""}
            </p>
          </div>

          <div
            role="img"
            aria-label={`Compatibility breakdown: ${supportedLabel}, ${partialLabel}, ${unsupportedLabel}`}
            className="mt-3 flex h-2.5 w-full overflow-hidden rounded-full bg-muted"
          >
            {view.summary.supported_pct > 0 ? (
              <div
                className="h-full bg-success"
                style={{ width: `${view.summary.supported_pct}%` }}
              />
            ) : null}
            {view.summary.partial_pct > 0 ? (
              <div
                className="h-full bg-warning"
                style={{ width: `${view.summary.partial_pct}%` }}
              />
            ) : null}
            {view.summary.unsupported_pct > 0 ? (
              <div
                className="h-full bg-destructive"
                style={{ width: `${view.summary.unsupported_pct}%` }}
              />
            ) : null}
          </div>

          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Badge variant="success" dot>
              {supportedLabel}
            </Badge>
            <Badge variant="warning" dot>
              {partialLabel}
            </Badge>
            <Badge variant="destructive" dot>
              {unsupportedLabel}
            </Badge>
          </div>
        </section>

        <div className="flex flex-wrap items-center justify-between gap-3">
          <fieldset
            aria-label="Filter by email client family"
            className="inline-flex flex-wrap items-center gap-0.5 rounded-md border border-border bg-muted p-0.5"
          >
            <legend className="sr-only">Filter by email client family</legend>
            {CLIENT_FAMILY_OPTIONS.map((option) => {
              const selected = family === option.value;
              return (
                <button
                  key={option.value}
                  type="button"
                  aria-pressed={selected}
                  onClick={() => setFamily(option.value)}
                  className={cn(
                    "inline-flex h-7 cursor-pointer items-center justify-center rounded-sm px-2.5 text-xs transition-colors",
                    selected
                      ? "bg-surface font-medium text-foreground shadow-xs"
                      : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {option.label}
                </button>
              );
            })}
          </fieldset>

          <div className="flex items-center gap-2">
            <Switch
              id={onlyIssuesId}
              checked={onlyIssues}
              onChange={(e) => setOnlyIssues(e.target.checked)}
            />
            <label
              htmlFor={onlyIssuesId}
              className="cursor-pointer select-none text-xs font-medium text-foreground"
            >
              Only show issues
            </label>
          </div>
        </div>

        {report.features.length === 0 ? (
          <section className="rounded-lg border border-border bg-surface">
            <EmptyState
              icon={CheckCircle2}
              headingLevel={3}
              title="All detected features are supported"
              description="No tracked HTML or CSS compatibility features were found in this email."
            />
          </section>
        ) : view.visibleFeatures.length === 0 ? (
          <section className="rounded-lg border border-border bg-surface">
            <EmptyState
              icon={CheckCircle2}
              headingLevel={3}
              title={emptyTitle}
              description={emptyDescription}
              action={
                <Button variant="outline" size="xs" onClick={() => setOnlyIssues(false)}>
                  Show all {formatCount(report.features.length)}{" "}
                  {report.features.length === 1 ? "feature" : "features"}
                </Button>
              }
            />
          </section>
        ) : (
          <section
            aria-labelledby={featuresHeadingId}
            className="overflow-hidden rounded-lg border border-border bg-surface"
          >
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2.5">
              <div className="flex items-baseline gap-2">
                <h3
                  id={featuresHeadingId}
                  className="text-xs font-semibold uppercase tracking-wider text-muted-foreground"
                >
                  {onlyIssues ? "Compatibility issues" : "Detected features"}
                </h3>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatCount(view.visibleFeatures.length)} of{" "}
                  {formatCount(report.features.length)}
                </span>
              </div>
              <Button variant="ghost" size="xs" onClick={toggleExpandAll}>
                {allExpanded ? "Collapse all" : "Expand all"}
              </Button>
            </div>

            <ul className="divide-y divide-border">
              {view.visibleFeatures.map((entry) => (
                <FeatureRow
                  key={entry.feature.slug}
                  entry={entry}
                  expanded={expandedSlugs.has(entry.feature.slug)}
                  onToggle={() => toggleFeature(entry.feature.slug)}
                  idPrefix={idPrefix}
                />
              ))}
            </ul>
          </section>
        )}

        <footer className="flex flex-wrap items-center justify-between gap-2 px-1 text-[11px] text-muted-foreground">
          <span>
            Offline dataset from{" "}
            <a
              href="https://www.caniemail.com"
              target="_blank"
              rel="noopener noreferrer"
              className="font-medium text-accent hover:underline"
            >
              caniemail.com
            </a>{" "}
            ({report.dataset.license})
          </span>
          {report.dataset.last_update_date ? (
            <span className="tabular-nums">Snapshot {report.dataset.last_update_date}</span>
          ) : null}
        </footer>
      </div>
    </div>
  );
}
