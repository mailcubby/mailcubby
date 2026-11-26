import { Download, Paperclip } from "lucide-react";
import {
  attachmentUrl,
  getAttachmentIcon,
  getAttachmentKind,
} from "@/features/message/attachments";
import type { AttachmentSummary } from "@/lib/api";
import { formatBytes, formatCount, isRasterImageMime } from "@/lib/format";

export interface AttachmentStripProps {
  projectSlug: string;
  messageId: string;
  attachments: ReadonlyArray<AttachmentSummary>;
}

/**
 * AttachmentStrip shows one compact download card per attachment below the
 * Preview and Text views (FR-072, docs/UI.md 3.5). Parts the HTML preview
 * already shows through cid: (`referenced_by_html`) stay in the Attachments
 * tab only. Thumbnails use `?inline=1`, which the server honours only for
 * allowlisted raster images (FR-074); every other type gets an icon.
 */
export function AttachmentStrip({ projectSlug, messageId, attachments }: AttachmentStripProps) {
  const items = attachments.filter((att) => !att.referenced_by_html);
  if (items.length === 0) {
    return null;
  }

  return (
    <section
      aria-label="Attachment downloads"
      data-testid="attachment-strip"
      className="shrink-0 border-t border-border bg-surface px-4 pt-2.5 pb-3"
    >
      <div className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <Paperclip className="size-3.5" aria-hidden="true" />
        <span>
          {formatCount(items.length)} {items.length === 1 ? "attachment" : "attachments"}
        </span>
      </div>
      <ul className="-m-1 flex max-h-36 flex-wrap gap-2 overflow-y-auto p-1">
        {items.map((att) => {
          const Icon = getAttachmentIcon(att.content_type);
          const url = attachmentUrl(projectSlug, messageId, att.id);
          const filename = att.filename || `attachment-${att.id}`;
          const size = formatBytes(att.size_bytes);
          return (
            <li key={att.id}>
              <a
                href={url}
                download={filename}
                title={filename}
                data-kind={getAttachmentKind(att.content_type)}
                className="group flex w-56 items-center gap-2.5 rounded-md border border-border bg-surface p-2 text-left transition-colors hover:border-input hover:bg-muted"
              >
                {isRasterImageMime(att.content_type) ? (
                  <img
                    src={`${url}?inline=1`}
                    alt=""
                    loading="lazy"
                    className="size-9 shrink-0 rounded-sm border border-border bg-muted object-cover"
                  />
                ) : (
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-sm bg-secondary text-muted-foreground">
                    <Icon className="size-4" aria-hidden="true" />
                  </span>
                )}
                {/* Accessible name "Download <filename> <size>" contains the visible text (WCAG 2.5.3). */}
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px] font-medium text-foreground">
                    <span className="sr-only">Download</span> {filename}
                  </span>{" "}
                  <span className="block text-[11px] tabular-nums text-muted-foreground">
                    {size}
                  </span>
                </span>
                <Download
                  className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground"
                  aria-hidden="true"
                />
              </a>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
