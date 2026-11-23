import { Toaster as SonnerToaster } from "sonner";
import { useTheme } from "@/lib/theme";

export function Toaster() {
  const { resolvedTheme } = useTheme();
  return (
    <SonnerToaster
      theme={resolvedTheme}
      position="bottom-right"
      closeButton
      toastOptions={{
        className:
          "border border-border bg-surface text-foreground text-[13px] shadow-md rounded-md",
      }}
    />
  );
}
