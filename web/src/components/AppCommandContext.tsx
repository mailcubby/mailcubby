import { createContext, type ReactNode, useCallback, useContext, useMemo, useState } from "react";
import type { MessageSummary, TagSummary } from "@/lib/api";

export interface ActiveInboxActions {
  projectSlug: string;
  projectName: string;
  messageCount: number;
  messages: ReadonlyArray<MessageSummary>;
  tags: ReadonlyArray<TagSummary>;
  toggleSidebar: () => void;
  openDeleteAll: () => void;
  sendTestEmail: () => void;
  applySearchOperator: (operator: string, complete: boolean) => void;
  selectTagFilter: (tagName: string) => void;
}

export interface AppCommandContextValue {
  paletteOpen: boolean;
  setPaletteOpen: (open: boolean) => void;
  openPalette: () => void;
  shortcutsOpen: boolean;
  setShortcutsOpen: (open: boolean) => void;
  openShortcuts: () => void;
  createProjectOpen: boolean;
  setCreateProjectOpen: (open: boolean) => void;
  openCreateProject: () => void;
  activeInbox: ActiveInboxActions | null;
  setActiveInbox: (actions: ActiveInboxActions | null) => void;
}

const AppCommandContext = createContext<AppCommandContextValue | null>(null);

const NOOP_CONTEXT: AppCommandContextValue = {
  paletteOpen: false,
  setPaletteOpen: () => {},
  openPalette: () => {},
  shortcutsOpen: false,
  setShortcutsOpen: () => {},
  openShortcuts: () => {},
  createProjectOpen: false,
  setCreateProjectOpen: () => {},
  openCreateProject: () => {},
  activeInbox: null,
  setActiveInbox: () => {},
};

export function useAppCommands(): AppCommandContextValue {
  return useContext(AppCommandContext) ?? NOOP_CONTEXT;
}

export function AppCommandProvider({ children }: { children: ReactNode }) {
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [createProjectOpen, setCreateProjectOpen] = useState(false);
  const [activeInbox, setActiveInbox] = useState<ActiveInboxActions | null>(null);

  const openPalette = useCallback(() => {
    setShortcutsOpen(false);
    setPaletteOpen(true);
  }, []);

  const openShortcuts = useCallback(() => {
    setPaletteOpen(false);
    setShortcutsOpen(true);
  }, []);

  const openCreateProject = useCallback(() => {
    setPaletteOpen(false);
    setCreateProjectOpen(true);
  }, []);

  const value = useMemo<AppCommandContextValue>(
    () => ({
      paletteOpen,
      setPaletteOpen,
      openPalette,
      shortcutsOpen,
      setShortcutsOpen,
      openShortcuts,
      createProjectOpen,
      setCreateProjectOpen,
      openCreateProject,
      activeInbox,
      setActiveInbox,
    }),
    [
      paletteOpen,
      openPalette,
      shortcutsOpen,
      openShortcuts,
      createProjectOpen,
      openCreateProject,
      activeInbox,
    ],
  );

  return <AppCommandContext.Provider value={value}>{children}</AppCommandContext.Provider>;
}
