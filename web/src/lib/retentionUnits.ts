export type AgeUnit = "days" | "hours";
export type SizeUnit = "KB" | "MB" | "GB";

const SECONDS_PER_HOUR = 3_600;
const SECONDS_PER_DAY = 86_400;

const BYTES_PER_KB = 1_024;
const BYTES_PER_MB = 1_024 * 1_024;
const BYTES_PER_GB = 1_024 * 1_024 * 1_024;

export function splitAgeSeconds(seconds: number): { value: string; unit: AgeUnit } {
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return { value: "0", unit: "days" };
  }
  if (seconds % SECONDS_PER_DAY === 0) {
    return { value: String(seconds / SECONDS_PER_DAY), unit: "days" };
  }
  if (seconds % SECONDS_PER_HOUR === 0) {
    return { value: String(seconds / SECONDS_PER_HOUR), unit: "hours" };
  }
  // Round fractional hours to nearest tenth for display if a custom env duration used minutes/seconds.
  const hours = Math.round((seconds / SECONDS_PER_HOUR) * 10) / 10;
  return { value: String(hours), unit: "hours" };
}

export function combineAgeSeconds(rawValue: string, unit: AgeUnit): number | null {
  const trimmed = rawValue.trim();
  if (trimmed === "") {
    return null;
  }
  const num = Number(trimmed);
  if (!Number.isFinite(num) || num < 0) {
    return null;
  }
  const mult = unit === "days" ? SECONDS_PER_DAY : SECONDS_PER_HOUR;
  return Math.round(num * mult);
}

export function splitStorageBytes(bytes: number): { value: string; unit: SizeUnit } {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return { value: "0", unit: "MB" };
  }
  if (bytes % BYTES_PER_GB === 0) {
    return { value: String(bytes / BYTES_PER_GB), unit: "GB" };
  }
  if (bytes % BYTES_PER_MB === 0) {
    return { value: String(bytes / BYTES_PER_MB), unit: "MB" };
  }
  if (bytes % BYTES_PER_KB === 0) {
    return { value: String(bytes / BYTES_PER_KB), unit: "KB" };
  }
  const mb = Math.round((bytes / BYTES_PER_MB) * 100) / 100;
  return { value: String(mb), unit: "MB" };
}

export function combineStorageBytes(rawValue: string, unit: SizeUnit): number | null {
  const trimmed = rawValue.trim();
  if (trimmed === "") {
    return null;
  }
  const num = Number(trimmed);
  if (!Number.isFinite(num) || num < 0) {
    return null;
  }
  const mult = unit === "GB" ? BYTES_PER_GB : unit === "MB" ? BYTES_PER_MB : BYTES_PER_KB;
  return Math.round(num * mult);
}

export function parseNonNegativeInt(rawValue: string): number | null {
  const trimmed = rawValue.trim();
  if (!/^\d+$/.test(trimmed)) {
    return null;
  }
  const parsed = Number.parseInt(trimmed, 10);
  if (!Number.isSafeInteger(parsed) || parsed < 0) {
    return null;
  }
  return parsed;
}
