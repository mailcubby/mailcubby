import { defaultParseSearch } from "@tanstack/react-router";
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

  // Browsers strip tab/CR/LF and treat "\" as "/" while parsing URLs, so these
  // would otherwise resolve to another origin after login (open redirect).
  it.each([
    "/\t/evil.example/phish?x=1",
    "/\n/evil.example?x",
    "/\r/evil.example?x",
    "/\r\n/evil.example",
    "/\t\\evil.example?x",
    "\t//evil.example",
    "/\u007f/evil.example",
    "/\u0000/evil.example",
    "/\\evil.example",
    "/\\\\evil.example",
    "\\/evil.example",
    "/\\/evil.example",
    "//evil.example",
    "///evil.example",
    "https://evil.example",
    "javascript:alert(1)",
    "evil.example",
    "/p/../login",
    "/./setup?x=1",
  ])("rejects unsafe returnTo %j", (raw) => {
    const target = sanitizeReturnTo(raw);
    expect(target).toBe("/");
  });

  it("rejects the tab-smuggled returnTo after router search parsing", () => {
    const search = defaultParseSearch("?returnTo=%2F%09%2Fevil.example%2Fphish%3Fx%3D1") as {
      returnTo: string;
    };
    expect(search.returnTo).toBe("/\t/evil.example/phish?x=1");
    expect(sanitizeReturnTo(search.returnTo)).toBe("/");
  });

  it.each([
    ["/invite/tok-abc_123", "/invite/tok-abc_123"],
    ["/p/shop", "/p/shop"],
    ["/p/shop/m/01JZ0000000000000000000000?x=1", "/p/shop/m/01JZ0000000000000000000000?x=1"],
    ["/account/security", "/account/security"],
    ["/p/shop?q=from%3Aalice#top", "/p/shop?q=from%3Aalice#top"],
    ["  /p/shop  ", "/p/shop"],
  ])("keeps same-origin returnTo %j", (raw, want) => {
    const target = sanitizeReturnTo(raw);
    expect(target).toBe(want);
    expect(new URL(target, window.location.origin).origin).toBe(window.location.origin);
  });
});
