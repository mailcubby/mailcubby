import { createFileRoute } from "@tanstack/react-router";
import { Monitor, Moon, Sun } from "lucide-react";
import { type ThemePreference, useTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/")({
  component: HomePage,
});

const THEME_OPTIONS: ReadonlyArray<{
  value: ThemePreference;
  label: string;
  Icon: typeof Sun;
}> = [
  { value: "light", label: "Light", Icon: Sun },
  { value: "dark", label: "Dark", Icon: Moon },
  { value: "system", label: "System", Icon: Monitor },
];

export function HomePage() {
  const { theme, systemTheme, setTheme } = useTheme();

  return (
    <main className="mx-auto flex min-h-screen max-w-xl flex-col items-center justify-center gap-6 px-6 py-12">
      <div className="flex flex-col items-center gap-2 text-center">
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">Mailcubby</h1>
        <p className="text-sm text-muted-foreground">Self-hosted email testing server</p>
      </div>

      <fieldset
        aria-label="Theme preference"
        className="inline-flex items-center gap-1 rounded-md border border-border bg-surface p-1"
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
              title={displayLabel}
              onClick={() => setTheme(value)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-sm px-3 py-1.5 text-xs font-medium transition-colors",
                active
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon className="size-3.5" aria-hidden="true" />
              <span>{label}</span>
            </button>
          );
        })}
      </fieldset>
    </main>
  );
}
