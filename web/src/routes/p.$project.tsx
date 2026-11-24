import { createFileRoute } from "@tanstack/react-router";
import { InboxLayout } from "@/features/inbox/InboxLayout";

export type InboxFilter = "all" | "unread";

export interface ProjectInboxSearch {
  q?: string;
  tag?: string;
  filter?: InboxFilter;
}

export const Route = createFileRoute("/p/$project")({
  validateSearch: (search: Record<string, unknown>): ProjectInboxSearch => {
    const out: ProjectInboxSearch = {};
    if (typeof search.q === "string" && search.q.trim() !== "") {
      out.q = search.q;
    }
    if (typeof search.tag === "string" && search.tag.trim() !== "") {
      out.tag = search.tag.trim();
    }
    if (search.filter === "unread" || search.filter === "all") {
      out.filter = search.filter;
    }
    return out;
  },
  component: InboxLayout,
});
