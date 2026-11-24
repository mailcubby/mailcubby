import { createFileRoute } from "@tanstack/react-router";
import { ResetPasswordPage } from "@/features/auth/ResetPasswordPage";

export const Route = createFileRoute("/reset/$token")({
  component: ResetPasswordRouteComponent,
});

function ResetPasswordRouteComponent() {
  const { token } = Route.useParams();
  return <ResetPasswordPage token={token} />;
}
