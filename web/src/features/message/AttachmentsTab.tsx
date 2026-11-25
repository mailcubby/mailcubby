import { Download, Paperclip } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { attachmentUrl, getAttachmentIcon } from "@/features/message/attachments";
import type { AttachmentSummary } from "@/lib/api";
import { formatBytes, isRasterImageMime } from "@/lib/format";

export interface AttachmentsTabProps {
  projectSlug: string;
  messageId: string;
  attachments: ReadonlyArray<AttachmentSummary>;
}

export function AttachmentsTab({ projectSlug, messageId, attachments }: AttachmentsTabProps) {
  if (attachments.length === 0) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-muted-foreground">
        <Paperclip className="size-6 opacity-50" aria-hidden="true" />
        <p className="text-sm font-medium text-foreground">No attachments</p>
        <p className="text-xs text-muted-foreground">
          This message has no attached or inline files.
        </p>
      </div>
    );
  }

  return (
    <div className="h-full overflow-auto bg-background p-4">
      <ul className="mx-auto flex max-w-3xl flex-col gap-2.5">
        {attachments.map((att) => {
          const Icon = getAttachmentIcon(att.content_type);
          const isRaster = isRasterImageMime(att.content_type);
          const baseUrl = attachmentUrl(projectSlug, messageId, att.id);
          const inlineUrl = `${baseUrl}?inline=1`;
          const filename = att.filename || `attachment-${att.id}`;

          return (
            <li
              key={att.id}
              className="flex flex-col gap-3 rounded-lg border border-border bg-surface p-3 sm:flex-row sm:items-center sm:justify-between"
            >
              <div className="flex min-w-0 items-center gap-3">
                {isRaster ? (
                  <img
                    src={inlineUrl}
                    alt={filename}
                    loading="lazy"
                    className="size-12 shrink-0 rounded-md border border-border bg-muted object-cover"
                  />
                ) : (
                  <div className="flex size-12 shrink-0 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground">
                    <Icon className="size-5" aria-hidden="true" />
                  </div>
                )}

                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px] font-medium text-foreground">{filename}</p>
                  <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                    <span className="tabular-nums">{formatBytes(att.size_bytes)}</span>
                    <span aria-hidden="true">·</span>
                    <span className="truncate font-mono text-[11px]">{att.content_type}</span>
                    <Badge variant="outline">{att.disposition}</Badge>
                    {att.referenced_by_html ? (
                      <Badge variant="secondary">shown in preview</Badge>
                    ) : null}
                    {att.content_id ? (
                      <span className="truncate font-mono text-[11px]">cid:{att.content_id}</span>
                    ) : null}
                  </div>
                </div>
              </div>

              <a
                href={baseUrl}
                download={filename}
                className="inline-flex h-8 shrink-0 items-center justify-center gap-1.5 rounded-md border border-border bg-surface px-3 text-xs font-medium text-foreground transition-colors hover:bg-muted"
              >
                <Download className="size-3.5" aria-hidden="true" />
                <span>Download</span>
              </a>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
