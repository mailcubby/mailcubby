import type { HighlighterCore, ThemeRegistration } from "shiki/core";

export interface HighlightToken {
  offset: number;
  content: string;
  color: string | undefined;
  fontStyle: number | undefined;
}

export type HighlightLine = ReadonlyArray<HighlightToken>;
export type SupportedLang = "html" | "css" | "json";

const LIGHT_THEME: ThemeRegistration = {
  name: "mailcubby-light",
  type: "light",
  colors: {
    "editor.background": "#ffffff",
    "editor.foreground": "#18181b",
  },
  tokenColors: [
    {
      scope: ["comment", "punctuation.definition.comment"],
      settings: { foreground: "#71717a", fontStyle: "italic" },
    },
    {
      scope: ["entity.name.tag", "support.type.property-name.json"],
      settings: { foreground: "#1d4ed8" },
    },
    {
      scope: ["entity.other.attribute-name", "support.type.property-name.css"],
      settings: { foreground: "#6d28d9" },
    },
    {
      scope: ["string", "string.quoted", "meta.attribute-value"],
      settings: { foreground: "#15803d" },
    },
    {
      scope: ["constant.numeric", "constant.language", "keyword"],
      settings: { foreground: "#b45309" },
    },
    {
      scope: ["punctuation.definition.tag", "punctuation"],
      settings: { foreground: "#52525b" },
    },
  ],
};

const DARK_THEME: ThemeRegistration = {
  name: "mailcubby-dark",
  type: "dark",
  colors: {
    "editor.background": "#18181b",
    "editor.foreground": "#f4f4f5",
  },
  tokenColors: [
    {
      scope: ["comment", "punctuation.definition.comment"],
      settings: { foreground: "#a1a1aa", fontStyle: "italic" },
    },
    {
      scope: ["entity.name.tag", "support.type.property-name.json"],
      settings: { foreground: "#60a5fa" },
    },
    {
      scope: ["entity.other.attribute-name", "support.type.property-name.css"],
      settings: { foreground: "#c084fc" },
    },
    {
      scope: ["string", "string.quoted", "meta.attribute-value"],
      settings: { foreground: "#4ade80" },
    },
    {
      scope: ["constant.numeric", "constant.language", "keyword"],
      settings: { foreground: "#fbbf24" },
    },
    {
      scope: ["punctuation.definition.tag", "punctuation"],
      settings: { foreground: "#a1a1aa" },
    },
  ],
};

let highlighterPromise: Promise<HighlighterCore> | null = null;

function getHighlighter(): Promise<HighlighterCore> {
  if (!highlighterPromise) {
    highlighterPromise = (async () => {
      const [
        { createHighlighterCore },
        { createJavaScriptRegexEngine },
        htmlLang,
        cssLang,
        jsonLang,
      ] = await Promise.all([
        import("shiki/core"),
        import("shiki/engine/javascript"),
        import("shiki/langs/html.mjs"),
        import("shiki/langs/css.mjs"),
        import("shiki/langs/json.mjs"),
      ]);

      return createHighlighterCore({
        themes: [LIGHT_THEME, DARK_THEME],
        langs: [htmlLang.default, cssLang.default, jsonLang.default],
        engine: createJavaScriptRegexEngine(),
      });
    })();
  }
  return highlighterPromise;
}

export const MAX_HIGHLIGHT_BYTES = 512 * 1024;

export async function highlightCodeToLines(
  code: string,
  lang: SupportedLang,
  theme: "light" | "dark",
): Promise<HighlightLine[]> {
  if (code.length > MAX_HIGHLIGHT_BYTES) {
    return code
      .split(/\r?\n/)
      .map((line) => [{ offset: 0, content: line, color: undefined, fontStyle: undefined }]);
  }

  const highlighter = await getHighlighter();
  const result = highlighter.codeToTokens(code, {
    lang,
    theme: theme === "dark" ? "mailcubby-dark" : "mailcubby-light",
  });

  return result.tokens.map((line) =>
    line.map((token) => ({
      offset: token.offset,
      content: token.content,
      color: token.color,
      fontStyle: token.fontStyle,
    })),
  );
}
