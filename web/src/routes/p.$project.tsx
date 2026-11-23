import { createFileRoute } from "@tanstack/react-router";
import { InboxLayout } from "@/features/inbox/InboxLayout";

export type InboxFilter = "all" | "unread";

export interface ProjectInboxSearch {
  filter?: InboxFilter;
}

export const Route = createFileRoute("/p/$project")({
  validateSearch: (search: Record<string, unknown>): ProjectInboxSearch => {
    const raw = search.filter;
    if (raw === "unread" || raw === "all") {
      return { filter: raw };
    }
    return {};
  },
  component: InboxLayout,
});
