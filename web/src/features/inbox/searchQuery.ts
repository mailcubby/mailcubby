import type { SMTPCredential, TagSummary } from "@/lib/api";

export type KnownOperator =
  | "from"
  | "to"
  | "subject"
  | "tag"
  | "credential"
  | "has"
  | "is"
  | "before"
  | "after";

const KNOWN_OPERATORS = new Set<string>([
  "from",
  "to",
  "subject",
  "tag",
  "credential",
  "has",
  "is",
  "before",
  "after",
]);

const VALID_HAS_VALUES = new Set(["attachment", "html", "text"]);
const VALID_IS_VALUES = new Set(["unread", "read"]);

export interface SearchChip {
  raw: string;
  key: KnownOperator;
  value: string;
  negated: boolean;
  start: number;
  end: number;
}

export interface AutocompleteSuggestion {
  id: string;
  label: string;
  description: string;
  insertText: string;
  complete: boolean;
}

export interface AutocompleteContext {
  tags: ReadonlyArray<TagSummary>;
  senders: ReadonlyArray<string>;
  credentials?: ReadonlyArray<SMTPCredential>;
  todayISO?: string;
}

export interface AutocompleteResult {
  replaceStart: number;
  replaceEnd: number;
  suggestions: AutocompleteSuggestion[];
}

const OPERATOR_TEMPLATES: ReadonlyArray<{
  op: string;
  label: string;
  description: string;
  complete: boolean;
}> = [
  { op: "from:", label: "from:", description: "Sender name or address", complete: false },
  { op: "to:", label: "to:", description: "Recipient (To, Cc, Bcc)", complete: false },
  { op: "subject:", label: "subject:", description: "Subject text or phrase", complete: false },
  { op: "tag:", label: "tag:", description: "Has tag", complete: false },
  { op: "-tag:", label: "-tag:", description: "Exclude tag", complete: false },
  {
    op: "credential:",
    label: "credential:",
    description: "SMTP credential label",
    complete: false,
  },
  { op: "is:unread", label: "is:unread", description: "Unread messages", complete: true },
  { op: "is:read", label: "is:read", description: "Read messages", complete: true },
  {
    op: "has:attachment",
    label: "has:attachment",
    description: "Messages with attachments",
    complete: true,
  },
  { op: "has:html", label: "has:html", description: "Messages with HTML part", complete: true },
  { op: "has:text", label: "has:text", description: "Messages with text part", complete: true },
  { op: "after:", label: "after:", description: "Received on or after date", complete: false },
  { op: "before:", label: "before:", description: "Received before date", complete: false },
];

const TAG_NAME_RE = /^[\p{L}\p{N}_.-]{1,40}$/u;

export function isValidTagName(name: string): boolean {
  return TAG_NAME_RE.test(name.trim());
}

/**
 * Extracts recognized search operator tokens from a raw query string so they
 * can be displayed as removable filter chips (UI.md 3.4, SPEC FR-081).
 */
export function extractOperatorChips(query: string): SearchChip[] {
  const chips: SearchChip[] = [];
  let i = 0;
  const n = query.length;

  while (i < n) {
    while (i < n && /\s/.test(query[i] ?? "")) {
      i += 1;
    }
    if (i >= n) {
      break;
    }

    const tokenStart = i;
    let negated = false;
    if (query[i] === "-" && i + 1 < n && !/\s/.test(query[i + 1] ?? "")) {
      negated = true;
      i += 1;
    }

    if (query[i] === '"') {
      i = advanceQuoted(query, i);
      continue;
    }

    const opStart = i;
    while (i < n && /[a-zA-Z]/.test(query[i] ?? "")) {
      i += 1;
    }

    if (i > opStart && query[i] === ":") {
      const opKey = query.slice(opStart, i).toLowerCase();
      if (KNOWN_OPERATORS.has(opKey)) {
        const valStart = i + 1;
        let value = "";
        let tokenEnd = valStart;

        if (query[valStart] === '"') {
          tokenEnd = advanceQuoted(query, valStart);
          value = unquote(query.slice(valStart, tokenEnd));
        } else {
          while (tokenEnd < n && !/\s/.test(query[tokenEnd] ?? "")) {
            tokenEnd += 1;
          }
          value = query.slice(valStart, tokenEnd);
        }

        i = tokenEnd;
        if (value !== "" && isValidOperatorValue(opKey as KnownOperator, value)) {
          chips.push({
            raw: query.slice(tokenStart, tokenEnd),
            key: opKey as KnownOperator,
            value,
            negated,
            start: tokenStart,
            end: tokenEnd,
          });
        }
        continue;
      }
    }

    while (i < n && !/\s/.test(query[i] ?? "")) {
      if (query[i] === '"') {
        i = advanceQuoted(query, i);
      } else {
        i += 1;
      }
    }
  }

  return chips;
}

