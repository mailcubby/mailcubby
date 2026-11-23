import { describe, expect, it } from "vitest";
import {
  formatAddress,
  formatBytes,
  formatExactTimestamp,
  formatRelativeTime,
  getSenderLabel,
  isRasterImageMime,
} from "@/lib/format";

describe("formatRelativeTime", () => {
  const now = new Date(2026, 10, 23, 15, 30, 0); // 2026-11-23 15:30:00 local

  it("formats recent timestamps under 45 seconds as now", () => {
    const tenSecondsAgo = new Date(now.getTime() - 10_000);
    expect(formatRelativeTime(tenSecondsAgo, now)).toBe("now");

    const slightlyFuture = new Date(now.getTime() + 5_000);
    expect(formatRelativeTime(slightlyFuture, now)).toBe("now");
  });

  it("formats minutes and same-day hours compactly", () => {
    const threeMinutesAgo = new Date(now.getTime() - 3 * 60_000);
    expect(formatRelativeTime(threeMinutesAgo, now)).toBe("3m");

    const fortyFiveSecondsAgo = new Date(now.getTime() - 45_000);
    expect(formatRelativeTime(fortyFiveSecondsAgo, now)).toBe("1m");

    const twoHoursAgo = new Date(now.getTime() - 2 * 3_600_000);
    expect(formatRelativeTime(twoHoursAgo, now)).toBe("2h");
  });

  it("formats yesterday with 24h HH:MM time", () => {
    const yesterday = new Date(2026, 10, 22, 14, 3, 0);
    expect(formatRelativeTime(yesterday, now)).toBe("Yesterday 14:03");
  });

  it("formats earlier dates in the same year as day and short month", () => {
    const earlier = new Date(2026, 10, 12, 9, 15, 0);
    expect(formatRelativeTime(earlier, now)).toBe("12 Nov");
  });

  it("includes year for timestamps from a previous year", () => {
    const lastYear = new Date(2025, 10, 12, 9, 15, 0);
    expect(formatRelativeTime(lastYear, now)).toBe("12 Nov 2025");
  });

  it("returns empty string for invalid dates", () => {
    expect(formatRelativeTime("not-a-date", now)).toBe("");
  });
});

describe("formatExactTimestamp", () => {
  it("formats local, UTC, and combined strings", () => {
    const result = formatExactTimestamp("2026-11-23T14:05:09.000Z");
    expect(result.local.length).toBeGreaterThan(0);
    expect(result.utc).toContain("UTC");
    expect(result.utc).toContain("14:05:09");
    expect(result.combined).toBe(`${result.local} (${result.utc})`);
  });

  it("returns empty fields for invalid timestamps", () => {
    expect(formatExactTimestamp("invalid")).toEqual({ local: "", utc: "", combined: "" });
  });
});

describe("formatBytes and address helpers", () => {
  it("formats byte sizes cleanly", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(-5)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(2 * 1024 * 1024)).toBe("2 MB");
  });

  it("formats addresses and sender labels", () => {
    expect(formatAddress({ name: "Alice", address: "alice@example.test" })).toBe(
      "Alice <alice@example.test>",
    );
    expect(formatAddress({ name: "", address: "alice@example.test" })).toBe("alice@example.test");
    expect(getSenderLabel({ name: "Alice", address: "alice@example.test" })).toBe("Alice");
    expect(getSenderLabel({ name: " ", address: "alice@example.test" })).toBe("alice@example.test");
    expect(getSenderLabel({ name: "", address: "" })).toBe("(no sender)");
  });

  it("identifies allowlisted raster image MIME types only", () => {
    expect(isRasterImageMime("image/png")).toBe(true);
    expect(isRasterImageMime("image/jpeg; charset=binary")).toBe(true);
    expect(isRasterImageMime("image/gif")).toBe(true);
    expect(isRasterImageMime("image/webp")).toBe(true);
    expect(isRasterImageMime("image/svg+xml")).toBe(false);
    expect(isRasterImageMime("application/pdf")).toBe(false);
    expect(isRasterImageMime("text/html")).toBe(false);
  });
});
