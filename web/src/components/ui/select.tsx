import { ChevronDown } from "lucide-react";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export interface NativeSelectProps extends ComponentProps<"select"> {
  /** "sm" (28 px) for table cells, "md" (32 px, default) for forms. */
  controlSize?: "sm" | "md";
  /** Classes for the wrapper (width, margins); `className` styles the select itself. */
  wrapperClassName?: string;
}

/**
 * NativeSelect is a real `<select>` styled like `Input` (audit M11): same border,
 * radius, height and focus treatment, with a lucide chevron. It keeps native
 * keyboard, screen-reader and mobile behaviour.
 */
export function NativeSelect({
  controlSize = "md",
  className,
  wrapperClassName,
  children,
  ...props
}: NativeSelectProps) {
  return (
    <span className={cn("relative inline-flex", wrapperClassName)}>
      <select
        className={cn(
          "w-full cursor-pointer appearance-none rounded-md border border-input bg-surface text-foreground transition-colors focus-visible:border-accent disabled:cursor-not-allowed disabled:opacity-50",
          controlSize === "sm" ? "h-7 pr-7 pl-2 text-xs" : "h-8 pr-8 pl-2.5 text-[13px]",
          className,
        )}
        {...props}
      >
        {children}
      </select>
      <ChevronDown
        className={cn(
          "pointer-events-none absolute top-1/2 -translate-y-1/2 text-muted-foreground",
          controlSize === "sm" ? "right-2 size-3" : "right-2.5 size-3.5",
        )}
        aria-hidden="true"
      />
    </span>
  );
}
