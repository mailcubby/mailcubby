import { createFileRoute } from "@tanstack/react-router";
import { PreferencesPage } from "@/features/account/PreferencesPage";

export const Route = createFileRoute("/account/preferences")({
  component: PreferencesPage,
});
