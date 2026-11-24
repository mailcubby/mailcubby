import { describe, expect, it } from "vitest";
import { formatRetentionAge, formatUptime } from "@/lib/format";
import {
  combineAgeSeconds,
  combineStorageBytes,
  parseNonNegativeInt,
  splitAgeSeconds,
  splitStorageBytes,
} from "@/lib/retentionUnits";

describe("retentionUnits", () => {
  it("splits and combines age seconds cleanly", () => {
    expect(splitAgeSeconds(0)).toEqual({ value: "0", unit: "days" });
    expect(splitAgeSeconds(30 * 86_400)).toEqual({ value: "30", unit: "days" });
    expect(splitAgeSeconds(12 * 3_600)).toEqual({ value: "12", unit: "hours" });
    expect(combineAgeSeconds("7", "days")).toBe(7 * 86_400);
    expect(combineAgeSeconds("24", "hours")).toBe(86_400);
    expect(combineAgeSeconds("0", "days")).toBe(0);
    expect(combineAgeSeconds("-1", "days")).toBeNull();
    expect(combineAgeSeconds("", "days")).toBeNull();
  });

  it("splits and combines storage bytes cleanly", () => {
    expect(splitStorageBytes(0)).toEqual({ value: "0", unit: "MB" });
    expect(splitStorageBytes(1024 ** 3)).toEqual({ value: "1", unit: "GB" });
    expect(splitStorageBytes(250 * 1024 * 1024)).toEqual({ value: "250", unit: "MB" });
    expect(splitStorageBytes(512 * 1024)).toEqual({ value: "512", unit: "KB" });
    expect(combineStorageBytes("2", "GB")).toBe(2 * 1024 ** 3);
    expect(combineStorageBytes("50", "MB")).toBe(50 * 1024 ** 2);
    expect(combineStorageBytes("0", "MB")).toBe(0);
    expect(combineStorageBytes("-5", "MB")).toBeNull();
  });

  it("parses non-negative integers strictly", () => {
    expect(parseNonNegativeInt("0")).toBe(0);
    expect(parseNonNegativeInt("5000")).toBe(5000);
    expect(parseNonNegativeInt("-1")).toBeNull();
    expect(parseNonNegativeInt("1.5")).toBeNull();
    expect(parseNonNegativeInt("")).toBeNull();
  });

  it("formats retention age and uptime", () => {
    expect(formatRetentionAge(0)).toBe("Unlimited");
    expect(formatRetentionAge(86_400)).toBe("1 day");
    expect(formatRetentionAge(30 * 86_400)).toBe("30 days");
    expect(formatRetentionAge(3_600)).toBe("1 hour");
    expect(formatRetentionAge(7_200)).toBe("2 hours");
    expect(formatUptime(42)).toBe("42s");
    expect(formatUptime(125)).toBe("2m 5s");
    expect(formatUptime(3_661)).toBe("1h 1m 1s");
    expect(formatUptime(90_060)).toBe("1d 1h 1m");
  });
});
