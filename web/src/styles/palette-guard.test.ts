// @vitest-environment node
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Phase 15a rule (docs/UI.md section 1): colours in components come only from
// the tokens in globals.css. This test fails on raw Tailwind palette classes
// (bg-amber-500, dark:text-emerald-300, bg-white, bg-black/40, ...), hex
// colours and CSS colour functions in web/src/**/*.tsx. Tests are excluded.
// Project colours use bg-project-* tokens and never match.

const SRC_DIR = fileURLToPath(new URL("..", import.meta.url));

const PALETTE =
  "slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose";
const UTILITIES =
  "bg|text|border(?:-[xytrblse])?|ring(?:-offset)?|outline|fill|stroke|from|via|to|divide|decoration|shadow|inset-shadow|caret|placeholder|accent";

// A class token starts at a quote, whitespace, backtick or variant colon.
const RAW_PALETTE_CLASS = new RegExp(
  `(?<![\\w/-])(?:${UTILITIES})-(?:(?:${PALETTE})-\\d{2,3}|white|black)(?:/\\d+)?(?![\\w-])`,
  "g",
);
const HEX_COLOUR = /(?<![\w&#])#([0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})(?![\w-])/g;
const COLOUR_FUNCTION = /\b(?:rgba?|hsla?|oklch|oklab|lab|lch)\(/g;

function listTsx(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...listTsx(path));
    } else if (entry.name.endsWith(".tsx") && !entry.name.endsWith(".test.tsx")) {
      out.push(path);
    }
  }
  return out;
}

function findColourViolations(source: string): string[] {
  const found: string[] = [];
  for (const m of source.matchAll(RAW_PALETTE_CLASS)) {
    found.push(m[0]);
  }
  for (const m of source.matchAll(HEX_COLOUR)) {
    const digitsOnly = /^\d+$/.test(m[1] ?? "");
    const before = source[(m.index ?? 0) - 1];
    // "#123" in copy (issue or order numbers) is text, not a colour, unless it
    // sits in an arbitrary Tailwind value such as bg-[#123].
    if (!digitsOnly || before === "[") {
      found.push(m[0]);
    }
  }
  for (const m of source.matchAll(COLOUR_FUNCTION)) {
    found.push(m[0]);
  }
  return found;
}

describe("colour tokens only", () => {
  it("detects raw palette classes, hex colours and colour functions", () => {
    expect(
      findColourViolations(
        'cn("bg-amber-500/10 dark:text-emerald-300", "bg-white", "hover:bg-black/40", "bg-[#fafafa]", "rgb(0 0 0)")',
      ),
    ).toEqual([
      "bg-amber-500/10",
      "text-emerald-300",
      "bg-white",
      "bg-black/40",
      "#fafafa",
      "rgb(",
    ]);
    expect(
      findColourViolations(
        'cn("bg-project-amber bg-warning-subtle text-success border-input", "Order #123", "&#123;")',
      ),
    ).toEqual([]);
  });

  it("finds no raw colours in web/src/**/*.tsx", () => {
    const files = listTsx(SRC_DIR);
    expect(files.length).toBeGreaterThan(50);
    const violations: string[] = [];
    for (const file of files) {
      const hits = findColourViolations(readFileSync(file, "utf8"));
      if (hits.length > 0) {
        violations.push(`${relative(SRC_DIR, file)}: ${hits.join(", ")}`);
      }
    }
    expect(violations).toEqual([]);
  });
});
