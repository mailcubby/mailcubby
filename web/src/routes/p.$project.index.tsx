import { createFileRoute } from "@tanstack/react-router";
import { Mail } from "lucide-react";
import { EmptyInboxGuide } from "@/features/inbox/EmptyInboxGuide";
import { useInboxContext } from "@/features/inbox/InboxContext";

export const Route = createFileRoute("/p/$project/")({
  component: ProjectInboxEmptyPane,
});

function ProjectInboxEmptyPane() {
  const { project, allLoadedCount, isLoadingMessages, invalidateInbox } = useInboxContext();

  if (!isLoadingMessages && project.message_count === 0 && allLoadedCount === 0) {
    return <EmptyInboxGuide project={project} onSentTest={invalidateInbox} />;
  }

  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-muted-foreground">
      <Mail className="size-8 opacity-50" aria-hidden="true" />
      <p className="text-sm font-medium text-foreground">Select a message to inspect</p>
      <p className="max-w-sm text-xs text-muted-foreground">
        Choose any message from the list to view its preview, plain text, HTML source, headers, raw
        source, attachments, and envelope details.
      </p>
    </div>
  );
}
