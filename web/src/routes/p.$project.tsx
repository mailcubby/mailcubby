import { createFileRoute } from "@tanstack/react-router";
import { InboxLayout } from "@/features/inbox/InboxLayout";

export type InboxFilter = "all" | "unread";

export interface ProjectInboxSearch {
  q?: string;
  tag?: string;
  credential?: number;
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
    if (
      typeof search.credential === "number" &&
      Number.isInteger(search.credential) &&
      search.credential > 0
    ) {
      out.credential = search.credential;
    } else if (typeof search.credential === "string" && /^\d+$/.test(search.credential.trim())) {
      const parsed = Number.parseInt(search.credential.trim(), 10);
      if (Number.isSafeInteger(parsed) && parsed > 0) {
        out.credential = parsed;
      }
    }
    if (search.filter === "unread" || search.filter === "all") {
      out.filter = search.filter;
    }
    return out;
  },
  component: InboxLayout,
});
