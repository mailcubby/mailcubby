import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { MessageDetail, MessageSummary, ProjectDetail, ProjectSummary } from "@/lib/api";
import { queryKeys } from "@/lib/queryKeys";
import { applySSEEvent, insertMessageIntoPages, type MessageInfiniteData } from "@/lib/sseReducer";

function makeMessage(id: string, overrides: Partial<MessageSummary> = {}): MessageSummary {
  return {
    id,
    received_at: "2026-11-23T12:00:00.000Z",
    from: { name: "Alice", address: "alice@example.test" },
    to: [{ name: "Bob", address: "bob@example.test" }],
    cc: [],
    subject: `Subject ${id}`,
    snippet: `Snippet ${id}`,
    size_bytes: 512,
    seen: false,
    attachment_count: 0,
    has_html: true,
    has_text: true,
    tags: [],
    ...overrides,
  };
}

function makeProjectSummary(slug: string, overrides: Partial<ProjectSummary> = {}): ProjectSummary {
  return {
    id: 1,
    slug,
    name: "Shop",
    color: "blue",
    role: "owner",
    message_count: 1,
    unread_count: 1,
    size_bytes: 512,
    last_message_at: "2026-11-23T12:00:00.000Z",
    sparkline: Array.from({ length: 24 }, (_, idx) => (idx === 23 ? 1 : 0)),
    ...overrides,
  };
}

function makeProjectDetail(slug: string, overrides: Partial<ProjectDetail> = {}): ProjectDetail {
  return {
    id: 1,
    slug,
    name: "Shop",
    color: "blue",
    role: "owner",
    message_count: 1,
    unread_count: 1,
    size_bytes: 512,
    last_message_at: "2026-11-23T12:00:00.000Z",
    created_at: "2026-11-23T10:00:00.000Z",
    updated_at: "2026-11-23T12:00:00.000Z",
    settings: {
      retention_max_age: null,
      retention_max_messages: null,
      retention_max_bytes: null,
      recipient_routing_enabled: false,
      plus_address_tags: true,
      viewer_hide_technical: true,
      members_can_reveal_smtp: false,
    },
    ...overrides,
  };
}

