import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  ArrowLeft,
  Check,
  ChevronsUpDown,
  Copy,
  Inbox,
  KeyRound,
  MailOpen,
  PanelLeftClose,
  Plus,
  Search,
  Settings,
  Trash2,
} from "lucide-react";
import { type FormEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { useRealtime } from "@/components/RealtimeProvider";
import { Button } from "@/components/ui/button";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { isValidTagName } from "@/features/inbox/searchQuery";
import {
  api,
  handleApiError,
  type ProjectColor,
  type ProjectDetail,
  type ProjectRole,
  type SMTPConnectionInfo,
  type SMTPCredential,
  type TagSummary,
  unwrap,
  unwrapVoid,
} from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatCount, PROJECT_COLOR_DOT_CLASS, PROJECT_COLORS } from "@/lib/format";
import { authMeQueryOptions, metaQueryOptions, projectsQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";
import { getSingleProjectSlug } from "@/lib/viewerPreview";
import type { InboxFilter } from "@/routes/p.$project";

export interface InboxSidebarProps {
  project: ProjectDetail;
  effectiveRole: ProjectRole;
  tags: ReadonlyArray<TagSummary>;
  credentials: ReadonlyArray<SMTPCredential>;
  smtpConnection?: SMTPConnectionInfo;
  filter: InboxFilter;
  activeTag?: string;
  activeCredentialId?: number;
  onSelectFilter: (next: InboxFilter) => void;
  onSelectTag: (tagName: string | undefined) => void;
  onSelectCredential: (credentialId: number | undefined) => void;
  onCollapse?: () => void;
  onCloseDrawer?: () => void;
}

export function InboxSidebar({
  project,
  effectiveRole,
  tags,
  credentials,
  smtpConnection,
  filter,
  activeTag,
  activeCredentialId,
  onSelectFilter,
  onSelectTag,
  onSelectCredential,
  onCollapse,
  onCloseDrawer,
}: InboxSidebarProps) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { status: realtimeStatus } = useRealtime();
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: projects } = useQuery(projectsQueryOptions());
  const isServer = meta?.mode === "server";
  const { data: me } = useQuery({ ...authMeQueryOptions(), enabled: isServer });

  const [switcherOpen, setSwitcherOpen] = useState(false);
  const [switcherFilter, setSwitcherFilter] = useState("");
  const [createTagOpen, setCreateTagOpen] = useState(false);
  const [newTagName, setNewTagName] = useState("");
  const [newTagColor, setNewTagColor] = useState<ProjectColor>("blue");
  const [newTagError, setNewTagError] = useState<string | null>(null);
  const [deletingTag, setDeletingTag] = useState<TagSummary | null>(null);
  const switcherRef = useRef<HTMLDivElement | null>(null);
  const switcherSearchId = useId();
  const newTagInputId = useId();

  const canManageTags = effectiveRole !== "viewer";

  const createTagMutation = useMutation({
    mutationFn: async ({ name, color }: { name: string; color: ProjectColor }) =>
      unwrap(
        await api.POST("/api/v1/projects/{project}/tags", {
          params: { path: { project: project.slug } },
          body: { name, color },
        }),
      ),
    onSuccess: async (created) => {
      setCreateTagOpen(false);
      setNewTagName("");
      setNewTagError(null);
      toast.success(`Tag "${created.name}" created`);
      await queryClient.invalidateQueries({ queryKey: queryKeys.tags(project.slug) });
    },
    onError: (err) => {
      handleApiError(err, "Failed to create tag");
    },
  });

  const deleteTagMutation = useMutation({
    mutationFn: async (tag: TagSummary) =>
      unwrapVoid(
        await api.DELETE("/api/v1/projects/{project}/tags/{id}", {
          params: { path: { project: project.slug, id: tag.id } },
        }),
      ),
    onSuccess: async (_data, tag) => {
      setDeletingTag(null);
      if (activeTag && activeTag.toLowerCase() === tag.name.toLowerCase()) {
        onSelectTag(undefined);
      }
      toast.success(`Deleted tag "${tag.name}"`);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.tags(project.slug) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.messages(project.slug) }),
      ]);
    },
    onError: (err) => {
      handleApiError(err, "Failed to delete tag");
    },
  });

  const handleCreateTagSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = newTagName.trim();
    if (!isValidTagName(trimmed)) {
      setNewTagError("Tag name must be 1–40 characters (letters, numbers, _, ., -).");
      return;
    }
    setNewTagError(null);
    createTagMutation.mutate({ name: trimmed, color: newTagColor });
  };

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
  const smtpHost = smtpConnection?.host ?? meta?.smtp.host ?? "localhost";
  const smtpPort = smtpConnection?.port ?? meta?.smtp.port ?? 1025;
  const showServerCredentials = meta?.mode === "server" && effectiveRole !== "viewer";
  const quickCopyCredential = credentials.find((c) => !c.disabled) ?? credentials[0];
  const quickCopyUser = meta?.mode === "local" ? project.slug : quickCopyCredential?.username;
  // Audit V4: with exactly one project there is nowhere to go back to (server
  // mode, non-admins only). The link waits for the user and the project list,
  // so it never flashes before the rule can hide it.
  const showBackToProjects =
    meta?.mode === "local" ||
    (isServer &&
      me !== undefined &&
      projects !== undefined &&
      getSingleProjectSlug("server", me.user, projects) === null);

  return (
    <aside
      aria-label="Project sidebar"
      data-chrome
      className="flex h-full flex-col justify-between bg-sidebar text-[13px] text-sidebar-foreground"
    >
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-2.5">
        <div className="relative" ref={switcherRef}>
          <div className="flex items-center gap-1">
            <button
              type="button"
              aria-expanded={switcherOpen}
              aria-haspopup="listbox"
              onClick={() => setSwitcherOpen((prev) => !prev)}
              className="flex h-9 min-w-0 flex-1 cursor-pointer items-center justify-between gap-2 rounded-md border border-sidebar-border bg-sidebar-input px-2.5 text-left transition-colors duration-150 hover:bg-sidebar-hover"
            >
              <span className="flex min-w-0 items-center gap-2">
                <span
                  className={cn("size-2.5 shrink-0 rounded-full", dotClass)}
                  aria-hidden="true"
                />
                <span className="truncate font-medium text-sidebar-foreground">{project.name}</span>
              </span>
              <ChevronsUpDown className="size-3.5 shrink-0 text-sidebar-muted" aria-hidden="true" />
            </button>
            {onCollapse ? (
              <Button
                variant="chrome"
                size="icon"
                aria-label="Collapse sidebar"
                title="Collapse sidebar"
                onClick={onCollapse}
                className="size-9"
              >
                <PanelLeftClose className="size-4" aria-hidden="true" />
              </Button>
            ) : null}
          </div>

          {switcherOpen ? (
            <div
              data-surface
              className="absolute top-full left-0 z-30 mt-1 w-full min-w-52 rounded-md border border-border bg-surface p-1.5 text-foreground shadow-lg"
            >
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
            aria-current={
              filter === "all" && !activeTag && activeCredentialId === undefined
                ? "page"
                : undefined
            }
            onClick={() => {
              onSelectFilter("all");
              onCloseDrawer?.();
            }}
            className={cn(
              "flex h-8 w-full cursor-pointer items-center justify-between rounded-md px-2.5 text-[13px] transition-colors duration-150",
              filter === "all" && !activeTag && activeCredentialId === undefined
                ? "bg-sidebar-active font-medium text-sidebar-active-foreground"
                : "text-sidebar-foreground hover:bg-sidebar-hover",
            )}
          >
            <span className="flex items-center gap-2.5">
              <Inbox className="size-4" aria-hidden="true" />
              <span>Inbox</span>
            </span>
            <span className="text-xs tabular-nums">{formatCount(project.message_count)}</span>
          </button>

          <button
            type="button"
            aria-current={
              filter === "unread" && !activeTag && activeCredentialId === undefined
                ? "page"
                : undefined
            }
            onClick={() => {
              onSelectFilter("unread");
              onCloseDrawer?.();
            }}
            className={cn(
              "flex h-8 w-full cursor-pointer items-center justify-between rounded-md px-2.5 text-[13px] transition-colors duration-150",
              filter === "unread" && !activeTag && activeCredentialId === undefined
                ? "bg-sidebar-active font-medium text-sidebar-active-foreground"
                : "text-sidebar-foreground hover:bg-sidebar-hover",
            )}
          >
            <span className="flex items-center gap-2.5">
              <MailOpen className="size-4" aria-hidden="true" />
              <span>Unread</span>
            </span>
            {project.unread_count > 0 ? (
              <span className="inline-flex min-h-5 min-w-5 items-center justify-center rounded-full bg-sidebar-accent px-1.5 text-[11px] font-semibold tabular-nums text-sidebar-accent-foreground">
                {formatCount(project.unread_count)}
              </span>
            ) : (
              <span className="text-xs tabular-nums">0</span>
            )}
          </button>
        </nav>

        <section aria-label="Tags" className="mt-5">
          <div className="mb-1 flex items-center justify-between px-2.5">
            <h2 className="text-xs font-medium text-sidebar-muted">Tags</h2>
            {canManageTags ? (
              <Button
                variant="chrome"
                size="icon"
                aria-label="New tag"
                title="Create tag"
                onClick={() => {
                  setNewTagName("");
                  setNewTagError(null);
                  setCreateTagOpen(true);
                }}
                className="size-6"
              >
                <Plus className="size-3.5" aria-hidden="true" />
              </Button>
            ) : null}
          </div>

          {tags.length === 0 ? (
            <p className="px-2.5 py-1 text-xs text-sidebar-muted">No tags yet</p>
          ) : (
            <ul className="flex flex-col gap-0.5">
              {tags.map((tag) => {
                const isSelected = Boolean(
                  activeTag && activeTag.toLowerCase() === tag.name.toLowerCase(),
                );
                const tagDot = PROJECT_COLOR_DOT_CLASS[tag.color] ?? PROJECT_COLOR_DOT_CLASS.slate;
                return (
                  <li key={tag.id} className="group flex items-center gap-0.5">
                    <button
                      type="button"
                      data-testid={`sidebar-tag-${tag.name}`}
                      aria-current={isSelected ? "page" : undefined}
                      onClick={() => {
                        onSelectTag(isSelected ? undefined : tag.name);
                        onCloseDrawer?.();
                      }}
                      className={cn(
                        "flex h-8 min-w-0 flex-1 cursor-pointer items-center justify-between gap-2 rounded-md px-2.5 text-[13px] transition-colors duration-150",
                        isSelected
                          ? "bg-sidebar-active font-medium text-sidebar-active-foreground"
                          : "text-sidebar-foreground hover:bg-sidebar-hover",
                      )}
                    >
                      <span className="flex min-w-0 items-center gap-2">
                        <span
                          className={cn("size-2 shrink-0 rounded-full", tagDot)}
                          aria-hidden="true"
                        />
                        <span className="truncate">{tag.name}</span>
                      </span>
                      <span
                        data-testid={`sidebar-tag-count-${tag.name}`}
                        className={cn(
                          "shrink-0 text-xs tabular-nums",
                          !isSelected && "text-sidebar-muted",
                        )}
                      >
                        {formatCount(tag.message_count)}
                      </span>
                    </button>
                    {canManageTags ? (
                      <button
                        type="button"
                        aria-label={`Delete tag ${tag.name}`}
                        title={`Delete tag ${tag.name}`}
                        onClick={() => setDeletingTag(tag)}
                        className="inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-sm text-sidebar-muted opacity-0 transition-opacity hover:bg-sidebar-hover hover:text-sidebar-foreground focus-visible:opacity-100 group-hover:opacity-100"
                      >
                        <Trash2 className="size-3" aria-hidden="true" />
                      </button>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          )}
        </section>

        {showServerCredentials ? (
          <section aria-label="Credentials" className="mt-5">
            <div className="mb-1 flex items-center justify-between px-2.5">
              <h2 className="text-xs font-medium text-sidebar-muted">Credentials</h2>
            </div>

            {credentials.length === 0 ? (
              <p className="px-2.5 py-1 text-xs text-sidebar-muted">No credentials yet</p>
            ) : (
              <ul className="flex flex-col gap-0.5">
                {credentials.map((cred) => {
                  const isSelected = activeCredentialId === cred.id;
                  return (
                    <li key={cred.id}>
                      <button
                        type="button"
                        data-testid={`sidebar-credential-${cred.id}`}
                        aria-current={isSelected ? "page" : undefined}
                        onClick={() => {
                          onSelectCredential(isSelected ? undefined : cred.id);
                          onCloseDrawer?.();
                        }}
                        className={cn(
                          "flex h-8 w-full cursor-pointer items-center justify-between gap-2 rounded-md px-2.5 text-[13px] transition-colors duration-150",
                          isSelected
                            ? "bg-sidebar-active font-medium text-sidebar-active-foreground"
                            : "text-sidebar-foreground hover:bg-sidebar-hover",
                        )}
                      >
                        <span className="flex min-w-0 items-center gap-2">
                          <KeyRound
                            className="size-3.5 shrink-0 text-sidebar-muted"
                            aria-hidden="true"
                          />
                          <span className="truncate">{cred.label}</span>
                        </span>
                        {cred.disabled ? (
                          <span className="shrink-0 text-[11px] text-sidebar-muted">Disabled</span>
                        ) : null}
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
        ) : null}

        <div
          className={cn(
            "mt-5 flex flex-col gap-0.5 border-t border-sidebar-border pt-3",
            effectiveRole === "viewer" && !showBackToProjects && "hidden",
          )}
        >
          {effectiveRole !== "viewer" ? (
            <Link
              to={
                effectiveRole === "owner"
                  ? "/p/$project/settings/general"
                  : "/p/$project/settings/members"
              }
              params={{ project: project.slug }}
              onClick={onCloseDrawer}
              className="flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] text-sidebar-foreground transition-colors duration-150 hover:bg-sidebar-hover"
            >
              <Settings className="size-4 text-sidebar-muted" aria-hidden="true" />
              <span>Settings</span>
            </Link>
          ) : null}
          {showBackToProjects ? (
            <Link
              to="/"
              onClick={onCloseDrawer}
              className="flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] text-sidebar-foreground transition-colors duration-150 hover:bg-sidebar-hover"
            >
              <ArrowLeft className="size-4 text-sidebar-muted" aria-hidden="true" />
              <span>Back to all projects</span>
            </Link>
          ) : null}
        </div>
      </div>

      <div className="border-t border-sidebar-border">
        {effectiveRole !== "viewer" && (meta?.mode === "local" || meta?.mode === "server") ? (
          <div className="p-3">
            <p className="text-xs font-medium text-sidebar-muted">SMTP quick copy</p>
            <dl className="mt-2 flex flex-col gap-1.5 text-xs">
              <div className="flex items-center justify-between gap-2">
                <dt className="text-sidebar-muted">Server</dt>
                <dd className="min-w-0">
                  <button
                    type="button"
                    onClick={() => void copyToClipboard(`${smtpHost}:${smtpPort}`, "SMTP server")}
                    className="inline-flex max-w-full cursor-pointer items-center gap-1 rounded-sm px-1 py-0.5 font-mono text-[11px] text-sidebar-foreground hover:bg-sidebar-hover"
                    title="Copy host and port"
                  >
                    <span className="truncate">
                      {smtpHost}:{smtpPort}
                    </span>
                    <Copy className="size-3 shrink-0 text-sidebar-muted" aria-hidden="true" />
                  </button>
                </dd>
              </div>
              {quickCopyUser ? (
                <div className="flex items-center justify-between gap-2">
                  <dt className="text-sidebar-muted">User</dt>
                  <dd className="min-w-0">
                    <button
                      type="button"
                      onClick={() => void copyToClipboard(quickCopyUser, "SMTP username")}
                      className="inline-flex max-w-full cursor-pointer items-center gap-1 rounded-sm px-1 py-0.5 font-mono text-[11px] text-sidebar-foreground hover:bg-sidebar-hover"
                      title="Copy SMTP username"
                    >
                      <span className="truncate">{quickCopyUser}</span>
                      <Copy className="size-3 shrink-0 text-sidebar-muted" aria-hidden="true" />
                    </button>
                  </dd>
                </div>
              ) : null}
            </dl>
          </div>
        ) : null}

        <div
          className="flex items-center gap-2 border-t border-sidebar-border px-3 py-2 text-xs text-sidebar-muted"
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
                ? "bg-sidebar-success"
                : realtimeStatus === "reconnecting"
                  ? "animate-pulse bg-sidebar-warning"
                  : "bg-sidebar-muted",
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

      <Dialog
        open={createTagOpen}
        onOpenChange={setCreateTagOpen}
        title="New tag"
        description={`Create a tag for ${project.name}.`}
      >
        <form onSubmit={handleCreateTagSubmit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <label htmlFor={newTagInputId} className="text-xs font-medium text-foreground">
              Tag name
            </label>
            <Input
              id={newTagInputId}
              autoFocus
              required
              maxLength={40}
              placeholder="e.g. invoice, staging, urgent"
              value={newTagName}
              onChange={(e) => {
                setNewTagName(e.target.value);
                if (newTagError) {
                  setNewTagError(null);
                }
              }}
            />
            {newTagError ? <p className="text-xs text-destructive">{newTagError}</p> : null}
          </div>

          <fieldset className="flex flex-col gap-1.5 border-0 p-0">
            <legend className="text-xs font-medium text-foreground">Color</legend>
            <div className="flex flex-wrap gap-2">
              {PROJECT_COLORS.map((color) => (
                <button
                  key={color}
                  type="button"
                  aria-label={color}
                  aria-pressed={newTagColor === color}
                  onClick={() => setNewTagColor(color)}
                  className={cn(
                    "flex size-6 cursor-pointer items-center justify-center rounded-full border transition-transform",
                    newTagColor === color
                      ? "scale-110 border-foreground ring-2 ring-accent/40"
                      : "border-transparent hover:scale-105",
                  )}
                >
                  <span
                    className={cn("size-4 rounded-full", PROJECT_COLOR_DOT_CLASS[color])}
                    aria-hidden="true"
                  />
                </button>
              ))}
            </div>
          </fieldset>

          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setCreateTagOpen(false)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              variant="primary"
              size="sm"
              disabled={createTagMutation.isPending}
            >
              {createTagMutation.isPending ? "Creating..." : "Create tag"}
            </Button>
          </div>
        </form>
      </Dialog>

      <ConfirmDialog
        open={Boolean(deletingTag)}
        onOpenChange={(open) => {
          if (!open) {
            setDeletingTag(null);
          }
        }}
        title={deletingTag ? `Delete tag "${deletingTag.name}"?` : "Delete tag?"}
        description="This removes the tag from all messages in this project. The messages themselves are kept."
        confirmLabel="Delete tag"
        destructive
        pending={deleteTagMutation.isPending}
        onConfirm={() => {
          if (deletingTag) {
            deleteTagMutation.mutate(deletingTag);
          }
        }}
      />
    </aside>
  );
}
