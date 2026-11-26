// Computes WCAG 2.x contrast for the design token pairs in
// src/styles/globals.css and writes docs/ui-contrast.md.
//
//   node scripts/contrast.mjs          write the table; exit 1 if a check fails
//   node scripts/contrast.mjs --check  also fail if docs/ui-contrast.md is stale
//
// Checks: every required pair meets its minimum in light and dark; the two
// dark blocks (prefers-color-scheme and .dark) define identical tokens; the
// dark chrome layer (--sidebar) is lighter than the canvas. Plain Node, no
// dependencies. src/styles/contrast.test.ts runs it with --check.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const CSS_PATH = resolve(SCRIPT_DIR, "..", "src", "styles", "globals.css");
const DOC_PATH = resolve(SCRIPT_DIR, "..", "..", "docs", "ui-contrast.md");

// Dark chrome must be at least this much lighter (luminance ratio) than the
// canvas (--background) and the panes (--surface), so it reads as a layer.
const CHROME_MIN_RATIO = 1.25;

// [foreground, background, minimum ratio, use]. 4.5 = text, 3 = UI component
// or focus ring (WCAG 1.4.3 / 1.4.11).
const PAIRS = [
  ["--foreground", "--background", 4.5, "body text"],
  ["--foreground", "--surface", 4.5, "body text on panes and cards"],
  ["--foreground", "--muted", 4.5, "text on muted fills (hover, active rows)"],
  ["--muted-foreground", "--background", 4.5, "secondary text"],
  ["--muted-foreground", "--surface", 4.5, "secondary text on panes"],
  [
    "--muted-foreground",
    "--muted",
    4.5,
    "secondary text on muted fills (active sidebar count, A1)",
  ],
  ["--muted-foreground", "--secondary", 4.5, "secondary text on secondary fills"],
  ["--secondary-foreground", "--secondary", 4.5, "secondary button, neutral badge"],
  ["--accent-foreground", "--accent", 4.5, "label on accent (A6)"],
  ["--accent", "--surface", 4.5, "accent as text (links, active tab)"],
  ["--accent", "--background", 4.5, "accent as text on the canvas"],
  ["--accent-subtle-foreground", "--accent-subtle", 4.5, "accent badge, selected item text"],
  ["--foreground", "--accent-subtle", 4.5, "row text on a selected row"],
  ["--muted-foreground", "--accent-subtle", 4.5, "snippet on a selected row"],
  ["--accent", "--accent-subtle", 3, "unread dot or icon on a selected row"],
  ["--primary-foreground", "--primary", 4.5, "primary button label"],
  ["--primary-foreground", "--primary-hover", 4.5, "primary button label, hover"],
  ["--ring", "--background", 3, "focus ring on the canvas"],
  ["--ring", "--surface", 3, "focus ring on panes"],
  ["--ring", "--muted", 3, "focus ring on muted fills"],
  ["--input", "--surface", 3, "input and select border"],
  ["--input", "--background", 3, "input border on the canvas"],
  ["--success-foreground", "--success-subtle", 4.5, "success badge and alert text"],
  ["--success", "--success-subtle", 3, "success icon or dot on its badge"],
  ["--success", "--surface", 4.5, "success as text"],
  ["--warning-foreground", "--warning-subtle", 4.5, "warning badge and alert text"],
  ["--warning", "--warning-subtle", 3, "warning icon or dot on its badge"],
  ["--warning", "--surface", 4.5, "warning as text"],
  ["--info-foreground", "--info-subtle", 4.5, "info badge and alert text"],
  ["--info", "--info-subtle", 3, "info icon or dot on its badge"],
  ["--info", "--surface", 4.5, "info as text"],
  ["--destructive", "--destructive-subtle", 4.5, "destructive badge and alert text"],
  ["--destructive", "--surface", 4.5, "destructive as text"],
  ["--destructive", "--background", 4.5, "destructive as text on the canvas"],
  ["--destructive-foreground", "--destructive", 4.5, "destructive button label"],
  ["--destructive-foreground", "--destructive-hover", 4.5, "destructive button label, hover"],
  ["--sidebar-foreground", "--sidebar", 4.5, "chrome text"],
  ["--sidebar-muted", "--sidebar", 4.5, "chrome secondary text"],
  ["--sidebar-foreground", "--sidebar-active", 4.5, "chrome text on the active item"],
  ["--sidebar-active-foreground", "--sidebar-active", 4.5, "active chrome item"],
  ["--sidebar-muted", "--sidebar-active", 4.5, "chrome count on the active item"],
  ["--sidebar-foreground", "--sidebar-hover", 4.5, "chrome text on a hovered item"],
  ["--sidebar-active-foreground", "--sidebar-hover", 4.5, "chrome ghost button, hover"],
  ["--sidebar-muted", "--sidebar-hover", 4.5, "chrome count on a hovered item"],
  ["--sidebar-muted", "--sidebar-input", 4.5, "chrome field placeholder"],
  ["--sidebar-foreground", "--sidebar-input", 4.5, "chrome field text (search, project switcher)"],
  ["--sidebar-accent-foreground", "--sidebar-accent", 4.5, "chrome unread pill"],
  ["--sidebar-accent", "--sidebar", 3, "chrome unread pill against the rail"],
  ["--sidebar-ring", "--sidebar", 3, "focus ring on the chrome"],
  ["--sidebar-ring", "--sidebar-active", 3, "focus ring on the active chrome item"],
  ["--sidebar-ring", "--sidebar-hover", 3, "focus ring on a hovered chrome item"],
  ["--sidebar-ring", "--sidebar-input", 3, "focus ring on a chrome field"],
  ["--sidebar-accent", "--sidebar-active", 3, "chrome unread pill on the active item"],
  ["--sidebar-success", "--sidebar", 3, "chrome Live dot"],
  ["--sidebar-warning", "--sidebar", 3, "chrome Reconnecting dot"],
  ["--border", "--surface", 0, "decorative divider (not required)"],
  ["--sidebar-border", "--sidebar", 0, "decorative chrome divider (not required)"],
];

