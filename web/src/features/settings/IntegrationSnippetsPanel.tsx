import { Copy, Eye, EyeOff } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { generateIntegrationSnippets, type SnippetInput } from "@/features/settings/snippets";
import { copyToClipboard } from "@/lib/clipboard";
import { cn } from "@/lib/utils";

export interface IntegrationSnippetsPanelProps {
  input: SnippetInput;
  /** True when the real password is currently loaded into input.password (server mode). */
  passwordRevealed?: boolean;
  /** True when the caller is allowed to reveal the selected credential password. */
  canRevealPassword?: boolean;
  /** Triggers reveal or re-mask toggle for the selected credential in server mode. */
  onToggleRevealPassword?: (() => void) | undefined;
  revealingPassword?: boolean;
  className?: string;
}

export function IntegrationSnippetsPanel({
  input,
  passwordRevealed = false,
  canRevealPassword = false,
  onToggleRevealPassword,
  revealingPassword = false,
  className,
}: IntegrationSnippetsPanelProps) {
  const [activeId, setActiveId] = useState("laravel");
  const tabsId = useId();

  const snippets = useMemo(() => generateIntegrationSnippets(input), [input]);
  const activeSnippet = snippets.find((s) => s.id === activeId) ?? snippets[0];

  if (!activeSnippet) {
    return null;
  }

  return (
    <div className={cn("overflow-hidden rounded-lg border border-border bg-surface", className)}>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-1.5">
        <div
          role="tablist"
          aria-label="Integration snippets"
          className="flex flex-wrap items-center gap-1"
        >
          {snippets.map((snippet) => {
            const selected = snippet.id === activeSnippet.id;
            return (
              <button
                key={snippet.id}
                id={`${tabsId}-tab-${snippet.id}`}
                type="button"
                role="tab"
                aria-selected={selected}
                aria-controls={`${tabsId}-panel-${snippet.id}`}
                onClick={() => setActiveId(snippet.id)}
                className={cn(
                  "cursor-pointer rounded-sm px-2 py-1 text-xs font-medium transition-colors",
                  selected
                    ? "bg-muted text-foreground"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {snippet.label}
              </button>
            );
          })}
        </div>

        <div className="flex items-center gap-1.5">
          {input.mode === "server" && canRevealPassword && onToggleRevealPassword ? (
            <Button
              type="button"
              variant="ghost"
              size="xs"
              disabled={revealingPassword}
              onClick={onToggleRevealPassword}
            >
              {passwordRevealed ? (
                <>
                  <EyeOff className="size-3" aria-hidden="true" />
                  <span>Mask password</span>
                </>
              ) : (
                <>
                  <Eye className="size-3" aria-hidden="true" />
                  <span>{revealingPassword ? "Revealing..." : "Reveal password"}</span>
                </>
              )}
            </Button>
          ) : null}

          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() =>
              void copyToClipboard(activeSnippet.code, `${activeSnippet.label} snippet`)
            }
          >
            <Copy className="size-3" aria-hidden="true" />
            <span>Copy snippet</span>
          </Button>
        </div>
      </div>

      <div
        id={`${tabsId}-panel-${activeSnippet.id}`}
        role="tabpanel"
        aria-labelledby={`${tabsId}-tab-${activeSnippet.id}`}
      >
        {activeSnippet.table ? (
          <div className="border-b border-border bg-background/40 p-4">
            <div className="mb-2 text-[11px] font-medium text-muted-foreground">
              WP Mail SMTP plugin settings
            </div>
            <dl className="grid grid-cols-1 gap-x-6 gap-y-1.5 text-xs sm:grid-cols-2">
              {activeSnippet.table.map((row) => (
                <div
                  key={row.label}
                  className="flex items-center justify-between gap-2 border-b border-border/60 py-1"
                >
                  <dt className="text-muted-foreground">{row.label}</dt>
                  <dd className="flex min-w-0 items-center gap-1 font-mono text-foreground">
                    <span className="truncate">{row.value}</span>
                    {row.value !== "********" ? (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={`Copy ${row.label}`}
                        onClick={() => void copyToClipboard(row.value, row.label)}
                        className="size-5 shrink-0"
                      >
                        <Copy className="size-2.5" aria-hidden="true" />
                      </Button>
                    ) : null}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        ) : null}

        <pre
          // biome-ignore lint/a11y/noNoninteractiveTabindex: scrollable code region needs keyboard focus (WCAG 2.1.1).
          tabIndex={0}
          className="overflow-x-auto p-4 font-mono text-xs leading-5 text-foreground"
        >
          <code>{activeSnippet.code}</code>
        </pre>
      </div>
    </div>
  );
}
