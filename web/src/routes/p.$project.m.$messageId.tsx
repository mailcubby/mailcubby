import { createFileRoute } from "@tanstack/react-router";
import { MessageView } from "@/features/message/MessageView";

export type MessageTab =
  | "preview"
  | "text"
  | "source"
  | "headers"
  | "raw"
  | "attachments"
  | "tech"
  | "links"
  | "check";

const VALID_TABS = new Set<MessageTab>([
  "preview",
  "text",
  "source",
  "headers",
  "raw",
  "attachments",
  "tech",
  "links",
  "check",
]);

export function isMessageTab(value: unknown): value is MessageTab {
  return typeof value === "string" && VALID_TABS.has(value as MessageTab);
}

export interface MessageViewSearch {
  tab?: MessageTab;
}

export const Route = createFileRoute("/p/$project/m/$messageId")({
  validateSearch: (search: Record<string, unknown>): MessageViewSearch => {
    if (isMessageTab(search.tab)) {
      return { tab: search.tab };
    }
    return {};
  },
  component: MessageView,
});
