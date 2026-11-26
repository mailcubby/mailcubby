import { Inbox } from "lucide-react";
import { cn } from "@/lib/utils";

export interface BrandMarkProps {
  /** "chrome" sits on the ink-green top bar; "standalone" sits on the canvas (auth pages). */
  tone?: "chrome" | "standalone";
  size?: "sm" | "md";
  className?: string;
}

/** BrandMark is the Mailcubby logo square (decorative; the name is always next to it). */
export function BrandMark({ tone = "standalone", size = "md", className }: BrandMarkProps) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "flex shrink-0 items-center justify-center rounded-md",
        tone === "chrome"
          ? "bg-sidebar-accent text-sidebar-accent-foreground"
          : "bg-sidebar text-sidebar-accent",
        size === "sm" ? "size-7" : "size-9",
        className,
      )}
    >
      <Inbox className={size === "sm" ? "size-4" : "size-[18px]"} />
    </span>
  );
}
