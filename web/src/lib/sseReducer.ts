import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type {
  MessageDetail,
  MessageListResponse,
  MessageSummary,
  ProjectDetail,
  ProjectSummary,
} from "@/lib/api";
import { queryKeys } from "@/lib/queryKeys";

export interface MessageCreatedEvent {
  type: "message.created";
  payload: {
    project: string;
    message: MessageSummary;
  };
}

export interface MessageUpdatedEvent {
  type: "message.updated";
  payload: {
    project: string;
    ids: string[];
    seen?: boolean;
    tags?: string[];
  };
}

export interface MessageDeletedEvent {
  type: "message.deleted";
  payload: {
    project: string;
    ids?: string[];
    all?: boolean;
  };
}

export interface ProjectUpdatedEvent {
  type: "project.updated";
  payload: {
    project: string;
    message_count: number;
    unread_count: number;
    size_bytes: number;
    last_message_at: string | null;
  };
}

export interface ProjectCreatedEvent {
  type: "project.created";
  payload: {
    project: string;
  };
}

export interface ProjectDeletedEvent {
  type: "project.deleted";
  payload: {
    project: string;
  };
}

export interface MembershipChangedEvent {
  type: "membership.changed";
  payload: Record<string, unknown>;
}

export type SSEEvent =
  | MessageCreatedEvent
  | MessageUpdatedEvent
  | MessageDeletedEvent
  | ProjectUpdatedEvent
  | ProjectCreatedEvent
  | ProjectDeletedEvent
  | MembershipChangedEvent;

export type MessageInfiniteData = InfiniteData<MessageListResponse, string | undefined>;

export interface ApplySSEResult {
  insertedMessage: boolean;
  needsProjectsRefetch: boolean;
}

function hasMessageInPages(data: MessageInfiniteData, messageId: string): boolean {
  for (const page of data.pages) {
    for (const item of page.items) {
      if (item.id === messageId) {
        return true;
      }
    }
  }
  return false;
}

/**
 * Inserts a message into an infinite-list cache while maintaining descending ULID order
 * and deduplicating by message ID.
 */
export function insertMessageIntoPages(
  data: MessageInfiniteData | undefined,
  message: MessageSummary,
): { data: MessageInfiniteData | undefined; inserted: boolean } {
  if (!data) {
    return { data, inserted: false };
  }
  if (hasMessageInPages(data, message.id)) {
    return { data, inserted: false };
  }

  const firstPage = data.pages[0];
  if (!firstPage) {
    return {
      data: {
        pages: [{ items: [message], next_cursor: null }],
        pageParams: [undefined],
      },
      inserted: true,
    };
  }

  // Insert in descending ULID order inside the first page (newest first, FR-070).
  const nextFirstItems = [...firstPage.items];
  const insertAt = nextFirstItems.findIndex((item) => item.id < message.id);
  if (insertAt === -1) {
    // Only append to page 0 if there are no subsequent pages or it belongs before page 1.
    if (data.pages.length === 1) {
      nextFirstItems.push(message);
    } else {
      // Find the right page for restoring older deleted messages.
      let placed = false;
      const nextPages = data.pages.map((page) => {
        if (placed) {
          return page;
        }
        const idx = page.items.findIndex((item) => item.id < message.id);
        if (idx !== -1) {
          placed = true;
          const items = [...page.items];
          items.splice(idx, 0, message);
          return { ...page, items };
        }
        return page;
      });
      if (!placed) {
        const lastIdx = nextPages.length - 1;
        const last = nextPages[lastIdx];
        if (last) {
          nextPages[lastIdx] = { ...last, items: [...last.items, message] };
        }
      }
      return {
        data: { ...data, pages: nextPages },
        inserted: true,
      };
    }
  } else {
    nextFirstItems.splice(insertAt, 0, message);
  }

  const nextPages = [{ ...firstPage, items: nextFirstItems }, ...data.pages.slice(1)];
  return {
    data: { ...data, pages: nextPages },
    inserted: true,
  };
}

