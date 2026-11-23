import { createRootRoute, Outlet, useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";
import { AppCommandProvider, useAppCommands } from "@/components/AppCommandContext";
import { AppHeader } from "@/components/AppHeader";
import { CommandPalette } from "@/components/CommandPalette";
import { RealtimeProvider } from "@/components/RealtimeProvider";
import { ShortcutsHelpDialog } from "@/components/ShortcutsHelpDialog";
import { Toaster } from "@/components/ui/toaster";
import { CreateProjectDialog } from "@/features/projects/CreateProjectDialog";
import { useSingleKeyShortcutsPreference } from "@/lib/preferences";
import { type KeyShortcutBinding, useKeyShortcuts } from "@/lib/shortcuts";

export const Route = createRootRoute({
  component: RootLayout,
});

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
        keys: "g a",
        handler: () => {
          void navigate({ to: "/account/preferences" });
        },
      },
    ],
    [navigate, paletteOpen, setPaletteOpen, setShortcutsOpen],
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

function RootLayout() {
  return (
    <RealtimeProvider>
      <AppCommandProvider>
        <RootShell />
      </AppCommandProvider>
    </RealtimeProvider>
  );
}
