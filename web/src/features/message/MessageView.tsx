import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { AlertCircle } from "lucide-react";
import { type KeyboardEvent, useCallback, useEffect, useMemo, useRef } from "react";
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
import { ApiError, getApiErrorMessage } from "@/lib/api";
import { useMarkReadDelayPreference, useSingleKeyShortcutsPreference } from "@/lib/preferences";
import { messageDetailQueryOptions } from "@/lib/queries";
import { type KeyShortcutBinding, useKeyShortcuts } from "@/lib/shortcuts";
import { usePersistentState } from "@/lib/usePersistentState";
import { cn } from "@/lib/utils";
import { isMessageTab, type MessageTab } from "@/routes/p.$project.m.$messageId";

interface TabDefinition {
  id: MessageTab;
  label: string;
}

interface NetworkInformationLike {
  saveData?: boolean;
}

const NUMBER_KEY_TO_TAB: ReadonlyArray<{ key: string; tab: MessageTab }> = [
  { key: "1", tab: "preview" },
  { key: "2", tab: "text" },
  { key: "3", tab: "source" },
  { key: "4", tab: "headers" },
  { key: "5", tab: "raw" },
  { key: "6", tab: "attachments" },
  { key: "7", tab: "tech" },
];

const prefetchedRenderUrls = new Set<string>();

function isSaveDataEnabled(): boolean {
  if (typeof navigator === "undefined") {
    return false;
  }
  const nav = navigator as Navigator & { connection?: NetworkInformationLike };
  return Boolean(nav.connection?.saveData);
}

export function MessageView() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { project: projectSlug, messageId } = useParams({ from: "/p/$project/m/$messageId" });
  const search = useSearch({ from: "/p/$project/m/$messageId" });
  const {
    project,
    tags,
    messages,
    deleteMessageWithUndo,
    toggleMessageSeen,
    updateMessageTags,
    isUpdatingSeen,
    isUpdatingTags,
    tagEditorOpen,
    setTagEditorOpen,
  } = useInboxContext();

  const [markReadDelay] = useMarkReadDelayPreference();
  const [singleKeyEnabled] = useSingleKeyShortcutsPreference();
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
  } = useQuery(messageDetailQueryOptions(projectSlug, messageId));

  // Mark message seen after configured delay when opened by a member/owner (FR-075, UI 3.7).
  useEffect(() => {
    if (!message || message.seen || project.role === "viewer" || markReadDelay === "never") {
      return;
    }
    if (autoMarkedRef.current === message.id) {
      return;
    }
    if (markReadDelay === 0) {
      autoMarkedRef.current = message.id;
      toggleMessageSeen(message, true);
      return;
    }
    const timer = window.setTimeout(() => {
      autoMarkedRef.current = message.id;
      toggleMessageSeen(message, true);
    }, markReadDelay);
    return () => {
      window.clearTimeout(timer);
    };
  }, [message, project.role, markReadDelay, toggleMessageSeen]);

  const currentIdx = messages.findIndex((m) => m.id === messageId);
  const previousMessage = currentIdx > 0 ? messages[currentIdx - 1] : undefined;
  const nextMessage =
    currentIdx >= 0 && currentIdx < messages.length - 1 ? messages[currentIdx + 1] : undefined;

  // Prefetch next and previous message details + warm next message render cache on fast connections (UI 4 item 7).
  useEffect(() => {
    if (nextMessage) {
      void queryClient.prefetchQuery(messageDetailQueryOptions(projectSlug, nextMessage.id));
      if (nextMessage.has_html && !isSaveDataEnabled()) {
        const renderUrl = `/render/${encodeURIComponent(projectSlug)}/${encodeURIComponent(nextMessage.id)}`;
        if (!prefetchedRenderUrls.has(renderUrl)) {
          prefetchedRenderUrls.add(renderUrl);
          if (prefetchedRenderUrls.size > 64) {
            prefetchedRenderUrls.clear();
          }
          void fetch(renderUrl, { credentials: "same-origin" }).catch(() => {
            prefetchedRenderUrls.delete(renderUrl);
          });
        }
      }
    }
    if (previousMessage) {
      void queryClient.prefetchQuery(messageDetailQueryOptions(projectSlug, previousMessage.id));
    }
  }, [queryClient, projectSlug, nextMessage, previousMessage]);

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
        search: (prev) => ({ ...prev, tab }),
        replace: true,
      });
    },
    [navigate, projectSlug, messageId, setSavedTab],
  );

  // Number keys 1 to 7 switch message tabs; `t` opens the tag editor (UI 3.5 & 4 item 2).
  const tabShortcutBindings = useMemo<KeyShortcutBinding[]>(
    () => [
      ...NUMBER_KEY_TO_TAB.map(({ key, tab }) => ({
        keys: key,
        enabled: availableTabs.some((t) => t.id === tab),
        handler: () => selectTab(tab),
      })),
      {
        keys: "t",
        enabled: project.role !== "viewer",
        handler: () => setTagEditorOpen(true),
      },
    ],
    [availableTabs, selectTab, project.role, setTagEditorOpen],
  );

  useKeyShortcuts(tabShortcutBindings, { singleKeyEnabled });

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
        projectRole={project.role}
        projectTags={tags}
        message={message}
        hasPrevious={Boolean(previousMessage)}
        hasNext={Boolean(nextMessage)}
        onPrevious={() => {
          if (previousMessage) {
            void navigate({
              to: "/p/$project/m/$messageId",
              params: { project: projectSlug, messageId: previousMessage.id },
              search: (prev) => prev,
            });
          }
        }}
        onNext={() => {
          if (nextMessage) {
            void navigate({
              to: "/p/$project/m/$messageId",
              params: { project: projectSlug, messageId: nextMessage.id },
              search: (prev) => prev,
            });
          }
        }}
        onToggleSeen={() => toggleMessageSeen(message)}
        onUpdateTags={(nextTags) => updateMessageTags(message, nextTags)}
        onDelete={() => deleteMessageWithUndo(message)}
        isUpdatingSeen={isUpdatingSeen}
        isUpdatingTags={isUpdatingTags}
        tagEditorOpen={tagEditorOpen}
        onTagEditorOpenChange={setTagEditorOpen}
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
