import { createContext, type RefObject, useContext } from "react";
import type { MessageSummary, ProjectDetail } from "@/lib/api";
import type { InboxFilter } from "@/routes/p.$project";

export interface InboxContextValue {
  project: ProjectDetail;
  messages: ReadonlyArray<MessageSummary>;
  allLoadedCount: number;
  filter: InboxFilter;
  activeMessageId: string | undefined;
  isLoadingMessages: boolean;
  readingPaneRef: RefObject<HTMLDivElement | null>;
  invalidateInbox: () => Promise<void>;
  deleteMessageWithUndo: (message: MessageSummary) => void;
  toggleMessageSeen: (message: MessageSummary, nextSeen?: boolean) => void;
  isUpdatingSeen: boolean;
}

const InboxContext = createContext<InboxContextValue | null>(null);

export const InboxProvider = InboxContext.Provider;

export function useInboxContext(): InboxContextValue {
  const ctx = useContext(InboxContext);
  if (!ctx) {
    throw new Error("useInboxContext must be used inside InboxProvider");
  }
  return ctx;
}