/** Returns the body of the first top-level block whose selector matches. */
function findBlock(css, selectorTest) {
  let depth = 0;
  let selectorStart = 0;
  for (let i = 0; i < css.length; i++) {
    const ch = css[i];
    if (ch === "{") {
      const selector = css.slice(selectorStart, i).trim();
      if (depth === 0 && selectorTest(selector)) {
        return blockBody(css, i);
      }
      depth++;
      selectorStart = i + 1;
    } else if (ch === "}") {
      depth--;
      selectorStart = i + 1;
    } else if (ch === ";") {
      selectorStart = i + 1;
    }
  }
  throw new Error(`contrast: block not found in ${CSS_PATH}`);
}

function blockBody(css, openIndex) {
  let depth = 0;
  for (let i = openIndex; i < css.length; i++) {
    if (css[i] === "{") depth++;
    if (css[i] === "}") {
      depth--;
      if (depth === 0) return css.slice(openIndex + 1, i);
    }
  }
  throw new Error("contrast: unbalanced braces");
}

/** Collects custom properties declared directly in a block (not in nested blocks). */
function declarations(body) {
  const out = new Map();
  let depth = 0;
  let flat = "";
  for (const ch of body) {
    if (ch === "{") depth++;
    if (depth === 0) flat += ch;
    if (ch === "}") depth--;
  }
  for (const m of flat.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    out.set(m[1], m[2].trim());
  }
  return out;
}

function resolveValue(tokens, name, seen = new Set()) {
  const raw = tokens.get(name);
  if (raw === undefined) throw new Error(`contrast: token ${name} is not defined`);
  const ref = raw.match(/^var\((--[\w-]+)\)$/);
  if (!ref) return raw;
  if (seen.has(name)) throw new Error(`contrast: var() cycle at ${name}`);
  seen.add(name);
  return resolveValue(tokens, ref[1], seen);
}

