import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  Eye,
  Folder,
  FolderKanban,
  Inbox,
  Keyboard,
  LogOut,
  Mail,
  Monitor,
  Moon,
  PanelLeft,
  Plus,
  Search,
  Send,
  Settings,
  Shield,
  SlidersHorizontal,
  Sun,
  Tag,
  Trash2,
  User,
  Users,
} from "lucide-react";
import { type KeyboardEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import { useAppCommands } from "@/components/AppCommandContext";
import { Badge } from "@/components/ui/badge";
import { api, handleApiError, setCsrfToken, unwrapVoid } from "@/lib/api";
import { formatCount, getSenderLabel, PROJECT_COLOR_DOT_CLASS } from "@/lib/format";
import { authMeQueryOptions, metaQueryOptions, projectsQueryOptions } from "@/lib/queries";
import { type ThemePreference, useTheme } from "@/lib/theme";
import { usePersistentState } from "@/lib/usePersistentState";
import { cn } from "@/lib/utils";
import { canUserCreateProjects } from "@/lib/viewerPreview";

type PaletteGroup =
  | "Recent"
  | "Projects"
  | "Tags"
  | "Search operators"
  | "Recent messages"
  | "Actions";

interface PaletteItem {
  id: string;
  group: Exclude<PaletteGroup, "Recent">;
  label: string;
  subtitle?: string;
  keywords?: string;
  shortcut?: string;
  dotClass?: string;
  badge?: string;
  destructive?: boolean;
  icon:
    | "project"
    | "tag"
    | "search"
    | "message"
    | "send"
    | "trash"
    | "plus"
    | "sidebar"
    | "theme"
    | "home"
    | "prefs"
    | "profile"
    | "security"
    | "logout"
    | "settings"
    | "eye"
    | "admin-users"
    | "admin-projects"
    | "keys";
  onSelect: () => void;
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((v) => typeof v === "string");
}

/**
 * Lightweight fuzzy scorer returning >0 for a match (higher is better) or 0 for no match.
 */
export function fuzzyScore(query: string, candidate: string): number {
  const q = query.trim().toLowerCase();
  if (!q) {
    return 1;
  }
  const text = candidate.toLowerCase();
  const exactIdx = text.indexOf(q);
  if (exactIdx === 0) {
    return 100 + q.length;
  }
  if (exactIdx > 0) {
    return 70 - Math.min(exactIdx, 40) + q.length;
  }

  let qi = 0;
  let score = 0;
  let streak = 0;
  for (let i = 0; i < text.length && qi < q.length; i += 1) {
    if (text[i] === q[qi]) {
      qi += 1;
      streak += 1;
      const boundaryBonus = i === 0 || text[i - 1] === " " || text[i - 1] === "-" ? 6 : 0;
      score += 2 + streak * 2 + boundaryBonus;
    } else {
      streak = 0;
    }
  }
  return qi === q.length ? score : 0;
}

function renderItemIcon(item: PaletteItem) {
  if (item.icon === "project") {
    return (
      <span
        className={cn(
          "size-2.5 shrink-0 rounded-full",
          item.dotClass ?? PROJECT_COLOR_DOT_CLASS.slate,
        )}
        aria-hidden="true"
      />
    );
  }
  const className = cn(
    "size-3.5 shrink-0",
    item.destructive ? "text-destructive" : "text-muted-foreground",
  );
  switch (item.icon) {
    case "tag":
      return <Tag className={className} aria-hidden="true" />;
    case "search":
      return <Search className={className} aria-hidden="true" />;
    case "message":
      return <Mail className={className} aria-hidden="true" />;
    case "send":
      return <Send className={className} aria-hidden="true" />;
    case "trash":
      return <Trash2 className={className} aria-hidden="true" />;
    case "plus":
      return <Plus className={className} aria-hidden="true" />;
    case "sidebar":
      return <PanelLeft className={className} aria-hidden="true" />;
    case "theme":
      return <Sun className={className} aria-hidden="true" />;
    case "home":
      return <Inbox className={className} aria-hidden="true" />;
    case "prefs":
      return <SlidersHorizontal className={className} aria-hidden="true" />;
    case "profile":
      return <User className={className} aria-hidden="true" />;
    case "security":
      return <Shield className={className} aria-hidden="true" />;
    case "logout":
      return <LogOut className={className} aria-hidden="true" />;
    case "settings":
      return <Settings className={className} aria-hidden="true" />;
    case "eye":
      return <Eye className={className} aria-hidden="true" />;
    case "admin-users":
      return <Users className={className} aria-hidden="true" />;
    case "admin-projects":
      return <FolderKanban className={className} aria-hidden="true" />;
    case "keys":
      return <Keyboard className={className} aria-hidden="true" />;
  }
}

export interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

const THEME_CYCLE: ReadonlyArray<ThemePreference> = ["light", "dark", "system"];

export function CommandPalette({ open, onOpenChange }: CommandPaletteProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { activeInbox, openCreateProject, openShortcuts, previewAsViewer, togglePreviewAsViewer } =
    useAppCommands();
  const { theme, setTheme } = useTheme();
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: me } = useQuery(authMeQueryOptions());
  const { data: projects } = useQuery(projectsQueryOptions());
  const canCreateProjects = canUserCreateProjects(meta?.mode, me?.user);

  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const [recentIds, setRecentIds] = usePersistentState<string[]>(
    "recent-commands",
    [],
    isStringArray,
  );

  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const listboxId = useId();
  const labelId = useId();

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) {
      return;
    }
    if (open && !dialog.open) {
      setQuery("");
      setActiveIndex(0);
      dialog.showModal();
      inputRef.current?.focus();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) {
      return;
    }
    const handleClose = () => {
      dialog.returnValue = "";
      onOpenChange(false);
    };
    dialog.addEventListener("close", handleClose);

    if ("closedBy" in HTMLDialogElement.prototype) {
      return () => {
        dialog.removeEventListener("close", handleClose);
      };
    }

    let pressedOutside = false;
    const isOutside = (e: PointerEvent) => {
      const rect = dialog.getBoundingClientRect();
      return (
        e.clientX < rect.left ||
        e.clientX > rect.right ||
        e.clientY < rect.top ||
        e.clientY > rect.bottom
      );
    };
    const handlePointerDown = (e: PointerEvent) => {
      pressedOutside = isOutside(e);
    };
    const handlePointerUp = (e: PointerEvent) => {
      if (pressedOutside && isOutside(e)) {
        dialog.close();
      }
      pressedOutside = false;
    };
    dialog.addEventListener("pointerdown", handlePointerDown);
    dialog.addEventListener("pointerup", handlePointerUp);
    return () => {
      dialog.removeEventListener("close", handleClose);
      dialog.removeEventListener("pointerdown", handlePointerDown);
      dialog.removeEventListener("pointerup", handlePointerUp);
    };
  }, [onOpenChange]);

  const allItems = useMemo<PaletteItem[]>(() => {
    const items: PaletteItem[] = [];

    if (projects) {
      for (const proj of projects) {
        items.push({
          id: `project:${proj.slug}`,
          group: "Projects",
          label: proj.name,
          subtitle: proj.slug,
          keywords: `project inbox ${proj.slug}`,
          dotClass: PROJECT_COLOR_DOT_CLASS[proj.color] ?? PROJECT_COLOR_DOT_CLASS.slate,
          ...(proj.unread_count > 0 ? { badge: `${formatCount(proj.unread_count)} unread` } : {}),
          icon: "project",
          onSelect: () => {
            void navigate({ to: "/p/$project", params: { project: proj.slug } });
          },
        });
      }
    }

    if (activeInbox) {
      for (const tag of activeInbox.tags) {
        items.push({
          id: `tag:${activeInbox.projectSlug}:${tag.id}`,
          group: "Tags",
          label: `Filter by tag: ${tag.name}`,
          subtitle: `${formatCount(tag.message_count)} ${tag.message_count === 1 ? "message" : "messages"}`,
          keywords: `tag filter label ${tag.name}`,
          icon: "tag",
          onSelect: () => activeInbox.selectTagFilter(tag.name),
        });
      }

      const searchOps: ReadonlyArray<{
        op: string;
        desc: string;
        complete: boolean;
      }> = [
        { op: "from:", desc: "Search by sender", complete: false },
        { op: "to:", desc: "Search by recipient", complete: false },
        { op: "subject:", desc: "Search by subject", complete: false },
        { op: "tag:", desc: "Search by tag", complete: false },
        { op: "is:unread", desc: "Show unread messages", complete: true },
        { op: "has:attachment", desc: "Messages with attachments", complete: true },
        { op: "after:", desc: "Received on or after date", complete: false },
        { op: "before:", desc: "Received before date", complete: false },
      ];
      for (const entry of searchOps) {
        items.push({
          id: `search:${entry.op}`,
          group: "Search operators",
          label: entry.op,
          subtitle: entry.desc,
          keywords: `search filter operator query ${entry.op} ${entry.desc}`,
          icon: "search",
          onSelect: () => activeInbox.applySearchOperator(entry.op, entry.complete),
        });
      }
    }

    if (activeInbox && activeInbox.messages.length > 0) {
      for (const msg of activeInbox.messages.slice(0, 8)) {
        const subject = msg.subject.trim() || "(no subject)";
        const sender = getSenderLabel(msg.from);
        items.push({
          id: `message:${activeInbox.projectSlug}:${msg.id}`,
          group: "Recent messages",
          label: subject,
          subtitle: sender,
          keywords: `message email ${sender} ${msg.from.address}`,
          icon: "message",
          onSelect: () => {
            void navigate({
              to: "/p/$project/m/$messageId",
              params: { project: activeInbox.projectSlug, messageId: msg.id },
            });
          },
        });
      }
    }

    if (activeInbox) {
      if (activeInbox.effectiveRole !== "viewer") {
        items.push({
          id: "action:send-test",
          group: "Actions",
          label: `Send test email to ${activeInbox.projectName}`,
          keywords: "send test sample email",
          icon: "send",
          onSelect: () => activeInbox.sendTestEmail(),
        });
        if (activeInbox.messageCount > 0) {
          items.push({
            id: "action:delete-all",
            group: "Actions",
            label: `Delete all in ${activeInbox.projectName}`,
            keywords: "clear empty delete all messages",
            destructive: true,
            icon: "trash",
            onSelect: () => activeInbox.openDeleteAll(),
          });
        }
        items.push({
          id: "action:project-settings",
          group: "Actions",
          label: `Go to ${activeInbox.projectName} settings`,
          shortcut: "g s",
          keywords: "project settings general members invites",
          icon: "settings",
          onSelect: () => {
            void navigate({
              to:
                activeInbox.effectiveRole === "owner"
                  ? "/p/$project/settings/general"
                  : "/p/$project/settings/members",
              params: { project: activeInbox.projectSlug },
            });
          },
        });
      }
      if (activeInbox.actualRole === "owner") {
        items.push({
          id: "action:preview-as-viewer",
          group: "Actions",
          label: previewAsViewer ? "Exit viewer preview" : "Preview as viewer",
          keywords: "client view mode preview viewer role",
          icon: "eye",
          onSelect: () => togglePreviewAsViewer(),
        });
      }
      items.push({
        id: "action:toggle-sidebar",
        group: "Actions",
        label: "Toggle sidebar",
        shortcut: "[",
        keywords: "sidebar collapse expand navigation",
        icon: "sidebar",
        onSelect: () => activeInbox.toggleSidebar(),
      });
    }

    const nextTheme =
      THEME_CYCLE[(THEME_CYCLE.indexOf(theme) + 1) % THEME_CYCLE.length] ?? "system";
    items.push(
      ...(canCreateProjects
        ? ([
            {
              id: "action:new-project",
              group: "Actions",
              label: "New project",
              shortcut: "n",
              keywords: "create add project",
              icon: "plus",
              onSelect: () => openCreateProject(),
            },
          ] satisfies PaletteItem[])
        : []),
      {
        id: "action:toggle-theme",
        group: "Actions",
        label: `Toggle theme (switch to ${nextTheme})`,
        keywords: "theme dark light system appearance color",
        icon: "theme",
        onSelect: () => setTheme(nextTheme),
      },
      {
        id: "action:go-projects",
        group: "Actions",
        label: "Go to Projects",
        shortcut: "g p",
        keywords: "home all projects list",
        icon: "home",
        onSelect: () => {
          void navigate({ to: "/" });
        },
      },
      {
        id: "action:go-preferences",
        group: "Actions",
        label: "Go to Preferences",
        shortcut: "g a",
        keywords: "account settings preferences density shortcuts theme clock",
        icon: "prefs",
        onSelect: () => {
          void navigate({ to: "/account/preferences" });
        },
      },
      ...(meta?.mode === "server" && me?.user.is_admin
        ? ([
            {
              id: "action:admin-users",
              group: "Actions",
              label: "Admin: Users",
              keywords: "admin users invite permissions reset password",
              icon: "admin-users",
              onSelect: () => {
                void navigate({ to: "/admin/users" });
              },
            },
            {
              id: "action:admin-projects",
              group: "Actions",
              label: "Admin: Projects",
              keywords: "admin all projects owners storage",
              icon: "admin-projects",
              onSelect: () => {
                void navigate({ to: "/admin/projects" });
              },
            },
          ] satisfies PaletteItem[])
        : []),
      ...(meta?.mode === "server"
        ? ([
            {
              id: "action:account-profile",
              group: "Actions",
              label: "Account profile",
              keywords: "account profile name email",
              icon: "profile",
              onSelect: () => {
                void navigate({ to: "/account/profile" });
              },
            },
            {
              id: "action:account-security",
              group: "Actions",
              label: "Account security & sessions",
              keywords: "account security password sessions revoke",
              icon: "security",
              onSelect: () => {
                void navigate({ to: "/account/security" });
              },
            },
            {
              id: "action:sign-out",
              group: "Actions",
              label: "Sign out",
              keywords: "logout sign out exit account",
              destructive: true,
              icon: "logout",
              onSelect: () => {
                void (async () => {
                  try {
                    unwrapVoid(await api.POST("/api/v1/auth/logout"));
                    setCsrfToken("");
                    queryClient.clear();
                    await navigate({ to: "/login", search: { returnTo: undefined } });
                  } catch (err) {
                    handleApiError(err, "Failed to sign out");
                  }
                })();
              },
            },
          ] satisfies PaletteItem[])
        : []),
      {
        id: "action:shortcuts",
        group: "Actions",
        label: "Keyboard shortcuts",
        shortcut: "?",
        keywords: "help hotkeys keys bindings",
        icon: "keys",
        onSelect: () => openShortcuts(),
      },
    );

    return items;
  }, [
    projects,
    activeInbox,
    previewAsViewer,
    togglePreviewAsViewer,
    canCreateProjects,
    theme,
    setTheme,
    navigate,
    openCreateProject,
    openShortcuts,
    meta?.mode,
    me?.user.is_admin,
    queryClient,
  ]);

  const sections = useMemo<Array<{ group: PaletteGroup; items: PaletteItem[] }>>(() => {
    const trimmed = query.trim();
    if (!trimmed) {
      const byId = new Map(allItems.map((item) => [item.id, item]));
      const recentItems = recentIds
        .map((id) => byId.get(id))
        .filter((item): item is PaletteItem => Boolean(item))
        .slice(0, 4);
      const recentSet = new Set(recentItems.map((item) => item.id));

      const grouped: Array<{ group: PaletteGroup; items: PaletteItem[] }> = [];
      if (recentItems.length > 0) {
        grouped.push({ group: "Recent", items: recentItems });
      }
      for (const groupName of [
        "Projects",
        "Tags",
        "Search operators",
        "Recent messages",
        "Actions",
      ] as const) {
        const list = allItems.filter((item) => item.group === groupName && !recentSet.has(item.id));
        if (list.length > 0) {
          grouped.push({ group: groupName, items: list });
        }
      }
      return grouped;
    }

    const scored = allItems
      .map((item) => ({
        item,
        score: fuzzyScore(trimmed, `${item.label} ${item.subtitle ?? ""} ${item.keywords ?? ""}`),
      }))
      .filter((entry) => entry.score > 0)
      .sort((a, b) => b.score - a.score);

    const grouped: Array<{ group: PaletteGroup; items: PaletteItem[] }> = [];
    for (const groupName of [
      "Projects",
      "Tags",
      "Search operators",
      "Recent messages",
      "Actions",
    ] as const) {
      const list = scored
        .filter((entry) => entry.item.group === groupName)
        .map((entry) => entry.item);
      if (list.length > 0) {
        grouped.push({ group: groupName, items: list });
      }
    }
    return grouped;
  }, [allItems, query, recentIds]);

  const flatItems = useMemo(() => sections.flatMap((s) => s.items), [sections]);

  useEffect(() => {
    if (activeIndex >= flatItems.length) {
      setActiveIndex(0);
    }
  }, [activeIndex, flatItems.length]);

  const executeItem = (item: PaletteItem) => {
    setRecentIds((prev) => [item.id, ...prev.filter((id) => id !== item.id)].slice(0, 8));
    onOpenChange(false);
    item.onSelect();
  };

  const handleInputKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || (e.ctrlKey && e.key.toLowerCase() === "n")) {
      e.preventDefault();
      if (flatItems.length > 0) {
        setActiveIndex((prev) => (prev + 1) % flatItems.length);
      }
      return;
    }
    if (e.key === "ArrowUp" || (e.ctrlKey && e.key.toLowerCase() === "p")) {
      e.preventDefault();
      if (flatItems.length > 0) {
        setActiveIndex((prev) => (prev - 1 + flatItems.length) % flatItems.length);
      }
      return;
    }
    if (e.key === "Enter") {
      const target = flatItems[activeIndex];
      if (target) {
        e.preventDefault();
        executeItem(target);
      }
    }
  };

  const activeItem = flatItems[activeIndex];
  const activeOptionId = activeItem ? `${listboxId}-opt-${activeIndex}` : undefined;
  const closedByProp = { closedby: "any" } as Record<string, string>;

  let optionCounter = 0;

  return (
    <dialog
      ref={dialogRef}
      aria-labelledby={labelId}
      {...closedByProp}
      className="m-auto mt-[12vh] w-[min(94vw,36rem)] overflow-hidden rounded-lg border border-border bg-surface p-0 text-foreground shadow-2xl"
    >
      {open ? (
        <div className="flex flex-col">
          <h2 id={labelId} className="sr-only">
            Command palette
          </h2>
          <div className="flex items-center gap-2 border-b border-border px-3.5 py-2.5">
            <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <input
              ref={inputRef}
              type="text"
              role="combobox"
              aria-expanded="true"
              aria-controls={listboxId}
              aria-activedescendant={activeOptionId}
              aria-autocomplete="list"
              aria-label="Search projects, messages, and actions"
              placeholder="Jump to a project, message, or action..."
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setActiveIndex(0);
              }}
              onKeyDown={handleInputKeyDown}
              className="h-7 min-w-0 flex-1 bg-transparent text-sm text-foreground placeholder:text-muted-foreground focus-visible:outline-offset-4"
            />
            <kbd className="rounded-sm border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
              Esc
            </kbd>
          </div>

          <div
            id={listboxId}
            role="listbox"
            aria-label="Command palette results"
            className="max-h-80 overflow-y-auto p-1.5"
          >
            {sections.length === 0 ? (
              <div className="flex flex-col items-center justify-center gap-1.5 py-8 text-center text-xs text-muted-foreground">
                <Folder className="size-5 opacity-50" aria-hidden="true" />
                <p>No matching projects, messages, or actions.</p>
              </div>
            ) : (
              sections.map((section) => {
                const groupHeadingId = `${listboxId}-grp-${section.group.replace(/\s+/g, "-")}`;
                return (
                  <fieldset
                    key={section.group}
                    aria-labelledby={groupHeadingId}
                    className="mb-1 border-0 p-0 last:mb-0"
                  >
                    <legend
                      id={groupHeadingId}
                      className="w-full px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground"
                    >
                      {section.group}
                    </legend>
                    {section.items.map((item) => {
                      const idx = optionCounter;
                      optionCounter += 1;
                      const isSelected = idx === activeIndex;
                      const optionId = `${listboxId}-opt-${idx}`;
                      return (
                        <button
                          key={item.id}
                          id={optionId}
                          type="button"
                          role="option"
                          aria-selected={isSelected}
                          onMouseEnter={() => setActiveIndex(idx)}
                          onClick={() => executeItem(item)}
                          className={cn(
                            "flex w-full cursor-pointer items-center justify-between gap-3 rounded-md px-2.5 py-1.5 text-left text-[13px] transition-colors",
                            isSelected
                              ? "bg-accent/12 text-foreground"
                              : "text-foreground hover:bg-muted/60",
                          )}
                        >
                          <span className="flex min-w-0 items-center gap-2.5">
                            {renderItemIcon(item)}
                            <span
                              className={cn(
                                "truncate font-medium",
                                item.destructive ? "text-destructive" : "text-foreground",
                              )}
                            >
                              {item.label}
                            </span>
                            {item.subtitle ? (
                              <span className="truncate text-xs text-muted-foreground">
                                {item.subtitle}
                              </span>
                            ) : null}
                          </span>
                          <span className="flex shrink-0 items-center gap-1.5">
                            {item.badge ? <Badge variant="accent">{item.badge}</Badge> : null}
                            {item.shortcut ? (
                              <kbd className="rounded-sm border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                                {item.shortcut}
                              </kbd>
                            ) : null}
                          </span>
                        </button>
                      );
                    })}
                  </fieldset>
                );
              })
            )}
          </div>

          <div className="flex items-center justify-between border-t border-border bg-background/60 px-3 py-1.5 text-[11px] text-muted-foreground">
            <span className="flex items-center gap-2">
              <span className="inline-flex items-center gap-1">
                <kbd className="rounded-sm border border-border bg-surface px-1 font-mono">↑↓</kbd>
                navigate
              </span>
              <span className="inline-flex items-center gap-1">
                <kbd className="rounded-sm border border-border bg-surface px-1 font-mono">↵</kbd>
                select
              </span>
            </span>
            <span className="inline-flex items-center gap-1">
              {theme === "dark" ? (
                <Moon className="size-3" aria-hidden="true" />
              ) : theme === "light" ? (
                <Sun className="size-3" aria-hidden="true" />
              ) : (
                <Monitor className="size-3" aria-hidden="true" />
              )}
              <span>{theme}</span>
            </span>
          </div>
        </div>
      ) : null}
    </dialog>
  );
}
