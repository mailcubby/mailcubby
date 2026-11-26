import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface EmptyStateProps {
  /** Decorative lucide icon, drawn on a neutral tinted circle. */
  icon: LucideIcon;
  title: string;
  /** One line of help text. */
  description?: ReactNode;
  /** At most one action, usually a single Button. */
  action?: ReactNode;
  /** Supplementary content under the action (keyboard hints, a setup guide). */
  children?: ReactNode;
  headingLevel?: 2 | 3;
  /** `sm` is the compact variant for narrow panes. */
  size?: "md" | "sm";
  className?: string;
}

/**
 * EmptyState is the one structured empty state of the app (docs/UI.md 3.4.2):
 * an icon on a tinted circle, a title, one line of help text and an optional
 * single action. The tint is neutral because the accent is reserved for
 * selection, unread, focus and primary actions (UI.md 1.6).
 */
export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  children,
  headingLevel = 2,
  size = "md",
  className,
}: EmptyStateProps) {
  const Heading = headingLevel === 3 ? "h3" : "h2";
  const compact = size === "sm";
  return (
    <div
      className={cn(
        "flex flex-col items-center text-center",
        compact ? "gap-2.5 px-4 py-8" : "gap-3 px-6 py-10",
        className,
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "flex shrink-0 items-center justify-center rounded-full bg-secondary text-muted-foreground",
          compact ? "size-10" : "size-14",
        )}
      >
        <Icon className={compact ? "size-5" : "size-6"} strokeWidth={1.75} />
      </span>
      <div className="flex max-w-sm flex-col gap-1">
        <Heading
          className={cn(
            "font-semibold text-foreground",
            compact ? "text-[13px]" : "text-[15px] leading-[22px]",
          )}
        >
          {title}
        </Heading>
        {description ? (
          <p className={cn("text-muted-foreground", compact ? "text-xs" : "text-[13px]")}>
            {description}
          </p>
        ) : null}
      </div>
      {action ? <div className="mt-1 flex items-center justify-center">{action}</div> : null}
      {children}
    </div>
  );
}
