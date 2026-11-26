import type { ButtonHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export type ButtonVariant =
  | "primary"
  | "secondary"
  | "outline"
  | "ghost"
  | "destructive"
  | "chrome";
export type ButtonSize = "xs" | "sm" | "md" | "icon";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

const VARIANT_CLASSES: Record<ButtonVariant, string> = {
  primary:
    "bg-primary text-primary-foreground hover:bg-primary-hover border border-transparent shadow-xs",
  secondary: "bg-secondary text-secondary-foreground hover:bg-border border border-transparent",
  outline: "bg-surface text-foreground hover:bg-muted border border-input",
  ghost:
    "bg-transparent text-muted-foreground hover:bg-muted hover:text-foreground border border-transparent",
  destructive:
    "bg-destructive text-destructive-foreground hover:bg-destructive-hover border border-transparent shadow-xs",
  // Ghost button on the chrome layer (top bar, project sidebar).
  chrome:
    "bg-transparent text-sidebar-muted hover:bg-sidebar-active hover:text-sidebar-active-foreground border border-transparent",
};

const SIZE_CLASSES: Record<ButtonSize, string> = {
  xs: "h-7 min-w-7 px-2 text-xs gap-1",
  sm: "h-8 min-w-8 px-2.5 text-[13px] gap-1.5",
  md: "h-9 min-w-9 px-3.5 text-sm gap-2",
  icon: "size-8 p-0",
};

/** Returns the button classes, for links and anchors that look like buttons. */
export function buttonClassName({
  variant = "outline",
  size = "sm",
  className,
}: {
  variant?: ButtonVariant;
  size?: ButtonSize;
  className?: string | undefined;
} = {}): string {
  return cn(
    "inline-flex shrink-0 cursor-pointer items-center justify-center rounded-md font-medium transition-colors duration-150 disabled:pointer-events-none disabled:opacity-50",
    VARIANT_CLASSES[variant],
    SIZE_CLASSES[size],
    className,
  );
}

export function Button({
  variant = "outline",
  size = "sm",
  type = "button",
  className,
  ...props
}: ButtonProps) {
  return (
    <button type={type} className={buttonClassName({ variant, size, className })} {...props} />
  );
}
