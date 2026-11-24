import { createFileRoute } from "@tanstack/react-router";
import { LoginPage } from "@/features/auth/LoginPage";

export const Route = createFileRoute("/login")({
  validateSearch: (search: Record<string, unknown>): { returnTo: string | undefined } => ({
    returnTo: typeof search.returnTo === "string" ? search.returnTo : undefined,
  }),
  component: LoginRouteComponent,
});

function LoginRouteComponent() {
  const { returnTo } = Route.useSearch();
  return <LoginPage returnTo={returnTo} />;
}
