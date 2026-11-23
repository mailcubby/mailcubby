import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  ArrowUp,
  CheckSquare,
  Mail,
  MailOpen,
  PanelLeftOpen,
  Paperclip,
  RefreshCw,
  Trash2,
  X,
} from "lucide-react";
import {
  type MouseEvent,
  type RefObject,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { toast } from "sonner";
import { useRealtime } from "@/components/RealtimeProvider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { api, type MessageSummary, type ProjectDetail, unwrap } from "@/lib/api";
import {
  formatCount,
  formatExactTimestamp,
  formatRelativeTime,
  getSenderLabel,
} from "@/lib/format";
import {
  useClock24hPreference,
  useDensityPreference,
  useSingleKeyShortcutsPreference,
} from "@/lib/preferences";
import { messageDetailQueryOptions } from "@/lib/queries";
import { type KeyShortcutBinding, useKeyShortcuts } from "@/lib/shortcuts";
import { cn } from "@/lib/utils";
import type { InboxFilter } from "@/routes/p.$project";

const SKELETON_ROWS = ["r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8"] as const;
const HOVER_PREFETCH_DELAY_MS = 100;

export interface MessageListPaneProps {
  project: ProjectDetail;
  messages: ReadonlyArray<MessageSummary>;
  filter: InboxFilter;
  activeMessageId: string | undefined;
  isLoading: boolean;
  isFetchingNextPage: boolean;
  hasNextPage: boolean;
  onFetchNextPage: () => void;
  onRefresh: () => void;
  onMutated: () => Promise<void>;
  onDeleteSingle: (message: MessageSummary) => void;
  onToggleSeen: (message: MessageSummary) => void;
  readingPaneRef: RefObject<HTMLDivElement | null>;
  deleteAllOpen: boolean;
  onDeleteAllOpenChange: (open: boolean) => void;
  showSidebarTrigger?: boolean;
  onOpenSidebar?: () => void;
}

export function MessageListPane({
  project,
  messages,
  filter,
  activeMessageId,
  isLoading,
  isFetchingNextPage,
  hasNextPage,
  onFetchNextPage,
  onRefresh,
  onMutated,
  onDeleteSingle,
  onToggleSeen,
  readingPaneRef,
  deleteAllOpen,
  onDeleteAllOpenChange,
  showSidebarTrigger = false,
  onOpenSidebar,
}: MessageListPaneProps) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const hoverTimerRef = useRef<number | null>(null);

  const { highlightedMessageIds, subscribeArrivals } = useRealtime();
  const [density] = useDensityPreference();
  const [singleKeyEnabled] = useSingleKeyShortcutsPreference();
  const [clock24h] = useClock24hPreference();

  const [selectedIds, setSelectedIds] = useState<Set<string>>(() => new Set());
  const [lastSelectedIndex, setLastSelectedIndex] = useState<number | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [newSinceScrolled, setNewSinceScrolled] = useState(0);
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    return () => {
      if (hoverTimerRef.current !== null) {
        window.clearTimeout(hoverTimerRef.current);
      }
    };
  }, []);

  // Track new arrivals when the list is scrolled down ("N new" pill, UI 4 item 3).
  useEffect(() => {
    setNewSinceScrolled(0);
    return subscribeArrivals((arrivedProject) => {
      if (arrivedProject !== project.slug) {
        return;
      }
      const el = scrollRef.current;
      if (el && el.scrollTop > 40) {
        setNewSinceScrolled((prev) => prev + 1);
      }
    });
  }, [project.slug, subscribeArrivals]);

  // Prune selection when project changes or deleted messages disappear.
  useEffect(() => {
    const existing = new Set(messages.map((m) => m.id));
    setSelectedIds((prev) => {
      if (prev.size === 0) {
        return prev;
      }
      let changed = false;
      const next = new Set<string>();
      for (const id of prev) {
        if (existing.has(id)) {
          next.add(id);
        } else {
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [messages]);

  const bulkMutation = useMutation({
    mutationFn: async ({
      ids,
      action,
    }: {
      ids: string[];
      action: "delete" | "seen" | "unseen";
    }) => {
      return unwrap(
        await api.POST("/api/v1/projects/{project}/messages/bulk", {
          params: { path: { project: project.slug } },
          body: { ids, action },
        }),
      );
    },
    onSuccess: async (res, vars) => {
      setConfirmBulkDelete(false);
      setSelectedIds(new Set());
      setLastSelectedIndex(null);

      if (vars.action === "delete") {
        toast.success(`Deleted ${res.affected} ${res.affected === 1 ? "message" : "messages"}`);
        if (activeMessageId && vars.ids.includes(activeMessageId)) {
          void navigate({ to: "/p/$project", params: { project: project.slug } });
        }
      } else if (vars.action === "seen") {
        toast.success("Marked as read");
      } else {
        toast.success("Marked as unread");
      }
      await onMutated();
    },
  });

  const deleteAllMutation = useMutation({
    mutationFn: async () =>
      unwrap(
        await api.DELETE("/api/v1/projects/{project}/messages", {
          params: { path: { project: project.slug } },
        }),
      ),
    onSuccess: async (res) => {
      onDeleteAllOpenChange(false);
      setSelectedIds(new Set());
      setLastSelectedIndex(null);
      toast.success(`Deleted ${res.affected} ${res.affected === 1 ? "message" : "messages"}`);
      if (activeMessageId) {
        void navigate({ to: "/p/$project", params: { project: project.slug } });
      }
      await onMutated();
    },
  });

  const rowEstimate = density === "compact" ? 56 : 70;
  const rowVirtualizer = useVirtualizer({
    count: messages.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowEstimate,
    overscan: 8,
  });

  const prevDensityRef = useRef(density);
  useEffect(() => {
    if (prevDensityRef.current !== density) {
      prevDensityRef.current = density;
      rowVirtualizer.measure();
    }
  }, [density, rowVirtualizer]);

  const virtualItems = rowVirtualizer.getVirtualItems();

  useEffect(() => {
    const lastItem = virtualItems[virtualItems.length - 1];
    if (!lastItem) {
      return;
    }
    if (lastItem.index >= messages.length - 5 && hasNextPage && !isFetchingNextPage) {
      onFetchNextPage();
    }
  }, [virtualItems, messages.length, hasNextPage, isFetchingNextPage, onFetchNextPage]);

  const openMessageByIndex = useCallback(
    (index: number) => {
      const target = messages[index];
      if (!target) {
        return;
      }
      rowVirtualizer.scrollToIndex(index, { align: "auto" });
      void navigate({
        to: "/p/$project/m/$messageId",
        params: { project: project.slug, messageId: target.id },
      });
    },
    [messages, navigate, project.slug, rowVirtualizer],
  );

  const handleToggleSelect = useCallback(
    (id: string, index: number, shiftKey: boolean) => {
      setSelectedIds((prev) => {
        const next = new Set(prev);
        if (shiftKey && lastSelectedIndex !== null) {
          const start = Math.min(lastSelectedIndex, index);
          const end = Math.max(lastSelectedIndex, index);
          for (let i = start; i <= end; i += 1) {
            const msg = messages[i];
            if (msg) {
              next.add(msg.id);
            }
          }
        } else if (next.has(id)) {
          next.delete(id);
        } else {
          next.add(id);
        }
        return next;
      });
      setLastSelectedIndex(index);
    },
    [lastSelectedIndex, messages],
  );

  const selectedArray = useMemo(() => Array.from(selectedIds), [selectedIds]);
  const bulkMutate = bulkMutation.mutate;

  const inboxShortcuts = useMemo<KeyShortcutBinding[]>(() => {
    const currentIdx = activeMessageId ? messages.findIndex((m) => m.id === activeMessageId) : -1;
    const activeMsg = currentIdx >= 0 ? messages[currentIdx] : undefined;

    const moveSelection = (delta: number, extend: boolean) => {
      if (messages.length === 0) {
        return;
      }
      let nextIdx: number;
      if (currentIdx === -1) {
        nextIdx = delta > 0 ? 0 : messages.length - 1;
      } else {
        nextIdx = Math.max(0, Math.min(messages.length - 1, currentIdx + delta));
      }
      const nextMsg = messages[nextIdx];
      if (!nextMsg) {
        return;
      }
      if (extend) {
        setSelectedIds((prev) => {
          const next = new Set(prev);
          if (activeMsg) {
            next.add(activeMsg.id);
          }
          next.add(nextMsg.id);
          return next;
        });
        setLastSelectedIndex(nextIdx);
      }
      openMessageByIndex(nextIdx);
    };

    const deleteCurrentOrBulk = () => {
      if (selectedArray.length > 0) {
        setConfirmBulkDelete(true);
        return;
      }
      if (activeMsg) {
        onDeleteSingle(activeMsg);
      }
    };

    return [
      {
        keys: "j",
        handler: () => moveSelection(1, false),
      },
      {
        keys: "k",
        handler: () => moveSelection(-1, false),
      },
      {
        keys: "Shift+j",
        handler: () => moveSelection(1, true),
      },
      {
        keys: "Shift+k",
        handler: () => moveSelection(-1, true),
      },
      {
        keys: "o",
        handler: () => {
          if (activeMsg) {
            readingPaneRef.current?.focus();
          } else if (messages.length > 0) {
            openMessageByIndex(0);
          }
        },
      },
      {
        keys: "Enter",
        when: (event) => {
          const target = event.target;
          return target === scrollRef.current || target === document.body;
        },
        handler: () => {
          if (activeMsg) {
            readingPaneRef.current?.focus();
          } else if (messages.length > 0) {
            openMessageByIndex(0);
          }
        },
      },
      {
        keys: "Escape",
        handler: () => {
          if (selectedIds.size > 0) {
            setSelectedIds(new Set());
            setLastSelectedIndex(null);
            return;
          }
          scrollRef.current?.focus();
        },
      },
      {
        keys: "x",
        handler: () => {
          const idx = Math.max(currentIdx, 0);
          const target = messages[idx];
          if (target) {
            handleToggleSelect(target.id, idx, false);
          }
        },
      },
      {
        keys: "e",
        handler: deleteCurrentOrBulk,
      },
      {
        keys: "#",
        handler: deleteCurrentOrBulk,
      },
      {
        keys: "Delete",
        handler: deleteCurrentOrBulk,
      },
      {
        keys: "u",
        handler: () => {
          if (selectedArray.length > 0) {
            const anyUnread = messages.some((m) => selectedIds.has(m.id) && !m.seen);
            bulkMutate({ ids: selectedArray, action: anyUnread ? "seen" : "unseen" });
            return;
          }
          if (activeMsg) {
            onToggleSeen(activeMsg);
          }
        },
      },
    ];
  }, [
    activeMessageId,
    messages,
    openMessageByIndex,
    selectedArray,
    selectedIds,
    onDeleteSingle,
    onToggleSeen,
    readingPaneRef,
    handleToggleSelect,
    bulkMutate,
  ]);

  useKeyShortcuts(inboxShortcuts, { singleKeyEnabled });

  const scheduleHoverPrefetch = useCallback(
    (messageId: string) => {
      if (hoverTimerRef.current !== null) {
        window.clearTimeout(hoverTimerRef.current);
      }
      hoverTimerRef.current = window.setTimeout(() => {
        hoverTimerRef.current = null;
        void queryClient.prefetchQuery(messageDetailQueryOptions(project.slug, messageId));
      }, HOVER_PREFETCH_DELAY_MS);
    },
    [queryClient, project.slug],
  );

  const cancelHoverPrefetch = useCallback(() => {
    if (hoverTimerRef.current !== null) {
      window.clearTimeout(hoverTimerRef.current);
      hoverTimerRef.current = null;
    }
  }, []);

  const allLoadedSelected = messages.length > 0 && selectedIds.size === messages.length;
  const headingLabel = filter === "unread" ? "Unread" : "Inbox";

  return (
    <section
      aria-label="Message list"
      className="relative flex h-full min-w-0 flex-col bg-surface text-[13px]"
    >
      {selectedIds.size > 0 ? (
        <div
          role="toolbar"
          aria-label="Bulk message actions"
          className="flex h-11 shrink-0 items-center justify-between gap-2 border-b border-border bg-accent/10 px-3"
        >
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="icon"
              aria-label="Clear selection"
              onClick={() => {
                setSelectedIds(new Set());
                setLastSelectedIndex(null);
              }}
              className="size-7"
            >
              <X className="size-3.5" aria-hidden="true" />
            </Button>
            <span className="text-xs font-medium text-foreground tabular-nums">
              {selectedIds.size} selected
            </span>
            <Button
              variant="ghost"
              size="xs"
              onClick={() => {
                if (allLoadedSelected) {
                  setSelectedIds(new Set());
                } else {
                  setSelectedIds(new Set(messages.map((m) => m.id)));
                }
              }}
            >
              {allLoadedSelected ? "None" : "Select all"}
            </Button>
          </div>

          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="xs"
              disabled={bulkMutation.isPending}
              onClick={() => bulkMutation.mutate({ ids: selectedArray, action: "seen" })}
              title="Mark selected as read"
            >
              <MailOpen className="size-3.5" aria-hidden="true" />
              <span className="hidden xl:inline">Mark read</span>
            </Button>
            <Button
              variant="outline"
              size="xs"
              disabled={bulkMutation.isPending}
              onClick={() => bulkMutation.mutate({ ids: selectedArray, action: "unseen" })}
              title="Mark selected as unread"
            >
              <Mail className="size-3.5" aria-hidden="true" />
              <span className="hidden xl:inline">Mark unread</span>
            </Button>
            <Button
              variant="destructive"
              size="xs"
              disabled={bulkMutation.isPending}
              onClick={() => setConfirmBulkDelete(true)}
            >
              <Trash2 className="size-3.5" aria-hidden="true" />
              <span>Delete</span>
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex h-11 shrink-0 items-center justify-between gap-2 border-b border-border px-3">
          <div className="flex min-w-0 items-center gap-2">
            {showSidebarTrigger && onOpenSidebar ? (
              <Button
                variant="ghost"
                size="icon"
                aria-label="Open sidebar"
                title="Open sidebar ([)"
                onClick={onOpenSidebar}
                className="size-7"
              >
                <PanelLeftOpen className="size-4" aria-hidden="true" />
              </Button>
            ) : null}
            <h1 className="truncate text-[13px] font-semibold text-foreground">{headingLabel}</h1>
            <span className="text-xs text-muted-foreground tabular-nums">
              {formatCount(filter === "unread" ? project.unread_count : project.message_count)}
            </span>
          </div>

          <div className="flex items-center gap-1">
            {messages.length > 0 ? (
              <Button
                variant="ghost"
                size="icon"
                aria-label="Select all messages"
                title="Select all loaded messages"
                onClick={() => setSelectedIds(new Set(messages.map((m) => m.id)))}
                className="size-7"
              >
                <CheckSquare className="size-3.5" aria-hidden="true" />
              </Button>
            ) : null}
            <Button
              variant="ghost"
              size="icon"
              aria-label="Refresh messages"
              title="Refresh messages"
              onClick={onRefresh}
              className="size-7"
            >
              <RefreshCw className="size-3.5" aria-hidden="true" />
            </Button>
            {project.message_count > 0 ? (
              <Button
                variant="ghost"
                size="xs"
                className="text-muted-foreground hover:text-destructive"
                onClick={() => onDeleteAllOpenChange(true)}
              >
                <Trash2 className="size-3.5" aria-hidden="true" />
                <span>Delete all</span>
              </Button>
            ) : null}
          </div>
        </div>
      )}

      {newSinceScrolled > 0 ? (
        <div className="pointer-events-none absolute top-13 right-0 left-0 z-20 flex justify-center">
          <button
            type="button"
            onClick={() => {
              scrollRef.current?.scrollTo({ top: 0, behavior: "smooth" });
              setNewSinceScrolled(0);
            }}
            className="pointer-events-auto inline-flex cursor-pointer items-center gap-1.5 rounded-full bg-accent px-3 py-1 text-xs font-medium text-accent-foreground shadow-md transition-opacity hover:opacity-95"
          >
            <ArrowUp className="size-3" aria-hidden="true" />
            <span className="tabular-nums">{formatCount(newSinceScrolled)} new</span>
          </button>
        </div>
      ) : null}

      <div
        ref={scrollRef}
        role="listbox"
        tabIndex={0}
        aria-multiselectable="true"
        aria-activedescendant={activeMessageId ? `msg-row-${activeMessageId}` : undefined}
        aria-label={`${project.name} messages`}
        onScroll={(e) => {
          if (e.currentTarget.scrollTop <= 16 && newSinceScrolled > 0) {
            setNewSinceScrolled(0);
          }
        }}
        className="min-h-0 flex-1 overflow-y-auto"
      >
        {isLoading ? (
          <div className="flex flex-col divide-y divide-border">
            {SKELETON_ROWS.map((id) => (
              <div key={id} className="flex flex-col gap-2 px-3 py-3">
                <div className="flex items-center justify-between gap-2">
                  <Skeleton className="h-3.5 w-28" />
                  <Skeleton className="h-3 w-10" />
                </div>
                <Skeleton className="h-3 w-48" />
              </div>
            ))}
          </div>
        ) : messages.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 px-4 py-12 text-center text-muted-foreground">
            <Mail className="size-6 opacity-60" aria-hidden="true" />
            <p className="text-[13px] font-medium text-foreground">
              {filter === "unread" ? "No unread messages" : "No messages yet"}
            </p>
            <p className="text-xs text-muted-foreground">
              {filter === "unread"
                ? "All messages in this project have been read."
                : "Send mail via SMTP or use the setup guide to test."}
            </p>
          </div>
        ) : (
          <div className="relative w-full" style={{ height: `${rowVirtualizer.getTotalSize()}px` }}>
            {virtualItems.map((virtualRow) => {
              const msg = messages[virtualRow.index];
              if (!msg) {
                return null;
              }
              const isSelected = selectedIds.has(msg.id);
              const isActive = activeMessageId === msg.id;
              const isArrived = highlightedMessageIds.has(msg.id);
              const sender = getSenderLabel(msg.from);
              const subject = msg.subject.trim() || "(no subject)";
              const relTime = formatRelativeTime(msg.received_at, now, clock24h);
              const exactTime = formatExactTimestamp(msg.received_at, clock24h).combined;
              const visibleTags = msg.tags.slice(0, 2);
              const extraTags = Math.max(0, msg.tags.length - 2);

              const ariaLabelParts = [
                msg.seen ? "Read" : "Unread",
                `From ${sender}`,
                subject,
                relTime,
                msg.attachment_count > 0
                  ? `${msg.attachment_count} ${msg.attachment_count === 1 ? "attachment" : "attachments"}`
                  : null,
              ].filter(Boolean);

              const handleCheckboxClick = (e: MouseEvent<HTMLInputElement>) => {
                e.stopPropagation();
                handleToggleSelect(msg.id, virtualRow.index, e.shiftKey);
              };

              return (
                <div
                  key={msg.id}
                  id={`msg-row-${msg.id}`}
                  data-index={virtualRow.index}
                  ref={rowVirtualizer.measureElement}
                  role="option"
                  tabIndex={-1}
                  aria-selected={isActive || isSelected}
                  onMouseEnter={() => scheduleHoverPrefetch(msg.id)}
                  onMouseLeave={cancelHoverPrefetch}
                  style={{
                    transform: `translateY(${virtualRow.start}px)`,
                  }}
                  className={cn(
                    "absolute top-0 left-0 flex w-full items-start gap-2.5 border-b border-border px-3 transition-colors duration-150",
                    density === "compact" ? "py-1.5" : "py-3",
                    isActive ? "bg-accent/10" : isSelected ? "bg-muted/70" : "hover:bg-muted/40",
                    isArrived && "arrival-highlight",
                  )}
                >
                  <div className="flex shrink-0 items-center gap-1.5 pt-0.5">
                    <input
                      type="checkbox"
                      aria-label={`Select message: ${subject}`}
                      checked={isSelected}
                      onChange={() => {}}
                      onClick={handleCheckboxClick}
                      className="size-3.5 cursor-pointer rounded-xs border border-border accent-accent"
                    />
                    <span
                      className={cn(
                        "size-2 rounded-full",
                        msg.seen ? "bg-transparent" : "bg-accent",
                      )}
                      aria-hidden="true"
                    />
                  </div>

                  <button
                    type="button"
                    aria-label={ariaLabelParts.join(". ")}
                    onClick={() => {
                      void navigate({
                        to: "/p/$project/m/$messageId",
                        params: { project: project.slug, messageId: msg.id },
                      });
                    }}
                    className="flex min-w-0 flex-1 cursor-pointer flex-col gap-0.5 text-left"
                  >
                    <div className="flex items-baseline justify-between gap-2">
                      <span
                        className={cn(
                          "truncate text-[13px]",
                          msg.seen
                            ? "font-normal text-foreground"
                            : "font-semibold text-foreground",
                        )}
                      >
                        {sender}
                      </span>
                      <span
                        title={exactTime}
                        className="shrink-0 text-[11px] text-muted-foreground tabular-nums"
                      >
                        {relTime}
                      </span>
                    </div>

                    <div className="flex items-center justify-between gap-2">
                      <span
                        className={cn(
                          "truncate text-xs",
                          msg.seen ? "text-foreground/85" : "font-medium text-foreground",
                        )}
                      >
                        {subject}
                      </span>
                      <div className="flex shrink-0 items-center gap-1">
                        {msg.attachment_count > 0 ? (
                          <Paperclip className="size-3 text-muted-foreground" aria-hidden="true" />
                        ) : null}
                        {visibleTags.map((tag) => (
                          <Badge key={tag} variant="secondary" className="max-w-20 truncate">
                            {tag}
                          </Badge>
                        ))}
                        {extraTags > 0 ? <Badge variant="outline">+{extraTags}</Badge> : null}
                      </div>
                    </div>

                    {msg.snippet ? (
                      <p className="truncate text-xs text-muted-foreground">{msg.snippet}</p>
                    ) : null}
                  </button>
                </div>
              );
            })}
          </div>
        )}

        {hasNextPage ? (
          <div className="flex justify-center py-3">
            <Button
              variant="ghost"
              size="xs"
              disabled={isFetchingNextPage}
              onClick={onFetchNextPage}
            >
              {isFetchingNextPage ? "Loading more..." : "Load more"}
            </Button>
          </div>
        ) : null}
      </div>

      <ConfirmDialog
        open={confirmBulkDelete}
        onOpenChange={setConfirmBulkDelete}
        title={`Delete ${selectedIds.size} ${selectedIds.size === 1 ? "message" : "messages"}?`}
        description={`This permanently removes ${selectedIds.size} selected ${selectedIds.size === 1 ? "message" : "messages"} from ${project.name}.`}
        confirmLabel="Delete"
        destructive
        pending={bulkMutation.isPending}
        onConfirm={() => bulkMutation.mutate({ ids: selectedArray, action: "delete" })}
      />

      <ConfirmDialog
        open={deleteAllOpen}
        onOpenChange={onDeleteAllOpenChange}
        title={`Delete all ${formatCount(project.message_count)} messages?`}
        description={`This permanently deletes all ${formatCount(project.message_count)} ${project.message_count === 1 ? "message" : "messages"} in ${project.name}. This cannot be undone.`}
        confirmLabel="Delete all"
        destructive
        pending={deleteAllMutation.isPending}
        onConfirm={() => deleteAllMutation.mutate()}
      />
    </section>
  );
}
