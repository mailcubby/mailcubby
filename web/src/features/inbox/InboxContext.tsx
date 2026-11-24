import { createContext, type RefObject, useContext } from "react";
import type { MessageSummary, ProjectDetail, TagSummary } from "@/lib/api";
import type { InboxFilter } from "@/routes/p.$project";

export interface InboxContextValue {
  project: ProjectDetail;
  tags: ReadonlyArray<TagSummary>;
  messages: ReadonlyArray<MessageSummary>;
  allLoadedCount: number;
  filter: InboxFilter;
  searchQuery: string;
  activeTag: string | undefined;
  activeMessageId: string | undefined;
  isLoadingMessages: boolean;
  readingPaneRef: RefObject<HTMLDivElement | null>;
  invalidateInbox: () => Promise<void>;
  deleteMessageWithUndo: (message: MessageSummary) => void;
  toggleMessageSeen: (message: MessageSummary, nextSeen?: boolean) => void;
  updateMessageTags: (message: MessageSummary, nextTags: string[]) => void;
  isUpdatingSeen: boolean;
  isUpdatingTags: boolean;
  tagEditorOpen: boolean;
  setTagEditorOpen: (open: boolean) => void;
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
