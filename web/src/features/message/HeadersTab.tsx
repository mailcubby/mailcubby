import { useQuery } from "@tanstack/react-query";
import { Code2, Copy, Search, Table } from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { type HighlightLine, highlightCodeToLines } from "@/features/message/shiki";
import { api, getApiErrorMessage, type Header, unwrap } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { queryKeys } from "@/lib/queryKeys";
import { useTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

export interface HeadersTabProps {
  projectSlug: string;
  messageId: string;
  initialHeaders?: ReadonlyArray<Header> | undefined;
}

export function HeadersTab({ projectSlug, messageId, initialHeaders }: HeadersTabProps) {
  const filterId = useId();
  const { resolvedTheme } = useTheme();
  const [filter, setFilter] = useState("");
  const [mode, setMode] = useState<"table" | "json">("table");
  const [highlightedLines, setHighlightedLines] = useState<HighlightLine[] | null>(null);

  const {
    data: fetchedHeaders,
    isPending,
    error,
  } = useQuery({
    queryKey: queryKeys.messageHeaders(projectSlug, messageId),
    enabled: initialHeaders === undefined,
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/messages/{id}/headers", {
          params: { path: { project: projectSlug, id: messageId } },
        }),
      ),
  });

  const headers = initialHeaders ?? fetchedHeaders ?? [];

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const keyed = headers.map((h, order) => ({ ...h, order }));
    if (!q) {
      return keyed;
    }
    return keyed.filter(
      (h) => h.name.toLowerCase().includes(q) || h.value.toLowerCase().includes(q),
    );
  }, [headers, filter]);

  const jsonPayload = useMemo(() => {
    const plain = filtered.map(({ name, value }) => ({ name, value }));
    return JSON.stringify(plain, null, 2);
  }, [filtered]);

  useEffect(() => {
    if (mode !== "json") {
      return;
    }
    let cancelled = false;
    void highlightCodeToLines(jsonPayload, "json", resolvedTheme).then((lines) => {
      if (!cancelled) {
        setHighlightedLines(lines);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [mode, jsonPayload, resolvedTheme]);

  if (initialHeaders === undefined && isPending) {
    return (
      <div className="flex flex-col gap-2 p-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-full" />
      </div>
    );
  }

  if (error) {
    return (
      <div role="alert" className="p-4 text-sm text-destructive">
        Failed to load headers: {getApiErrorMessage(error)}
      </div>
    );
  }

  const displayLines: HighlightLine[] =
    highlightedLines ??
    jsonPayload
      .split(/\r?\n/)
      .map((line) => [{ offset: 0, content: line, color: undefined, fontStyle: undefined }]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-surface px-3 py-1.5 text-xs">
        <div className="relative w-full max-w-xs">
          <label htmlFor={filterId} className="sr-only">
            Filter headers
          </label>
          <Search
            className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <Input
            id={filterId}
            type="search"
            placeholder="Filter headers..."
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="h-7 pl-7 text-xs"
          />
        </div>

        <div className="flex items-center gap-1.5">
          <span className="text-muted-foreground tabular-nums">
            {filtered.length} of {headers.length}
          </span>
          <fieldset
            aria-label="Headers view mode"
            className="inline-flex items-center gap-0.5 rounded-md border border-border bg-background p-0.5"
          >
            <legend className="sr-only">Headers view mode</legend>
            <button
              type="button"
              aria-pressed={mode === "table"}
              onClick={() => setMode("table")}
              className={cn(
                "inline-flex h-6 cursor-pointer items-center gap-1 rounded-sm px-2 text-xs transition-colors",
                mode === "table"
                  ? "bg-surface font-medium text-foreground shadow-2xs"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Table className="size-3" aria-hidden="true" />
              <span>Table</span>
            </button>
            <button
              type="button"
              aria-pressed={mode === "json"}
              onClick={() => setMode("json")}
              className={cn(
                "inline-flex h-6 cursor-pointer items-center gap-1 rounded-sm px-2 text-xs transition-colors",
                mode === "json"
                  ? "bg-surface font-medium text-foreground shadow-2xs"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Code2 className="size-3" aria-hidden="true" />
              <span>Highlighted</span>
            </button>
          </fieldset>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto bg-surface">
        {filtered.length === 0 ? (
          <div className="p-6 text-center text-xs text-muted-foreground">
            {headers.length === 0 ? "No headers recorded." : `No headers matching "${filter}".`}
          </div>
        ) : mode === "table" ? (
          <table className="w-full border-collapse text-left text-xs">
            <caption className="sr-only">Message headers in original order</caption>
            <thead className="sticky top-0 border-b border-border bg-muted/60 text-muted-foreground">
              <tr>
                <th scope="col" className="w-48 px-3 py-2 font-medium">
                  Header
                </th>
                <th scope="col" className="px-3 py-2 font-medium">
                  Value
                </th>
                <th scope="col" className="w-12 px-2 py-2 text-right font-medium">
                  <span className="sr-only">Copy</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {filtered.map((header) => (
                <tr key={`${header.order}-${header.name}`} className="group hover:bg-muted/30">
                  <th
                    scope="row"
                    className="align-top px-3 py-2 font-mono font-medium text-foreground"
                  >
                    {header.name}
                  </th>
                  <td className="align-top break-all px-3 py-2 font-mono text-muted-foreground">
                    {header.value}
                  </td>
                  <td className="align-top px-2 py-1.5 text-right">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Copy ${header.name} header`}
                      title={`Copy ${header.name}`}
                      onClick={() =>
                        void copyToClipboard(`${header.name}: ${header.value}`, header.name)
                      }
                      className="size-6 opacity-70 hover:opacity-100"
                    >
                      <Copy className="size-3" aria-hidden="true" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="p-3 font-mono text-xs leading-5">
            <ol className="m-0 list-none p-0">
              {displayLines.map((tokens, lineIdx) => {
                const lineNumber = lineIdx + 1;
                return (
                  <li key={`hdr-line-${lineNumber}`} className="flex gap-3 hover:bg-muted/40">
                    <span
                      aria-hidden="true"
                      className="w-8 shrink-0 select-none text-right text-muted-foreground/70 tabular-nums"
                    >
                      {lineNumber}
                    </span>
                    <code className="min-w-0 flex-1 whitespace-pre-wrap break-all">
                      {tokens.map((tok) => (
                        <span
                          key={`hdr-tok-${lineNumber}-${tok.offset}`}
                          style={tok.color ? { color: tok.color } : undefined}
                        >
                          {tok.content}
                        </span>
                      ))}
                    </code>
                  </li>
                );
              })}
            </ol>
          </div>
        )}
      </div>
    </div>
  );
}
