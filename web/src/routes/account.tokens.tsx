import { createFileRoute } from "@tanstack/react-router";
import { ApiTokensPage } from "@/features/account/ApiTokensPage";

export interface AccountTokensSearch {
  project?: string;
}

export const Route = createFileRoute("/account/tokens")({
  validateSearch: (search: Record<string, unknown>): AccountTokensSearch => {
    const project =
      typeof search.project === "string" && search.project.trim().length > 0
        ? search.project.trim()
        : undefined;
    return project ? { project } : {};
  },
  component: AccountTokensRoute,
});

function AccountTokensRoute() {
  const { project } = Route.useSearch();
  return <ApiTokensPage initialProjectSlug={project} />;
}
