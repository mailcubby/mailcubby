import {
  ChevronLeft,
  ChevronRight,
  Copy,
  Download,
  ExternalLink,
  Link2,
  Mail,
  MailOpen,
  Trash2,
} from "lucide-react";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/dialog";
import type { Address, MessageDetail } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatAddress, formatBytes, formatExactTimestamp, formatRelativeTime } from "@/lib/format";

export interface MessageHeaderProps {
  projectSlug: string;
  message: MessageDetail;
  hasPrevious: boolean;
  hasNext: boolean;
  onPrevious: () => void;
  onNext: () => void;
  onToggleSeen: () => void;
  onDelete: () => void;
  isUpdatingSeen: boolean;
  isDeleting: boolean;
}

function AddressList({ label, addresses }: { label: string; addresses: ReadonlyArray<Address> }) {
  const [expanded, setExpanded] = useState(false);

  if (addresses.length === 0) {
    return null;
  }

  const visible = (expanded ? addresses : addresses.slice(0, 3)).map((addr, order) => ({
    key: `${addr.address}|${addr.name}|${order}`,
    addr,
  }));
  const hiddenCount = addresses.length - visible.length;

  return (
    <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5 text-xs">
      <span className="w-10 shrink-0 text-muted-foreground">{label}</span>
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1">
        {visible.map(({ key, addr }) => {
          const text = formatAddress(addr);
          const copyValue = addr.address || addr.name;
          return (
            <button
              key={key}
              type="button"
              onClick={() => void copyToClipboard(copyValue, "address")}
              title={`Copy ${copyValue}`}
              className="max-w-full cursor-pointer truncate rounded-sm px-1 py-0.5 text-left text-foreground hover:bg-muted"
            >
              {text}
            </button>
          );
        })}
        {hiddenCount > 0 ? (
          <button
            type="button"
            onClick={() => setExpanded(true)}
            className="cursor-pointer rounded-sm px-1.5 py-0.5 text-[11px] font-medium text-accent hover:bg-muted"
          >
            +{hiddenCount}
          </button>
        ) : null}
      </div>
    </div>
  );
}

export function MessageHeader({
  projectSlug,
  message,
  hasPrevious,
  hasNext,
  onPrevious,
  onNext,
  onToggleSeen,
  onDelete,
  isUpdatingSeen,
  isDeleting,
}: MessageHeaderProps) {
  const [confirmDelete, setConfirmDelete] = useState(false);

  const subject = message.subject.trim() || "(no subject)";
  const relative = formatRelativeTime(message.received_at);
  const exact = formatExactTimestamp(message.received_at).combined;
  const emlHref = `/api/v1/projects/${encodeURIComponent(projectSlug)}/messages/${encodeURIComponent(message.id)}/eml`;

  return (
    <header className="flex shrink-0 flex-col gap-2.5 border-b border-border bg-surface px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <button
            type="button"
            onClick={() => void copyToClipboard(subject, "subject")}
            title="Click to copy subject"
            className="group inline-flex max-w-full cursor-pointer items-center gap-1.5 rounded-sm text-left hover:text-accent"
          >
            <h2 className="truncate text-[15px] font-semibold leading-snug text-foreground group-hover:text-accent">
              {subject}
            </h2>
            <Copy
              className="size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100"
              aria-hidden="true"
            />
          </button>
        </div>

        <div className="flex shrink-0 items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            aria-label="Previous message"
            title="Previous message"
            disabled={!hasPrevious}
            onClick={onPrevious}
            className="size-7"
          >
            <ChevronLeft className="size-4" aria-hidden="true" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Next message"
            title="Next message"
            disabled={!hasNext}
            onClick={onNext}
            className="size-7"
          >
            <ChevronRight className="size-4" aria-hidden="true" />
          </Button>

          <span className="mx-1 h-4 w-px bg-border" aria-hidden="true" />

          <Button
            variant="ghost"
            size="icon"
            aria-label={message.seen ? "Mark unread" : "Mark read"}
            title={message.seen ? "Mark unread" : "Mark read"}
            disabled={isUpdatingSeen}
            onClick={onToggleSeen}
            className="size-7"
          >
            {message.seen ? (
              <Mail className="size-3.5" aria-hidden="true" />
            ) : (
              <MailOpen className="size-3.5" aria-hidden="true" />
            )}
          </Button>

          <a
            href={emlHref}
            download
            aria-label="Download .eml"
            title="Download .eml"
            className="inline-flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <Download className="size-3.5" aria-hidden="true" />
          </a>

          <Button
            variant="ghost"
            size="icon"
            aria-label="Copy link to message"
            title="Copy link to message"
            onClick={() => void copyToClipboard(window.location.href, "message link")}
            className="size-7"
          >
            <Link2 className="size-3.5" aria-hidden="true" />
          </Button>

          {message.render_url ? (
            <a
              href={message.render_url}
              target="_blank"
              rel="noopener noreferrer"
              aria-label="Open preview in new window"
              title="Open preview in new window"
              className="inline-flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            >
              <ExternalLink className="size-3.5" aria-hidden="true" />
            </a>
          ) : null}

          <Button
            variant="ghost"
            size="icon"
            aria-label="Delete message"
            title="Delete message"
            disabled={isDeleting}
            onClick={() => setConfirmDelete(true)}
            className="size-7 text-muted-foreground hover:text-destructive"
          >
            <Trash2 className="size-3.5" aria-hidden="true" />
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <div className="flex flex-wrap items-baseline gap-x-1.5 text-xs">
            <span className="w-10 shrink-0 text-muted-foreground">From</span>
            <button
              type="button"
              onClick={() =>
                void copyToClipboard(message.from.address || message.from.name, "sender address")
              }
              title={`Copy ${message.from.address || message.from.name}`}
              className="max-w-full cursor-pointer truncate rounded-sm px-1 py-0.5 text-left font-medium text-foreground hover:bg-muted"
            >
              {formatAddress(message.from)}
            </button>
          </div>
          <AddressList label="To" addresses={message.to} />
          <AddressList label="Cc" addresses={message.cc} />
          <AddressList label="Bcc" addresses={message.bcc} />
        </div>

        <div className="flex shrink-0 flex-col items-end gap-1 text-xs text-muted-foreground tabular-nums">
          <span title={exact}>{relative}</span>
          <span>{formatBytes(message.size_bytes)}</span>
        </div>
      </div>

      {message.credential_label || message.tags.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1.5">
          {message.credential_label ? (
            <Badge variant="outline">via {message.credential_label}</Badge>
          ) : null}
          {message.tags.map((tag) => (
            <Badge key={tag} variant="secondary">
              {tag}
            </Badge>
          ))}
        </div>
      ) : null}

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="Delete message?"
        description={`Permanently delete "${subject}"?`}
        confirmLabel="Delete"
        destructive
        pending={isDeleting}
        onConfirm={() => {
          setConfirmDelete(false);
          onDelete();
        }}
      />
    </header>
  );
}
