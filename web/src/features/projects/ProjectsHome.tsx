import { useQuery } from "@tanstack/react-query";
import { Navigate } from "@tanstack/react-router";
import {
  AlertCircle,
  Copy,
  Inbox,
  LayoutGrid,
  List,
  Plus,
  Search,
  SearchX,
  Terminal,
} from "lucide-react";
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { useAppCommands } from "@/components/AppCommandContext";
import { EmptyState } from "@/components/EmptyState";
import { useRealtime } from "@/components/RealtimeProvider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { ProjectCard } from "@/features/projects/ProjectCard";
import { getApiErrorMessage } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { useClock24hPreference, useSingleKeyShortcutsPreference } from "@/lib/preferences";
import { authMeQueryOptions, metaQueryOptions, projectsQueryOptions } from "@/lib/queries";
import { type KeyShortcutBinding, useKeyShortcuts } from "@/lib/shortcuts";
import { usePersistentState } from "@/lib/usePersistentState";
import { cn } from "@/lib/utils";
import { canUserCreateProjects, getSingleProjectSlug } from "@/lib/viewerPreview";

const SKELETON_CARD_IDS = ["s1", "s2", "s3", "s4", "s5", "s6"] as const;

type ViewMode = "grid" | "list";

function isViewMode(value: unknown): value is ViewMode {
  return value === "grid" || value === "list";
}

