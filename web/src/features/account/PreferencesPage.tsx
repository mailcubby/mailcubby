import { useQuery } from "@tanstack/react-query";
import { Monitor, Moon, Sun } from "lucide-react";
import { useEffect, useId } from "react";
import { AccountLayout } from "@/features/account/AccountLayout";
import {
  type DensityPreference,
  type MarkReadDelayPreference,
  useClock24hPreference,
  useDensityPreference,
  useMarkReadDelayPreference,
  useSingleKeyShortcutsPreference,
} from "@/lib/preferences";
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

const DENSITY_OPTIONS: ReadonlyArray<{
  value: DensityPreference;
  label: string;
  description: string;
}> = [
  { value: "compact", label: "Compact", description: "Denser rows for scanning more messages" },
  { value: "comfortable", label: "Comfortable", description: "Extra breathing room per row" },
];

const MARK_READ_OPTIONS: ReadonlyArray<{
  value: MarkReadDelayPreference;
  label: string;
}> = [
  { value: 0, label: "Immediate (0 s)" },
  { value: 1000, label: "After 1 second (default)" },
  { value: 3000, label: "After 3 seconds" },
  { value: "never", label: "Never automatically" },
];

export function PreferencesPage() {
  const { data: meta } = useQuery(metaQueryOptions());
  const { theme, setTheme } = useTheme();
  const [density, setDensity] = useDensityPreference();
  const [markReadDelay, setMarkReadDelay] = useMarkReadDelayPreference();
  const [singleKeyShortcuts, setSingleKeyShortcuts] = useSingleKeyShortcutsPreference();
  const [clock24h, setClock24h] = useClock24hPreference();

  const readDelayId = useId();
  const shortcutsId = useId();
  const shortcutsDescId = useId();
  const clockId = useId();
  const clockDescId = useId();

  useEffect(() => {
    const instanceName = meta?.instance_name || "Mailcubby";
    document.title = `Preferences · ${instanceName}`;
  }, [meta?.instance_name]);

  return (
    <AccountLayout
      section="preferences"
      title="Preferences"
      description="Saved locally in this browser. Changes apply immediately across open views."
    >
      <div className="flex flex-col divide-y divide-border rounded-lg border border-border bg-surface">
        <section aria-label="Appearance" className="flex flex-col gap-3 p-5">
          <div>
            <h2 className="text-sm font-medium text-foreground">Theme</h2>
            <p className="text-xs text-muted-foreground">
              Choose light, dark, or match your operating system appearance.
            </p>
          </div>
          <fieldset aria-label="Theme" className="grid grid-cols-3 gap-2">
            <legend className="sr-only">Theme</legend>
            {THEME_OPTIONS.map(({ value, label, Icon }) => {
              const active = theme === value;
              return (
                <button
                  key={value}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setTheme(value)}
                  className={cn(
                    "flex cursor-pointer items-center justify-center gap-2 rounded-md border px-3 py-2 text-[13px] font-medium transition-colors",
                    active
                      ? "border-accent bg-accent/10 text-foreground"
                      : "border-border bg-background text-muted-foreground hover:text-foreground",
                  )}
                >
                  <Icon className="size-4" aria-hidden="true" />
                  <span>{label}</span>
                </button>
              );
            })}
          </fieldset>
        </section>

        <section aria-label="Message list density" className="flex flex-col gap-3 p-5">
          <div>
            <h2 className="text-sm font-medium text-foreground">Message list density</h2>
            <p className="text-xs text-muted-foreground">
              Adjust row spacing in the project inbox list.
            </p>
          </div>
          <fieldset aria-label="Message list density" className="grid gap-2 sm:grid-cols-2">
            <legend className="sr-only">Message list density</legend>
            {DENSITY_OPTIONS.map((opt) => {
              const active = density === opt.value;
              return (
                <button
                  key={opt.value}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setDensity(opt.value)}
                  className={cn(
                    "flex cursor-pointer flex-col items-start gap-0.5 rounded-md border px-3.5 py-2.5 text-left transition-colors",
                    active
                      ? "border-accent bg-accent/10 text-foreground"
                      : "border-border bg-background text-muted-foreground hover:text-foreground",
                  )}
                >
                  <span className="text-[13px] font-medium text-foreground">{opt.label}</span>
                  <span className="text-xs text-muted-foreground">{opt.description}</span>
                </button>
              );
            })}
          </fieldset>
        </section>

        <section aria-label="Reading behaviour" className="flex flex-col gap-3 p-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <label htmlFor={readDelayId} className="text-sm font-medium text-foreground">
                Mark as read delay
              </label>
              <p className="text-xs text-muted-foreground">
                Wait before marking an opened message as seen.
              </p>
            </div>
            <select
              id={readDelayId}
              value={String(markReadDelay)}
              onChange={(e) => {
                const raw = e.target.value;
                if (raw === "never") {
                  setMarkReadDelay("never");
                } else {
                  const num = Number(raw);
                  if (num === 0 || num === 1000 || num === 3000) {
                    setMarkReadDelay(num);
                  }
                }
              }}
              className="h-8 cursor-pointer rounded-md border border-border bg-background px-2.5 text-[13px] text-foreground"
            >
              {MARK_READ_OPTIONS.map((opt) => (
                <option key={String(opt.value)} value={String(opt.value)}>
                  {opt.label}
                </option>
              ))}
            </select>
          </div>
        </section>

        <section aria-label="Keyboard shortcuts" className="flex flex-col gap-4 p-5">
          <div className="flex items-start justify-between gap-4">
            <div>
              <label htmlFor={shortcutsId} className="text-sm font-medium text-foreground">
                Single-key keyboard shortcuts
              </label>
              <p id={shortcutsDescId} className="text-xs text-muted-foreground">
                Enable single-character shortcuts such as j/k, e, u, and 1–7. Modifier shortcuts
                (Cmd/Ctrl+K) stay active either way.
              </p>
            </div>
            <input
              id={shortcutsId}
              type="checkbox"
              role="switch"
              aria-checked={singleKeyShortcuts}
              aria-describedby={shortcutsDescId}
              checked={singleKeyShortcuts}
              onChange={(e) => setSingleKeyShortcuts(e.target.checked)}
              className="mt-1 size-4 shrink-0 cursor-pointer rounded-sm border border-border accent-accent"
            />
          </div>

          <div className="flex items-start justify-between gap-4 border-t border-border/70 pt-4">
            <div>
              <label htmlFor={clockId} className="text-sm font-medium text-foreground">
                24-hour clock
              </label>
              <p id={clockDescId} className="text-xs text-muted-foreground">
                Display message times in 24-hour format (e.g. 14:03 instead of 2:03 PM).
              </p>
            </div>
            <input
              id={clockId}
              type="checkbox"
              role="switch"
              aria-checked={clock24h}
              aria-describedby={clockDescId}
              checked={clock24h}
              onChange={(e) => setClock24h(e.target.checked)}
              className="mt-1 size-4 shrink-0 cursor-pointer rounded-sm border border-border accent-accent"
            />
          </div>
        </section>
      </div>
    </AccountLayout>
  );
}
