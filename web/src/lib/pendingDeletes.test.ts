import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MessageSummary } from "@/lib/api";
import { scheduleOptimisticDelete, undoPendingDelete } from "@/lib/pendingDeletes";
import { queryKeys } from "@/lib/queryKeys";
import type { MessageInfiniteData } from "@/lib/sseReducer";

vi.mock("sonner", () => ({
  toast: Object.assign(
    vi.fn(() => "toast-id"),
    {
      dismiss: vi.fn(),
      error: vi.fn(),
      success: vi.fn(),
    },
  ),
}));

function makeMsg(id: string, overrides?: Partial<MessageSummary>): MessageSummary {
  return {
    id,
    received_at: "2026-11-24T12:00:00.000Z",
    from: { name: "Alice", address: "alice@example.test" },
    to: [{ name: "", address: "dev@example.test" }],
    cc: [],
    subject: `Subject ${id}`,
    snippet: "Hello",
    size_bytes: 512,
    seen: false,
    attachment_count: 0,
    has_html: true,
    has_text: true,
    tags: [],
    ...overrides,
  };
}

describe("pendingDeletes", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("restores only into cached lists that originally contained the message and avoids duplicates across pages", () => {
    const qc = new QueryClient();
    const m3 = makeMsg("01J00000000000000000000003", { tags: ["invoice"] });
    const m2 = makeMsg("01J00000000000000000000002", { tags: [] });
    const m1 = makeMsg("01J00000000000000000000001", { tags: [] });

    const unfilteredKey = queryKeys.messages("shop", {});
    const filteredKey = queryKeys.messages("shop", { tag: "invoice" });

    // Multi-page unfiltered cache with m2 on page 1.
    qc.setQueryData<MessageInfiniteData>(unfilteredKey, {
      pages: [
        { items: [m3], next_cursor: "cursor-1" },
        { items: [m2, m1], next_cursor: null },
      ],
      pageParams: [undefined, "cursor-1"],
    });

    // Filtered list (tag=invoice) that does NOT contain m2.
    qc.setQueryData<MessageInfiniteData>(filteredKey, {
      pages: [{ items: [m3], next_cursor: null }],
      pageParams: [undefined],
    });

    scheduleOptimisticDelete(qc, "shop", m2);

    // Removed from unfiltered page 1.
    const afterDelete = qc.getQueryData<MessageInfiniteData>(unfilteredKey);
    expect(afterDelete?.pages[1]?.items.map((m) => m.id)).toEqual([m1.id]);

    // Undo restores m2 back into unfiltered page 1 only — NOT into filteredKey.
    expect(undoPendingDelete(m2.id)).toBe(true);

    const afterUndoUnfiltered = qc.getQueryData<MessageInfiniteData>(unfilteredKey);
    expect(afterUndoUnfiltered?.pages[0]?.items.map((m) => m.id)).toEqual([m3.id]);
    expect(afterUndoUnfiltered?.pages[1]?.items.map((m) => m.id)).toEqual([m2.id, m1.id]);

    const afterUndoFiltered = qc.getQueryData<MessageInfiniteData>(filteredKey);
    expect(afterUndoFiltered?.pages[0]?.items.map((m) => m.id)).toEqual([m3.id]);
  });

  it("does not duplicate a message on undo if the list already refetched it", () => {
    const qc = new QueryClient();
    const m2 = makeMsg("01J00000000000000000000002");
    const m1 = makeMsg("01J00000000000000000000001");
    const key = queryKeys.messages("shop", {});

    qc.setQueryData<MessageInfiniteData>(key, {
      pages: [{ items: [m2, m1], next_cursor: null }],
      pageParams: [undefined],
    });

    scheduleOptimisticDelete(qc, "shop", m2);

    // Simulate a background refetch putting m2 back into page 0.
    qc.setQueryData<MessageInfiniteData>(key, {
      pages: [{ items: [m2, m1], next_cursor: null }],
      pageParams: [undefined],
    });

    expect(undoPendingDelete(m2.id)).toBe(true);
    const data = qc.getQueryData<MessageInfiniteData>(key);
    expect(data?.pages[0]?.items.map((m) => m.id)).toEqual([m2.id, m1.id]);
  });
});
