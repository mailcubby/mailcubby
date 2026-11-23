import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Inbox, Monitor, Moon, Sun } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { metaQueryOptions } from "@/lib/queries";
import { type ThemePreference, useTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

const THEME_OPTIONS: ReadonlyArray<{
  value: ThemePreference;
  label: string;
  Icon: typeof Sun;
}> = [
  { value: "light", label: "Light", Icon: Sun },
  { value: "dark", label: "Dark", Icon: Moon },
  { value: "system", label: "System", Icon: Monitor },
];

export function ThemeToggle() {
  const { theme, systemTheme, setTheme } = useTheme();

  return (
    <fieldset
      aria-label="Theme preference"
      className="inline-flex items-center gap-0.5 rounded-md border border-border bg-background p-0.5"
    >
      <legend className="sr-only">Theme preference</legend>
      {THEME_OPTIONS.map(({ value, label, Icon }) => {
        const active = theme === value;
        const displayLabel = value === "system" ? `${label} (Currently ${systemTheme})` : label;
        return (
          <button
            key={value}
            type="button"
            aria-pressed={active}
            aria-label={label}
            title={displayLabel}
            onClick={() => setTheme(value)}
            className={cn(
              "inline-flex h-6 min-w-6 cursor-pointer items-center justify-center gap-1 rounded-sm px-2 text-xs font-medium transition-colors duration-150",
              active
                ? "bg-surface text-foreground shadow-2xs"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Icon className="size-3.5" aria-hidden="true" />
            <span className="hidden md:inline">{label}</span>
          </button>
        );
      })}
    </fieldset>
  );
}

export function AppHeader() {
  const { data: meta } = useQuery(metaQueryOptions());
  const instanceName = meta?.instance_name || "Mailcubby";

  return (
    <header className="flex h-11 shrink-0 items-center justify-between gap-3 border-b border-border bg-surface px-4">
      <div className="flex min-w-0 items-center gap-3">
        <Link
          to="/"
          className="inline-flex items-center gap-2 rounded-sm text-[13px] font-semibold tracking-tight text-foreground hover:opacity-80"
        >
          <span className="flex size-6 items-center justify-center rounded-md bg-foreground text-background">
            <Inbox className="size-3.5" aria-hidden="true" />
          </span>
          <span className="truncate">{instanceName}</span>
        </Link>
        {meta?.mode === "local" ? (
          <Badge variant="outline" className="hidden sm:inline-flex">
            Local mode
          </Badge>
        ) : null}
      </div>

      <nav aria-label="Application utilities" className="flex items-center gap-2">
        <ThemeToggle />
      </nav>
    </header>
  );
}
