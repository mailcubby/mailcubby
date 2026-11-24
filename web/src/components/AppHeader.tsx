import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  ChevronDown,
  Inbox,
  Keyboard,
  LogOut,
  Monitor,
  Moon,
  Search,
  Shield,
  SlidersHorizontal,
  Sun,
  User,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useAppCommands } from "@/components/AppCommandContext";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api, handleApiError, setCsrfToken, unwrapVoid } from "@/lib/api";
import { authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
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

function AccountMenu() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: me } = useQuery(authMeQueryOptions());
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

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

  const displayName = me?.user.name || me?.user.email || "Account";

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Account menu"
        onClick={() => setOpen((prev) => !prev)}
        className="inline-flex h-7 max-w-44 cursor-pointer items-center gap-1.5 rounded-md border border-border bg-background px-2 text-xs font-medium text-foreground transition-colors hover:bg-muted"
      >
        <User className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="hidden truncate sm:inline">{displayName}</span>
        <ChevronDown className="size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
      </button>

      {open ? (
        <div
          role="menu"
          aria-label="Account"
          className="absolute right-0 z-50 mt-1.5 w-56 rounded-md border border-border bg-surface p-1 shadow-md"
        >
          <div className="border-b border-border px-2.5 py-2">
            <div className="truncate text-xs font-medium text-foreground">{displayName}</div>
            {me?.user.email ? (
              <div className="truncate text-[11px] text-muted-foreground">{me.user.email}</div>
            ) : null}
          </div>

          <div className="flex flex-col py-1">
            <Link
              to="/account/profile"
              role="menuitem"
              onClick={() => setOpen(false)}
              className="flex items-center gap-2 rounded-sm px-2.5 py-1.5 text-xs text-foreground hover:bg-muted"
            >
              <User className="size-3.5 text-muted-foreground" aria-hidden="true" />
              <span>Profile</span>
            </Link>
            <Link
              to="/account/security"
              role="menuitem"
              onClick={() => setOpen(false)}
              className="flex items-center gap-2 rounded-sm px-2.5 py-1.5 text-xs text-foreground hover:bg-muted"
            >
              <Shield className="size-3.5 text-muted-foreground" aria-hidden="true" />
              <span>Security &amp; sessions</span>
            </Link>
            <Link
              to="/account/preferences"
              role="menuitem"
              onClick={() => setOpen(false)}
              className="flex items-center gap-2 rounded-sm px-2.5 py-1.5 text-xs text-foreground hover:bg-muted"
            >
              <SlidersHorizontal className="size-3.5 text-muted-foreground" aria-hidden="true" />
              <span>Preferences</span>
            </Link>
          </div>

          <div className="border-t border-border pt-1">
            <button
              type="button"
              role="menuitem"
              disabled={logoutMutation.isPending}
              onClick={() => logoutMutation.mutate()}
              className="flex w-full cursor-pointer items-center gap-2 rounded-sm px-2.5 py-1.5 text-left text-xs text-destructive hover:bg-destructive/10 disabled:opacity-50"
            >
              <LogOut className="size-3.5" aria-hidden="true" />
              <span>{logoutMutation.isPending ? "Signing out..." : "Sign out"}</span>
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}

export function AppHeader() {
  const { data: meta } = useQuery(metaQueryOptions());
  const { openPalette, openShortcuts } = useAppCommands();
  const instanceName = meta?.instance_name || "Mailcubby";
  const isServer = meta?.mode === "server";

  return (
    <header className="flex h-11 shrink-0 items-center justify-between gap-3 border-b border-border bg-surface px-4">
      <div className="flex min-w-0 items-center gap-3">
        <Link
          to="/"
          className="inline-flex items-center gap-2 rounded-sm text-[13px] font-semibold tracking-tight text-foreground hover:opacity-80"
        >
          <span className="flex size-6 items-center justify-center rounded-md bg-foreground text-background">
            <Inbox className="size-3.5" aria-hidden="true" />
          </span>
          <span className="truncate">{instanceName}</span>
        </Link>
        {meta?.mode === "local" ? (
          <Badge variant="outline" className="hidden sm:inline-flex">
            Local mode
          </Badge>
        ) : null}
      </div>

      <nav aria-label="Application utilities" className="flex items-center gap-1.5 sm:gap-2">
        <button
          type="button"
          aria-label="Open command palette"
          onClick={openPalette}
          className="inline-flex h-7 cursor-pointer items-center gap-2 rounded-md border border-border bg-background px-2 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <Search className="size-3.5" aria-hidden="true" />
          <span className="hidden sm:inline">Search</span>
          <kbd className="rounded-xs border border-border bg-surface px-1 py-0.25 font-mono text-[10px] text-muted-foreground">
            ⌘K
          </kbd>
        </button>

        <Button
          variant="ghost"
          size="icon"
          aria-label="Keyboard shortcuts"
          title="Keyboard shortcuts (?)"
          onClick={openShortcuts}
          className="size-7"
        >
          <Keyboard className="size-3.5" aria-hidden="true" />
        </Button>

        {isServer ? (
          <AccountMenu />
        ) : (
          <Link
            to="/account/preferences"
            aria-label="Preferences"
            title="Preferences (g a)"
            className="inline-flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <SlidersHorizontal className="size-3.5" aria-hidden="true" />
          </Link>
        )}

        <ThemeToggle />
      </nav>
    </header>
  );
}
