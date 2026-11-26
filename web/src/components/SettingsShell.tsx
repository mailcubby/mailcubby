import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface SettingsShellProps {
  /** Back link above the title (a router `Link` styled with `settingsBackLinkClassName`). */
  back: ReactNode;
  /** Page title (20 px). */
  title: ReactNode;
  /** Inline content before the title (project colour dot). */
  leading?: ReactNode;
  /** Inline content after the title (role badge). */
  trailing?: ReactNode;
  description?: ReactNode;
  /** The page's main action, right of the title (at most one primary button). */
  actions?: ReactNode;
  /** Section nav; omitted when the page has a single section. */
  nav?: ReactNode;
  /** `wide` gives admin tables more room. */
  width?: "default" | "wide";
  children: ReactNode;
}

/**
 * SettingsShell is the shared frame of project settings, account and admin
 * pages (docs/UI.md 3.6 and 3.7): back link, page title with description and
 * action, then a section nav beside the content cards.
 */
export function SettingsShell({
  back,
  title,
  leading,
  trailing,
  description,
  actions,
  nav,
  width = "default",
  children,
}: SettingsShellProps) {
  return (
    <main className="h-full overflow-y-auto px-4 py-6 sm:px-6 lg:py-8">
      <div
        className={cn(
          "mx-auto flex w-full flex-col gap-6",
          width === "wide" ? "max-w-6xl" : "max-w-5xl",
        )}
      >
        <div className="flex flex-col gap-3">
          {back}
          <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
            <div className="min-w-0">
              <div className="flex min-w-0 items-center gap-2.5">
                {leading}
                <h1 className="truncate text-xl leading-7 font-semibold tracking-tight text-foreground">
                  {title}
                </h1>
                {trailing}
              </div>
              {description ? (
                <p className="mt-0.5 text-[13px] leading-5 text-muted-foreground">{description}</p>
              ) : null}
            </div>
            {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
          </div>
        </div>

        {nav ? (
          <div className="grid gap-6 md:grid-cols-[200px_minmax(0,1fr)] lg:gap-8">
            {nav}
            <div className="flex min-w-0 flex-col gap-4">{children}</div>
          </div>
        ) : (
          <div className="flex min-w-0 flex-col gap-4">{children}</div>
        )}
      </div>
    </main>
  );
}

/** Classes for the back link above a settings page title. */
export const settingsBackLinkClassName =
  "inline-flex w-fit items-center gap-1.5 rounded-sm text-[13px] font-medium text-muted-foreground transition-colors hover:text-foreground";

/** Classes for the section nav container (a `<nav>` with an `aria-label`). */
export const settingsNavClassName =
  "-mx-1 flex flex-row gap-0.5 overflow-x-auto px-1 pb-1 md:mx-0 md:flex-col md:overflow-visible md:px-0 md:pb-0";

/** Classes for one section nav link; the active one uses the accent tint. */
export function settingsNavItemClassName(active: boolean): string {
  return cn(
    "group inline-flex h-8.5 shrink-0 items-center gap-2.5 rounded-md px-2.5 text-[13px] whitespace-nowrap transition-colors",
    active
      ? "bg-accent-subtle font-semibold text-accent-subtle-foreground"
      : "font-medium text-foreground hover:bg-muted",
  );
}

/** Classes for the icon inside a section nav link. */
export function settingsNavIconClassName(active: boolean): string {
  return cn(
    "size-4 shrink-0",
    active ? "text-accent-subtle-foreground" : "text-muted-foreground group-hover:text-foreground",
  );
}
