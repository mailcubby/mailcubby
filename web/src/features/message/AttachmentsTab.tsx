import { Download, Eye, Paperclip } from "lucide-react";
import { EmptyState } from "@/components/EmptyState";
import { Badge } from "@/components/ui/badge";
import { Button, buttonClassName } from "@/components/ui/button";
import { attachmentUrl, getAttachmentIcon, isPdfMime } from "@/features/message/attachments";
import type { AttachmentSummary } from "@/lib/api";
import { formatBytes, isRasterImageMime } from "@/lib/format";

export interface AttachmentsTabProps {
  projectSlug: string;
  messageId: string;
  attachments: ReadonlyArray<AttachmentSummary>;
  inlinePdfEnabled?: boolean;
  onPreviewPdf?: (attachment: AttachmentSummary) => void;
}

export function AttachmentsTab({
  projectSlug,
  messageId,
  attachments,
  inlinePdfEnabled = false,
  onPreviewPdf,
}: AttachmentsTabProps) {
  if (attachments.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <EmptyState
          icon={Paperclip}
          headingLevel={3}
          title="No attachments"
          description="This message has no attached or inline files."
        />
      </div>
    );
  }

  return (
    <div className="h-full overflow-auto bg-background p-4">
      <ul className="mx-auto flex max-w-3xl flex-col gap-2.5">
        {attachments.map((att) => {
          const Icon = getAttachmentIcon(att.content_type);
          const isRaster = isRasterImageMime(att.content_type);
          const canPreviewPdf = inlinePdfEnabled && isPdfMime(att.content_type) && onPreviewPdf;
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
                      <Badge variant="neutral">shown in preview</Badge>
                    ) : null}
                    {att.content_id ? (
                      <span className="truncate font-mono text-[11px]">cid:{att.content_id}</span>
                    ) : null}
                  </div>
                </div>
              </div>

              <div className="flex shrink-0 items-center gap-2">
                {canPreviewPdf ? (
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label={`Preview ${filename}`}
                    onClick={() => onPreviewPdf(att)}
                    className="gap-1.5"
                  >
                    <Eye className="size-3.5" aria-hidden="true" />
                    <span>Preview</span>
                  </Button>
                ) : null}
                <a
                  href={baseUrl}
                  download={filename}
                  aria-label={`Download ${filename}`}
                  className={buttonClassName({
                    variant: "outline",
                    size: "sm",
                    className: "gap-1.5",
                  })}
                >
                  <Download className="size-3.5" aria-hidden="true" />
                  <span>Download</span>
                </a>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
