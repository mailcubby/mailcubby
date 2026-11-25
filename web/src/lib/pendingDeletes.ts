import type { QueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  ApiError,
  api,
  getCsrfToken,
  handleApiError,
  type MessageSummary,
  type ProjectDetail,
  type ProjectSummary,
  unwrapVoid,
} from "@/lib/api";
import { queryKeys } from "@/lib/queryKeys";
import {
  insertMessageIntoPages,
  type MessageInfiniteData,
  removeMessagesFromPages,
} from "@/lib/sseReducer";

const UNDO_WINDOW_MS = 5000;

interface RemovedListSnapshot {
  queryKey: readonly unknown[];
  pageIndex: number;
}

interface PendingDeleteEntry {
  projectSlug: string;
  message: MessageSummary;
  removedFrom: RemovedListSnapshot[];
  timerId: number;
  toastId: string | number;
  queryClient: QueryClient;
}

const pendingDeletes = new Map<string, PendingDeleteEntry>();
let pagehideRegistered = false;

function deleteEndpoint(projectSlug: string, messageId: string): string {
  return `/api/v1/projects/${encodeURIComponent(projectSlug)}/messages/${encodeURIComponent(messageId)}`;
}

function adjustProjectCounters(
  queryClient: QueryClient,
  projectSlug: string,
  deltaMessages: number,
  deltaUnread: number,
  deltaBytes: number,
): void {
  queryClient.setQueryData<ProjectDetail>(queryKeys.project(projectSlug), (prev) => {
    if (!prev) {
      return prev;
    }
    return {
      ...prev,
      message_count: Math.max(0, prev.message_count + deltaMessages),
      unread_count: Math.max(0, prev.unread_count + deltaUnread),
      size_bytes: Math.max(0, prev.size_bytes + deltaBytes),
    };
  });

  queryClient.setQueryData<ProjectSummary[]>(queryKeys.projects(), (prev) =>
    prev?.map((p) =>
      p.slug === projectSlug
        ? {
            ...p,
            message_count: Math.max(0, p.message_count + deltaMessages),
            unread_count: Math.max(0, p.unread_count + deltaUnread),
            size_bytes: Math.max(0, p.size_bytes + deltaBytes),
          }
        : p,
    ),
  );
}

function findMessagePageIndex(data: MessageInfiniteData | undefined, messageId: string): number {
  if (!data) {
    return -1;
  }
  for (let i = 0; i < data.pages.length; i++) {
    const page = data.pages[i];
    if (page?.items.some((item) => item.id === messageId)) {
      return i;
    }
  }
  return -1;
}

function removeFromCachedLists(
  queryClient: QueryClient,
  projectSlug: string,
  messageId: string,
): RemovedListSnapshot[] {
  const removedFrom: RemovedListSnapshot[] = [];
  const listQueries = queryClient.getQueriesData<MessageInfiniteData>({
    predicate: (q) =>
      q.queryKey.length === 4 &&
      q.queryKey[0] === "projects" &&
      q.queryKey[1] === projectSlug &&
      q.queryKey[2] === "messages" &&
      typeof q.queryKey[3] === "object" &&
      q.queryKey[3] !== null,
  });
  for (const [key, current] of listQueries) {
    const pageIndex = findMessagePageIndex(current, messageId);
    if (pageIndex === -1) {
      continue;
    }
    const next = removeMessagesFromPages(current, [messageId], false);
    if (next !== current) {
      removedFrom.push({ queryKey: key, pageIndex });
      queryClient.setQueryData<MessageInfiniteData>(key, next);
    }
  }
  return removedFrom;
}

function restoreIntoPage(
  data: MessageInfiniteData,
  message: MessageSummary,
  preferredPageIndex: number,
): { data: MessageInfiniteData; inserted: boolean } {
  // Prevent duplicates across all pages (e.g. if the query refetched while pending).
  if (findMessagePageIndex(data, message.id) !== -1) {
    return { data, inserted: false };
  }

  const targetPage = data.pages[preferredPageIndex];
  if (targetPage) {
    const nextItems = [...targetPage.items];
    const insertAt = nextItems.findIndex((item) => item.id < message.id);
    if (insertAt === -1) {
      nextItems.push(message);
    } else {
      nextItems.splice(insertAt, 0, message);
    }
    const nextPages = data.pages.map((page, idx) =>
      idx === preferredPageIndex ? { ...page, items: nextItems } : page,
    );
    return { data: { ...data, pages: nextPages }, inserted: true };
  }

  const fallback = insertMessageIntoPages(data, message);
  return { data: fallback.data ?? data, inserted: fallback.inserted };
}

