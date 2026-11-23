import { createContext, useContext } from "react";
import type { MessageSummary, ProjectDetail } from "@/lib/api";
import type { InboxFilter } from "@/routes/p.$project";

export interface InboxContextValue {
  project: ProjectDetail;
  messages: ReadonlyArray<MessageSummary>;
  allLoadedCount: number;
  filter: InboxFilter;
  activeMessageId: string | undefined;
  isLoadingMessages: boolean;
  invalidateInbox: () => Promise<void>;
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
