import { createFileRoute } from "@tanstack/react-router";

export type MessageTab = "preview" | "text" | "source" | "headers" | "raw" | "attachments" | "tech";

const VALID_TABS = new Set<MessageTab>([
  "preview",
  "text",
  "source",
  "headers",
  "raw",
  "attachments",
  "tech",
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
  component: MessageViewRoute,
});

function MessageViewRoute() {
  return null;
}
