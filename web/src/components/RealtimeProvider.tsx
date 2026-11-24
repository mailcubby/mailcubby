import { useQueryClient } from "@tanstack/react-query";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { MessageSummary } from "@/lib/api";
import { getSenderLabel } from "@/lib/format";
import { queryKeys } from "@/lib/queryKeys";
import { connectSSE, type SSEConnectionStatus } from "@/lib/sse";
import { applySSEEvent, isFilteredMessageListKey } from "@/lib/sseReducer";

const ARRIVAL_HIGHLIGHT_MS = 1500;
const ANNOUNCEMENT_THROTTLE_MS = 5000;

export type ArrivalListener = (projectSlug: string, message: MessageSummary) => void;

export interface RealtimeContextValue {
  status: SSEConnectionStatus;
  highlightedMessageIds: ReadonlySet<string>;
  highlightedProjectSlugs: ReadonlySet<string>;
  subscribeArrivals: (listener: ArrivalListener) => () => void;
}

const RealtimeContext = createContext<RealtimeContextValue | null>(null);

const FALLBACK_REALTIME: RealtimeContextValue = {
  status: "connecting",
  highlightedMessageIds: new Set(),
  highlightedProjectSlugs: new Set(),
  subscribeArrivals: () => () => {},
};

export function useRealtime(): RealtimeContextValue {
  return useContext(RealtimeContext) ?? FALLBACK_REALTIME;
}

export function RealtimeProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<SSEConnectionStatus>("connecting");
  const [highlightedMessageIds, setHighlightedMessageIds] = useState<Set<string>>(() => new Set());
  const [highlightedProjectSlugs, setHighlightedProjectSlugs] = useState<Set<string>>(
    () => new Set(),
  );
  const [announcement, setAnnouncement] = useState("");

  const arrivalListenersRef = useRef<Set<ArrivalListener>>(new Set());
  const highlightTimersRef = useRef<Map<string, number>>(new Map());
  const lastAnnouncedAtRef = useRef(0);
  const pendingAnnouncementsRef = useRef<MessageSummary[]>([]);
  const announcementTimerRef = useRef<number | null>(null);

  const subscribeArrivals = useCallback((listener: ArrivalListener) => {
    arrivalListenersRef.current.add(listener);
    return () => {
      arrivalListenersRef.current.delete(listener);
    };
  }, []);

  const flashHighlight = useCallback((projectSlug: string, messageId: string) => {
    setHighlightedMessageIds((prev) => {
      const next = new Set(prev);
      next.add(messageId);
      return next;
    });
    setHighlightedProjectSlugs((prev) => {
      const next = new Set(prev);
      next.add(projectSlug);
      return next;
    });

    const msgKey = `m:${messageId}`;
    const prevMsgTimer = highlightTimersRef.current.get(msgKey);
    if (prevMsgTimer !== undefined) {
      window.clearTimeout(prevMsgTimer);
    }
    highlightTimersRef.current.set(
      msgKey,
      window.setTimeout(() => {
        highlightTimersRef.current.delete(msgKey);
        setHighlightedMessageIds((prev) => {
          if (!prev.has(messageId)) {
            return prev;
          }
          const next = new Set(prev);
          next.delete(messageId);
          return next;
        });
      }, ARRIVAL_HIGHLIGHT_MS),
    );

    const projKey = `p:${projectSlug}`;
    const prevProjTimer = highlightTimersRef.current.get(projKey);
    if (prevProjTimer !== undefined) {
      window.clearTimeout(prevProjTimer);
    }
    highlightTimersRef.current.set(
      projKey,
      window.setTimeout(() => {
        highlightTimersRef.current.delete(projKey);
        setHighlightedProjectSlugs((prev) => {
          if (!prev.has(projectSlug)) {
            return prev;
          }
          const next = new Set(prev);
          next.delete(projectSlug);
          return next;
        });
      }, ARRIVAL_HIGHLIGHT_MS),
    );
  }, []);

  const queueAnnouncement = useCallback((message: MessageSummary) => {
    const formatSingle = (m: MessageSummary) => {
      const sender = getSenderLabel(m.from);
      const subject = m.subject.trim() || "(no subject)";
      return `New message from ${sender}: ${subject}`;
    };

    const flush = () => {
      announcementTimerRef.current = null;
      const batch = pendingAnnouncementsRef.current;
      pendingAnnouncementsRef.current = [];
      if (batch.length === 0) {
        return;
      }
      lastAnnouncedAtRef.current = Date.now();
      const first = batch[0];
      if (batch.length === 1 && first) {
        setAnnouncement(formatSingle(first));
      } else {
        setAnnouncement(`${batch.length} new messages arrived`);
      }
    };

    const elapsed = Date.now() - lastAnnouncedAtRef.current;
    if (elapsed >= ANNOUNCEMENT_THROTTLE_MS && announcementTimerRef.current === null) {
      lastAnnouncedAtRef.current = Date.now();
      setAnnouncement(formatSingle(message));
      return;
    }

    pendingAnnouncementsRef.current.push(message);
    if (announcementTimerRef.current === null) {
      const remaining = Math.max(0, ANNOUNCEMENT_THROTTLE_MS - elapsed);
      announcementTimerRef.current = window.setTimeout(flush, remaining);
    }
  }, []);

  useEffect(() => {
    const disconnect = connectSSE({
      onStatusChange: setStatus,
      onReconnect: () => {
        void queryClient.invalidateQueries();
      },
      onEvent: (event) => {
        const result = applySSEEvent(queryClient, event);
        if (result.needsProjectsRefetch) {
          void queryClient.invalidateQueries({ queryKey: queryKeys.projects() });
        }
        if (result.invalidateTagsForProject) {
          void queryClient.invalidateQueries({
            queryKey: queryKeys.tags(result.invalidateTagsForProject),
          });
        }
        if (result.invalidateFilteredListsForProject) {
          const slug = result.invalidateFilteredListsForProject;
          void queryClient.invalidateQueries({
            predicate: (q) => isFilteredMessageListKey(q.queryKey, slug),
          });
        }
        if (event.type === "message.created" && result.insertedMessage) {
          const { project, message } = event.payload;
          flashHighlight(project, message.id);
          queueAnnouncement(message);
          for (const listener of arrivalListenersRef.current) {
            listener(project, message);
          }
        }
      },
    });

    const timers = highlightTimersRef.current;
    return () => {
      disconnect();
      for (const timer of timers.values()) {
        window.clearTimeout(timer);
      }
      timers.clear();
      if (announcementTimerRef.current !== null) {
        window.clearTimeout(announcementTimerRef.current);
        announcementTimerRef.current = null;
      }
    };
  }, [queryClient, flashHighlight, queueAnnouncement]);

  const value = useMemo<RealtimeContextValue>(
    () => ({
      status,
      highlightedMessageIds,
      highlightedProjectSlugs,
      subscribeArrivals,
    }),
    [status, highlightedMessageIds, highlightedProjectSlugs, subscribeArrivals],
  );

  return (
    <RealtimeContext.Provider value={value}>
      {children}
      {/* Centralized polite live region outside dialogs (UI 4 item 3, accessibility guide). */}
      <div aria-live="polite" aria-atomic="true" className="sr-only">
        {announcement}
      </div>
    </RealtimeContext.Provider>
  );
}
