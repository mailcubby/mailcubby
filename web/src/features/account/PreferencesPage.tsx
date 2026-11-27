import { useQuery } from "@tanstack/react-query";
import { Monitor, Moon, Sun } from "lucide-react";
import { useEffect, useId } from "react";
import { MadeBy, SOURCE_URL } from "@/components/MadeBy";
import { Card } from "@/components/ui/card";
import { NativeSelect } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
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
  const aboutId = useId();

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
      <Card className="flex flex-col divide-y divide-border">
        <section aria-label="Appearance" className="flex flex-col gap-3 p-5">
          <div>
            <h2 className="text-[15px] leading-[22px] font-semibold text-foreground">Theme</h2>
            <p className="mt-0.5 text-[13px] text-muted-foreground">
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
                      ? "border-accent bg-accent-subtle text-accent-subtle-foreground"
                      : "border-input bg-surface text-foreground hover:bg-muted",
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
            <h2 className="text-[15px] leading-[22px] font-semibold text-foreground">
              Message list density
            </h2>
            <p className="mt-0.5 text-[13px] text-muted-foreground">
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
                      ? "border-accent bg-accent-subtle text-accent-subtle-foreground"
                      : "border-input bg-surface text-foreground hover:bg-muted",
                  )}
                >
                  <span className="text-[13px] font-medium">{opt.label}</span>
                  <span
                    className={cn(
                      "text-xs",
                      active ? "text-accent-subtle-foreground" : "text-muted-foreground",
                    )}
                  >
                    {opt.description}
                  </span>
                </button>
              );
            })}
          </fieldset>
        </section>

        <section aria-label="Reading behaviour" className="flex flex-col gap-3 p-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <label
                htmlFor={readDelayId}
                className="text-[15px] leading-[22px] font-semibold text-foreground"
              >
                Mark as read delay
              </label>
              <p className="mt-0.5 text-[13px] text-muted-foreground">
                Wait before marking an opened message as seen.
              </p>
            </div>
            <NativeSelect
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
            >
              {MARK_READ_OPTIONS.map((opt) => (
                <option key={String(opt.value)} value={String(opt.value)}>
                  {opt.label}
                </option>
              ))}
            </NativeSelect>
          </div>
        </section>

        <section aria-label="Keyboard shortcuts" className="flex flex-col gap-4 p-5">
          <div className="flex items-start justify-between gap-4">
            <div>
              <label
                htmlFor={shortcutsId}
                className="text-[15px] leading-[22px] font-semibold text-foreground"
              >
                Single-key keyboard shortcuts
              </label>
              <p id={shortcutsDescId} className="mt-0.5 text-[13px] text-muted-foreground">
                Enable single-character shortcuts such as j/k, e, u, and 1–9. Modifier shortcuts
                (Cmd/Ctrl+K) stay active either way.
              </p>
            </div>
            <Switch
              id={shortcutsId}
              aria-describedby={shortcutsDescId}
              checked={singleKeyShortcuts}
              onChange={(e) => setSingleKeyShortcuts(e.target.checked)}
              className="mt-1"
            />
          </div>

          <div className="flex items-start justify-between gap-4 border-t border-border/70 pt-4">
            <div>
              <label
                htmlFor={clockId}
                className="text-[15px] leading-[22px] font-semibold text-foreground"
              >
                24-hour clock
              </label>
              <p id={clockDescId} className="mt-0.5 text-[13px] text-muted-foreground">
                Display message times in 24-hour format (e.g. 14:03 instead of 2:03 PM).
              </p>
            </div>
            <Switch
              id={clockId}
              aria-describedby={clockDescId}
              checked={clock24h}
              onChange={(e) => setClock24h(e.target.checked)}
              className="mt-1"
            />
          </div>
        </section>
      </Card>

      <Card>
        <section aria-labelledby={aboutId} className="flex flex-col gap-2 p-5">
          <h2 id={aboutId} className="text-[15px] leading-[22px] font-semibold text-foreground">
            About Mailcubby
          </h2>
          <p className="text-[13px] text-muted-foreground">
            {meta?.version ? <>Version {meta.version}. </> : null}
            <MadeBy />. Source-available under the{" "}
            <a
              href={`${SOURCE_URL}/blob/main/LICENSE`}
              target="_blank"
              rel="noopener"
              className="underline underline-offset-2 hover:text-foreground"
            >
              PolyForm Shield licence
            </a>
            .
          </p>
        </section>
      </Card>
    </AccountLayout>
  );
}
