import { Paperclip } from "lucide-react";
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
      aria-label="Attachments"
      data-testid="attachment-strip"
      className="shrink-0 border-t border-border bg-surface px-3 py-2"
    >
      <div className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <Paperclip className="size-3.5" aria-hidden="true" />
        <span>
          {formatCount(items.length)} {items.length === 1 ? "attachment" : "attachments"}
        </span>
      </div>
      <ul className="flex max-h-32 flex-wrap gap-2 overflow-y-auto">
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
                aria-label={`Download ${filename}, ${size}`}
                title={filename}
                data-kind={getAttachmentKind(att.content_type)}
                className="flex w-56 items-center gap-2 rounded-md border border-border bg-background p-1.5 text-left transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              >
                {isRasterImageMime(att.content_type) ? (
                  <img
                    src={`${url}?inline=1`}
                    alt=""
                    loading="lazy"
                    className="size-9 shrink-0 rounded border border-border bg-muted object-cover"
                  />
                ) : (
                  <span className="flex size-9 shrink-0 items-center justify-center rounded border border-border bg-muted text-muted-foreground">
                    <Icon className="size-4" aria-hidden="true" />
                  </span>
                )}
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs font-medium text-foreground">
                    {filename}
                  </span>
                  <span className="block text-[11px] tabular-nums text-muted-foreground">
                    {size}
                  </span>
                </span>
              </a>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
