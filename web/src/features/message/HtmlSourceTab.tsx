import { useQuery } from "@tanstack/react-query";
import { Copy, WrapText } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  type HighlightLine,
  highlightCodeToLines,
  MAX_HIGHLIGHT_BYTES,
} from "@/features/message/shiki";
import { api, getApiErrorMessage, unwrap } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { queryKeys } from "@/lib/queryKeys";
import { useTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

export interface HtmlSourceTabProps {
  projectSlug: string;
  messageId: string;
  hasHtml: boolean;
}

export function HtmlSourceTab({ projectSlug, messageId, hasHtml }: HtmlSourceTabProps) {
  const { resolvedTheme } = useTheme();
  const [wrap, setWrap] = useState(true);
  const [lines, setLines] = useState<HighlightLine[] | null>(null);

  const {
    data: htmlSource,
    isPending,
    error,
  } = useQuery({
    queryKey: queryKeys.messageHtml(projectSlug, messageId),
    enabled: hasHtml,
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/messages/{id}/html", {
          params: { path: { project: projectSlug, id: messageId } },
          parseAs: "text",
        }),
      ),
  });

  useEffect(() => {
    if (htmlSource === undefined) {
      setLines(null);
      return;
    }
    let cancelled = false;
    void highlightCodeToLines(htmlSource, "html", resolvedTheme).then((highlighted) => {
      if (!cancelled) {
        setLines(highlighted);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [htmlSource, resolvedTheme]);

  if (!hasHtml) {
    return (
      <div className="flex h-full items-center justify-center p-6 text-sm text-muted-foreground">
        This message does not have an HTML part.
      </div>
    );
  }

  if (isPending) {
    return (
      <div className="flex flex-col gap-2 p-4">
        <Skeleton className="h-4 w-3/4" />
        <Skeleton className="h-4 w-1/2" />
        <Skeleton className="h-4 w-5/6" />
      </div>
    );
  }

  if (error) {
    return (
      <div role="alert" className="p-4 text-sm text-destructive">
        Failed to load HTML source: {getApiErrorMessage(error)}
      </div>
    );
  }

  const rawText = htmlSource ?? "";
  const renderedLines: HighlightLine[] =
    lines ??
    rawText
      .split(/\r?\n/)
      .map((line) => [{ offset: 0, content: line, color: undefined, fontStyle: undefined }]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex items-center justify-between gap-2 border-b border-border bg-surface px-3 py-1.5 text-xs">
        <span className="text-muted-foreground tabular-nums">
          {renderedLines.length} {renderedLines.length === 1 ? "line" : "lines"}
          {rawText.length > MAX_HIGHLIGHT_BYTES ? " · plain text (over 512 KB)" : ""}
        </span>
        <div className="flex items-center gap-1.5">
          <Button
            variant={wrap ? "secondary" : "outline"}
            size="xs"
            aria-pressed={wrap}
            onClick={() => setWrap((prev) => !prev)}
          >
            <WrapText className="size-3" aria-hidden="true" />
            <span>Wrap</span>
          </Button>
          <Button
            variant="outline"
            size="xs"
            onClick={() => void copyToClipboard(rawText, "HTML source")}
          >
            <Copy className="size-3" aria-hidden="true" />
            <span>Copy HTML</span>
          </Button>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto bg-surface p-3 font-mono text-xs leading-5">
        <ol className="m-0 list-none p-0">
          {renderedLines.map((tokens, lineIdx) => {
            const lineNumber = lineIdx + 1;
            return (
              <li key={lineNumber} className="flex gap-3 hover:bg-muted/40">
                <span
                  aria-hidden="true"
                  className="w-10 shrink-0 select-none text-right text-muted-foreground/70 tabular-nums"
                >
                  {lineNumber}
                </span>
                <code
                  className={cn(
                    "min-w-0 flex-1",
                    wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre",
                  )}
                >
                  {tokens.length === 0 ? "\n" : null}
                  {tokens.map((tok) => (
                    <span
                      key={`${lineNumber}-${tok.offset}`}
                      style={
                        tok.color
                          ? {
                              color: tok.color,
                              fontStyle: tok.fontStyle === 1 ? "italic" : undefined,
                            }
                          : undefined
                      }
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
    </div>
  );
}