export function ProjectsHome() {
  const filterId = useId();
  const filterRef = useRef<HTMLInputElement | null>(null);
  const cardRefs = useRef<Array<HTMLAnchorElement | null>>([]);
  const [filter, setFilter] = useState("");
  const [viewMode, setViewMode] = usePersistentState<ViewMode>("projects-view", "grid", isViewMode);
  const [now, setNow] = useState(() => new Date());

  const { openCreateProject } = useAppCommands();
  const { highlightedProjectSlugs } = useRealtime();
  const [singleKeyEnabled] = useSingleKeyShortcutsPreference();
  const [clock24h] = useClock24hPreference();

  const { data: meta } = useQuery(metaQueryOptions());
  const { data: me } = useQuery(authMeQueryOptions());
  const { data: projects, isPending, error, refetch } = useQuery(projectsQueryOptions());
  const canCreateProjects = canUserCreateProjects(meta?.mode, me?.user);
  // Audit V1 (SPEC FR-035): a non-admin with exactly one project opens that
  // inbox instead of a one-card list. Sign-in without a return URL, instance
  // invites, the logo and `g p` all land here, so this one rule covers them.
  const singleProjectSlug = getSingleProjectSlug(meta?.mode, me?.user, projects);

  useEffect(() => {
    const instanceName = meta?.instance_name || "Mailcubby";
    document.title = `Projects · ${instanceName}`;
  }, [meta?.instance_name]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  const filteredProjects = useMemo(() => {
    if (!projects) {
      return [];
    }
    const q = filter.trim().toLowerCase();
    if (!q) {
      return projects;
    }
    return projects.filter(
      (p) => p.name.toLowerCase().includes(q) || p.slug.toLowerCase().includes(q),
    );
  }, [projects, filter]);

  const moveCardFocus = useCallback(
    (delta: number) => {
      if (filteredProjects.length === 0) {
        return;
      }
      const activeEl = document.activeElement;
      const currentIdx =
        activeEl instanceof HTMLAnchorElement ? cardRefs.current.indexOf(activeEl) : -1;
      let nextIdx: number;
      if (currentIdx === -1) {
        nextIdx = delta > 0 ? 0 : filteredProjects.length - 1;
      } else {
        nextIdx = Math.max(0, Math.min(filteredProjects.length - 1, currentIdx + delta));
      }
      cardRefs.current[nextIdx]?.focus();
    },
    [filteredProjects.length],
  );

  const bindings = useMemo<KeyShortcutBinding[]>(
    () => [
      {
        keys: "/",
        handler: () => {
          filterRef.current?.focus();
          filterRef.current?.select();
        },
      },
      {
        keys: "n",
        enabled: canCreateProjects,
        handler: () => openCreateProject(),
      },
      {
        keys: "j",
        handler: () => moveCardFocus(1),
      },
      {
        keys: "k",
        handler: () => moveCardFocus(-1),
      },
      {
        keys: "ArrowDown",
        handler: () => moveCardFocus(1),
      },
      {
        keys: "ArrowUp",
        handler: () => moveCardFocus(-1),
      },
    ],
    [canCreateProjects, moveCardFocus, openCreateProject],
  );

  useKeyShortcuts(bindings, { singleKeyEnabled });

  const smtpHost = meta?.smtp.host ?? "localhost";
  const smtpPort = meta?.smtp.port ?? 1025;
  const smtpEndpoint = `${smtpHost}:${smtpPort}`;

  if (singleProjectSlug) {
    return <Navigate to="/p/$project" params={{ project: singleProjectSlug }} replace />;
  }

  // While the V1 redirect is still possible (a server-mode user without admin
  // or create rights whose project list is loading), render a neutral pane
  // instead of the page skeleton, so the projects page never flashes before
  // the inbox opens. Admins, creators and local mode skip this.
  const redirectPossible =
    meta?.mode === "server" &&
    me !== undefined &&
    !me.user.is_admin &&
    !me.user.can_create_projects;
  if (meta === undefined || (redirectPossible && projects === undefined && !error)) {
    return <main aria-busy="true" className="h-full" />;
  }

  return (
    <main className="h-full overflow-y-auto px-4 py-6 sm:px-6 lg:py-8">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-xl leading-7 font-semibold tracking-tight text-foreground">
              Projects
            </h1>
            <p className="mt-0.5 text-[13px] leading-5 text-muted-foreground">
              Select a project inbox or send mail over SMTP to capture messages.
            </p>
          </div>
          {canCreateProjects ? (
            <Button variant="primary" size="sm" onClick={openCreateProject}>
              <Plus className="size-3.5" aria-hidden="true" />
              <span>New project</span>
            </Button>
          ) : null}
        </div>

        {meta?.mode === "local" ? (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-surface px-4 py-3 text-[13px]">
            <div className="flex min-w-0 items-center gap-2.5">
              <Terminal className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <p className="text-muted-foreground">
                SMTP{" "}
                <code className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">
                  {smtpEndpoint}
                </code>
                , any username creates a project.
              </p>
            </div>
            <div className="flex items-center gap-1.5">
              <Button
                variant="outline"
                size="xs"
                onClick={() => void copyToClipboard(smtpEndpoint, "SMTP address")}
              >
                <Copy className="size-3" aria-hidden="true" />
                <span>Copy address</span>
              </Button>
              <Button
                variant="outline"
                size="xs"
                onClick={() =>
                  void copyToClipboard(
                    `swaks --to test@example.com --server ${smtpEndpoint} --auth PLAIN --auth-user default --auth-password dev`,
                    "swaks command",
                  )
                }
              >
                <Copy className="size-3" aria-hidden="true" />
                <span>Copy swaks</span>
              </Button>
            </div>
          </div>
        ) : null}

        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="relative w-full max-w-xs">
            <label htmlFor={filterId} className="sr-only">
              Filter projects
            </label>
            <Search
              className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              ref={filterRef}
              id={filterId}
              type="search"
              placeholder="Filter projects... (/)"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              className="pl-8"
            />
          </div>

          <fieldset
            aria-label="Project layout"
            className="inline-flex items-center gap-0.5 rounded-md border border-border bg-surface p-0.5"
          >
            <legend className="sr-only">Project layout</legend>
            <button
              type="button"
              aria-label="Grid view"
              aria-pressed={viewMode === "grid"}
              onClick={() => setViewMode("grid")}
              className={cn(
                "inline-flex size-7 cursor-pointer items-center justify-center rounded-sm transition-colors",
                viewMode === "grid"
                  ? "bg-muted text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <LayoutGrid className="size-3.5" aria-hidden="true" />
            </button>
            <button
              type="button"
              aria-label="List view"
              aria-pressed={viewMode === "list"}
              onClick={() => setViewMode("list")}
              className={cn(
                "inline-flex size-7 cursor-pointer items-center justify-center rounded-sm transition-colors",
                viewMode === "list"
                  ? "bg-muted text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <List className="size-3.5" aria-hidden="true" />
            </button>
          </fieldset>
        </div>

        {error ? (
          <div role="alert" className="rounded-lg border border-border bg-surface">
            <EmptyState
              icon={AlertCircle}
              title="Could not load projects"
              description={getApiErrorMessage(error)}
              action={
                <Button variant="outline" size="sm" onClick={() => void refetch()}>
                  Retry
                </Button>
              }
            />
          </div>
        ) : isPending ? (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {SKELETON_CARD_IDS.map((skeletonId) => (
              <div
                key={skeletonId}
                className="flex flex-col gap-3 rounded-lg border border-border bg-surface p-4"
              >
                <Skeleton className="h-4 w-32" />
                <Skeleton className="h-3 w-20" />
                <Skeleton className="mt-2 h-6 w-full" />
              </div>
            ))}
          </div>
        ) : filteredProjects.length === 0 ? (
          <div className="rounded-lg border border-dashed border-border bg-surface">
            {projects && projects.length > 0 ? (
              <EmptyState
                icon={SearchX}
                title="No matching projects"
                description={`No projects match "${filter}". Try a different filter.`}
                action={
                  <Button variant="outline" size="sm" onClick={() => setFilter("")}>
                    Clear filter
                  </Button>
                }
              />
            ) : (
              <EmptyState
                icon={Inbox}
                title="No projects yet"
                description={
                  canCreateProjects
                    ? "Projects isolate captured emails per app or environment. Create one or send mail via SMTP."
                    : "You do not belong to any projects yet. Ask an admin to add you to a project."
                }
                action={
                  canCreateProjects ? (
                    <Button variant="primary" size="sm" onClick={openCreateProject}>
                      <Plus className="size-3.5" aria-hidden="true" />
                      <span>New project</span>
                    </Button>
                  ) : undefined
                }
              />
            )}
          </div>
        ) : viewMode === "list" ? (
          <div className="flex flex-col gap-1.5">
            {filteredProjects.map((project, idx) => (
              <ProjectCard
                key={project.id}
                project={project}
                viewMode="list"
                now={now}
                hour24={clock24h}
                highlighted={highlightedProjectSlugs.has(project.slug)}
                linkRef={(el) => {
                  cardRefs.current[idx] = el;
                }}
              />
            ))}
          </div>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {filteredProjects.map((project, idx) => (
              <ProjectCard
                key={project.id}
                project={project}
                viewMode="grid"
                now={now}
                hour24={clock24h}
                highlighted={highlightedProjectSlugs.has(project.slug)}
                linkRef={(el) => {
                  cardRefs.current[idx] = el;
                }}
              />
            ))}
          </div>
        )}
      </div>
    </main>
  );
}
