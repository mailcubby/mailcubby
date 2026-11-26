import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  ChevronDown,
  Eye,
  Keyboard,
  KeyRound,
  LogOut,
  Monitor,
  Moon,
  Search,
  Shield,
  ShieldCheck,
  SlidersHorizontal,
  Sun,
  User,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useAppCommands } from "@/components/AppCommandContext";
import { BrandMark } from "@/components/BrandMark";
import { Button, buttonClassName } from "@/components/ui/button";
import { api, handleApiError, setCsrfToken, unwrapVoid } from "@/lib/api";
import { authMeQueryOptions, metaQueryOptions, projectsQueryOptions } from "@/lib/queries";
import { type ThemePreference, useTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

const THEME_OPTIONS: ReadonlyArray<{
  value: ThemePreference;
  label: string;
  Icon: typeof Sun;
}> = [
  { value: "light", label: "Light", Icon: Sun },
  { value: "dark", label: "Dark", Icon: Moon },
  { value: "system", label: "System", Icon: Monitor },
];

/**
 * ThemeToggle is the segmented theme control on the sign-in, setup, invite and
 * reset pages, which have no account menu. Inside the app the theme lives in
 * the header menu (ThemeMenuGroup), the command palette and Preferences.
 */
export function ThemeToggle() {
  const { theme, systemTheme, setTheme } = useTheme();

  return (
    <fieldset
      aria-label="Theme preference"
      className="inline-flex items-center gap-0.5 rounded-md border border-border bg-background p-0.5"
    >
      <legend className="sr-only">Theme preference</legend>
      {THEME_OPTIONS.map(({ value, label, Icon }) => {
        const active = theme === value;
        const displayLabel = value === "system" ? `${label} (Currently ${systemTheme})` : label;
        return (
          <button
            key={value}
            type="button"
            aria-pressed={active}
            aria-label={label}
            title={displayLabel}
            onClick={() => setTheme(value)}
            className={cn(
              "inline-flex h-6 min-w-6 cursor-pointer items-center justify-center gap-1 rounded-sm px-2 text-xs font-medium transition-colors duration-150",
              active
                ? "bg-surface text-foreground shadow-2xs"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Icon className="size-3.5" aria-hidden="true" />
            <span className="hidden md:inline">{label}</span>
          </button>
        );
      })}
    </fieldset>
  );
}

const MENU_ITEM_CLASS =
  "flex items-center gap-2 rounded-sm px-2.5 py-1.5 text-xs text-foreground hover:bg-muted";

/** ThemeMenuGroup is the theme choice inside the header menu (UI.md 3.7). */
function ThemeMenuGroup({ onSelect }: { onSelect: () => void }) {
  const { theme, systemTheme, setTheme } = useTheme();

  return (
    <fieldset className="m-0 min-w-0 border-0 border-t border-border px-1.5 pt-2 pb-1.5">
      <legend className="float-left mb-1.5 w-full px-1 text-[11px] font-medium text-muted-foreground">
        Theme
      </legend>
      <div className="clear-left grid grid-cols-3 gap-0.5 rounded-md bg-muted p-0.5">
        {THEME_OPTIONS.map(({ value, label, Icon }) => {
          const active = theme === value;
          return (
            <button
              key={value}
              type="button"
              role="menuitemradio"
              aria-checked={active}
              title={value === "system" ? `${label} (currently ${systemTheme})` : label}
              onClick={() => {
                setTheme(value);
                onSelect();
              }}
              className={cn(
                "inline-flex h-7 cursor-pointer items-center justify-center gap-1 rounded-sm text-xs font-medium transition-colors duration-150",
                active
                  ? "bg-surface text-foreground shadow-2xs"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon className="size-3.5" aria-hidden="true" />
              <span>{label}</span>
            </button>
          );
        })}
      </div>
    </fieldset>
  );
}

/**
 * HeaderMenu is the account menu in server mode and the preferences menu in
 * local mode (which has no accounts). Both hold the theme choice.
 */
function HeaderMenu({ mode }: { mode: "server" | "local" }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const isServer = mode === "server";
  const { data: me } = useQuery({ ...authMeQueryOptions(), enabled: isServer });
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    const handlePointerDown = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener("mousedown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  const logoutMutation = useMutation({
    mutationFn: async () => {
      unwrapVoid(await api.POST("/api/v1/auth/logout"));
    },
    onSuccess: async () => {
      setOpen(false);
      setCsrfToken("");
      queryClient.clear();
      await navigate({ to: "/login", search: { returnTo: undefined } });
    },
    onError: (err) => {
      handleApiError(err, "Failed to sign out");
    },
  });

  const close = () => setOpen(false);
  const displayName = me?.user.name || me?.user.email || "Account";
  const initial = displayName.trim().charAt(0).toUpperCase() || "A";

  return (
    <div ref={containerRef} className="relative">
      <button
        ref={triggerRef}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={isServer ? undefined : "Preferences menu"}
        title={isServer ? undefined : "Theme and preferences"}
        onClick={() => setOpen((prev) => !prev)}
        className="inline-flex h-8 max-w-48 cursor-pointer items-center gap-1.5 rounded-md px-1.5 text-[13px] font-medium text-sidebar-foreground transition-colors duration-150 hover:bg-sidebar-hover"
      >
        {isServer ? (
          <span
            aria-hidden="true"
            className="flex size-6 shrink-0 items-center justify-center rounded-full bg-sidebar-active text-[11px] font-semibold text-sidebar-active-foreground"
          >
            {initial}
          </span>
        ) : (
          <SlidersHorizontal className="size-4 shrink-0 text-sidebar-muted" aria-hidden="true" />
        )}
        {isServer ? (
          <span className="truncate max-sm:sr-only">
            {displayName}
            <span className="sr-only"> — Account menu</span>
          </span>
        ) : null}
        <ChevronDown className="size-3.5 shrink-0 text-sidebar-muted" aria-hidden="true" />
      </button>

      {open ? (
        <div
          role="menu"
          aria-label={isServer ? "Account" : "Preferences"}
          data-surface
          className="absolute right-0 z-50 mt-1.5 w-60 rounded-md border border-border bg-surface p-1 text-foreground shadow-md"
        >
          {isServer ? (
            <div className="border-b border-border px-2.5 py-2">
              <div className="truncate text-xs font-medium text-foreground">{displayName}</div>
              {me?.user.email ? (
                <div className="truncate text-[11px] text-muted-foreground">{me.user.email}</div>
              ) : null}
            </div>
          ) : null}

          <div className="flex flex-col py-1">
            {isServer ? (
              <>
                <Link
                  to="/account/profile"
                  role="menuitem"
                  onClick={close}
                  className={MENU_ITEM_CLASS}
                >
                  <User className="size-3.5 text-muted-foreground" aria-hidden="true" />
                  <span>Profile</span>
                </Link>
                <Link
                  to="/account/security"
                  role="menuitem"
                  onClick={close}
                  className={MENU_ITEM_CLASS}
                >
                  <Shield className="size-3.5 text-muted-foreground" aria-hidden="true" />
                  <span>Security &amp; sessions</span>
                </Link>
                <Link
                  to="/account/tokens"
                  search={{}}
                  role="menuitem"
                  onClick={close}
                  className={MENU_ITEM_CLASS}
                >
                  <KeyRound className="size-3.5 text-muted-foreground" aria-hidden="true" />
                  <span>API tokens</span>
                </Link>
              </>
            ) : null}
            <Link
              to="/account/preferences"
              role="menuitem"
              onClick={close}
              className={MENU_ITEM_CLASS}
            >
              <SlidersHorizontal className="size-3.5 text-muted-foreground" aria-hidden="true" />
              <span>Preferences</span>
            </Link>
            {isServer && me?.user.is_admin ? (
              <Link to="/admin/users" role="menuitem" onClick={close} className={MENU_ITEM_CLASS}>
                <ShieldCheck className="size-3.5 text-muted-foreground" aria-hidden="true" />
                <span>Admin</span>
              </Link>
            ) : null}
          </div>

          <ThemeMenuGroup onSelect={close} />

          {isServer ? (
            <div className="border-t border-border pt-1">
              <button
                type="button"
                role="menuitem"
                disabled={logoutMutation.isPending}
                onClick={() => logoutMutation.mutate()}
                className="flex w-full cursor-pointer items-center gap-2 rounded-sm px-2.5 py-1.5 text-left text-xs text-destructive hover:bg-destructive-subtle disabled:opacity-50"
              >
                <LogOut className="size-3.5" aria-hidden="true" />
                <span>{logoutMutation.isPending ? "Signing out..." : "Sign out"}</span>
              </button>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/**
 * useIsViewerContext reports whether the header is shown to a viewer: the
 * effective role in a project (so "Preview as viewer" counts), or, outside a
 * project in server mode, a non-admin whose every project role is viewer.
 */
function useIsViewerContext(isServer: boolean, isAdmin: boolean): boolean {
  const { activeInbox } = useAppCommands();
  const { data: projects } = useQuery({ ...projectsQueryOptions(), enabled: isServer });
  if (activeInbox) {
    return activeInbox.effectiveRole === "viewer";
  }
  if (!isServer || isAdmin || !projects || projects.length === 0) {
    return false;
  }
  return projects.every((p) => p.role === "viewer");
}

/**
 * AppHeader is the top bar on the chrome layer (UI.md 3.3). A viewer sees at
 * most the logo, search and the account menu.
 */
export function AppHeader() {
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: me } = useQuery(authMeQueryOptions());
  const { openPalette, openShortcuts, activeInbox, previewAsViewer, togglePreviewAsViewer } =
    useAppCommands();
  const instanceName = meta?.instance_name || "Mailcubby";
  const isServer = meta?.mode === "server";
  const canPreviewAsViewer = activeInbox?.actualRole === "owner";
  const isViewer = useIsViewerContext(isServer, Boolean(me?.user.is_admin));

  return (
    <header
      data-chrome
      className="flex h-12 shrink-0 items-center justify-between gap-3 border-b border-sidebar-border bg-sidebar px-3 text-sidebar-foreground sm:px-4"
    >
      <div className="flex min-w-0 items-center gap-3">
        <Link
          to="/"
          className="inline-flex min-w-0 items-center gap-2.5 rounded-md text-sm font-semibold tracking-tight text-sidebar-foreground"
        >
          <BrandMark tone="chrome" size="sm" />
          <span className="truncate">{instanceName}</span>
        </Link>
        {meta?.mode === "local" ? (
          <span className="hidden h-5 shrink-0 items-center rounded-full border border-sidebar-border px-2 text-[11.5px] font-medium text-sidebar-muted sm:inline-flex">
            Local mode
          </span>
        ) : null}
        {isServer && me?.user.is_admin ? (
          <Link
            to="/admin/users"
            className={buttonClassName({
              variant: "chrome",
              size: "sm",
              className: "hidden sm:inline-flex",
            })}
          >
            <ShieldCheck className="size-4" aria-hidden="true" />
            <span>Admin</span>
          </Link>
        ) : null}
      </div>

      <nav aria-label="Application utilities" className="flex items-center gap-1 sm:gap-1.5">
        {canPreviewAsViewer ? (
          <Button
            variant="chrome"
            size="sm"
            aria-pressed={previewAsViewer}
            aria-label={previewAsViewer ? "Exit viewer preview" : "Preview as viewer"}
            title={previewAsViewer ? "Exit viewer preview" : "Preview as viewer"}
            onClick={togglePreviewAsViewer}
            className={cn(
              previewAsViewer &&
                "bg-sidebar-accent text-sidebar-accent-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
            )}
          >
            <Eye className="size-4" aria-hidden="true" />
            <span className="hidden md:inline">
              {previewAsViewer ? "Viewer preview" : "Preview as viewer"}
            </span>
          </Button>
        ) : null}

        <button
          type="button"
          title="Search and open command palette (Cmd/Ctrl+K)"
          onClick={openPalette}
          className="inline-flex h-8 min-w-8 cursor-pointer items-center justify-center gap-2 rounded-md border border-sidebar-border bg-sidebar-input px-2 text-[13px] text-sidebar-muted transition-colors duration-150 hover:bg-sidebar-hover hover:text-sidebar-foreground sm:w-52 sm:justify-start sm:px-2.5 lg:w-64"
        >
          <Search className="size-4 shrink-0" aria-hidden="true" />
          <span className="hidden sm:inline">Search</span>
          <span className="sr-only sm:hidden">Search</span>
          <kbd className="ml-auto hidden rounded-sm border border-sidebar-border px-1 font-mono text-[10px] text-sidebar-muted sm:inline">
            ⌘K
          </kbd>
        </button>

        {!isViewer ? (
          <Button
            variant="chrome"
            size="icon"
            aria-label="Keyboard shortcuts"
            title="Keyboard shortcuts (?)"
            onClick={openShortcuts}
          >
            <Keyboard className="size-4" aria-hidden="true" />
          </Button>
        ) : null}

        <span aria-hidden="true" className="mx-1 hidden h-5 w-px bg-sidebar-border sm:block" />

        <HeaderMenu mode={isServer ? "server" : "local"} />
      </nav>
    </header>
  );
}
