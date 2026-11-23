import { createFileRoute, Outlet } from "@tanstack/react-router";

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
  component: ProjectInboxRoute,
});

function ProjectInboxRoute() {
  return <Outlet />;
}