function hexToRgb(hex) {
  const m = hex.match(/^#([0-9a-f]{6})$/i);
  if (!m) throw new Error(`contrast: ${hex} is not a 6-digit hex colour`);
  const n = Number.parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function luminance(hex) {
  const [r, g, b] = hexToRgb(hex).map((v) => {
    const c = v / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function ratio(a, b) {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

function compute(source) {
  const css = source.replace(/\/\*[\s\S]*?\*\//g, "");
  const light = declarations(findBlock(css, (s) => s === ":root"));
  const media = declarations(blockBody(css, css.indexOf("{", css.indexOf(":root:not(.light)"))));
  const darkClass = declarations(findBlock(css, (s) => s === ".dark"));
  const errors = [];

  const mediaKeys = [...media.keys()].sort();
  const classKeys = [...darkClass.keys()].sort();
  for (const key of new Set([...mediaKeys, ...classKeys])) {
    if (media.get(key) !== darkClass.get(key)) {
      errors.push(
        `dark blocks differ for ${key}: media ${media.get(key) ?? "(missing)"} vs .dark ${darkClass.get(key) ?? "(missing)"}`,
      );
    }
  }

  const themes = [
    ["Light", light],
    ["Dark", new Map([...light, ...darkClass])],
  ];
  const sections = [];
  let failing = 0;
  for (const [name, tokens] of themes) {
    const rows = [];
    for (const [fg, bg, min, use] of PAIRS) {
      const fgHex = resolveValue(tokens, fg);
      const bgHex = resolveValue(tokens, bg);
      const r = ratio(fgHex, bgHex);
      const pass = r >= min;
      let result = "info";
      if (min > 0) result = pass ? "PASS" : "FAIL";
      if (min > 0 && !pass) {
        failing++;
        errors.push(`${name}: ${fg} on ${bg} is ${r.toFixed(2)}, needs ${min}`);
      }
      rows.push(
        `| \`${fg}\` ${fgHex} | \`${bg}\` ${bgHex} | ${r.toFixed(2)} | ${min > 0 ? min : "-"} | ${result} | ${use} |`,
      );
    }
    sections.push(
      `## ${name}\n\n| Foreground | Background | Ratio | Required | Result | Use |\n|---|---|---:|---:|---|---|\n${rows.join("\n")}\n`,
    );
  }

  const dark = themes[1][1];
  const chrome = resolveValue(dark, "--sidebar");
  const chromeRows = [];
  for (const canvas of ["--background", "--surface"]) {
    const canvasHex = resolveValue(dark, canvas);
    const r = ratio(chrome, canvasHex);
    const lighter = luminance(chrome) > luminance(canvasHex);
    const pass = lighter && r >= CHROME_MIN_RATIO;
    if (!pass) {
      failing++;
      errors.push(`Dark: --sidebar ${chrome} is not lighter than ${canvas} by ${CHROME_MIN_RATIO}`);
    }
    chromeRows.push(
      `| \`--sidebar\` ${chrome} | \`${canvas}\` ${canvasHex} | ${r.toFixed(2)} | ${CHROME_MIN_RATIO} | ${pass ? "PASS" : "FAIL"} |`,
    );
  }

  const markdown = `# UI token contrast

Generated by \`web/scripts/contrast.mjs\` from \`web/src/styles/globals.css\` (WCAG 2.x relative luminance). Do not edit by hand: run \`node web/scripts/contrast.mjs\` after changing a token. A Vitest test fails if this file is stale or a required pair fails.

Required: 4.5 for text, 3 for UI components, icons and focus rings (WCAG 1.4.3 and 1.4.11). Rows marked "info" are decorative and not required. Token meanings: \`--success/warning/info-foreground\` are text on the matching \`-subtle\` fill; \`--destructive-foreground\` is the label on the solid \`--destructive\` fill; destructive text on \`--destructive-subtle\` uses \`--destructive\`.

${sections.join("\n")}
## Dark chrome layer

The chrome (top bar and sidebar) must be visibly lighter than the canvas in the dark theme: luminance ratio ${CHROME_MIN_RATIO} or more, chrome lighter.

| Chrome | Canvas | Ratio | Required | Result |
|---|---|---:|---:|---|
${chromeRows.join("\n")}

**Required rows failing: ${failing}.**
`;
  return { markdown, errors };
}

function main() {
  const check = process.argv.includes("--check");
  const { markdown, errors } = compute(readFileSync(CSS_PATH, "utf8"));
  if (check) {
    let current = "";
    try {
      current = readFileSync(DOC_PATH, "utf8");
    } catch {
      current = "";
    }
    if (current !== markdown) {
      errors.push("docs/ui-contrast.md is stale: run `node web/scripts/contrast.mjs`");
    }
  } else {
    writeFileSync(DOC_PATH, markdown);
  }
  for (const err of errors) {
    console.error(`contrast: ${err}`);
  }
  if (errors.length > 0) {
    process.exit(1);
  }
  console.log(`contrast: ${PAIRS.length * 2} pairs checked, all required pairs pass`);
}

main();
