(() => {
  const STORAGE_KEY = "mailcubby:theme";

  let pref = "system";
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (raw) {
      let parsed = raw;
      try {
        const json = JSON.parse(raw);
        if (typeof json === "string") {
          parsed = json;
        }
      } catch {
        // Accept plain string values as well as JSON-encoded strings.
      }
      if (parsed === "light" || parsed === "dark" || parsed === "system") {
        pref = parsed;
      }
    }
  } catch {
    // Ignore storage errors (e.g. blocked site data).
  }

  const meta = document.querySelector('meta[name="color-scheme"]');
  if (meta) {
    meta.setAttribute("content", pref === "system" ? "light dark" : pref);
  }

  const systemDark =
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches;
  const resolved = pref === "system" ? (systemDark ? "dark" : "light") : pref;

  const root = document.documentElement;
  root.classList.toggle("dark", resolved === "dark");
  root.classList.toggle("light", pref === "light");
})();
