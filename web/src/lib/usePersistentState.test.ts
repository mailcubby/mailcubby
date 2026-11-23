import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  readPersistentValue,
  STORAGE_PREFIX,
  usePersistentState,
  writePersistentValue,
} from "@/lib/usePersistentState";

describe("usePersistentState", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns default value when storage is empty and persists updates under mailcubby: prefix", () => {
    const { result } = renderHook(() => usePersistentState("density", "compact"));

    expect(result.current[0]).toBe("compact");
    expect(window.localStorage.getItem(`${STORAGE_PREFIX}density`)).toBeNull();

    act(() => {
      result.current[1]("comfortable");
    });

    expect(result.current[0]).toBe("comfortable");
    expect(window.localStorage.getItem("mailcubby:density")).toBe('"comfortable"');
  });

  it("supports functional updates and does not double-prefix keys", () => {
    const { result } = renderHook(() => usePersistentState("mailcubby:counter", 1));

    act(() => {
      result.current[1]((prev) => prev + 4);
    });

    expect(result.current[0]).toBe(5);
    expect(window.localStorage.getItem("mailcubby:counter")).toBe("5");
    expect(window.localStorage.getItem("mailcubby:mailcubby:counter")).toBeNull();
  });

  it("reads previously persisted values and falls back on invalid JSON or failed validator", () => {
    window.localStorage.setItem("mailcubby:view", '"list"');
    const isView = (v: unknown): v is "grid" | "list" => v === "grid" || v === "list";

    const { result: validResult } = renderHook(() => usePersistentState("view", "grid", isView));
    expect(validResult.current[0]).toBe("list");

    window.localStorage.setItem("mailcubby:view", '"invalid-value"');
    const { result: invalidValueResult } = renderHook(() =>
      usePersistentState("view", "grid", isView),
    );
    expect(invalidValueResult.current[0]).toBe("grid");

    window.localStorage.setItem("mailcubby:broken", "{not-json");
    expect(readPersistentValue("broken", "fallback")).toBe("fallback");
  });

  it("syncs state across tabs when a storage event fires", () => {
    const { result } = renderHook(() => usePersistentState("sidebar", false));
    expect(result.current[0]).toBe(false);

    act(() => {
      window.localStorage.setItem("mailcubby:sidebar", "true");
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "mailcubby:sidebar",
          newValue: "true",
        }),
      );
    });

    expect(result.current[0]).toBe(true);

    act(() => {
      window.localStorage.clear();
      window.dispatchEvent(new StorageEvent("storage", { key: null }));
    });

    expect(result.current[0]).toBe(false);
  });

  it("handles storage errors gracefully without throwing", () => {
    const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });

    expect(() => writePersistentValue("big", { a: 1 })).not.toThrow();
    expect(spy).toHaveBeenCalled();
  });
});
