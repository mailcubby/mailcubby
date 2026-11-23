import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  ArrowLeft,
  Check,
  ChevronDown,
  Copy,
  Inbox,
  MailOpen,
  PanelLeftClose,
  Search,
} from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useRealtime } from "@/components/RealtimeProvider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { ProjectDetail } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatCount, PROJECT_COLOR_DOT_CLASS } from "@/lib/format";
import { metaQueryOptions, projectsQueryOptions } from "@/lib/queries";
import { cn } from "@/lib/utils";
import type { InboxFilter } from "@/routes/p.$project";

export interface InboxSidebarProps {
  project: ProjectDetail;
  filter: InboxFilter;
  onSelectFilter: (next: InboxFilter) => void;
  onCollapse?: () => void;
  onCloseDrawer?: () => void;
}

export function InboxSidebar({
  project,
  filter,
  onSelectFilter,
  onCollapse,
  onCloseDrawer,
}: InboxSidebarProps) {
  const navigate = useNavigate();
  const { status: realtimeStatus } = useRealtime();
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: projects } = useQuery(projectsQueryOptions());

  const [switcherOpen, setSwitcherOpen] = useState(false);
  const [switcherFilter, setSwitcherFilter] = useState("");
  const switcherRef = useRef<HTMLDivElement | null>(null);
  const switcherSearchId = useId();

  useEffect(() => {
    if (!switcherOpen) {
      return;
    }
    const handlePointerDown = (e: PointerEvent) => {
      if (switcherRef.current && !switcherRef.current.contains(e.target as Node)) {
        setSwitcherOpen(false);
      }
    };
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setSwitcherOpen(false);
      }
    };
    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [switcherOpen]);

  const filteredProjects = useMemo(() => {
    if (!projects) {
      return [];
    }
    const q = switcherFilter.trim().toLowerCase();
    if (!q) {
      return projects;
    }
    return projects.filter(
      (p) => p.name.toLowerCase().includes(q) || p.slug.toLowerCase().includes(q),
    );
  }, [projects, switcherFilter]);

  const dotClass = PROJECT_COLOR_DOT_CLASS[project.color] ?? PROJECT_COLOR_DOT_CLASS.slate;
  const smtpHost = meta?.smtp.host ?? "localhost";
  const smtpPort = meta?.smtp.port ?? 1025;

  return (
    <aside
      aria-label="Project sidebar"
      className="flex h-full flex-col justify-between bg-surface text-[13px]"
    >
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-2.5">
        <div className="relative" ref={switcherRef}>
          <div className="flex items-center gap-1">
            <button
              type="button"
              aria-expanded={switcherOpen}
              aria-haspopup="listbox"
              onClick={() => setSwitcherOpen((prev) => !prev)}
              className="flex h-8 min-w-0 flex-1 cursor-pointer items-center justify-between gap-2 rounded-md border border-border bg-background px-2.5 text-left transition-colors hover:bg-muted"
            >
              <span className="flex min-w-0 items-center gap-2">
                <span
                  className={cn("size-2.5 shrink-0 rounded-full", dotClass)}
                  aria-hidden="true"
                />
                <span className="truncate font-medium text-foreground">{project.name}</span>
              </span>
              <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
            </button>
            {onCollapse ? (
              <Button
                variant="ghost"
                size="icon"
                aria-label="Collapse sidebar"
                title="Collapse sidebar"
                onClick={onCollapse}
                className="size-8"
              >
                <PanelLeftClose className="size-4" aria-hidden="true" />
              </Button>
            ) : null}
          </div>

          {switcherOpen ? (
            <div className="absolute top-full left-0 z-30 mt-1 w-full min-w-52 rounded-md border border-border bg-surface p-1.5 shadow-lg">
              <div className="relative mb-1.5">
                <label htmlFor={switcherSearchId} className="sr-only">
                  Switch project
                </label>
                <Search
                  className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground"
                  aria-hidden="true"
                />
                <Input
                  id={switcherSearchId}
                  autoFocus
                  placeholder="Switch project..."
                  value={switcherFilter}
                  onChange={(e) => setSwitcherFilter(e.target.value)}
                  className="h-7 pl-7 text-xs"
                />
              </div>

              <div role="listbox" aria-label="Projects" className="max-h-56 overflow-y-auto">
                {filteredProjects.map((item) => {
                  const active = item.slug === project.slug;
                  const itemDot =
                    PROJECT_COLOR_DOT_CLASS[item.color] ?? PROJECT_COLOR_DOT_CLASS.slate;
                  return (
                    <button
                      key={item.id}
                      type="button"
                      role="option"
                      aria-selected={active}
                      onClick={() => {
                        setSwitcherOpen(false);
                        setSwitcherFilter("");
                        onCloseDrawer?.();
                        void navigate({
                          to: "/p/$project",
                          params: { project: item.slug },
                        });
                      }}
                      className={cn(
                        "flex w-full cursor-pointer items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-xs transition-colors",
                        active
                          ? "bg-muted font-medium text-foreground"
                          : "text-foreground hover:bg-muted/60",
                      )}
                    >
                      <span className="flex min-w-0 items-center gap-2">
                        <span
                          className={cn("size-2 shrink-0 rounded-full", itemDot)}
                          aria-hidden="true"
                        />
                        <span className="truncate">{item.name}</span>
                      </span>
                      <span className="flex shrink-0 items-center gap-1.5 tabular-nums text-muted-foreground">
                        {item.unread_count > 0 ? (
                          <span className="text-[11px] font-medium text-accent">
                            {formatCount(item.unread_count)}
                          </span>
                        ) : null}
                        {active ? (
                          <Check className="size-3.5 text-accent" aria-hidden="true" />
                        ) : null}
                      </span>
                    </button>
                  );
                })}
                {filteredProjects.length === 0 ? (
                  <p className="px-2 py-2 text-xs text-muted-foreground">No matching projects</p>
                ) : null}
              </div>

              <div className="mt-1.5 border-t border-border pt-1.5">
                <Link
                  to="/"
                  onClick={() => {
                    setSwitcherOpen(false);
                    onCloseDrawer?.();
                  }}
                  className="flex items-center gap-1.5 rounded-sm px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                >
                  <ArrowLeft className="size-3" aria-hidden="true" />
                  <span>All projects</span>
                </Link>
              </div>
            </div>
          ) : null}
        </div>

        <nav aria-label="Mailbox filters" className="mt-4 flex flex-col gap-0.5">
          <button
            type="button"
            aria-current={filter === "all" ? "page" : undefined}
            onClick={() => {
              onSelectFilter("all");
              onCloseDrawer?.();
            }}
            className={cn(
              "flex h-8 w-full cursor-pointer items-center justify-between rounded-md px-2.5 text-[13px] transition-colors",
              filter === "all"
                ? "bg-muted font-medium text-foreground"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
            )}
          >
            <span className="flex items-center gap-2">
              <Inbox className="size-3.5" aria-hidden="true" />
              <span>Inbox</span>
            </span>
            <span className="text-xs tabular-nums text-muted-foreground">
              {formatCount(project.message_count)}
            </span>
          </button>

          <button
            type="button"
            aria-current={filter === "unread" ? "page" : undefined}
            onClick={() => {
              onSelectFilter("unread");
              onCloseDrawer?.();
            }}
            className={cn(
              "flex h-8 w-full cursor-pointer items-center justify-between rounded-md px-2.5 text-[13px] transition-colors",
              filter === "unread"
                ? "bg-muted font-medium text-foreground"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
            )}
          >
            <span className="flex items-center gap-2">
              <MailOpen className="size-3.5" aria-hidden="true" />
              <span>Unread</span>
            </span>
            {project.unread_count > 0 ? (
              <Badge variant="accent">{formatCount(project.unread_count)}</Badge>
            ) : (
              <span className="text-xs tabular-nums text-muted-foreground">0</span>
            )}
          </button>
        </nav>

        <div className="mt-6 border-t border-border pt-3">
          <Link
            to="/"
            onClick={onCloseDrawer}
            className="flex h-7 items-center gap-2 rounded-md px-2.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            <span>Back to all projects</span>
          </Link>
        </div>
      </div>

      <div className="border-t border-border">
        {meta?.mode === "local" ? (
          <div className="p-3">
            <p className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
              SMTP Quick Copy
            </p>
            <dl className="mt-2 flex flex-col gap-1.5 text-xs">
              <div className="flex items-center justify-between gap-2">
                <dt className="text-muted-foreground">Server</dt>
                <dd className="min-w-0">
                  <button
                    type="button"
                    onClick={() => void copyToClipboard(`${smtpHost}:${smtpPort}`, "SMTP server")}
                    className="inline-flex max-w-full cursor-pointer items-center gap-1 rounded-sm px-1 py-0.5 font-mono text-[11px] text-foreground hover:bg-muted"
                    title="Copy host and port"
                  >
                    <span className="truncate">
                      {smtpHost}:{smtpPort}
                    </span>
                    <Copy className="size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
                  </button>
                </dd>
              </div>
              <div className="flex items-center justify-between gap-2">
                <dt className="text-muted-foreground">User</dt>
                <dd className="min-w-0">
                  <button
                    type="button"
                    onClick={() => void copyToClipboard(project.slug, "SMTP username")}
                    className="inline-flex max-w-full cursor-pointer items-center gap-1 rounded-sm px-1 py-0.5 font-mono text-[11px] text-foreground hover:bg-muted"
                    title="Copy SMTP username"
                  >
                    <span className="truncate">{project.slug}</span>
                    <Copy className="size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
                  </button>
                </dd>
              </div>
            </dl>
          </div>
        ) : null}

        <div
          className="flex items-center gap-2 border-t border-border px-3 py-2 text-[11px] text-muted-foreground"
          title={
            realtimeStatus === "connected"
              ? "Live updates connected"
              : realtimeStatus === "reconnecting"
                ? "Reconnecting to live updates"
                : "Connecting to live updates"
          }
        >
          <span
            className={cn(
              "size-2 shrink-0 rounded-full",
              realtimeStatus === "connected"
                ? "bg-emerald-500"
                : realtimeStatus === "reconnecting"
                  ? "animate-pulse bg-amber-500"
                  : "bg-muted-foreground/50",
            )}
            aria-hidden="true"
          />
          <span className="truncate">
            {realtimeStatus === "connected"
              ? "Live connected"
              : realtimeStatus === "reconnecting"
                ? "Reconnecting..."
                : "Connecting..."}
          </span>
        </div>
      </div>
    </aside>
  );
}