export function updateMessagesInPages(
  data: MessageInfiniteData | undefined,
  ids: ReadonlyArray<string>,
  patch: { seen?: boolean; tags?: string[] },
): MessageInfiniteData | undefined {
  if (!data || ids.length === 0) {
    return data;
  }
  const idSet = new Set(ids);
  let changed = false;
  const nextPages = data.pages.map((page) => {
    let pageChanged = false;
    const nextItems = page.items.map((item) => {
      if (!idSet.has(item.id)) {
        return item;
      }
      const nextItem: MessageSummary = {
        ...item,
        ...(patch.seen !== undefined ? { seen: patch.seen } : {}),
        ...(patch.tags !== undefined ? { tags: patch.tags } : {}),
      };
      pageChanged = true;
      changed = true;
      return nextItem;
    });
    return pageChanged ? { ...page, items: nextItems } : page;
  });
  return changed ? { ...data, pages: nextPages } : data;
}

export function removeMessagesFromPages(
  data: MessageInfiniteData | undefined,
  ids: ReadonlyArray<string>,
  all = false,
): MessageInfiniteData | undefined {
  if (!data) {
    return data;
  }
  if (all) {
    return {
      pages: [{ items: [], next_cursor: null }],
      pageParams: [undefined],
    };
  }
  if (ids.length === 0) {
    return data;
  }
  const idSet = new Set(ids);
  let changed = false;
  const nextPages = data.pages.map((page) => {
    const filtered = page.items.filter((item) => !idSet.has(item.id));
    if (filtered.length !== page.items.length) {
      changed = true;
      return { ...page, items: filtered };
    }
    return page;
  });
  return changed ? { ...data, pages: nextPages } : data;
}

function isMessageListKey(queryKey: readonly unknown[], projectSlug: string): boolean {
  // List keys have shape ["projects", slug, "messages", paramsObj]
  return (
    queryKey.length === 4 &&
    queryKey[0] === "projects" &&
    queryKey[1] === projectSlug &&
    queryKey[2] === "messages" &&
    typeof queryKey[3] === "object" &&
    queryKey[3] !== null
  );
}

function bumpSparklineLatest(sparkline: ReadonlyArray<number>): number[] {
  if (sparkline.length === 0) {
    return [1];
  }
  const copy = [...sparkline];
  const lastIndex = copy.length - 1;
  copy[lastIndex] = (copy[lastIndex] ?? 0) + 1;
  return copy;
}

/**
 * Applies a single realtime SSE event to TanStack Query caches without triggering
 * refetch storms (AGENTS.md section 6, ADR 0003).
 */
