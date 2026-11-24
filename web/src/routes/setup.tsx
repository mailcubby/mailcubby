import { createFileRoute } from "@tanstack/react-router";
import { SetupWizardPage } from "@/features/setup/SetupWizardPage";

export const Route = createFileRoute("/setup")({
  component: SetupWizardPage,
});
