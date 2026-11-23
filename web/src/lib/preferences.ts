import { usePersistentState } from "@/lib/usePersistentState";

export type DensityPreference = "compact" | "comfortable";
export type MarkReadDelayPreference = 0 | 1000 | 3000 | "never";

export function isDensityPreference(value: unknown): value is DensityPreference {
  return value === "compact" || value === "comfortable";
}

export function isMarkReadDelayPreference(value: unknown): value is MarkReadDelayPreference {
  return value === 0 || value === 1000 || value === 3000 || value === "never";
}

function isBoolean(value: unknown): value is boolean {
  return typeof value === "boolean";
}

export function useDensityPreference() {
  return usePersistentState<DensityPreference>("density", "compact", isDensityPreference);
}

export function useMarkReadDelayPreference() {
  return usePersistentState<MarkReadDelayPreference>(
    "mark-read-delay",
    1000,
    isMarkReadDelayPreference,
  );
}

export function useSingleKeyShortcutsPreference() {
  return usePersistentState<boolean>("single-key-shortcuts", true, isBoolean);
}

export function useClock24hPreference() {
  return usePersistentState<boolean>("clock-24h", true, isBoolean);
}
