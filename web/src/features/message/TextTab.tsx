import { Copy, Info } from "lucide-react";
import { type ReactNode, useMemo } from "react";
import { Button } from "@/components/ui/button";
import type { MessageDetail } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";

const URL_REGEX = /(https?:\/\/[^\s<>()[\]"']+)/g;

function renderLinkedText(text: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  let lastIndex = 0;
  let matchIndex = 0;

  for (const match of text.matchAll(URL_REGEX)) {
    const start = match.index ?? 0;
    const rawUrl = match[0];
    if (start > lastIndex) {
      nodes.push(text.slice(lastIndex, start));
    }

    // Trim common trailing sentence punctuation from auto-linked URLs.
    const trimmed = rawUrl.replace(/[.,;:!?]+$/, "");
    const trailing = rawUrl.slice(trimmed.length);

    nodes.push(
      <a
        key={`link-${matchIndex}-${start}`}
        href={trimmed}
        target="_blank"
        rel="noopener noreferrer"
        className="text-accent underline underline-offset-2 hover:opacity-80"
      >
        {trimmed}
      </a>,
    );
    if (trailing) {
      nodes.push(trailing);
    }

    lastIndex = start + rawUrl.length;
    matchIndex += 1;
  }

  if (lastIndex < text.length) {
    nodes.push(text.slice(lastIndex));
  }

  return nodes;
}

export interface TextTabProps {
  message: MessageDetail;
}

export function TextTab({ message }: TextTabProps) {
  const linked = useMemo(() => renderLinkedText(message.text), [message.text]);

  if (!message.text) {
    return (
      <div className="flex h-full items-center justify-center p-6 text-sm text-muted-foreground">
        No plain text content available.
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-surface px-3 py-1.5 text-xs">
        <div className="flex flex-wrap items-center gap-2 text-muted-foreground">
          {message.text_generated ? (
            <span className="inline-flex items-center gap-1 text-foreground">
              <Info className="size-3.5 text-accent" aria-hidden="true" />
              Generated from HTML
            </span>
          ) : (
            <span>Plain text part</span>
          )}
          {message.text_truncated ? (
            <span className="text-destructive">Truncated at 1 MiB</span>
          ) : null}
        </div>
        <Button
          variant="outline"
          size="xs"
          onClick={() => void copyToClipboard(message.text, "text body")}
        >
          <Copy className="size-3" aria-hidden="true" />
          <span>Copy text</span>
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-auto p-5">
        <pre className="mx-auto max-w-[80ch] whitespace-pre-wrap break-words font-sans text-sm leading-relaxed text-foreground">
          {linked}
        </pre>
      </div>
    </div>
  );
}
