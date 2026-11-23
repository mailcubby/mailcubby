import { createFileRoute } from "@tanstack/react-router";
import { Mail } from "lucide-react";
import { useInboxContext } from "@/features/inbox/InboxContext";

export const Route = createFileRoute("/p/$project/")({
  component: ProjectInboxEmptyPane,
});

function ProjectInboxEmptyPane() {
  const { project } = useInboxContext();

  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-muted-foreground">
      <Mail className="size-8 opacity-50" aria-hidden="true" />
      <p className="text-sm font-medium text-foreground">
        {project.message_count === 0 ? "No messages yet" : "Select a message to inspect"}
      </p>
      <p className="max-w-sm text-xs text-muted-foreground">
        {project.message_count === 0
          ? `Messages sent to ${project.slug} will appear here.`
          : "Choose any message from the list to view its preview, source, headers, and attachments."}
      </p>
    </div>
  );
}
