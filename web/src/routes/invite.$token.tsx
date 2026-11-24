import { createFileRoute } from "@tanstack/react-router";
import { InviteAcceptPage } from "@/features/auth/InviteAcceptPage";

export const Route = createFileRoute("/invite/$token")({
  component: InviteAcceptRouteComponent,
});

function InviteAcceptRouteComponent() {
  const { token } = Route.useParams();
  return <InviteAcceptPage token={token} />;
}
