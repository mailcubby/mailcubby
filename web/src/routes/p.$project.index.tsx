import { createFileRoute } from "@tanstack/react-router";
import { Mail, SearchX } from "lucide-react";
import { EmptyState } from "@/components/EmptyState";
import { EmptyInboxGuide } from "@/features/inbox/EmptyInboxGuide";
import { useInboxContext } from "@/features/inbox/InboxContext";

export const Route = createFileRoute("/p/$project/")({
  component: ProjectInboxEmptyPane,
});

const KBD_CLASS =
  "rounded-sm border border-border bg-surface px-1.5 py-0.5 font-mono text-[11px] text-foreground";

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

  // A filtered list with no results: nothing to choose from, so no "choose" hint.
  if (!isLoadingMessages && allLoadedCount === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <EmptyState
          icon={SearchX}
          title="Nothing to show"
          description="Messages that match the current filters open here."
        />
      </div>
    );
  }

  // Audit V2: viewers get plain language; keyboard hints are for owners and members.
  if (effectiveRole === "viewer") {
    return (
      <div className="flex h-full items-center justify-center">
        <EmptyState
          icon={Mail}
          title="No email selected"
          description="Choose an email from the list to read it."
        />
      </div>
    );
  }

  return (
    <div className="flex h-full items-center justify-center">
      <EmptyState
        icon={Mail}
        title="Select a message to inspect"
        description="Choose any message from the list or navigate with keyboard shortcuts."
      >
        <div className="mt-1 flex flex-wrap items-center justify-center gap-x-4 gap-y-1.5 text-xs text-muted-foreground">
          <span className="inline-flex items-center gap-1">
            <kbd className={KBD_CLASS}>j</kbd>
            <kbd className={KBD_CLASS}>k</kbd>
            <span>next / prev</span>
          </span>
          <span className="inline-flex items-center gap-1">
            <kbd className={KBD_CLASS}>⌘K</kbd>
            <span>command palette</span>
          </span>
          <span className="inline-flex items-center gap-1">
            <kbd className={KBD_CLASS}>?</kbd>
            <span>shortcuts</span>
          </span>
        </div>
      </EmptyState>
    </div>
  );
}
