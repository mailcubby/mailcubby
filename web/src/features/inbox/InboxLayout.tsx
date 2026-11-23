import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { AlertCircle, ArrowLeft, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable";
import { Skeleton } from "@/components/ui/skeleton";
import { InboxProvider } from "@/features/inbox/InboxContext";
import { InboxSidebar } from "@/features/inbox/InboxSidebar";
import { MessageListPane } from "@/features/inbox/MessageListPane";
import { ApiError, api, getApiErrorMessage, unwrap } from "@/lib/api";
import { metaQueryOptions, projectQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";
import { usePersistentState } from "@/lib/usePersistentState";
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
  const filter: InboxFilter = search.filter === "unread" ? "unread" : "all";

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

  const { data: meta } = useQuery(metaQueryOptions());
  const {
    data: project,
    isPending: projectPending,
    error: projectError,
    refetch: refetchProject,
  } = useQuery(projectQueryOptions(projectSlug));

  const messagesQuery = useInfiniteQuery({
    queryKey: queryKeys.messages(projectSlug),
    initialPageParam: undefined as string | undefined,
    enabled: Boolean(project),
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/messages", {
          params: {
            path: { project: projectSlug },
            query: pageParam ? { limit: 50, cursor: pageParam } : { limit: 50 },
          },
        }),
      ),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
  });

  const allMessages = useMemo(
    () => messagesQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [messagesQuery.data],
  );

  // Phase 4a filters unread client-side until Phase 5 adds server-side `query=is:unread`.
  const visibleMessages = useMemo(
    () => (filter === "unread" ? allMessages.filter((m) => !m.seen) : allMessages),
    [allMessages, filter],
  );

  useEffect(() => {
    const instanceName = meta?.instance_name || "Mailcubby";
    if (!project) {
      document.title = instanceName;
      return;
    }
    const prefix = project.unread_count > 0 ? `(${project.unread_count}) ` : "";
    document.title = `${prefix}${project.name} · ${instanceName}`;
  }, [project, meta?.instance_name]);

  const handleSelectFilter = useCallback(
    (next: InboxFilter) => {
      void navigate({
        to: "/p/$project",
        params: { project: projectSlug },
        search: next === "unread" ? { filter: "unread" } : {},
      });
    },
    [navigate, projectSlug],
  );

  const invalidateInbox = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.project(projectSlug) }),
      queryClient.invalidateQueries({ queryKey: queryKeys.projects() }),
      queryClient.invalidateQueries({ queryKey: queryKeys.messages(projectSlug) }),
    ]);
  }, [queryClient, projectSlug]);

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
      messages={visibleMessages}
      filter={filter}
      activeMessageId={activeMessageId}
      isLoading={messagesQuery.isPending}
      isFetchingNextPage={messagesQuery.isFetchingNextPage}
      hasNextPage={Boolean(messagesQuery.hasNextPage)}
      onFetchNextPage={() => void messagesQuery.fetchNextPage()}
      onRefresh={() => void invalidateInbox()}
      onMutated={invalidateInbox}
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
    messages: visibleMessages,
    allLoadedCount: allMessages.length,
    filter,
    activeMessageId,
    isLoadingMessages: messagesQuery.isPending,
    invalidateInbox,
  };

  return (
    <InboxProvider value={contextValue}>
      <main className="relative h-full w-full overflow-hidden">
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
                    filter={filter}
                    onSelectFilter={handleSelectFilter}
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
              <div className="h-full min-w-0 overflow-hidden bg-background">
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
              <div className="min-h-0 flex-1 overflow-hidden">
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
              <div className="h-full min-w-0 overflow-hidden bg-background">
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
                  filter={filter}
                  onSelectFilter={handleSelectFilter}
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
      </main>
    </InboxProvider>
  );
}
