import { createFileRoute } from "@tanstack/react-router";
import { Mail } from "lucide-react";
import { EmptyInboxGuide } from "@/features/inbox/EmptyInboxGuide";
import { useInboxContext } from "@/features/inbox/InboxContext";

export const Route = createFileRoute("/p/$project/")({
  component: ProjectInboxEmptyPane,
});

function ProjectInboxEmptyPane() {
  const { project, effectiveRole, allLoadedCount, isLoadingMessages, invalidateInbox } =
    useInboxContext();

  if (!isLoadingMessages && project.message_count === 0 && allLoadedCount === 0) {
    return (
      <EmptyInboxGuide
        project={project}
        effectiveRole={effectiveRole}
        onSentTest={invalidateInbox}
      />
    );
  }

  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-muted-foreground">
      <Mail className="size-8 opacity-50" aria-hidden="true" />
      <p className="text-sm font-medium text-foreground">Select a message to inspect</p>
      <p className="max-w-sm text-xs text-muted-foreground">
        Choose any message from the list or navigate with keyboard shortcuts.
      </p>
      <div className="mt-1 flex flex-wrap items-center justify-center gap-x-4 gap-y-1.5 text-xs text-muted-foreground">
        <span className="inline-flex items-center gap-1">
          <kbd className="rounded-sm border border-border bg-surface px-1.5 py-0.5 font-mono text-[11px] text-foreground">
            j
          </kbd>
          <kbd className="rounded-sm border border-border bg-surface px-1.5 py-0.5 font-mono text-[11px] text-foreground">
            k
          </kbd>
          <span>next / prev</span>
        </span>
        <span className="inline-flex items-center gap-1">
          <kbd className="rounded-sm border border-border bg-surface px-1.5 py-0.5 font-mono text-[11px] text-foreground">
            ⌘K
          </kbd>
          <span>command palette</span>
        </span>
        <span className="inline-flex items-center gap-1">
          <kbd className="rounded-sm border border-border bg-surface px-1.5 py-0.5 font-mono text-[11px] text-foreground">
            ?
          </kbd>
          <span>shortcuts</span>
        </span>
      </div>
    </div>
  );
}
