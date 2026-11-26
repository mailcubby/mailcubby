import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

/**
 * Badge variants (docs/UI.md section 1, badges):
 * - neutral: default soft pill (viewer role, tags, metadata, expired/revoked/disabled);
 * - outline: transparent with a border, for low-emphasis metadata;
 * - accent: soft accent tint (owner role, "Current", "Admin");
 * - primary: solid accent, for unread counts only;
 * - success | warning | info | destructive: semantic states.
 * Status badges pass `dot` so colour is never the only signal.
 */
export type BadgeVariant =
  | "neutral"
  | "outline"
  | "accent"
  | "primary"
  | "success"
  | "warning"
  | "info"
  | "destructive";

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: BadgeVariant;
  /** Renders a leading status dot (decorative; the text carries the meaning). */
  dot?: boolean;
}

const VARIANT_CLASSES: Record<BadgeVariant, string> = {
  neutral: "bg-secondary text-secondary-foreground border-transparent",
  outline: "bg-transparent text-muted-foreground border-border",
  accent: "bg-accent-subtle text-accent-subtle-foreground border-transparent",
  primary: "bg-primary text-primary-foreground border-transparent font-semibold",
  success: "bg-success-subtle text-success-foreground border-transparent",
  warning: "bg-warning-subtle text-warning-foreground border-transparent",
  info: "bg-info-subtle text-info-foreground border-transparent",
  destructive: "bg-destructive-subtle text-destructive border-transparent",
};

const DOT_CLASSES: Record<BadgeVariant, string> = {
  neutral: "bg-muted-foreground",
  outline: "bg-muted-foreground",
  accent: "bg-accent",
  primary: "bg-primary-foreground",
  success: "bg-success",
  warning: "bg-warning",
  info: "bg-info",
  destructive: "bg-destructive",
};

export function Badge({
  variant = "neutral",
  dot = false,
  className,
  children,
  ...props
}: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex min-h-5 items-center gap-1 rounded-full border px-2 py-0.5 text-[11.5px] font-medium leading-none whitespace-nowrap tabular-nums",
        VARIANT_CLASSES[variant],
        className,
      )}
      {...props}
    >
      {dot ? (
        <span
          className={cn("size-1.5 shrink-0 rounded-full", DOT_CLASSES[variant])}
          aria-hidden="true"
        />
      ) : null}
      {children}
    </span>
  );
}