export function applySSEEvent(queryClient: QueryClient, event: SSEEvent): ApplySSEResult {
  switch (event.type) {
    case "message.created": {
      const { project: slug, message } = event.payload;
      let insertedMessage = false;
      let sawCachedList = false;

      const listQueries = queryClient.getQueriesData<MessageInfiniteData>({
        predicate: (q) => isMessageListKey(q.queryKey, slug),
      });

      for (const [key, current] of listQueries) {
        if (!current) {
          continue;
        }
        sawCachedList = true;
        const result = insertMessageIntoPages(current, message);
        if (result.inserted) {
          insertedMessage = true;
          queryClient.setQueryData<MessageInfiniteData>(key, result.data);
        }
      }

      // Only increment local counters when the message wasn't already present in a loaded list
      // (or when no list is loaded yet, e.g., while viewing Projects Home).
      const shouldBumpCounters = !sawCachedList || insertedMessage;
      let needsProjectsRefetch = false;

      if (shouldBumpCounters) {
        queryClient.setQueryData<ProjectDetail>(queryKeys.project(slug), (prev) => {
          if (!prev) {
            return prev;
          }
          return {
            ...prev,
            message_count: prev.message_count + 1,
            unread_count: prev.unread_count + (message.seen ? 0 : 1),
            size_bytes: prev.size_bytes + message.size_bytes,
            last_message_at: message.received_at,
          };
        });

        const existingProjects = queryClient.getQueryData<ProjectSummary[]>(queryKeys.projects());
        if (existingProjects) {
          const exists = existingProjects.some((p) => p.slug === slug);
          if (exists) {
            queryClient.setQueryData<ProjectSummary[]>(queryKeys.projects(), (prev) =>
              prev?.map((p) =>
                p.slug === slug
                  ? {
                      ...p,
                      message_count: p.message_count + 1,
                      unread_count: p.unread_count + (message.seen ? 0 : 1),
                      size_bytes: p.size_bytes + message.size_bytes,
                      last_message_at: message.received_at,
                      sparkline: bumpSparklineLatest(p.sparkline),
                    }
                  : p,
              ),
            );
          } else {
            // Auto-created project in local mode via SMTP: refetch projects list.
            needsProjectsRefetch = true;
          }
        }
      }

      return { insertedMessage: shouldBumpCounters, needsProjectsRefetch };
    }

    case "message.updated": {
      const { project: slug, ids, seen, tags } = event.payload;
      const patch = {
        ...(seen !== undefined ? { seen } : {}),
        ...(tags !== undefined ? { tags } : {}),
      };

      const listQueries = queryClient.getQueriesData<MessageInfiniteData>({
        predicate: (q) => isMessageListKey(q.queryKey, slug),
      });
      for (const [key, current] of listQueries) {
        const next = updateMessagesInPages(current, ids, patch);
        if (next !== current) {
          queryClient.setQueryData<MessageInfiniteData>(key, next);
        }
      }

      for (const id of ids) {
        queryClient.setQueryData<MessageDetail>(queryKeys.message(slug, id), (prev) => {
          if (!prev) {
            return prev;
          }
          return {
            ...prev,
            ...(seen !== undefined ? { seen } : {}),
            ...(tags !== undefined ? { tags } : {}),
          };
        });
      }

      return { insertedMessage: false, needsProjectsRefetch: false };
    }

    case "message.deleted": {
      const { project: slug, ids = [], all = false } = event.payload;
      const listQueries = queryClient.getQueriesData<MessageInfiniteData>({
        predicate: (q) => isMessageListKey(q.queryKey, slug),
      });
      for (const [key, current] of listQueries) {
        const next = removeMessagesFromPages(current, ids, all);
        if (next !== current) {
          queryClient.setQueryData<MessageInfiniteData>(key, next);
        }
      }

      if (all) {
        queryClient.removeQueries({
          predicate: (q) =>
            q.queryKey.length >= 4 &&
            q.queryKey[0] === "projects" &&
            q.queryKey[1] === slug &&
            q.queryKey[2] === "messages" &&
            typeof q.queryKey[3] === "string",
        });
      } else {
        for (const id of ids) {
          queryClient.removeQueries({ queryKey: queryKeys.message(slug, id) });
        }
      }

      return { insertedMessage: false, needsProjectsRefetch: false };
    }

    case "project.updated": {
      const {
        project: slug,
        message_count,
        unread_count,
        size_bytes,
        last_message_at,
      } = event.payload;

      queryClient.setQueryData<ProjectDetail>(queryKeys.project(slug), (prev) => {
        if (!prev) {
          return prev;
        }
        return {
          ...prev,
          message_count,
          unread_count,
          size_bytes,
          last_message_at,
        };
      });

      queryClient.setQueryData<ProjectSummary[]>(queryKeys.projects(), (prev) =>
        prev?.map((p) =>
          p.slug === slug
            ? {
                ...p,
                message_count,
                unread_count,
                size_bytes,
                last_message_at,
              }
            : p,
        ),
      );

      return { insertedMessage: false, needsProjectsRefetch: false };
    }

    case "project.created": {
      return { insertedMessage: false, needsProjectsRefetch: true };
    }

    case "project.deleted": {
      const { project: slug } = event.payload;
      queryClient.setQueryData<ProjectSummary[]>(queryKeys.projects(), (prev) =>
        prev?.filter((p) => p.slug !== slug),
      );
      queryClient.removeQueries({ queryKey: queryKeys.project(slug) });
      return { insertedMessage: false, needsProjectsRefetch: false };
    }

    case "membership.changed": {
      return { insertedMessage: false, needsProjectsRefetch: true };
    }
  }
}
