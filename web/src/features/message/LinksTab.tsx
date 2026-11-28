import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertCircle,
  AlertTriangle,
  Copy,
  ExternalLink,
  Info,
  Link2,
  Loader2,
  RefreshCw,
} from "lucide-react";
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { EmptyState } from "@/components/EmptyState";
import { Badge } from "@/components/ui/badge";
import { Button, buttonClassName } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  canOpenLinkInNewTab,
  formatCheckAnnouncement,
  formatDurationMs,
  getLinkCheckStatusMeta,
  getLinkSourceLabel,
  getLinkWarningMeta,
  summarizeCheckResults,
} from "@/features/message/links";
import {
  ApiError,
  api,
  getApiErrorMessage,
  handleApiError,
  type LinkCheckResult,
  type MetaResponse,
  type ProjectRole,
  unwrap,
} from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatCount } from "@/lib/format";
import { messageLinksQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";

export interface LinksTabProps {
  projectSlug: string;
  messageId: string;
  effectiveRole: ProjectRole;
  linkCheckEnabled: boolean | undefined;
  mode: MetaResponse["mode"] | undefined;
}

export function LinksTab({
  projectSlug,
  messageId,
  effectiveRole,
  linkCheckEnabled,
  mode,
}: LinksTabProps) {
  const queryClient = useQueryClient();
  const idPrefix = useId();
  const headingId = `${idPrefix}-heading`;

  const [resultsByUrl, setResultsByUrl] = useState<ReadonlyMap<string, LinkCheckResult>>(
    () => new Map(),
  );
  const [checkingAll, setCheckingAll] = useState(false);
  const [checkingUrls, setCheckingUrls] = useState<ReadonlySet<string>>(() => new Set());
  const [liveAnnouncement, setLiveAnnouncement] = useState("");

  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const { data, isPending, error, refetch } = useQuery(
    messageLinksQueryOptions(projectSlug, messageId),
  );

  // ADR 0010 / FR-050: viewers can inspect offline links (when viewer_hide_technical=false)
  // but can never trigger active HTTP link checks. While meta is undefined, fail closed
  // without flashing the disabled-by-config banner.
  const canActiveCheck = linkCheckEnabled === true && effectiveRole !== "viewer";
  const showDisabledBanner = linkCheckEnabled === false && effectiveRole !== "viewer";

  const checkedResults = useMemo(() => {
    if (!data) {
      return [];
    }
    const list: LinkCheckResult[] = [];
    for (const link of data.links) {
      if (link.url) {
        const res = resultsByUrl.get(link.url);
        if (res) {
          list.push(res);
        }
      }
    }
    return list;
  }, [data, resultsByUrl]);

  const summary = useMemo(() => summarizeCheckResults(checkedResults), [checkedResults]);

  const runActiveCheck = useCallback(
    async (urls?: string[]) => {
      if (!canActiveCheck) {
        return;
      }
      const isSingle = Boolean(urls && urls.length > 0);
      if (isSingle && urls) {
        setCheckingUrls((prev) => {
          const next = new Set(prev);
          for (const u of urls) {
            next.add(u);
          }
          return next;
        });
        setLiveAnnouncement("Checking link...");
      } else {
        setCheckingAll(true);
        setLiveAnnouncement("Checking all links...");
      }

      try {
        const response = unwrap(
          await api.POST("/api/v1/projects/{project}/messages/{id}/links/check", {
            params: { path: { project: projectSlug, id: messageId } },
            ...(urls && urls.length > 0 ? { body: { urls } } : {}),
          }),
        );
        if (!mountedRef.current) {
          return;
        }
        setResultsByUrl((prev) => {
          const next = new Map(prev);
          for (const item of response.results) {
            next.set(item.url, item);
          }
          return next;
        });
        setLiveAnnouncement(formatCheckAnnouncement(response.results, isSingle ? "single" : "all"));
      } catch (err) {
        if (!mountedRef.current) {
          return;
        }
        if (err instanceof ApiError && err.code === "feature_disabled") {
          void queryClient.invalidateQueries({ queryKey: queryKeys.meta() });
        }
        handleApiError(err, "Could not check links");
        setLiveAnnouncement(`Link check failed: ${getApiErrorMessage(err)}.`);
      } finally {
        if (mountedRef.current) {
          if (isSingle && urls) {
            setCheckingUrls((prev) => {
              const next = new Set(prev);
              for (const u of urls) {
                next.delete(u);
              }
              return next;
            });
          } else {
            setCheckingAll(false);
          }
        }
      }
    },
    [canActiveCheck, projectSlug, messageId, queryClient],
  );

  if (isPending) {
    return (
      <div aria-busy="true" className="h-full overflow-auto bg-background p-4">
        <div className="mx-auto flex max-w-4xl flex-col gap-4">
          <Skeleton className="h-24 w-full rounded-lg" />
          <Skeleton className="h-52 w-full rounded-lg" />
        </div>
      </div>
    );
  }

  if (error || !data) {
    return (
      <div className="flex h-full items-center justify-center bg-background">
        <EmptyState
          icon={AlertCircle}
          headingLevel={3}
          title="Could not load links"
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

  const isAnyCheckRunning = checkingAll || checkingUrls.size > 0;
  const showCheckedSummary = effectiveRole !== "viewer" && checkedResults.length > 0;
  const showStatusColumn = canActiveCheck || showCheckedSummary;

  return (
    <div className="h-full overflow-auto bg-background p-4">
      <div className="mx-auto flex max-w-4xl flex-col gap-4">
        <p className="sr-only" aria-live="polite">
          {liveAnnouncement}
        </p>

        {data.truncated ? (
          <div
            role="status"
            className="flex items-start gap-2.5 rounded-lg border border-warning/30 bg-warning-subtle px-3.5 py-2.5 text-xs text-warning-foreground"
          >
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden="true" />
            <p>
              This message exceeded the offline link extraction limit. Only the first{" "}
              {formatCount(data.links.length)} unique links are shown.
            </p>
          </div>
        ) : null}

        {showDisabledBanner ? (
          <div
            role="status"
            className="flex items-start gap-2.5 rounded-lg border border-info/25 bg-info-subtle px-3.5 py-2.5 text-xs text-info-foreground"
          >
            <Info className="mt-0.5 size-4 shrink-0 text-info" aria-hidden="true" />
            <p>
              {mode === "server"
                ? "Active link checking is disabled on this server. Set MAILCUBBY_LINK_CHECK_ENABLED=true to enable outbound link checks."
                : "Active link checking is disabled by configuration (MAILCUBBY_LINK_CHECK_ENABLED=false). Offline link extraction remains available."}
            </p>
          </div>
        ) : null}

        {data.links.length === 0 ? (
          <section className="rounded-lg border border-border bg-surface">
            <EmptyState
              icon={Link2}
              headingLevel={3}
              title="No links found"
              description="No links were extracted from this message's HTML, plain text, or headers."
            />
          </section>
        ) : (
          <>
            <section
              aria-labelledby={headingId}
              className="flex flex-col gap-3 rounded-lg border border-border bg-surface p-4"
            >
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex flex-wrap items-baseline gap-2.5">
                  <h3
                    id={headingId}
                    className="text-xs font-semibold uppercase tracking-wider text-muted-foreground"
                  >
                    Extracted Links
                  </h3>
                  <span className="text-xs text-muted-foreground tabular-nums">
                    {formatCount(data.total)} {data.total === 1 ? "link" : "links"} ·{" "}
                    {formatCount(data.checkable_count)} checkable
                  </span>
                </div>

                {canActiveCheck ? (
                  <Button
                    variant="primary"
                    size="sm"
                    disabled={data.checkable_count === 0 || isAnyCheckRunning}
                    onClick={() => void runActiveCheck()}
                  >
                    {checkingAll ? (
                      <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                    ) : (
                      <RefreshCw className="size-3.5" aria-hidden="true" />
                    )}
                    <span>{checkingAll ? "Checking…" : "Check all links"}</span>
                  </Button>
                ) : null}
              </div>

              {canActiveCheck ? (
                <div className="flex items-start gap-2 rounded-md border border-border bg-muted px-3 py-2 text-xs text-muted-foreground">
                  <AlertTriangle
                    className="mt-0.5 size-3.5 shrink-0 text-warning"
                    aria-hidden="true"
                  />
                  <p>
                    Active checks send HTTP requests from Mailcubby and may trigger single-use links
                    (password resets, magic links, or one-click unsubscribe). Use a row&apos;s Check
                    button to skip sensitive links.
                  </p>
                </div>
              ) : null}

              {showCheckedSummary ? (
                <div className="flex flex-wrap items-center gap-2 border-t border-border pt-3">
                  <span className="text-xs text-muted-foreground tabular-nums">
                    Checked {formatCount(summary.total)}:
                  </span>
                  {summary.ok > 0 ? (
                    <Badge variant="success" dot>
                      {formatCount(summary.ok)} OK
                    </Badge>
                  ) : null}
                  {summary.redirect > 0 ? (
                    <Badge variant="info" dot>
                      {formatCount(summary.redirect)}{" "}
                      {summary.redirect === 1 ? "redirect" : "redirects"}
                    </Badge>
                  ) : null}
                  {summary.broken > 0 ? (
                    <Badge variant="destructive" dot>
                      {formatCount(summary.broken)} broken
                    </Badge>
                  ) : null}
                  {summary.blocked > 0 ? (
                    <Badge variant="warning" dot>
                      {formatCount(summary.blocked)} blocked
                    </Badge>
                  ) : null}
                  {summary.error > 0 ? (
                    <Badge variant="destructive" dot>
                      {formatCount(summary.error)} {summary.error === 1 ? "error" : "errors"}
                    </Badge>
                  ) : null}
                  {summary.skipped > 0 ? (
                    <Badge variant="neutral" dot>
                      {formatCount(summary.skipped)} skipped
                    </Badge>
                  ) : null}
                </div>
              ) : null}
            </section>

            <div className="overflow-x-auto rounded-lg border border-border bg-surface">
              <Table>
                <caption className="sr-only">Extracted links from this message</caption>
                <TableHeader>
                  <TableRow>
                    <TableHead className="border-t-0">Link</TableHead>
                    <TableHead className="w-32 border-t-0">Source</TableHead>
                    <TableHead className="w-20 border-t-0 text-right">Count</TableHead>
                    {showStatusColumn ? (
                      <TableHead className="w-52 border-t-0">Status</TableHead>
                    ) : null}
                    <TableHead className="w-36 border-t-0 text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.links.map((link, index) => {
                    const rowKey = link.url ? `url:${link.url}` : `missing:${index}`;
                    const canOpen = canOpenLinkInNewTab(link);
                    const hasCredentialsWarning = link.warnings.includes("credentials_in_url");
                    const result = link.url ? resultsByUrl.get(link.url) : undefined;
                    const isRowChecking =
                      (checkingAll && link.checkable) ||
                      (link.url !== "" && checkingUrls.has(link.url));
                    const statusMeta = result ? getLinkCheckStatusMeta(result) : null;

                    return (
                      <TableRow key={rowKey}>
                        <TableCell className="max-w-md align-top">
                          <div className="flex min-w-0 flex-col gap-1">
                            {link.url ? (
                              <span className="break-all font-mono text-xs text-foreground">
                                {link.url}
                              </span>
                            ) : (
                              <span className="text-xs italic text-muted-foreground">
                                (missing href)
                              </span>
                            )}
                            {link.text ? (
                              <span
                                className="truncate text-xs text-muted-foreground"
                                title={link.text}
                              >
                                {link.text}
                              </span>
                            ) : null}
                            {link.warnings.length > 0 ? (
                              <div className="mt-0.5 flex flex-wrap items-center gap-1.5">
                                {link.warnings.map((warning) => {
                                  const meta = getLinkWarningMeta(warning);
                                  return (
                                    <Badge
                                      key={warning}
                                      variant={meta.variant}
                                      dot
                                      title={meta.description}
                                    >
                                      {meta.label}
                                    </Badge>
                                  );
                                })}
                              </div>
                            ) : null}
                          </div>
                        </TableCell>

                        <TableCell className="align-top">
                          <div className="flex flex-wrap items-center gap-1">
                            {link.sources.map((source) => (
                              <Badge key={source} variant="outline">
                                {getLinkSourceLabel(source)}
                              </Badge>
                            ))}
                          </div>
                        </TableCell>

                        <TableCell className="align-top text-right text-xs text-muted-foreground tabular-nums">
                          {formatCount(link.count)}×
                          <span className="sr-only">
                            {" "}
                            {link.count === 1 ? "occurrence" : "occurrences"}
                          </span>
                        </TableCell>

                        {showStatusColumn ? (
                          <TableCell className="align-top">
                            {isRowChecking ? (
                              <Badge variant="neutral" dot>
                                Checking…
                              </Badge>
                            ) : statusMeta && result ? (
                              <div className="flex flex-col items-start gap-1">
                                <Badge variant={statusMeta.variant} dot>
                                  {statusMeta.label}
                                </Badge>
                                {statusMeta.detail ? (
                                  <span
                                    className="max-w-[14rem] break-all font-mono text-[11px] text-muted-foreground"
                                    title={statusMeta.detail}
                                  >
                                    {result.status === "redirect" ||
                                    result.status === "client_error" ||
                                    result.status === "server_error"
                                      ? `→ ${statusMeta.detail}`
                                      : statusMeta.detail}
                                  </span>
                                ) : null}
                                {result.duration_ms > 0 || result.cached ? (
                                  <span className="text-[11px] text-muted-foreground tabular-nums">
                                    {formatDurationMs(result.duration_ms)}
                                    {result.cached ? " · cached" : ""}
                                  </span>
                                ) : null}
                              </div>
                            ) : link.checkable ? (
                              <span className="text-xs text-muted-foreground">Not checked</span>
                            ) : (
                              <Badge variant="outline">Not checkable</Badge>
                            )}
                          </TableCell>
                        ) : null}

                        <TableCell className="align-top text-right">
                          <div className="inline-flex items-center justify-end gap-1">
                            {canActiveCheck && link.checkable && link.url ? (
                              <Button
                                variant="outline"
                                size="xs"
                                disabled={isAnyCheckRunning}
                                aria-label={`${result ? "Recheck" : "Check"} ${link.url}`}
                                onClick={() => void runActiveCheck([link.url])}
                              >
                                {isRowChecking ? (
                                  <Loader2 className="size-3 animate-spin" aria-hidden="true" />
                                ) : null}
                                <span>{result ? "Recheck" : "Check"}</span>
                              </Button>
                            ) : null}

                            <Button
                              variant="ghost"
                              size="icon"
                              disabled={!link.url}
                              aria-label={
                                link.url ? `Copy URL ${link.url}` : "Copy URL unavailable"
                              }
                              title={link.url ? "Copy URL" : "No URL to copy"}
                              onClick={() => {
                                if (link.url) {
                                  void copyToClipboard(link.url, "Link URL");
                                }
                              }}
                              className="size-7"
                            >
                              <Copy className="size-3.5" aria-hidden="true" />
                            </Button>

                            {canOpen ? (
                              <a
                                href={link.url}
                                target="_blank"
                                rel="noopener noreferrer"
                                aria-label={`Open ${link.url} in new tab`}
                                title="Open in new tab"
                                className={buttonClassName({
                                  variant: "ghost",
                                  size: "icon",
                                  className: "size-7",
                                })}
                              >
                                <ExternalLink className="size-3.5" aria-hidden="true" />
                              </a>
                            ) : (
                              <Button
                                variant="ghost"
                                size="icon"
                                disabled
                                aria-label={
                                  hasCredentialsWarning
                                    ? "Open in new tab unavailable for link with embedded credentials"
                                    : `Open in new tab unavailable for ${link.scheme || "missing"} link`
                                }
                                title={
                                  hasCredentialsWarning
                                    ? "Links with embedded credentials cannot be opened in a new tab"
                                    : "Only HTTP(S) links can be opened in a new tab"
                                }
                                className="size-7"
                              >
                                <ExternalLink className="size-3.5" aria-hidden="true" />
                              </Button>
                            )}
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
