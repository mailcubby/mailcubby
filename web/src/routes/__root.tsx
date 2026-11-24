import { useQuery } from "@tanstack/react-query";
import { createRootRoute, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useEffect, useMemo } from "react";
import { AppCommandProvider, useAppCommands } from "@/components/AppCommandContext";
import { AppHeader, ThemeToggle } from "@/components/AppHeader";
import { CommandPalette } from "@/components/CommandPalette";
import { RealtimeProvider } from "@/components/RealtimeProvider";
import { ShortcutsHelpDialog } from "@/components/ShortcutsHelpDialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Toaster } from "@/components/ui/toaster";
import { CreateProjectDialog } from "@/features/projects/CreateProjectDialog";
import { ApiError } from "@/lib/api";
import { useSingleKeyShortcutsPreference } from "@/lib/preferences";
import { authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
import { type KeyShortcutBinding, useKeyShortcuts } from "@/lib/shortcuts";

export const Route = createRootRoute({
  component: RootLayout,
});

function isPublicAuthPath(pathname: string): boolean {
  return (
    pathname === "/login" ||
    pathname === "/setup" ||
    pathname.startsWith("/invite/") ||
    pathname.startsWith("/reset/")
  );
}

function RootShell() {
  const navigate = useNavigate();
  const [singleKeyEnabled] = useSingleKeyShortcutsPreference();
  const {
    paletteOpen,
    setPaletteOpen,
    shortcutsOpen,
    setShortcutsOpen,
    createProjectOpen,
    setCreateProjectOpen,
    activeInbox,
  } = useAppCommands();

  const globalBindings = useMemo<KeyShortcutBinding[]>(
    () => [
      {
        keys: "Mod+k",
        allowInInput: true,
        allowInModal: true,
        handler: () => {
          setShortcutsOpen(false);
          setPaletteOpen(!paletteOpen);
        },
      },
      {
        keys: "?",
        handler: () => {
          setPaletteOpen(false);
          setShortcutsOpen(true);
        },
      },
      {
        keys: "g p",
        handler: () => {
          void navigate({ to: "/" });
        },
      },
      {
        keys: "g s",
        enabled: Boolean(activeInbox && activeInbox.effectiveRole !== "viewer"),
        handler: () => {
          if (!activeInbox) {
            return;
          }
          void navigate({
            to:
              activeInbox.effectiveRole === "owner"
                ? "/p/$project/settings/general"
                : "/p/$project/settings/members",
            params: { project: activeInbox.projectSlug },
          });
        },
      },
      {
        keys: "g a",
        handler: () => {
          void navigate({ to: "/account/preferences" });
        },
      },
    ],
    [activeInbox, navigate, paletteOpen, setPaletteOpen, setShortcutsOpen],
  );

  useKeyShortcuts(globalBindings, { singleKeyEnabled });

  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-background text-foreground">
      <AppHeader />
      <div className="min-h-0 flex-1 overflow-hidden">
        <Outlet />
      </div>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
      <ShortcutsHelpDialog open={shortcutsOpen} onOpenChange={setShortcutsOpen} />
      <CreateProjectDialog open={createProjectOpen} onOpenChange={setCreateProjectOpen} />
      <Toaster />
    </div>
  );
}

function AuthPublicShell() {
  return (
    <div className="flex min-h-dvh flex-col bg-background text-foreground">
      <header className="flex h-11 shrink-0 items-center justify-end px-4">
        <ThemeToggle />
      </header>
      <div className="flex-1">
        <Outlet />
      </div>
      <Toaster />
    </div>
  );
}

function AuthenticatedApp() {
  return (
    <RealtimeProvider>
      <AppCommandProvider>
        <RootShell />
      </AppCommandProvider>
    </RealtimeProvider>
  );
}

function ServerProtectedGate({ pathname, searchStr }: { pathname: string; searchStr: string }) {
  const navigate = useNavigate();
  const authQuery = useQuery(authMeQueryOptions());

  const isUnauthorized = authQuery.error instanceof ApiError && authQuery.error.status === 401;

  useEffect(() => {
    if (isUnauthorized) {
      const returnTo = `${pathname}${searchStr}`;
      void navigate({
        to: "/login",
        search: { returnTo: returnTo === "/" ? undefined : returnTo },
        replace: true,
      });
    }
  }, [isUnauthorized, navigate, pathname, searchStr]);

  if (authQuery.isPending || isUnauthorized) {
    return (
      <div className="flex h-dvh flex-col bg-background text-foreground">
        <div className="flex h-11 items-center border-b border-border bg-surface px-4">
          <Skeleton className="h-5 w-28" />
        </div>
        <div className="flex flex-1 items-center justify-center p-6">
          <Skeleton className="h-32 w-full max-w-md" />
        </div>
        <Toaster />
      </div>
    );
  }

  return <AuthenticatedApp />;
}

function RootLayout() {
  const navigate = useNavigate();
  // Use resolvedLocation so the shell (AuthPublicShell vs ServerProtectedGate)
  // only swaps once the target route's lazy chunk has resolved, avoiding
  // mounting the outgoing route inside the incoming shell during transitions.
  const pathname = useRouterState({
    select: (s) => (s.resolvedLocation ?? s.location).pathname,
  });
  const searchStr = useRouterState({
    select: (s) => (s.resolvedLocation ?? s.location).searchStr,
  });
  const metaQuery = useQuery(metaQueryOptions());
  const meta = metaQuery.data;

  const isPublicRoute = isPublicAuthPath(pathname);

  useEffect(() => {
    if (!meta) {
      return;
    }
    if (meta.mode === "local") {
      if (pathname === "/login" || pathname === "/setup") {
        void navigate({ to: "/", replace: true });
      }
      return;
    }
    if (meta.setup_required && pathname !== "/setup") {
      void navigate({ to: "/setup", replace: true });
      return;
    }
  }, [meta, navigate, pathname]);

  if (metaQuery.isPending) {
    return (
      <div className="flex h-dvh flex-col bg-background text-foreground">
        <div className="flex h-11 items-center border-b border-border bg-surface px-4">
          <Skeleton className="h-5 w-28" />
        </div>
        <Toaster />
      </div>
    );
  }

  if (meta?.mode === "server") {
    if (meta.setup_required && pathname !== "/setup") {
      return null;
    }
    if (isPublicRoute) {
      return <AuthPublicShell />;
    }
    return <ServerProtectedGate pathname={pathname} searchStr={searchStr} />;
  }

  if (isPublicRoute && (pathname.startsWith("/reset/") || pathname.startsWith("/invite/"))) {
    return <AuthPublicShell />;
  }

  return <AuthenticatedApp />;
}
