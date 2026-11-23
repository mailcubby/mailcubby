import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { AlertCircle } from "lucide-react";
import { type KeyboardEvent, useCallback, useEffect, useMemo, useRef } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useInboxContext } from "@/features/inbox/InboxContext";
import { AttachmentsTab } from "@/features/message/AttachmentsTab";
import { HeadersTab } from "@/features/message/HeadersTab";
import { HtmlSourceTab } from "@/features/message/HtmlSourceTab";
import { MessageHeader } from "@/features/message/MessageHeader";
import { PreviewTab } from "@/features/message/PreviewTab";
import { RawTab } from "@/features/message/RawTab";
import { TechTab } from "@/features/message/TechTab";
import { TextTab } from "@/features/message/TextTab";
import { ApiError, api, getApiErrorMessage, unwrap, unwrapVoid } from "@/lib/api";
import { queryKeys } from "@/lib/queryKeys";
import { usePersistentState } from "@/lib/usePersistentState";
import { cn } from "@/lib/utils";
import { isMessageTab, type MessageTab } from "@/routes/p.$project.m.$messageId";

interface TabDefinition {
  id: MessageTab;
  label: string;
}

export function MessageView() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { project: projectSlug, messageId } = useParams({ from: "/p/$project/m/$messageId" });
  const search = useSearch({ from: "/p/$project/m/$messageId" });
  const { project, messages, invalidateInbox } = useInboxContext();

  const [savedTab, setSavedTab] = usePersistentState<MessageTab>(
    "message-tab",
    "preview",
    isMessageTab,
  );
  const autoMarkedRef = useRef<string | null>(null);
  const tabRefs = useRef<Partial<Record<MessageTab, HTMLButtonElement | null>>>({});

  const {
    data: message,
    isPending,
    error,
    refetch,
  } = useQuery({
    queryKey: queryKeys.message(projectSlug, messageId),
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/messages/{id}", {
          params: { path: { project: projectSlug, id: messageId } },
        }),
      ),
    retry: (count, err) => {
      if (err instanceof ApiError && err.status === 404) {
        return false;
      }
      return count < 1;
    },
  });

  const seenMutation = useMutation({
    mutationFn: async (seen: boolean) =>
      unwrap(
        await api.PATCH("/api/v1/projects/{project}/messages/{id}", {
          params: { path: { project: projectSlug, id: messageId } },
          body: { seen },
        }),
      ),
    onSuccess: async (updated) => {
      queryClient.setQueryData(queryKeys.message(projectSlug, messageId), (prev: unknown) => {
        if (prev && typeof prev === "object") {
          return { ...(prev as Record<string, unknown>), seen: updated.seen };
        }
        return prev;
      });
      await invalidateInbox();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async () =>
      unwrapVoid(
        await api.DELETE("/api/v1/projects/{project}/messages/{id}", {
          params: { path: { project: projectSlug, id: messageId } },
        }),
      ),
    onSuccess: async () => {
      toast.success("Message deleted");
      const currentIdx = messages.findIndex((m) => m.id === messageId);
      const nextMsg =
        currentIdx >= 0 ? (messages[currentIdx + 1] ?? messages[currentIdx - 1]) : undefined;
      await invalidateInbox();
      if (nextMsg) {
        void navigate({
          to: "/p/$project/m/$messageId",
          params: { project: projectSlug, messageId: nextMsg.id },
        });
      } else {
        void navigate({ to: "/p/$project", params: { project: projectSlug } });
      }
    },
  });

  // Mark message seen when opened by a member/owner (FR-075).
  useEffect(() => {
    if (!message || message.seen || project.role === "viewer") {
      return;
    }
    if (autoMarkedRef.current === message.id) {
      return;
    }
    autoMarkedRef.current = message.id;
    seenMutation.mutate(true);
  }, [message, project.role, seenMutation]);

  const hasPreview = Boolean(message?.has_html && message?.render_url);
  const hideTechnical = project.role === "viewer" && project.settings.viewer_hide_technical;

  const availableTabs = useMemo<TabDefinition[]>(() => {
    const list: TabDefinition[] = [];
    if (hasPreview) {
      list.push({ id: "preview", label: "Preview" });
    }
    list.push({ id: "text", label: "Text" });
    if (!hideTechnical) {
      list.push({ id: "source", label: "HTML source" });
      list.push({ id: "headers", label: "Headers" });
      list.push({ id: "raw", label: "Raw" });
    }
    list.push({ id: "attachments", label: "Attachments" });
    if (!hideTechnical) {
      list.push({ id: "tech", label: "Tech" });
    }
    return list;
  }, [hasPreview, hideTechnical]);

  const requestedTab = search.tab ?? savedTab;
  const activeTab: MessageTab = availableTabs.some((t) => t.id === requestedTab)
    ? requestedTab
    : (availableTabs[0]?.id ?? "text");

  const selectTab = useCallback(
    (tab: MessageTab) => {
      setSavedTab(tab);
      void navigate({
        to: "/p/$project/m/$messageId",
        params: { project: projectSlug, messageId },
        search: { tab },
        replace: true,
      });
    },
    [navigate, projectSlug, messageId, setSavedTab],
  );

  const handleTabKeyDown = (e: KeyboardEvent<HTMLButtonElement>, currentIndex: number) => {
    let nextIndex: number | null = null;
    if (e.key === "ArrowRight") {
      nextIndex = (currentIndex + 1) % availableTabs.length;
    } else if (e.key === "ArrowLeft") {
      nextIndex = (currentIndex - 1 + availableTabs.length) % availableTabs.length;
    } else if (e.key === "Home") {
      nextIndex = 0;
    } else if (e.key === "End") {
      nextIndex = availableTabs.length - 1;
    }
    if (nextIndex === null) {
      return;
    }
    e.preventDefault();
    const nextTab = availableTabs[nextIndex];
    if (nextTab) {
      selectTab(nextTab.id);
      tabRefs.current[nextTab.id]?.focus();
    }
  };

  const currentIdx = messages.findIndex((m) => m.id === messageId);
  const previousMessage = currentIdx > 0 ? messages[currentIdx - 1] : undefined;
  const nextMessage =
    currentIdx >= 0 && currentIdx < messages.length - 1 ? messages[currentIdx + 1] : undefined;

  if (isPending) {
    return (
      <div className="flex h-full flex-col bg-surface p-5">
        <Skeleton className="h-5 w-2/3" />
        <Skeleton className="mt-3 h-4 w-1/3" />
        <Skeleton className="mt-2 h-4 w-1/2" />
        <Skeleton className="mt-6 h-8 w-full" />
        <Skeleton className="mt-4 flex-1 w-full" />
      </div>
    );
  }

  if (error || !message) {
    const isNotFound = error instanceof ApiError && error.status === 404;
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
        <AlertCircle className="size-7 text-muted-foreground" aria-hidden="true" />
        <div className="max-w-sm space-y-1">
          <h2 className="text-sm font-semibold text-foreground">
            {isNotFound ? "Message not found" : "Failed to load message"}
          </h2>
          <p className="text-xs text-muted-foreground">
            {isNotFound
              ? "This message may have been deleted or removed by retention."
              : getApiErrorMessage(error)}
          </p>
        </div>
        {!isNotFound ? (
          <Button variant="outline" size="xs" onClick={() => void refetch()}>
            Retry
          </Button>
        ) : null}
      </div>
    );
  }

  return (
    <article aria-label="Message viewer" className="flex h-full min-h-0 flex-col bg-background">
      <MessageHeader
        projectSlug={projectSlug}
        message={message}
        hasPrevious={Boolean(previousMessage)}
        hasNext={Boolean(nextMessage)}
        onPrevious={() => {
          if (previousMessage) {
            void navigate({
              to: "/p/$project/m/$messageId",
              params: { project: projectSlug, messageId: previousMessage.id },
            });
          }
        }}
        onNext={() => {
          if (nextMessage) {
            void navigate({
              to: "/p/$project/m/$messageId",
              params: { project: projectSlug, messageId: nextMessage.id },
            });
          }
        }}
        onToggleSeen={() => seenMutation.mutate(!message.seen)}
        onDelete={() => deleteMutation.mutate()}
        isUpdatingSeen={seenMutation.isPending}
        isDeleting={deleteMutation.isPending}
      />

      <div
        role="tablist"
        aria-label="Message views"
        className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-border bg-surface px-3"
      >
        {availableTabs.map((tab, idx) => {
          const selected = tab.id === activeTab;
          return (
            <button
              key={tab.id}
              ref={(el) => {
                tabRefs.current[tab.id] = el;
              }}
              id={`msg-tab-${tab.id}`}
              type="button"
              role="tab"
              aria-selected={selected}
              aria-controls={`msg-tabpanel-${tab.id}`}
              tabIndex={selected ? 0 : -1}
              onClick={() => selectTab(tab.id)}
              onKeyDown={(e) => handleTabKeyDown(e, idx)}
              className={cn(
                "relative inline-flex h-9 shrink-0 cursor-pointer items-center gap-1.5 border-b-2 px-2.5 text-xs font-medium transition-colors",
                selected
                  ? "border-accent text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              <span>{tab.label}</span>
              {tab.id === "attachments" && message.attachments.length > 0 ? (
                <Badge variant="secondary">{message.attachments.length}</Badge>
              ) : null}
            </button>
          );
        })}
      </div>

      <div
        id={`msg-tabpanel-${activeTab}`}
        role="tabpanel"
        aria-labelledby={`msg-tab-${activeTab}`}
        className="min-h-0 flex-1 overflow-hidden"
      >
        {activeTab === "preview" ? <PreviewTab key={message.id} message={message} /> : null}
        {activeTab === "text" ? <TextTab message={message} /> : null}
        {activeTab === "source" ? (
          <HtmlSourceTab
            projectSlug={projectSlug}
            messageId={message.id}
            hasHtml={message.has_html}
          />
        ) : null}
        {activeTab === "headers" ? (
          <HeadersTab
            projectSlug={projectSlug}
            messageId={message.id}
            initialHeaders={message.headers}
          />
        ) : null}
        {activeTab === "raw" ? (
          <RawTab projectSlug={projectSlug} messageId={message.id} sizeBytes={message.size_bytes} />
        ) : null}
        {activeTab === "attachments" ? (
          <AttachmentsTab
            projectSlug={projectSlug}
            messageId={message.id}
            attachments={message.attachments}
          />
        ) : null}
        {activeTab === "tech" ? <TechTab message={message} /> : null}
      </div>
    </article>
  );
}
