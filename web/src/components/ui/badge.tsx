import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export type BadgeVariant = "default" | "secondary" | "accent" | "outline" | "destructive";

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: BadgeVariant;
}

const VARIANT_CLASSES: Record<BadgeVariant, string> = {
  default: "bg-foreground text-background border-transparent",
  secondary: "bg-secondary text-secondary-foreground border-border",
  accent: "bg-primary text-primary-foreground border-transparent",
  outline: "bg-transparent text-muted-foreground border-border",
  destructive: "bg-destructive/10 text-destructive border-destructive/20",
};

export function Badge({ variant = "secondary", className, ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-md border px-1.5 py-0.5 text-[11px] font-medium leading-none tabular-nums",
        VARIANT_CLASSES[variant],
        className,
      )}
      {...props}
    />
  );
}
