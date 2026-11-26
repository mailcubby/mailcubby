import type { Address, ProjectColor } from "@/lib/api";

const MINUTE_MS = 60_000;
const HOUR_MS = 3_600_000;

const timeFormatter24 = new Intl.DateTimeFormat("en-GB", {
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});

const timeFormatter12 = new Intl.DateTimeFormat("en-US", {
  hour: "numeric",
  minute: "2-digit",
  hour12: true,
});

const monthDayFormatter = new Intl.DateTimeFormat("en-GB", {
  day: "numeric",
  month: "short",
});

const monthDayYearFormatter = new Intl.DateTimeFormat("en-GB", {
  day: "numeric",
  month: "short",
  year: "numeric",
});

const exactLocalFormatter24 = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "medium",
  hour12: false,
});

const exactLocalFormatter12 = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "medium",
  hour12: true,
});

const exactUtcFormatter = new Intl.DateTimeFormat("en-GB", {
  dateStyle: "medium",
  timeStyle: "medium",
  timeZone: "UTC",
  hour12: false,
});

const numberFormatter = new Intl.NumberFormat();

const byteFormatter = new Intl.NumberFormat(undefined, {
  maximumFractionDigits: 1,
});

function toDate(value: string | number | Date): Date {
  return value instanceof Date ? value : new Date(value);
}

function startOfLocalDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

/**
 * Formats a timestamp relative to `now` following docs/UI.md section 4 item 11:
 * `now`, `3m`, `2h`, `Yesterday 14:03`, `12 Nov`, or `12 Nov 2025` for prior years.
 */
export function formatRelativeTime(
  value: string | number | Date,
  now: Date = new Date(),
  hour24 = true,
): string {
  const date = toDate(value);
  const timeMs = date.getTime();
  if (Number.isNaN(timeMs)) {
    return "";
  }

  const diffMs = now.getTime() - timeMs;

  if (diffMs < 45_000 && diffMs >= -60_000) {
    return "now";
  }

  if (diffMs >= 45_000 && diffMs < HOUR_MS) {
    const mins = Math.max(1, Math.floor(diffMs / MINUTE_MS));
    return `${mins}m`;
  }

  const dateDay = startOfLocalDay(date);
  const nowDay = startOfLocalDay(now);
  const dayDiff = Math.round((nowDay - dateDay) / (24 * HOUR_MS));

  if (diffMs >= HOUR_MS && diffMs < 24 * HOUR_MS && dayDiff === 0) {
    const hours = Math.floor(diffMs / HOUR_MS);
    return `${hours}h`;
  }

  if (dayDiff === 1) {
    return `Yesterday ${(hour24 ? timeFormatter24 : timeFormatter12).format(date)}`;
  }

  if (date.getFullYear() === now.getFullYear()) {
    return monthDayFormatter.format(date);
  }

  return monthDayYearFormatter.format(date);
}

export function formatExactTimestamp(
  value: string | number | Date,
  hour24 = true,
): {
  local: string;
  utc: string;
  combined: string;
} {
  const date = toDate(value);
  if (Number.isNaN(date.getTime())) {
    return { local: "", utc: "", combined: "" };
  }
  const local = (hour24 ? exactLocalFormatter24 : exactLocalFormatter12).format(date);
  const utc = `${exactUtcFormatter.format(date)} UTC`;
  return {
    local,
    utc,
    combined: `${local} (${utc})`,
  };
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB", "TB"] as const;
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  const unit = units[unitIndex] ?? "B";
  if (unitIndex === 0) {
    return `${Math.round(value)} ${unit}`;
  }
  return `${byteFormatter.format(value)} ${unit}`;
}

export function formatCount(count: number): string {
  return numberFormatter.format(count);
}

export function formatRetentionAge(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return "Unlimited";
  }
  if (seconds % 86_400 === 0) {
    const days = seconds / 86_400;
    return `${formatCount(days)} ${days === 1 ? "day" : "days"}`;
  }
  if (seconds % 3_600 === 0) {
    const hours = seconds / 3_600;
    return `${formatCount(hours)} ${hours === 1 ? "hour" : "hours"}`;
  }
  if (seconds % 60 === 0) {
    const minutes = seconds / 60;
    return `${formatCount(minutes)} ${minutes === 1 ? "minute" : "minutes"}`;
  }
  return `${formatCount(seconds)} ${seconds === 1 ? "second" : "seconds"}`;
}

export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) {
    return "0s";
  }
  const total = Math.floor(seconds);
  const days = Math.floor(total / 86_400);
  const hours = Math.floor((total % 86_400) / 3_600);
  const minutes = Math.floor((total % 3_600) / 60);
  const secs = total % 60;
  const parts: string[] = [];
  if (days > 0) {
    parts.push(`${days}d`);
  }
  if (hours > 0 || days > 0) {
    parts.push(`${hours}h`);
  }
  if (minutes > 0 || hours > 0 || days > 0) {
    parts.push(`${minutes}m`);
  }
  if (days === 0) {
    parts.push(`${secs}s`);
  }
  return parts.join(" ");
}

export function formatAddress(addr: Address): string {
  if (addr.name && addr.address) {
    return `${addr.name} <${addr.address}>`;
  }
  return addr.address || addr.name || "(unknown)";
}

export function getSenderLabel(addr: Address): string {
  return addr.name.trim() || addr.address.trim() || "(no sender)";
}

/**
 * getSenderInitials returns up to two initials for the sender avatar: the
 * first and last word of the display name, or of the address local part.
 * Splits by code point so accented letters and emoji stay whole; "?" when
 * there is no sender.
 */
export function getSenderInitials(addr: Address): string {
  const source = addr.name.trim() || addr.address.trim().split("@")[0] || "";
  const words = source.split(/[\s._+-]+/).filter((w) => /[\p{L}\p{N}]/u.test(w));
  const first = words[0];
  const last = words.length > 1 ? words[words.length - 1] : undefined;
  const initials = [first, last]
    .map((w) => (w ? (Array.from(w.replace(/^[^\p{L}\p{N}]+/u, ""))[0] ?? "") : ""))
    .join("");
  return initials ? initials.toLocaleUpperCase() : "?";
}

export const PROJECT_COLORS: ReadonlyArray<ProjectColor> = [
  "slate",
  "red",
  "orange",
  "amber",
  "lime",
  "green",
  "teal",
  "cyan",
  "blue",
  "violet",
  "fuchsia",
  "pink",
];

export const PROJECT_COLOR_DOT_CLASS: Record<ProjectColor, string> = {
  slate: "bg-[var(--project-slate)]",
  red: "bg-[var(--project-red)]",
  orange: "bg-[var(--project-orange)]",
  amber: "bg-[var(--project-amber)]",
  lime: "bg-[var(--project-lime)]",
  green: "bg-[var(--project-green)]",
  teal: "bg-[var(--project-teal)]",
  cyan: "bg-[var(--project-cyan)]",
  blue: "bg-[var(--project-blue)]",
  violet: "bg-[var(--project-violet)]",
  fuchsia: "bg-[var(--project-fuchsia)]",
  pink: "bg-[var(--project-pink)]",
};

export function isRasterImageMime(contentType: string): boolean {
  const normalized = contentType.split(";")[0]?.trim().toLowerCase() ?? "";
  return (
    normalized === "image/png" ||
    normalized === "image/jpeg" ||
    normalized === "image/gif" ||
    normalized === "image/webp"
  );
}
