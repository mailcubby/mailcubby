import { describe, expect, it } from "vitest";
import {
  applyAutocompleteSuggestion,
  combineQueryAndTag,
  extractOperatorChips,
  getAutocompleteSuggestions,
  isValidTagName,
  removeChipFromQuery,
} from "./searchQuery";

describe("extractOperatorChips", () => {
  it("extracts recognized operators including quoted phrases and negations", () => {
    const q = 'from:alice subject:"order 12" has:attachment -tag:spam after:2026-11-01 joao';
    const chips = extractOperatorChips(q);
    expect(chips.map((c) => c.raw)).toEqual([
      "from:alice",
      'subject:"order 12"',
      "has:attachment",
      "-tag:spam",
      "after:2026-11-01",
    ]);
    expect(chips[1]).toMatchObject({ key: "subject", value: "order 12", negated: false });
    expect(chips[3]).toMatchObject({ key: "tag", value: "spam", negated: true });
  });

  it("ignores unknown operators and invalid enum values", () => {
    expect(extractOperatorChips("foo:bar has:unknown is:maybe from:")).toEqual([]);
  });

  it("removes a chip cleanly without leaving extra spaces", () => {
    const q = "from:alice is:unread invoice";
    const chips = extractOperatorChips(q);
    expect(chips).toHaveLength(2);
    const [first, second] = chips;
    expect(first).toBeDefined();
    expect(second).toBeDefined();
    if (first && second) {
      expect(removeChipFromQuery(q, first)).toBe("is:unread invoice");
      expect(removeChipFromQuery(q, second)).toBe("from:alice invoice");
    }
  });
});

describe("getAutocompleteSuggestions", () => {
  const ctx = {
    tags: [
      { id: 1, name: "invoice", color: "blue" as const, message_count: 4 },
      { id: 2, name: "urgent", color: "red" as const, message_count: 1 },
    ],
    senders: ["alice@shop.test", "bob@shop.test"],
    todayISO: "2026-11-24",
  };

  it("suggests recent senders after from:", () => {
    const res = getAutocompleteSuggestions("from:ali", 8, ctx);
    expect(res.suggestions.map((s) => s.label)).toEqual(["from:alice@shop.test"]);
  });

  it("suggests project tags after tag: and -tag:", () => {
    const res = getAutocompleteSuggestions("-tag:inv", 8, ctx);
    expect(res.suggestions.map((s) => s.label)).toEqual(["-tag:invoice"]);
  });

  it("suggests enum values for is: and has:", () => {
    expect(getAutocompleteSuggestions("is:", 3, ctx).suggestions.map((s) => s.label)).toEqual([
      "is:unread",
      "is:read",
    ]);
    expect(getAutocompleteSuggestions("has:a", 5, ctx).suggestions.map((s) => s.label)).toEqual([
      "has:attachment",
    ]);
  });

  it("applies suggestion cleanly in place", () => {
    const applied = applyAutocompleteSuggestion("from:ali invoice", 0, 8, "from:alice@shop.test ");
    expect(applied.nextQuery).toBe("from:alice@shop.test  invoice");
    expect(applied.nextCursor).toBe("from:alice@shop.test ".length);
  });
});

describe("tag validation and query combining", () => {
  it("validates FR-085 tag names", () => {
    expect(isValidTagName("invoice")).toBe(true);
    expect(isValidTagName("v1.2_beta-3")).toBe(true);
    expect(isValidTagName("ação")).toBe(true);
    expect(isValidTagName("")).toBe(false);
    expect(isValidTagName("has space")).toBe(false);
    expect(isValidTagName("bad!tag")).toBe(false);
  });

  it("combines query and sidebar tag", () => {
    expect(combineQueryAndTag("from:alice", "invoice")).toBe("from:alice tag:invoice");
    expect(combineQueryAndTag("", "invoice")).toBe("tag:invoice");
    expect(combineQueryAndTag("is:unread", undefined)).toBe("is:unread");
  });
});
