import { Link } from "@tanstack/react-router";
import { Badge } from "@/components/ui/badge";
import { Sparkline } from "@/features/projects/Sparkline";
import type { ProjectSummary } from "@/lib/api";
import {
  formatBytes,
  formatCount,
  formatExactTimestamp,
  formatRelativeTime,
  PROJECT_COLOR_DOT_CLASS,
} from "@/lib/format";
import { cn } from "@/lib/utils";

export interface ProjectCardProps {
  project: ProjectSummary;
  viewMode: "grid" | "list";
  now: Date;
}

export function ProjectCard({ project, viewMode, now }: ProjectCardProps) {
  const dotClass = PROJECT_COLOR_DOT_CLASS[project.color] ?? PROJECT_COLOR_DOT_CLASS.slate;
  const lastRelative = project.last_message_at
    ? formatRelativeTime(project.last_message_at, now)
    : null;
  const lastExact = project.last_message_at
    ? formatExactTimestamp(project.last_message_at).combined
    : undefined;

  if (viewMode === "list") {
    return (
      <Link
        to="/p/$project"
        params={{ project: project.slug }}
        className="group flex items-center justify-between gap-4 rounded-md border border-border bg-surface px-3.5 py-2.5 transition-colors duration-150 hover:border-foreground/25 hover:bg-muted/40"
      >
        <div className="flex min-w-0 items-center gap-2.5">
          <span className={cn("size-2.5 shrink-0 rounded-full", dotClass)} aria-hidden="true" />
          <span className="truncate text-[13px] font-medium text-foreground">{project.name}</span>
          <span className="hidden truncate font-mono text-xs text-muted-foreground sm:inline">
            {project.slug}
          </span>
          <Badge variant="outline" className="hidden md:inline-flex">
            {project.role}
          </Badge>
        </div>

        <div className="flex shrink-0 items-center gap-4 text-xs text-muted-foreground tabular-nums">
          {project.unread_count > 0 ? (
            <Badge variant="accent" aria-label={`${project.unread_count} unread`}>
              {formatCount(project.unread_count)} unread
            </Badge>
          ) : null}
          <span className="w-16 text-right">{formatCount(project.message_count)} msg</span>
          <span className="hidden w-16 text-right sm:inline">
            {formatBytes(project.size_bytes)}
          </span>
          <span className="hidden w-24 text-right md:inline" title={lastExact}>
            {lastRelative ?? "No mail"}
          </span>
          <Sparkline values={project.sparkline} className="hidden lg:block" />
        </div>
      </Link>
    );
  }

  return (
    <Link
      to="/p/$project"
      params={{ project: project.slug }}
      className="group flex flex-col justify-between gap-3 rounded-lg border border-border bg-surface p-4 transition-colors duration-150 hover:border-foreground/25 hover:bg-muted/30"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className={cn("size-2.5 shrink-0 rounded-full", dotClass)} aria-hidden="true" />
            <h2 className="truncate text-sm font-semibold text-foreground">{project.name}</h2>
          </div>
          <div className="mt-1 flex items-center gap-2">
            <span className="truncate font-mono text-xs text-muted-foreground">{project.slug}</span>
            <Badge variant="outline">{project.role}</Badge>
          </div>
        </div>
        {project.unread_count > 0 ? (
          <Badge variant="accent" aria-label={`${project.unread_count} unread`}>
            {formatCount(project.unread_count)}
          </Badge>
        ) : null}
      </div>

      <div className="flex items-end justify-between gap-3 border-t border-border/70 pt-3">
        <div className="flex flex-col gap-0.5 text-xs text-muted-foreground tabular-nums">
          <div className="flex items-center gap-2">
            <span className="font-medium text-foreground">
              {formatCount(project.message_count)}{" "}
              {project.message_count === 1 ? "message" : "messages"}
            </span>
            <span aria-hidden="true">·</span>
            <span>{formatBytes(project.size_bytes)}</span>
          </div>
          <span title={lastExact}>
            {lastRelative
              ? `Last message ${lastRelative === "now" ? "just now" : lastRelative}`
              : "No messages yet"}
          </span>
        </div>
        <Sparkline values={project.sparkline} />
      </div>
    </Link>
  );
}
