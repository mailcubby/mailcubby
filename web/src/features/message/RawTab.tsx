import { useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Copy, Download } from "lucide-react";
import { useMemo, useRef } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { api, getApiErrorMessage, unwrap } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatBytes } from "@/lib/format";
import { queryKeys } from "@/lib/queryKeys";

export interface RawTabProps {
  projectSlug: string;
  messageId: string;
  sizeBytes: number;
}

export function RawTab({ projectSlug, messageId, sizeBytes }: RawTabProps) {
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const {
    data: rawSource,
    isPending,
    error,
  } = useQuery({
    queryKey: queryKeys.messageRaw(projectSlug, messageId),
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/messages/{id}/raw", {
          params: { path: { project: projectSlug, id: messageId } },
          parseAs: "text",
        }),
      ),
  });

  const lines = useMemo(() => (rawSource ?? "").split(/\r?\n/), [rawSource]);

  const virtualizer = useVirtualizer({
    count: lines.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 20,
    overscan: 30,
  });

  const emlHref = `/api/v1/projects/${encodeURIComponent(projectSlug)}/messages/${encodeURIComponent(messageId)}/eml`;

  if (isPending) {
    return (
      <div className="flex flex-col gap-2 p-4">
        <Skeleton className="h-4 w-3/4" />
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-4 w-5/6" />
      </div>
    );
  }

  if (error) {
    return (
      <div role="alert" className="p-4 text-sm text-destructive">
        Failed to load raw source: {getApiErrorMessage(error)}
      </div>
    );
  }

  const rawText = rawSource ?? "";

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex items-center justify-between gap-2 border-b border-border bg-surface px-3 py-1.5 text-xs">
        <span className="text-muted-foreground tabular-nums">
          {lines.length} {lines.length === 1 ? "line" : "lines"} · {formatBytes(sizeBytes)}
        </span>
        <div className="flex items-center gap-1.5">
          <Button
            variant="outline"
            size="xs"
            onClick={() => void copyToClipboard(rawText, "raw source")}
          >
            <Copy className="size-3" aria-hidden="true" />
            <span>Copy raw</span>
          </Button>
          <a
            href={emlHref}
            download
            className="inline-flex h-7 items-center gap-1 rounded-md border border-border bg-surface px-2 text-xs font-medium text-foreground transition-colors hover:bg-muted"
          >
            <Download className="size-3" aria-hidden="true" />
            <span>Download .eml</span>
          </a>
        </div>
      </div>

      <div
        ref={scrollRef}
        className="min-h-0 flex-1 overflow-auto bg-surface p-3 font-mono text-xs leading-5 text-foreground"
      >
        <div className="relative w-full" style={{ height: `${virtualizer.getTotalSize()}px` }}>
          {virtualizer.getVirtualItems().map((row) => {
            const lineNumber = row.index + 1;
            const text = lines[row.index] ?? "";
            return (
              <div
                key={lineNumber}
                data-index={row.index}
                ref={virtualizer.measureElement}
                style={{ transform: `translateY(${row.start}px)` }}
                className="absolute top-0 left-0 flex w-full gap-3 hover:bg-muted/40"
              >
                <span
                  aria-hidden="true"
                  className="w-10 shrink-0 select-none text-right text-muted-foreground/70 tabular-nums"
                >
                  {lineNumber}
                </span>
                <code className="min-w-0 flex-1 whitespace-pre-wrap break-all">
                  {text || "\u00a0"}
                </code>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
