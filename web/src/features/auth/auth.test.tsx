import { describe, expect, it } from "vitest";
import { getCsrfToken, sanitizeReturnTo, setCsrfToken } from "@/lib/api";

describe("auth API helpers", () => {
  it("stores CSRF token strictly in memory", () => {
    setCsrfToken("csrf-test-token-123");
    expect(getCsrfToken()).toBe("csrf-test-token-123");
    expect(window.localStorage.length).toBe(0);
    setCsrfToken("");
    expect(getCsrfToken()).toBe("");
  });

  it("sanitizes returnTo paths against open redirects", () => {
    expect(sanitizeReturnTo(undefined)).toBe("/");
    expect(sanitizeReturnTo("")).toBe("/");
    expect(sanitizeReturnTo("/p/shop?q=invoice")).toBe("/p/shop?q=invoice");
    expect(sanitizeReturnTo("/account/security")).toBe("/account/security");
    expect(sanitizeReturnTo("https://evil.example.com")).toBe("/");
    expect(sanitizeReturnTo("//evil.example.com")).toBe("/");
    expect(sanitizeReturnTo("/\\evil.example.com")).toBe("/");
    expect(sanitizeReturnTo("/login")).toBe("/");
    expect(sanitizeReturnTo("/login?returnTo=/")).toBe("/");
    expect(sanitizeReturnTo("/setup")).toBe("/");
  });
});
