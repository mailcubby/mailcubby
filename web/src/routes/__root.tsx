import { createRootRoute, Outlet } from "@tanstack/react-router";
import { AppHeader } from "@/components/AppHeader";
import { Toaster } from "@/components/ui/toaster";

export const Route = createRootRoute({
  component: RootLayout,
});

function RootLayout() {
  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-background text-foreground">
      <AppHeader />
      <div className="min-h-0 flex-1 overflow-hidden">
        <Outlet />
      </div>
      <Toaster />
    </div>
  );
}