function isValidOperatorValue(op: KnownOperator, value: string): boolean {
  if (op === "has") {
    return VALID_HAS_VALUES.has(value.toLowerCase());
  }
  if (op === "is") {
    return VALID_IS_VALUES.has(value.toLowerCase());
  }
  return true;
}

function advanceQuoted(s: string, start: number): number {
  let i = start + 1;
  while (i < s.length) {
    if (s[i] === "\\" && i + 1 < s.length) {
      i += 2;
      continue;
    }
    if (s[i] === '"') {
      return i + 1;
    }
    i += 1;
  }
  return s.length;
}

function unquote(raw: string): string {
  const inner = raw.startsWith('"')
    ? raw.slice(1, raw.endsWith('"') && raw.length > 1 ? -1 : undefined)
    : raw;
  return inner.replace(/\\(["\\])/g, "$1");
}

export function removeChipFromQuery(query: string, chip: SearchChip): string {
  const before = query.slice(0, chip.start).trimEnd();
  const after = query.slice(chip.end).trimStart();
  if (!before) {
    return after;
  }
  if (!after) {
    return before;
  }
  return `${before} ${after}`;
}

function formatDateOffsetISO(baseISO: string, deltaDays: number): string {
  const base = new Date(`${baseISO}T00:00:00Z`);
  if (Number.isNaN(base.getTime())) {
    return baseISO;
  }
  base.setUTCDate(base.getUTCDate() + deltaDays);
  return base.toISOString().slice(0, 10);
}

/**
 * Computes contextual operator and value autocomplete suggestions for the token
 * around `cursorPos` in the search input (UI.md 3.4).
 */
export function getAutocompleteSuggestions(
  query: string,
  cursorPos: number,
  ctx: AutocompleteContext,
): AutocompleteResult {
  const clamped = Math.max(0, Math.min(cursorPos, query.length));
  let start = clamped;
  while (start > 0 && !/\s/.test(query[start - 1] ?? "")) {
    start -= 1;
  }
  let end = clamped;
  while (end < query.length && !/\s/.test(query[end] ?? "")) {
    end += 1;
  }

  const token = query.slice(start, clamped);
  const negated = token.startsWith("-");
  const prefix = negated ? "-" : "";
  const body = negated ? token.slice(1) : token;
  const colonIdx = body.indexOf(":");

  if (colonIdx > 0) {
    const op = body.slice(0, colonIdx).toLowerCase();
    const typedVal = body.slice(colonIdx + 1).toLowerCase();

    if (op === "from" || op === "to") {
      const matches = ctx.senders
        .filter((s) => s.toLowerCase().includes(typedVal))
        .slice(0, 6)
        .map((sender) => {
          const formatted = /\s/.test(sender) ? `"${sender}"` : sender;
          return {
            id: `${prefix}${op}:${sender}`,
            label: `${prefix}${op}:${sender}`,
            description: op === "from" ? "Recent sender" : "Address",
            insertText: `${prefix}${op}:${formatted} `,
            complete: true,
          };
        });
      return { replaceStart: start, replaceEnd: end, suggestions: matches };
    }

    if (op === "tag") {
      const matches = ctx.tags
        .filter((t) => t.name.toLowerCase().includes(typedVal))
        .slice(0, 8)
        .map((tag) => ({
          id: `${prefix}tag:${tag.name}`,
          label: `${prefix}tag:${tag.name}`,
          description: `${tag.message_count} ${tag.message_count === 1 ? "message" : "messages"}`,
          insertText: `${prefix}tag:${tag.name} `,
          complete: true,
        }));
      return { replaceStart: start, replaceEnd: end, suggestions: matches };
    }

    if (op === "credential") {
      const seen = new Set<string>();
      const matches: AutocompleteSuggestion[] = [];
      for (const cred of ctx.credentials ?? []) {
        const lowerLabel = cred.label.toLowerCase();
        if (seen.has(lowerLabel) || !lowerLabel.includes(typedVal)) {
          continue;
        }
        seen.add(lowerLabel);
        const formatted = formatOperatorValue(cred.label);
        matches.push({
          id: `${prefix}credential:${cred.id}`,
          label: `${prefix}credential:${cred.label}`,
          description: cred.disabled ? "Disabled credential" : cred.username,
          insertText: `${prefix}credential:${formatted} `,
          complete: true,
        });
        if (matches.length >= 8) {
          break;
        }
      }
      return { replaceStart: start, replaceEnd: end, suggestions: matches };
    }

    if (op === "is") {
      const options = [
        { val: "unread", desc: "Unread messages" },
        { val: "read", desc: "Read messages" },
      ]
        .filter((o) => o.val.startsWith(typedVal))
        .map((o) => ({
          id: `${prefix}is:${o.val}`,
          label: `${prefix}is:${o.val}`,
          description: o.desc,
          insertText: `${prefix}is:${o.val} `,
          complete: true,
        }));
      return { replaceStart: start, replaceEnd: end, suggestions: options };
    }

    if (op === "has") {
      const options = [
        { val: "attachment", desc: "Has one or more attachments" },
        { val: "html", desc: "Has an HTML body" },
        { val: "text", desc: "Has a plain-text body" },
      ]
        .filter((o) => o.val.startsWith(typedVal))
        .map((o) => ({
          id: `${prefix}has:${o.val}`,
          label: `${prefix}has:${o.val}`,
          description: o.desc,
          insertText: `${prefix}has:${o.val} `,
          complete: true,
        }));
      return { replaceStart: start, replaceEnd: end, suggestions: options };
    }

    if (op === "before" || op === "after") {
      const today = ctx.todayISO ?? new Date().toISOString().slice(0, 10);
      const weekAgo = formatDateOffsetISO(today, -7);
      const options = [
        { val: today, desc: "Today (UTC)" },
        { val: weekAgo, desc: "7 days ago (UTC)" },
      ]
        .filter((o) => o.val.startsWith(typedVal))
        .map((o) => ({
          id: `${prefix}${op}:${o.val}`,
          label: `${prefix}${op}:${o.val}`,
          description: o.desc,
          insertText: `${prefix}${op}:${o.val} `,
          complete: true,
        }));
      return { replaceStart: start, replaceEnd: end, suggestions: options };
    }

    return { replaceStart: start, replaceEnd: end, suggestions: [] };
  }

  const lower = token.toLowerCase();
  const hasCreds = (ctx.credentials?.length ?? 0) > 0;
  const matchedTemplates = OPERATOR_TEMPLATES.filter((t) => {
    if (t.op === "credential:" && !hasCreds) {
      return false;
    }
    return lower === "" ? true : t.op.startsWith(lower);
  })
    .slice(0, 8)
    .map((t) => ({
      id: `op:${t.op}`,
      label: t.label,
      description: t.description,
      insertText: t.complete ? `${t.op} ` : t.op,
      complete: t.complete,
    }));

  return { replaceStart: start, replaceEnd: end, suggestions: matchedTemplates };
}

function formatOperatorValue(val: string): string {
  if (/[\s"\\]/.test(val)) {
    const escaped = val.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
    return `"${escaped}"`;
  }
  return val;
}

export function applyAutocompleteSuggestion(
  query: string,
  replaceStart: number,
  replaceEnd: number,
  insertText: string,
): { nextQuery: string; nextCursor: number } {
  const before = query.slice(0, replaceStart);
  const after = query.slice(replaceEnd);
  const needsGap = after.length > 0 && !insertText.endsWith(" ") && !after.startsWith(" ");
  const nextQuery = `${before}${insertText}${needsGap ? " " : ""}${after}`;
  const nextCursor = before.length + insertText.length;
  return { nextQuery, nextCursor };
}

/**
 * Combines the search query bar value with optional sidebar tag and credential
 * filters into a single FR-081 query string (e.g. for filtered DELETE /messages?query=...).
 */
export function combineQueryAndTag(q: string, tag?: string, credentialLabel?: string): string {
  const parts: string[] = [];
  const trimmedQ = q.trim();
  if (trimmedQ) {
    parts.push(trimmedQ);
  }
  const trimmedTag = tag?.trim();
  if (trimmedTag) {
    parts.push(`tag:${trimmedTag}`);
  }
  const trimmedCred = credentialLabel?.trim();
  if (trimmedCred) {
    parts.push(`credential:${formatOperatorValue(trimmedCred)}`);
  }
  return parts.join(" ");
}
