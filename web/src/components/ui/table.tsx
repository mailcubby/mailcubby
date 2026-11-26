import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

/**
 * Table primitives for settings, account and admin tables (docs/UI.md 3.6):
 * 12 px sentence-case headers on `--muted`, 13 px body rows with hairline
 * dividers, 20 px outer padding so cells line up with `CardHeader`.
 */
export function Table({ className, ...props }: ComponentProps<"table">) {
  return (
    <table className={cn("w-full border-collapse text-left text-[13px]", className)} {...props} />
  );
}

export function TableHeader({ className, ...props }: ComponentProps<"thead">) {
  return <thead className={className} {...props} />;
}

export function TableBody({ className, ...props }: ComponentProps<"tbody">) {
  return (
    <tbody
      className={cn(
        "[&>tr]:border-b [&>tr]:border-border [&>tr]:transition-colors [&>tr:hover]:bg-muted/40 [&>tr:last-child]:border-0",
        className,
      )}
      {...props}
    />
  );
}

export function TableRow(props: ComponentProps<"tr">) {
  return <tr {...props} />;
}

export function TableHead({ className, scope = "col", ...props }: ComponentProps<"th">) {
  return (
    <th
      scope={scope}
      className={cn(
        "h-9 border-y border-border bg-muted px-3 align-middle text-xs font-medium whitespace-nowrap text-muted-foreground first:pl-5 last:pr-5",
        className,
      )}
      {...props}
    />
  );
}

export function TableCell({ className, ...props }: ComponentProps<"td">) {
  return (
    <td className={cn("px-3 py-2.5 align-middle first:pl-5 last:pr-5", className)} {...props} />
  );
}
