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

interface PendingDeleteEntry {
  projectSlug: string;
  message: MessageSummary;
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

function removeFromCachedLists(
  queryClient: QueryClient,
  projectSlug: string,
  messageId: string,
): void {
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
    const next = removeMessagesFromPages(current, [messageId], false);
    if (next !== current) {
      queryClient.setQueryData<MessageInfiniteData>(key, next);
    }
  }
}

function restoreToCachedLists(
  queryClient: QueryClient,
  projectSlug: string,
  message: MessageSummary,
): void {
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
    const result = insertMessageIntoPages(current, message);
    if (result.inserted) {
      queryClient.setQueryData<MessageInfiniteData>(key, result.data);
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
  const { projectSlug, message, queryClient } = entry;
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
    restoreToCachedLists(queryClient, projectSlug, message);
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
  restoreToCachedLists(entry.queryClient, entry.projectSlug, entry.message);
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

  removeFromCachedLists(queryClient, projectSlug, message.id);
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
    timerId,
    toastId,
    queryClient,
  });
}
