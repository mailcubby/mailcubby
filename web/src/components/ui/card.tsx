import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * Card is the settings, account and admin surface (docs/UI.md 3.6): a 12 px
 * radius panel on `--surface` with a hairline border. Pass `aria-labelledby`
 * with the id of its `CardHeader` title so it is a named region.
 */
export function Card({ className, ...props }: ComponentProps<"section">) {
  return (
    <section
      className={cn(
        // A table that opens the card (no CardHeader) drops its top rule, which would double the card border.
        "overflow-hidden rounded-lg border border-border bg-surface shadow-xs [&>:first-child_th]:border-t-0",
        className,
      )}
      {...props}
    />
  );
}

export interface CardHeaderProps {
  title: ReactNode;
  /** Id for the title, referenced by the card's `aria-labelledby`. */
  titleId?: string;
  description?: ReactNode;
  /** Buttons aligned to the right of the title. */
  actions?: ReactNode;
  /** Title colour; `destructive` is for the danger zone. */
  tone?: "default" | "destructive";
  className?: string;
}

/** CardHeader renders the card title (15 px, semibold), description and actions. */
export function CardHeader({
  title,
  titleId,
  description,
  actions,
  tone = "default",
  className,
}: CardHeaderProps) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-start justify-between gap-x-4 gap-y-3 px-5 py-4",
        className,
      )}
    >
      <div className="min-w-0 max-w-2xl flex-1 basis-64">
        <h2
          id={titleId}
          className={cn(
            "text-[15px] leading-[22px] font-semibold",
            tone === "destructive" ? "text-destructive" : "text-foreground",
          )}
        >
          {title}
        </h2>
        {description ? (
          <div className="mt-0.5 text-[13px] leading-5 text-muted-foreground">{description}</div>
        ) : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}

/** CardContent is the padded body of a card, below its header. */
export function CardContent({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("px-5 pb-5", className)} {...props} />;
}

/** CardFooter holds the card's form actions, right-aligned above a divider. */
export function CardFooter({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex items-center justify-end gap-2 border-t border-border bg-muted/50 px-5 py-3",
        className,
      )}
      {...props}
    />
  );
}
