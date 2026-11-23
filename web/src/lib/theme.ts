import { useCallback, useEffect, useState } from "react";

export type ThemePreference = "light" | "dark" | "system";
export type ResolvedTheme = "light" | "dark";

export const THEME_STORAGE_KEY = "mailcubby:theme";

function isThemePreference(value: unknown): value is ThemePreference {
  return value === "light" || value === "dark" || value === "system";
}

export function getSystemTheme(): ResolvedTheme {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return "light";
  }
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function readStoredTheme(): ThemePreference {
  if (typeof window === "undefined") {
    return "system";
  }
  try {
    const raw = window.localStorage.getItem(THEME_STORAGE_KEY);
    if (!raw) {
      return "system";
    }
    try {
      const parsed: unknown = JSON.parse(raw);
      if (isThemePreference(parsed)) {
        return parsed;
      }
    } catch {
      if (isThemePreference(raw)) {
        return raw;
      }
    }
  } catch {
    // Ignore storage errors (e.g. blocked site data).
  }
  return "system";
}

export function applyTheme(pref: ThemePreference): ResolvedTheme {
  const resolved: ResolvedTheme = pref === "system" ? getSystemTheme() : pref;
  if (typeof document !== "undefined") {
    const meta = document.querySelector('meta[name="color-scheme"]');
    if (meta) {
      meta.setAttribute("content", pref === "system" ? "light dark" : pref);
    }
    const root = document.documentElement;
    root.classList.toggle("dark", resolved === "dark");
    root.classList.toggle("light", pref === "light");
  }
  return resolved;
}

export function writeStoredTheme(pref: ThemePreference): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    if (pref === "system") {
      window.localStorage.removeItem(THEME_STORAGE_KEY);
    } else {
      window.localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify(pref));
    }
  } catch {
    // Ignore storage errors while still applying the theme in memory.
  }
}

export function useTheme() {
  const [theme, setThemeState] = useState<ThemePreference>(() => readStoredTheme());
  const [systemTheme, setSystemTheme] = useState<ResolvedTheme>(() => getSystemTheme());

  const resolvedTheme: ResolvedTheme = theme === "system" ? systemTheme : theme;

  const setTheme = useCallback((next: ThemePreference) => {
    setThemeState(next);
    writeStoredTheme(next);
    applyTheme(next);
  }, []);

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    const media =
      typeof window.matchMedia === "function"
        ? window.matchMedia("(prefers-color-scheme: dark)")
        : null;

    const handleMediaChange = () => {
      const nextSystem = getSystemTheme();
      setSystemTheme(nextSystem);
      if (theme === "system") {
        applyTheme("system");
      }
    };

    const handleStorage = (event: StorageEvent) => {
      if (event.key === THEME_STORAGE_KEY || event.key === null) {
        const synced = readStoredTheme();
        setThemeState(synced);
        applyTheme(synced);
      }
    };

    media?.addEventListener("change", handleMediaChange);
    window.addEventListener("storage", handleStorage);
    return () => {
      media?.removeEventListener("change", handleMediaChange);
      window.removeEventListener("storage", handleStorage);
    };
  }, [theme]);

  return {
    theme,
    resolvedTheme,
    systemTheme,
    setTheme,
  };
}