function restoreToCachedLists(
  queryClient: QueryClient,
  removedFrom: ReadonlyArray<RemovedListSnapshot>,
  message: MessageSummary,
): void {
  for (const snapshot of removedFrom) {
    const current = queryClient.getQueryData<MessageInfiniteData>(snapshot.queryKey);
    if (!current) {
      continue;
    }
    const result = restoreIntoPage(current, message, snapshot.pageIndex);
    if (result.inserted) {
      queryClient.setQueryData<MessageInfiniteData>(snapshot.queryKey, result.data);
    }
  }
}

function ensurePagehideFlush(): void {
  if (pagehideRegistered || typeof window === "undefined") {
    return;
  }
  pagehideRegistered = true;
  window.addEventListener("pagehide", () => {
    for (const [id, entry] of pendingDeletes) {
      window.clearTimeout(entry.timerId);
      pendingDeletes.delete(id);
      try {
        const csrf = getCsrfToken();
        void fetch(deleteEndpoint(entry.projectSlug, id), {
          method: "DELETE",
          keepalive: true,
          credentials: "same-origin",
          ...(csrf ? { headers: { "X-CSRF-Token": csrf } } : {}),
        });
      } catch {
        // Ignore network errors during unload.
      }
    }
  });
}

async function commitDelete(entry: PendingDeleteEntry): Promise<void> {
  const { projectSlug, message, removedFrom, queryClient } = entry;
  try {
    unwrapVoid(
      await api.DELETE("/api/v1/projects/{project}/messages/{id}", {
        params: { path: { project: projectSlug, id: message.id } },
      }),
    );
    queryClient.removeQueries({ queryKey: queryKeys.message(projectSlug, message.id) });
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return;
    }
    restoreToCachedLists(queryClient, removedFrom, message);
    adjustProjectCounters(queryClient, projectSlug, 1, message.seen ? 0 : 1, message.size_bytes);
    handleApiError(err, "Failed to delete message");
  }
}

export function undoPendingDelete(messageId: string): boolean {
  const entry = pendingDeletes.get(messageId);
  if (!entry) {
    return false;
  }
  window.clearTimeout(entry.timerId);
  pendingDeletes.delete(messageId);
  toast.dismiss(entry.toastId);
  restoreToCachedLists(entry.queryClient, entry.removedFrom, entry.message);
  adjustProjectCounters(
    entry.queryClient,
    entry.projectSlug,
    1,
    entry.message.seen ? 0 : 1,
    entry.message.size_bytes,
  );
  return true;
}

/**
 * Optimistically removes a single message from the inbox list and schedules
 * its DELETE request after 5 seconds unless undone (docs/UI.md section 4 item 5).
 * Uncommitted deletes flush immediately on `pagehide` via `fetch(..., { keepalive: true })`.
 */
export function scheduleOptimisticDelete(
  queryClient: QueryClient,
  projectSlug: string,
  message: MessageSummary,
  onUndo?: () => void,
): void {
  if (pendingDeletes.has(message.id)) {
    return;
  }

  ensurePagehideFlush();

  const removedFrom = removeFromCachedLists(queryClient, projectSlug, message.id);
  adjustProjectCounters(queryClient, projectSlug, -1, message.seen ? 0 : -1, -message.size_bytes);

  const toastId = toast("Message deleted", {
    duration: UNDO_WINDOW_MS,
    action: {
      label: "Undo",
      onClick: () => {
        if (undoPendingDelete(message.id)) {
          onUndo?.();
        }
      },
    },
  });

  const timerId = window.setTimeout(() => {
    const entry = pendingDeletes.get(message.id);
    if (!entry) {
      return;
    }
    pendingDeletes.delete(message.id);
    void commitDelete(entry);
  }, UNDO_WINDOW_MS);

  pendingDeletes.set(message.id, {
    projectSlug,
    message,
    removedFrom,
    timerId,
    toastId,
    queryClient,
  });
}
