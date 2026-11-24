import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { AlertCircle, ArrowLeft, Eye, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { useAppCommands } from "@/components/AppCommandContext";
import { Button } from "@/components/ui/button";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable";
import { Skeleton } from "@/components/ui/skeleton";
import { InboxProvider } from "@/features/inbox/InboxContext";
import { InboxSidebar } from "@/features/inbox/InboxSidebar";
import { MessageListPane } from "@/features/inbox/MessageListPane";
import {
  ApiError,
  api,
  getApiErrorMessage,
  type MessageDetail,
  type MessageSummary,
  type ProjectDetail,
  type ProjectSummary,
  unwrap,
} from "@/lib/api";
import { scheduleOptimisticDelete } from "@/lib/pendingDeletes";
import { useSingleKeyShortcutsPreference } from "@/lib/preferences";
import {
  metaQueryOptions,
  projectQueryOptions,
  projectSMTPCredentialsQueryOptions,
  projectTagsQueryOptions,
} from "@/lib/queries";
import { type MessageListQueryParams, queryKeys } from "@/lib/queryKeys";
import { type KeyShortcutBinding, useKeyShortcuts } from "@/lib/shortcuts";
import {
  isFilteredMessageListKey,
  type MessageInfiniteData,
  updateMessagesInPages,
} from "@/lib/sseReducer";
import { usePersistentState } from "@/lib/usePersistentState";
import { getEffectiveRole } from "@/lib/viewerPreview";
import type { InboxFilter } from "@/routes/p.$project";

interface InboxPaneSizes {
  sidebar: number;
  list: number;
  reading: number;
}

const DEFAULT_PANE_SIZES: InboxPaneSizes = {
  sidebar: 18,
  list: 32,
  reading: 50,
};

function isPaneSizes(value: unknown): value is InboxPaneSizes {
  if (!value || typeof value !== "object") {
    return false;
  }
  const v = value as Record<string, unknown>;
  return (
    typeof v.sidebar === "number" &&
    typeof v.list === "number" &&
    typeof v.reading === "number" &&
    v.sidebar >= 10 &&
    v.list >= 15 &&
    v.reading >= 20
  );
}

function isBoolean(value: unknown): value is boolean {
  return typeof value === "boolean";
}

function useViewportWidth(): number {
  const [width, setWidth] = useState<number>(() =>
    typeof window === "undefined" ? 1280 : window.innerWidth,
  );
  useEffect(() => {
    const onResize = () => setWidth(window.innerWidth);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  return width;
}

export function InboxLayout() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const params = useParams({ strict: false });
  const search = useSearch({ from: "/p/$project" });
  const projectSlug = params.project ?? "";
  const activeMessageId = params.messageId;
  const searchQuery = search.q ?? (search.filter === "unread" ? "is:unread" : "");
  const activeTag = search.tag;
  const activeCredentialId = search.credential;
  const filter: InboxFilter =
    searchQuery.trim().toLowerCase() === "is:unread" &&
    !activeTag &&
    activeCredentialId === undefined
      ? "unread"
      : "all";

  const { setActiveInbox, previewAsViewer, setPreviewAsViewer } = useAppCommands();
  const [singleKeyEnabled] = useSingleKeyShortcutsPreference();

  const viewportWidth = useViewportWidth();
  const isDesktop = viewportWidth >= 1024;
  const isMobile = viewportWidth < 768;

  const [paneSizes, setPaneSizes] = usePersistentState<InboxPaneSizes>(
    "inbox-pane-sizes",
    DEFAULT_PANE_SIZES,
    isPaneSizes,
  );
  const [sidebarCollapsed, setSidebarCollapsed] = usePersistentState<boolean>(
    "inbox-sidebar-collapsed",
    false,
    isBoolean,
  );
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [deleteAllOpen, setDeleteAllOpen] = useState(false);
  const [tagEditorOpen, setTagEditorOpen] = useState(false);
  const readingPaneRef = useRef<HTMLDivElement | null>(null);
  const searchInputRef = useRef<HTMLInputElement | null>(null);

  const { data: meta } = useQuery(metaQueryOptions());
  const {
    data: project,
    isPending: projectPending,
    error: projectError,
    refetch: refetchProject,
  } = useQuery(projectQueryOptions(projectSlug));

  const { data: projectTags = [] } = useQuery({
    ...projectTagsQueryOptions(projectSlug),
    enabled: Boolean(project),
  });

  const effectiveRole = project ? getEffectiveRole(project.role, previewAsViewer) : "viewer";
  const isPreviewingAsViewer = Boolean(project && project.role === "owner" && previewAsViewer);

  const { data: credentialsData } = useQuery({
    ...projectSMTPCredentialsQueryOptions(projectSlug),
    enabled: Boolean(project) && meta?.mode === "server" && effectiveRole !== "viewer",
  });
  const projectCredentials = useMemo(
    () => (effectiveRole !== "viewer" ? (credentialsData?.items ?? []) : []),
    [credentialsData, effectiveRole],
  );
  const activeCredentialLabel = useMemo(() => {
    if (activeCredentialId === undefined) {
      return undefined;
    }
    return (
      projectCredentials.find((c) => c.id === activeCredentialId)?.label ?? `#${activeCredentialId}`
    );
  }, [activeCredentialId, projectCredentials]);

  const trimmedQuery = searchQuery.trim();
  const listParams = useMemo<MessageListQueryParams>(
    () => ({
      ...(trimmedQuery ? { query: trimmedQuery } : {}),
      ...(activeTag ? { tag: activeTag } : {}),
      ...(activeCredentialId !== undefined ? { credential: activeCredentialId } : {}),
    }),
    [trimmedQuery, activeTag, activeCredentialId],
  );

  const messagesQuery = useInfiniteQuery({
    queryKey: queryKeys.messages(projectSlug, listParams),
    initialPageParam: undefined as string | undefined,
    enabled: Boolean(project),
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/messages", {
          params: {
            path: { project: projectSlug },
            query: {
              limit: 50,
              ...(pageParam ? { cursor: pageParam } : {}),
              ...(listParams.query ? { query: listParams.query } : {}),
              ...(listParams.tag ? { tag: listParams.tag } : {}),
              ...(listParams.credential !== undefined ? { credential: listParams.credential } : {}),
            },
          },
        }),
      ),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
  });

  const visibleMessages = useMemo(
    () => messagesQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [messagesQuery.data],
  );

  const recentSenders = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    const addFromList = (list: ReadonlyArray<MessageSummary>) => {
      for (const m of list) {
        for (const candidate of [m.from.address, m.from.name]) {
          const val = candidate.trim();
          if (!val) {
            continue;
          }
          const key = val.toLowerCase();
          if (seen.has(key)) {
            continue;
          }
          seen.add(key);
          out.push(val);
          if (out.length >= 20) {
            return;
          }
        }
      }
    };
    addFromList(visibleMessages);
    if (out.length < 20) {
      const unfiltered = queryClient.getQueryData<MessageInfiniteData>(
        queryKeys.messages(projectSlug),
      );
      if (unfiltered) {
        addFromList(unfiltered.pages.flatMap((p) => p.items));
      }
    }
    return out;
  }, [visibleMessages, queryClient, projectSlug]);

  const prevMessageKeyRef = useRef(`${projectSlug}:${activeMessageId ?? ""}`);
  const currentMessageKey = `${projectSlug}:${activeMessageId ?? ""}`;
  if (prevMessageKeyRef.current !== currentMessageKey) {
    prevMessageKeyRef.current = currentMessageKey;
    if (tagEditorOpen) {
      setTagEditorOpen(false);
    }
  }

  useEffect(() => {
    const instanceName = meta?.instance_name || "Mailcubby";
    if (!project) {
      document.title = instanceName;
      return;
    }
    const prefix = project.unread_count > 0 ? `(${project.unread_count}) ` : "";
    document.title = `${prefix}${project.name} · ${instanceName}`;
  }, [project, meta?.instance_name]);

  const updateSearchState = useCallback(
    (
      nextQ: string | undefined,
      nextTag: string | undefined,
      nextCredential: number | undefined,
      replace = true,
    ) => {
      const cleanedQ = nextQ?.trim() || undefined;
      const cleanedTag = nextTag?.trim() || undefined;
      const nextSearch = {
        ...(cleanedQ ? { q: cleanedQ } : {}),
        ...(cleanedTag ? { tag: cleanedTag } : {}),
        ...(nextCredential !== undefined ? { credential: nextCredential } : {}),
      };
      if (activeMessageId) {
        void navigate({
          to: "/p/$project/m/$messageId",
          params: { project: projectSlug, messageId: activeMessageId },
          search: (prev) => ({
            ...(prev.tab ? { tab: prev.tab } : {}),
            ...nextSearch,
          }),
          replace,
        });
      } else {
        void navigate({
          to: "/p/$project",
          params: { project: projectSlug },
          search: nextSearch,
          replace,
        });
      }
    },
    [navigate, projectSlug, activeMessageId],
  );

  const handleSelectFilter = useCallback(
    (next: InboxFilter) => {
      updateSearchState(next === "unread" ? "is:unread" : undefined, undefined, undefined, false);
    },
    [updateSearchState],
  );

  const handleSelectTag = useCallback(
    (tagName: string | undefined) => {
      updateSearchState(searchQuery || undefined, tagName, activeCredentialId, false);
    },
    [updateSearchState, searchQuery, activeCredentialId],
  );

  const handleSelectCredential = useCallback(
    (credentialId: number | undefined) => {
      updateSearchState(searchQuery || undefined, activeTag, credentialId, false);
    },
    [updateSearchState, searchQuery, activeTag],
  );

  const handleChangeQuery = useCallback(
    (nextQ: string) => {
      updateSearchState(nextQ, activeTag, activeCredentialId, true);
    },
    [updateSearchState, activeTag, activeCredentialId],
  );

  const handleClearTag = useCallback(() => {
    updateSearchState(searchQuery || undefined, undefined, activeCredentialId, false);
  }, [updateSearchState, searchQuery, activeCredentialId]);

  const handleClearCredential = useCallback(() => {
    updateSearchState(searchQuery || undefined, activeTag, undefined, false);
  }, [updateSearchState, searchQuery, activeTag]);

  const handleClearSearchAll = useCallback(() => {
    updateSearchState(undefined, undefined, undefined, false);
  }, [updateSearchState]);

  const invalidateInbox = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.project(projectSlug) }),
      queryClient.invalidateQueries({ queryKey: queryKeys.projects() }),
      queryClient.invalidateQueries({ queryKey: queryKeys.tags(projectSlug) }),
      queryClient.invalidateQueries({
        predicate: (q) =>
          q.queryKey.length >= 3 &&
          q.queryKey[0] === "projects" &&
          q.queryKey[1] === projectSlug &&
          q.queryKey[2] === "messages",
      }),
    ]);
  }, [queryClient, projectSlug]);

  const seenMutation = useMutation({
    mutationFn: async ({ id, seen }: { id: string; seen: boolean }) =>
      unwrap(
        await api.PATCH("/api/v1/projects/{project}/messages/{id}", {
          params: { path: { project: projectSlug, id } },
          body: { seen },
        }),
      ),
    onMutate: ({ id, seen }) => {
      // Optimistically reflect seen state in cached list, detail, and unread count.
      const existing = visibleMessages.find((m) => m.id === id);
      const changed = existing ? existing.seen !== seen : true;
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
        const next = updateMessagesInPages(current, [id], { seen });
        if (next !== current) {
          queryClient.setQueryData<MessageInfiniteData>(key, next);
        }
      }
      queryClient.setQueryData<MessageDetail>(queryKeys.message(projectSlug, id), (prev) =>
        prev ? { ...prev, seen } : prev,
      );
      if (changed) {
        const delta = seen ? -1 : 1;
        queryClient.setQueryData<ProjectDetail>(queryKeys.project(projectSlug), (prev) =>
          prev ? { ...prev, unread_count: Math.max(0, prev.unread_count + delta) } : prev,
        );
        queryClient.setQueryData<ProjectSummary[]>(queryKeys.projects(), (prev) =>
          prev?.map((p) =>
            p.slug === projectSlug
              ? { ...p, unread_count: Math.max(0, p.unread_count + delta) }
              : p,
          ),
        );
      }
      return { id, previousSeen: existing?.seen };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx) {
        void invalidateInbox();
      }
    },
  });

  const toggleMessageSeen = useCallback(
    (message: MessageSummary, nextSeen?: boolean) => {
      if (effectiveRole === "viewer") {
        return;
      }
      const targetSeen = nextSeen ?? !message.seen;
      seenMutation.mutate({ id: message.id, seen: targetSeen });
    },
    [effectiveRole, seenMutation],
  );

  const tagsMutation = useMutation({
    mutationFn: async ({ id, tags }: { id: string; tags: string[] }) =>
      unwrap(
        await api.PATCH("/api/v1/projects/{project}/messages/{id}", {
          params: { path: { project: projectSlug, id } },
          body: { tags },
        }),
      ),
    onSuccess: async (updated) => {
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
        const next = updateMessagesInPages(current, [updated.id], { tags: updated.tags });
        if (next !== current) {
          queryClient.setQueryData<MessageInfiniteData>(key, next);
        }
      }
      queryClient.setQueryData<MessageDetail>(queryKeys.message(projectSlug, updated.id), (prev) =>
        prev ? { ...prev, tags: updated.tags } : prev,
      );
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.tags(projectSlug) }),
        queryClient.invalidateQueries({
          predicate: (q) => isFilteredMessageListKey(q.queryKey, projectSlug),
        }),
      ]);
    },
  });

  const updateMessageTags = useCallback(
    (message: MessageSummary, nextTags: string[]) => {
      if (effectiveRole === "viewer") {
        return;
      }
      tagsMutation.mutate({ id: message.id, tags: nextTags });
    },
    [effectiveRole, tagsMutation],
  );

  const deleteMessageWithUndo = useCallback(
    (message: MessageSummary) => {
      if (effectiveRole === "viewer") {
        return;
      }
      const currentIdx = visibleMessages.findIndex((m) => m.id === message.id);
      const nextMsg =
        currentIdx >= 0
          ? (visibleMessages[currentIdx + 1] ?? visibleMessages[currentIdx - 1])
          : undefined;

      scheduleOptimisticDelete(queryClient, projectSlug, message);

      if (activeMessageId === message.id) {
        if (nextMsg) {
          void navigate({
            to: "/p/$project/m/$messageId",
            params: { project: projectSlug, messageId: nextMsg.id },
            search: (prev) => prev,
          });
        } else {
          void navigate({
            to: "/p/$project",
            params: { project: projectSlug },
            search: (prev) => prev,
          });
        }
      }
    },
    [effectiveRole, visibleMessages, queryClient, projectSlug, activeMessageId, navigate],
  );

  const sendTestMutation = useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST("/api/v1/projects/{project}/send-test", {
          params: { path: { project: projectSlug } },
        }),
      ),
    onSuccess: async (created) => {
      toast.success("Test email sent");
      await invalidateInbox();
      void navigate({
        to: "/p/$project/m/$messageId",
        params: { project: projectSlug, messageId: created.id },
      });
    },
  });

  const toggleSidebar = useCallback(() => {
    if (isDesktop) {
      setSidebarCollapsed((prev) => !prev);
    } else {
      setDrawerOpen((prev) => !prev);
    }
  }, [isDesktop, setSidebarCollapsed]);

  const focusSearch = useCallback(() => {
    const el = searchInputRef.current;
    if (el) {
      el.focus();
      const len = el.value.length;
      el.setSelectionRange(len, len);
    }
  }, []);

  const applySearchOperator = useCallback(
    (operator: string, complete: boolean) => {
      const base = searchQuery.trim();
      const next = base ? `${base} ${operator}` : operator;
      if (complete) {
        updateSearchState(next, activeTag, activeCredentialId, false);
      } else {
        updateSearchState(next, activeTag, activeCredentialId, true);
        requestAnimationFrame(() => {
          const el = searchInputRef.current;
          if (el) {
            el.focus();
            el.value = next;
            el.setSelectionRange(next.length, next.length);
          }
        });
      }
    },
    [searchQuery, activeTag, activeCredentialId, updateSearchState],
  );

  const sendTestMutate = sendTestMutation.mutate;
  useEffect(() => {
    if (!project) {
      setActiveInbox(null);
      return;
    }
    setActiveInbox({
      projectSlug: project.slug,
      projectName: project.name,
      actualRole: project.role,
      effectiveRole,
      messageCount: project.message_count,
      messages: visibleMessages,
      tags: projectTags,
      toggleSidebar,
      openDeleteAll: () => setDeleteAllOpen(true),
      sendTestEmail: () => sendTestMutate(),
      applySearchOperator,
      selectTagFilter: (tagName) => handleSelectTag(tagName),
    });
    return () => {
      setActiveInbox(null);
    };
  }, [
    project,
    effectiveRole,
    visibleMessages,
    projectTags,
    toggleSidebar,
    sendTestMutate,
    applySearchOperator,
    handleSelectTag,
    setActiveInbox,
  ]);

  const layoutShortcuts = useMemo<KeyShortcutBinding[]>(
    () => [
      {
        keys: "[",
        handler: toggleSidebar,
      },
      {
        keys: "/",
        handler: focusSearch,
      },
    ],
    [toggleSidebar, focusSearch],
  );

  useKeyShortcuts(layoutShortcuts, { singleKeyEnabled });

  if (projectError) {
    const isNotFound = projectError instanceof ApiError && projectError.status === 404;
    return (
      <main className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
        <AlertCircle className="size-8 text-muted-foreground" aria-hidden="true" />
        <div className="max-w-md space-y-1">
          <h1 className="text-base font-semibold text-foreground">
            {isNotFound ? "Project not found or you do not have access" : "Could not load project"}
          </h1>
          <p className="text-[13px] text-muted-foreground">
            {isNotFound
              ? `The project "${projectSlug}" does not exist or is unavailable.`
              : getApiErrorMessage(projectError)}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Link
            to="/"
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-3 text-[13px] font-medium text-foreground hover:bg-muted"
          >
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            <span>Back to projects</span>
          </Link>
          {!isNotFound ? (
            <Button variant="default" size="sm" onClick={() => void refetchProject()}>
              Retry
            </Button>
          ) : null}
        </div>
      </main>
    );
  }

  if (projectPending || !project) {
    return (
      <main className="flex h-full gap-px bg-border">
        <div className="hidden w-56 flex-col gap-3 bg-surface p-3 lg:flex">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-7 w-full" />
          <Skeleton className="h-7 w-full" />
        </div>
        <div className="flex w-80 flex-col gap-2 bg-surface p-3">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
        <div className="hidden flex-1 flex-col gap-3 bg-surface p-6 md:flex">
          <Skeleton className="h-6 w-1/2" />
          <Skeleton className="h-4 w-1/3" />
          <Skeleton className="mt-4 h-64 w-full" />
        </div>
      </main>
    );
  }

  const listPane = (
    <MessageListPane
      project={project}
      effectiveRole={effectiveRole}
      tags={projectTags}
      credentials={projectCredentials}
      messages={visibleMessages}
      recentSenders={recentSenders}
      filter={filter}
      searchQuery={searchQuery}
      {...(activeTag ? { activeTag } : {})}
      {...(activeCredentialLabel ? { activeCredentialLabel } : {})}
      searchInputRef={searchInputRef}
      onChangeQuery={handleChangeQuery}
      onClearTag={handleClearTag}
      onClearCredential={handleClearCredential}
      onClearSearchAll={handleClearSearchAll}
      activeMessageId={activeMessageId}
      isLoading={messagesQuery.isPending}
      isFetchingNextPage={messagesQuery.isFetchingNextPage}
      hasNextPage={Boolean(messagesQuery.hasNextPage)}
      onFetchNextPage={() => void messagesQuery.fetchNextPage()}
      onRefresh={() => void invalidateInbox()}
      onMutated={invalidateInbox}
      onDeleteSingle={deleteMessageWithUndo}
      onToggleSeen={toggleMessageSeen}
      readingPaneRef={readingPaneRef}
      deleteAllOpen={deleteAllOpen}
      onDeleteAllOpenChange={setDeleteAllOpen}
      showSidebarTrigger={!isDesktop || sidebarCollapsed}
      onOpenSidebar={() => {
        if (isDesktop) {
          setSidebarCollapsed(false);
        } else {
          setDrawerOpen(true);
        }
      }}
    />
  );

  const contextValue = {
    project,
    effectiveRole,
    isPreviewingAsViewer,
    tags: projectTags,
    messages: visibleMessages,
    allLoadedCount: visibleMessages.length,
    filter,
    searchQuery,
    activeTag,
    activeCredentialId,
    activeMessageId,
    isLoadingMessages: messagesQuery.isPending,
    readingPaneRef,
    invalidateInbox,
    deleteMessageWithUndo,
    toggleMessageSeen,
    updateMessageTags,
    isUpdatingSeen: seenMutation.isPending,
    isUpdatingTags: tagsMutation.isPending,
    tagEditorOpen,
    setTagEditorOpen,
  };

  return (
    <InboxProvider value={contextValue}>
      <main className="relative flex h-full w-full flex-col overflow-hidden">
        {isPreviewingAsViewer ? (
          <div
            role="status"
            data-testid="viewer-preview-banner"
            className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-amber-500/35 bg-amber-500/10 px-4 py-1.5 text-xs text-foreground"
          >
            <span className="flex items-center gap-2">
              <Eye
                className="size-3.5 shrink-0 text-amber-600 dark:text-amber-400"
                aria-hidden="true"
              />
              <span>
                <strong className="font-semibold">Previewing as viewer.</strong>{" "}
                {project.settings.viewer_hide_technical
                  ? "Delete, tagging, settings, and technical views are hidden."
                  : "Delete, tagging, and settings are hidden."}
              </span>
            </span>
            <Button variant="outline" size="xs" onClick={() => setPreviewAsViewer(false)}>
              Exit preview
            </Button>
          </div>
        ) : null}

        <div className="relative min-h-0 flex-1 overflow-hidden">
          {isDesktop ? (
            <ResizablePanelGroup
              key={sidebarCollapsed ? "two-pane" : "three-pane"}
              orientation="horizontal"
              onLayoutChanged={(layout, metaInfo) => {
                if (!metaInfo.isUserInteraction) {
                  return;
                }
                const nextSidebar = layout.sidebar ?? paneSizes.sidebar;
                const nextList = layout.list ?? paneSizes.list;
                const nextReading = layout.reading ?? paneSizes.reading;
                setPaneSizes({
                  sidebar: nextSidebar,
                  list: nextList,
                  reading: nextReading,
                });
              }}
            >
              {!sidebarCollapsed ? (
                <>
                  <ResizablePanel
                    id="sidebar"
                    defaultSize={`${paneSizes.sidebar}%`}
                    minSize="13%"
                    maxSize="28%"
                  >
                    <InboxSidebar
                      project={project}
                      effectiveRole={effectiveRole}
                      tags={projectTags}
                      credentials={projectCredentials}
                      {...(credentialsData?.connection
                        ? { smtpConnection: credentialsData.connection }
                        : {})}
                      filter={filter}
                      {...(activeTag ? { activeTag } : {})}
                      {...(activeCredentialId !== undefined ? { activeCredentialId } : {})}
                      onSelectFilter={handleSelectFilter}
                      onSelectTag={handleSelectTag}
                      onSelectCredential={handleSelectCredential}
                      onCollapse={() => setSidebarCollapsed(true)}
                    />
                  </ResizablePanel>
                  <ResizableHandle aria-label="Resize sidebar" />
                </>
              ) : null}

              <ResizablePanel
                id="list"
                defaultSize={`${sidebarCollapsed ? Math.min(45, paneSizes.list + 6) : paneSizes.list}%`}
                minSize="22%"
                maxSize="55%"
              >
                {listPane}
              </ResizablePanel>

              <ResizableHandle aria-label="Resize message list" />

              <ResizablePanel
                id="reading"
                defaultSize={`${sidebarCollapsed ? Math.max(55, paneSizes.reading) : paneSizes.reading}%`}
                minSize="30%"
              >
                <div
                  ref={readingPaneRef}
                  tabIndex={-1}
                  className="h-full min-w-0 overflow-hidden bg-background"
                >
                  <Outlet />
                </div>
              </ResizablePanel>
            </ResizablePanelGroup>
          ) : isMobile ? (
            activeMessageId ? (
              <div className="flex h-full flex-col overflow-hidden bg-background">
                <div className="flex h-10 shrink-0 items-center border-b border-border bg-surface px-3">
                  <Link
                    to="/p/$project"
                    params={{ project: project.slug }}
                    className="inline-flex items-center gap-1.5 text-xs font-medium text-foreground hover:text-accent"
                  >
                    <ArrowLeft className="size-3.5" aria-hidden="true" />
                    <span>Back to {project.name}</span>
                  </Link>
                </div>
                <div ref={readingPaneRef} tabIndex={-1} className="min-h-0 flex-1 overflow-hidden">
                  <Outlet />
                </div>
              </div>
            ) : (
              <div className="h-full overflow-hidden">{listPane}</div>
            )
          ) : (
            <ResizablePanelGroup orientation="horizontal">
              <ResizablePanel id="list" defaultSize="40%" minSize="28%" maxSize="60%">
                {listPane}
              </ResizablePanel>
              <ResizableHandle aria-label="Resize message list" />
              <ResizablePanel id="reading" defaultSize="60%" minSize="35%">
                <div
                  ref={readingPaneRef}
                  tabIndex={-1}
                  className="h-full min-w-0 overflow-hidden bg-background"
                >
                  <Outlet />
                </div>
              </ResizablePanel>
            </ResizablePanelGroup>
          )}

          {!isDesktop && drawerOpen ? (
            <div className="fixed inset-0 z-40 flex">
              <div className="relative z-10 flex h-full w-64 flex-col border-r border-border bg-surface shadow-xl">
                <div className="flex h-11 items-center justify-between border-b border-border px-3">
                  <span className="text-xs font-semibold text-foreground">Navigation</span>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label="Close sidebar"
                    onClick={() => setDrawerOpen(false)}
                    className="size-7"
                  >
                    <X className="size-4" aria-hidden="true" />
                  </Button>
                </div>
                <div className="min-h-0 flex-1 overflow-hidden">
                  <InboxSidebar
                    project={project}
                    effectiveRole={effectiveRole}
                    tags={projectTags}
                    credentials={projectCredentials}
                    {...(credentialsData?.connection
                      ? { smtpConnection: credentialsData.connection }
                      : {})}
                    filter={filter}
                    {...(activeTag ? { activeTag } : {})}
                    {...(activeCredentialId !== undefined ? { activeCredentialId } : {})}
                    onSelectFilter={handleSelectFilter}
                    onSelectTag={handleSelectTag}
                    onSelectCredential={handleSelectCredential}
                    onCloseDrawer={() => setDrawerOpen(false)}
                  />
                </div>
              </div>
              <button
                type="button"
                aria-label="Dismiss sidebar"
                onClick={() => setDrawerOpen(false)}
                className="flex-1 bg-black/40"
              />
            </div>
          ) : null}
        </div>
      </main>
    </InboxProvider>
  );
}
