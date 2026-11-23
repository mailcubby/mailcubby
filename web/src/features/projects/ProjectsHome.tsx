import { useQuery } from "@tanstack/react-query";
import { Copy, Inbox, LayoutGrid, List, Plus, Search, Terminal } from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { CreateProjectDialog } from "@/features/projects/CreateProjectDialog";
import { ProjectCard } from "@/features/projects/ProjectCard";
import { getApiErrorMessage } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { metaQueryOptions, projectsQueryOptions } from "@/lib/queries";
import { usePersistentState } from "@/lib/usePersistentState";
import { cn } from "@/lib/utils";

const SKELETON_CARD_IDS = ["s1", "s2", "s3", "s4", "s5", "s6"] as const;

type ViewMode = "grid" | "list";

function isViewMode(value: unknown): value is ViewMode {
  return value === "grid" || value === "list";
}

export function ProjectsHome() {
  const filterId = useId();
  const [filter, setFilter] = useState("");
  const [viewMode, setViewMode] = usePersistentState<ViewMode>("projects-view", "grid", isViewMode);
  const [createOpen, setCreateOpen] = useState(false);
  const [now, setNow] = useState(() => new Date());

  const { data: meta } = useQuery(metaQueryOptions());
  const { data: projects, isPending, error, refetch } = useQuery(projectsQueryOptions());

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

  const smtpHost = meta?.smtp.host ?? "localhost";
  const smtpPort = meta?.smtp.port ?? 1025;
  const smtpEndpoint = `${smtpHost}:${smtpPort}`;

  return (
    <main className="h-full overflow-y-auto px-4 py-6 sm:px-6">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-lg font-semibold tracking-tight text-foreground">Projects</h1>
            <p className="text-[13px] text-muted-foreground">
              Select a project inbox or send mail over SMTP to capture messages.
            </p>
          </div>
          <Button variant="default" size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="size-3.5" aria-hidden="true" />
            <span>New project</span>
          </Button>
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
              id={filterId}
              type="search"
              placeholder="Filter projects..."
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
          <div
            role="alert"
            className="flex flex-col items-start gap-3 rounded-lg border border-destructive/30 bg-surface p-5 text-[13px]"
          >
            <p className="font-medium text-destructive">
              Failed to load projects: {getApiErrorMessage(error)}
            </p>
            <Button variant="outline" size="xs" onClick={() => void refetch()}>
              Retry
            </Button>
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
          <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-surface px-6 py-12 text-center">
            <Inbox className="size-8 text-muted-foreground" aria-hidden="true" />
            <div className="max-w-md space-y-1">
              <h2 className="text-sm font-medium text-foreground">
                {projects && projects.length > 0 ? "No matching projects" : "No projects yet"}
              </h2>
              <p className="text-[13px] text-muted-foreground">
                {projects && projects.length > 0
                  ? `No projects match "${filter}". Try a different filter.`
                  : "Projects isolate captured emails per app or environment. Create one below or send mail via SMTP."}
              </p>
            </div>
            {projects && projects.length > 0 ? (
              <Button variant="outline" size="sm" onClick={() => setFilter("")}>
                Clear filter
              </Button>
            ) : (
              <Button variant="default" size="sm" onClick={() => setCreateOpen(true)}>
                <Plus className="size-3.5" aria-hidden="true" />
                <span>New project</span>
              </Button>
            )}
          </div>
        ) : viewMode === "list" ? (
          <div className="flex flex-col gap-1.5">
            {filteredProjects.map((project) => (
              <ProjectCard key={project.id} project={project} viewMode="list" now={now} />
            ))}
          </div>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {filteredProjects.map((project) => (
              <ProjectCard key={project.id} project={project} viewMode="grid" now={now} />
            ))}
          </div>
        )}
      </div>

      <CreateProjectDialog open={createOpen} onOpenChange={setCreateOpen} />
    </main>
  );
}