describe("applySSEEvent", () => {
  it("prepends message.created, deduplicates repeats, and bumps project counters and sparkline", () => {
    const queryClient = new QueryClient();
    const older = makeMessage("01J00000000000000000000001");
    const newer = makeMessage("01J00000000000000000000002", {
      size_bytes: 1024,
      received_at: "2026-11-23T12:05:00.000Z",
    });

    queryClient.setQueryData<MessageInfiniteData>(queryKeys.messages("shop"), {
      pages: [{ items: [older], next_cursor: null }],
      pageParams: [undefined],
    });
    queryClient.setQueryData<ProjectDetail>(queryKeys.project("shop"), makeProjectDetail("shop"));
    queryClient.setQueryData<ProjectSummary[]>(queryKeys.projects(), [makeProjectSummary("shop")]);

    const first = applySSEEvent(queryClient, {
      type: "message.created",
      payload: { project: "shop", message: newer },
    });
    expect(first).toEqual({ insertedMessage: true, needsProjectsRefetch: false });

    const list = queryClient.getQueryData<MessageInfiniteData>(queryKeys.messages("shop"));
    expect(list?.pages[0]?.items.map((m) => m.id)).toEqual([newer.id, older.id]);

    const detail = queryClient.getQueryData<ProjectDetail>(queryKeys.project("shop"));
    expect(detail?.message_count).toBe(2);
    expect(detail?.unread_count).toBe(2);
    expect(detail?.size_bytes).toBe(1536);
    expect(detail?.last_message_at).toBe(newer.received_at);

    const projects = queryClient.getQueryData<ProjectSummary[]>(queryKeys.projects());
    expect(projects?.[0]?.message_count).toBe(2);
    expect(projects?.[0]?.unread_count).toBe(2);
    expect(projects?.[0]?.sparkline[23]).toBe(2);

    // Duplicate delivery is a no-op and does not double-count.
    const dup = applySSEEvent(queryClient, {
      type: "message.created",
      payload: { project: "shop", message: newer },
    });
    expect(dup.insertedMessage).toBe(false);
    expect(queryClient.getQueryData<ProjectDetail>(queryKeys.project("shop"))?.message_count).toBe(
      2,
    );
  });

  it("requests projects refetch when message.created arrives for a newly auto-created project", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData<ProjectSummary[]>(queryKeys.projects(), [makeProjectSummary("shop")]);

    const result = applySSEEvent(queryClient, {
      type: "message.created",
      payload: { project: "auto-created", message: makeMessage("01J00000000000000000000009") },
    });

    expect(result.needsProjectsRefetch).toBe(true);
  });

  it("updates message.updated seen and tags across list and detail caches", () => {
    const queryClient = new QueryClient();
    const msg = makeMessage("01J00000000000000000000001", { seen: false, tags: [] });
    const detail: MessageDetail = {
      ...msg,
      reply_to: [],
      bcc: [],
      message_id_header: null,
      date_header: null,
      text: "hello",
      text_generated: false,
      text_truncated: false,
      attachments: [],
      render_url: "/render/shop/01J00000000000000000000001",
      remote_resource_count: 0,
    };

    queryClient.setQueryData<MessageInfiniteData>(queryKeys.messages("shop"), {
      pages: [{ items: [msg], next_cursor: null }],
      pageParams: [undefined],
    });
    queryClient.setQueryData<MessageDetail>(queryKeys.message("shop", msg.id), detail);

    applySSEEvent(queryClient, {
      type: "message.updated",
      payload: { project: "shop", ids: [msg.id], seen: true, tags: ["invoice"] },
    });

    const list = queryClient.getQueryData<MessageInfiniteData>(queryKeys.messages("shop"));
    expect(list?.pages[0]?.items[0]?.seen).toBe(true);
    expect(list?.pages[0]?.items[0]?.tags).toEqual(["invoice"]);

    const updatedDetail = queryClient.getQueryData<MessageDetail>(
      queryKeys.message("shop", msg.id),
    );
    expect(updatedDetail?.seen).toBe(true);
    expect(updatedDetail?.tags).toEqual(["invoice"]);
  });

  it("removes deleted messages by ID or clears all pages on all=true", () => {
    const queryClient = new QueryClient();
    const m1 = makeMessage("01J00000000000000000000001");
    const m2 = makeMessage("01J00000000000000000000002");

    queryClient.setQueryData<MessageInfiniteData>(queryKeys.messages("shop"), {
      pages: [{ items: [m2, m1], next_cursor: null }],
      pageParams: [undefined],
    });

    applySSEEvent(queryClient, {
      type: "message.deleted",
      payload: { project: "shop", ids: [m2.id] },
    });

    let list = queryClient.getQueryData<MessageInfiniteData>(queryKeys.messages("shop"));
    expect(list?.pages[0]?.items.map((m) => m.id)).toEqual([m1.id]);

    applySSEEvent(queryClient, {
      type: "message.deleted",
      payload: { project: "shop", all: true },
    });

    list = queryClient.getQueryData<MessageInfiniteData>(queryKeys.messages("shop"));
    expect(list?.pages[0]?.items).toEqual([]);
  });

  it("applies authoritative counters from project.updated", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData<ProjectDetail>(queryKeys.project("shop"), makeProjectDetail("shop"));
    queryClient.setQueryData<ProjectSummary[]>(queryKeys.projects(), [makeProjectSummary("shop")]);

    applySSEEvent(queryClient, {
      type: "project.updated",
      payload: {
        project: "shop",
        message_count: 10,
        unread_count: 4,
        size_bytes: 9999,
        last_message_at: "2026-11-23T14:00:00.000Z",
      },
    });

    const detail = queryClient.getQueryData<ProjectDetail>(queryKeys.project("shop"));
    expect(detail?.message_count).toBe(10);
    expect(detail?.unread_count).toBe(4);
    expect(detail?.size_bytes).toBe(9999);

    const projects = queryClient.getQueryData<ProjectSummary[]>(queryKeys.projects());
    expect(projects?.[0]?.unread_count).toBe(4);
  });

  it("restores older messages in descending ULID order across pages", () => {
    const m3 = makeMessage("01J00000000000000000000003");
    const m2 = makeMessage("01J00000000000000000000002");
    const m1 = makeMessage("01J00000000000000000000001");

    const initial: MessageInfiniteData = {
      pages: [{ items: [m3, m1], next_cursor: null }],
      pageParams: [undefined],
    };

    const restored = insertMessageIntoPages(initial, m2);
    expect(restored.inserted).toBe(true);
    expect(restored.data?.pages[0]?.items.map((m) => m.id)).toEqual([m3.id, m2.id, m1.id]);
  });
});
