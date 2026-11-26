import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ExternalLink, Plus, Search, Settings } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { useAppCommands } from "@/components/AppCommandContext";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { AdminLayout } from "@/features/admin/AdminLayout";
import { getApiErrorMessage } from "@/lib/api";
import {
  formatBytes,
  formatCount,
  formatExactTimestamp,
  formatRelativeTime,
  PROJECT_COLOR_DOT_CLASS,
} from "@/lib/format";
import { useClock24hPreference } from "@/lib/preferences";
import { adminProjectsQueryOptions, authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
import { cn } from "@/lib/utils";

export function AdminProjectsPage() {
  const { openCreateProject } = useAppCommands();
  const [clock24h] = useClock24hPreference();
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: me } = useQuery(authMeQueryOptions());

  const isAdmin = Boolean(meta?.mode === "server" && me?.user.is_admin);
  const projectsQuery = useQuery({
    ...adminProjectsQueryOptions(),
    enabled: isAdmin,
  });

  const [filter, setFilter] = useState("");
  const filterId = useId();

  const filteredProjects = useMemo(() => {
    const items = projectsQuery.data ?? [];
    const q = filter.trim().toLowerCase();
    if (!q) {
      return items;
    }
    return items.filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.slug.toLowerCase().includes(q) ||
        p.owners.some((o) => o.name.toLowerCase().includes(q) || o.email.toLowerCase().includes(q)),
    );
  }, [projectsQuery.data, filter]);

  const now = new Date();

  return (
    <AdminLayout
      section="projects"
      title="Projects"
      description="All projects across the instance with owners, message counts, and storage usage."
      actions={
        <Button variant="primary" size="sm" onClick={openCreateProject}>
          <Plus className="size-3.5" aria-hidden="true" />
          <span>New project</span>
        </Button>
      }
    >
      <div className="flex flex-col gap-4">
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
              placeholder="Filter by name, slug, or owner..."
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              className="pl-8"
            />
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">
            {formatCount(filteredProjects.length)}{" "}
            {filteredProjects.length === 1 ? "project" : "projects"}
          </span>
        </div>

        <Card>
          {projectsQuery.isPending ? (
            <div className="flex flex-col gap-2 p-5">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : projectsQuery.error ? (
            <div role="alert" className="p-5 text-xs text-destructive">
              Failed to load projects: {getApiErrorMessage(projectsQuery.error)}
            </div>
          ) : filteredProjects.length === 0 ? (
            <div className="p-6 text-center text-xs text-muted-foreground">
              {filter.trim() ? `No projects match "${filter}".` : "No projects exist yet."}
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Project</TableHead>
                    <TableHead>Owners</TableHead>
                    <TableHead className="text-right">Messages</TableHead>
                    <TableHead className="text-right">Storage</TableHead>
                    <TableHead>Last message</TableHead>
                    <TableHead className="text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filteredProjects.map((project) => {
                    const dotClass =
                      PROJECT_COLOR_DOT_CLASS[project.color] ?? PROJECT_COLOR_DOT_CLASS.slate;
                    const lastRel = project.last_message_at
                      ? formatRelativeTime(project.last_message_at, now, clock24h)
                      : null;
                    const lastExact = project.last_message_at
                      ? formatExactTimestamp(project.last_message_at, clock24h).combined
                      : undefined;

                    return (
                      <TableRow key={project.id}>
                        <TableCell>
                          <Link
                            to="/p/$project"
                            params={{ project: project.slug }}
                            className="inline-flex items-center gap-2 font-medium text-foreground hover:text-accent"
                          >
                            <span
                              className={cn("size-2.5 shrink-0 rounded-full", dotClass)}
                              aria-hidden="true"
                            />
                            <span>{project.name}</span>
                          </Link>
                          <div className="mt-0.5 pl-4.5 font-mono text-xs text-muted-foreground">
                            {project.slug}
                          </div>
                        </TableCell>

                        <TableCell className="text-xs">
                          {project.owners.length === 0 ? (
                            <span className="text-muted-foreground">No explicit owners</span>
                          ) : (
                            <div className="flex flex-wrap gap-1">
                              {project.owners.map((owner) => (
                                <Badge key={owner.id} variant="outline" title={owner.email}>
                                  {owner.name}
                                </Badge>
                              ))}
                            </div>
                          )}
                        </TableCell>

                        <TableCell className="text-right text-xs tabular-nums">
                          <span className="font-medium text-foreground">
                            {formatCount(project.message_count)}
                          </span>
                          {project.unread_count > 0 ? (
                            <Badge variant="primary" className="ml-1.5">
                              {formatCount(project.unread_count)} unread
                            </Badge>
                          ) : null}
                        </TableCell>

                        <TableCell className="text-right text-xs text-muted-foreground tabular-nums">
                          {formatBytes(project.size_bytes)}
                        </TableCell>

                        <TableCell
                          className="text-xs text-muted-foreground tabular-nums"
                          title={lastExact}
                        >
                          {lastRel ?? "No mail"}
                        </TableCell>

                        <TableCell className="text-right">
                          <div className="inline-flex items-center gap-1">
                            <Link
                              to="/p/$project"
                              params={{ project: project.slug }}
                              className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs font-medium text-foreground hover:bg-muted"
                            >
                              <ExternalLink className="size-3" aria-hidden="true" />
                              <span>Open</span>
                            </Link>
                            <Link
                              to="/p/$project/settings/general"
                              params={{ project: project.slug }}
                              aria-label={`Settings for ${project.name}`}
                              className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                            >
                              <Settings className="size-3" aria-hidden="true" />
                              <span className="hidden lg:inline">Settings</span>
                            </Link>
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </Card>
      </div>
    </AdminLayout>
  );
}
