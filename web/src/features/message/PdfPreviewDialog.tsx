import { Download, ExternalLink } from "lucide-react";
import { buttonClassName } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { attachmentPdfRenderUrl, attachmentUrl } from "@/features/message/attachments";
import type { AttachmentSummary } from "@/lib/api";
import { formatBytes } from "@/lib/format";

export interface PdfPreviewDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  projectSlug: string;
  messageId: string;
  attachment: AttachmentSummary | null;
}

/**
 * PdfPreviewDialog renders a validated PDF attachment inside the browser's
 * native PDF viewer via the dedicated `/render/{project}/{id}/attachments/{attachment_id}`
 * endpoint (ADR 0009, FR-074). The iframe intentionally omits `sandbox` because
 * built-in browser PDF viewers refuse to render inside sandboxed frames;
 * isolation is enforced by the server's PDF-specific CSP, `nosniff`, and byte-0
 * `%PDF-` validation.
 */
export function PdfPreviewDialog({
  open,
  onOpenChange,
  projectSlug,
  messageId,
  attachment,
}: PdfPreviewDialogProps) {
  const filename = attachment
    ? attachment.filename || `attachment-${attachment.id}`
    : "PDF preview";
  const renderUrl = attachment ? attachmentPdfRenderUrl(projectSlug, messageId, attachment.id) : "";
  const downloadUrl = attachment ? attachmentUrl(projectSlug, messageId, attachment.id) : "";
  const description = attachment
    ? `${formatBytes(attachment.size_bytes)} · PDF preview`
    : undefined;

  return (
    <Dialog
      open={open && attachment !== null}
      onOpenChange={onOpenChange}
      title={filename}
      titleClassName="truncate"
      {...(description !== undefined ? { description } : {})}
      className="h-[min(90vh,56rem)] w-[min(94vw,68rem)]"
      containerClassName="h-full min-h-0"
      bodyClassName="flex min-h-0 flex-1 flex-col p-0"
      headerActions={
        attachment ? (
          <div className="flex items-center gap-1.5">
            <a
              href={renderUrl}
              target="_blank"
              rel="noopener noreferrer"
              className={buttonClassName({ variant: "outline", size: "xs" })}
            >
              <ExternalLink className="size-3.5" aria-hidden="true" />
              <span>Open in new tab</span>
            </a>
            <a
              href={downloadUrl}
              download={filename}
              className={buttonClassName({ variant: "primary", size: "xs" })}
            >
              <Download className="size-3.5" aria-hidden="true" />
              <span>Download</span>
            </a>
          </div>
        ) : null
      }
    >
      {attachment ? (
        <>
          <iframe
            src={renderUrl}
            title={`PDF preview: ${filename}`}
            referrerPolicy="no-referrer"
            className="min-h-0 w-full flex-1 border-0 bg-muted"
          />
          <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border bg-surface px-5 py-2.5 text-xs text-muted-foreground">
            <span>
              If your browser cannot display PDFs inline, open it in a new tab or download it.
            </span>
            <div className="flex shrink-0 items-center gap-3">
              <a
                href={renderUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="font-medium text-foreground underline underline-offset-2 transition-colors hover:text-accent"
              >
                Open in new tab
              </a>
              <a
                href={downloadUrl}
                download={filename}
                className="font-medium text-foreground underline underline-offset-2 transition-colors hover:text-accent"
              >
                Download
              </a>
            </div>
          </div>
        </>
      ) : null}
    </Dialog>
  );
}
