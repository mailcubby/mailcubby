import {
  Group,
  type GroupProps,
  Panel,
  type PanelProps,
  Separator,
  type SeparatorProps,
} from "react-resizable-panels";
import { cn } from "@/lib/utils";

export function ResizablePanelGroup({ className, ...props }: GroupProps) {
  return <Group className={cn("flex h-full w-full overflow-hidden", className)} {...props} />;
}

export function ResizablePanel({ className, ...props }: PanelProps) {
  return <Panel className={cn("min-w-0 overflow-hidden", className)} {...props} />;
}

export function ResizableHandle({ className, ...props }: SeparatorProps) {
  return (
    <Separator
      className={cn(
        "relative flex w-px shrink-0 items-center justify-center bg-border transition-colors after:absolute after:inset-y-0 after:-left-1 after:-right-1 after:content-[''] hover:bg-accent focus-visible:bg-accent data-[separator=active]:bg-accent",
        className,
      )}
      {...props}
    />
  );
}
