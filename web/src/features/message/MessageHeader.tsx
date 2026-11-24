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
  X,
} from "lucide-react";
import { useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TagPickerPopover } from "@/features/inbox/TagPickerPopover";
import type { Address, MessageDetail, ProjectRole, TagSummary } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import {
  formatAddress,
  formatBytes,
  formatExactTimestamp,
  formatRelativeTime,
  PROJECT_COLOR_DOT_CLASS,
} from "@/lib/format";
import { useClock24hPreference } from "@/lib/preferences";
import { cn } from "@/lib/utils";

export interface MessageHeaderProps {
  projectSlug: string;
  projectRole: ProjectRole;
  projectTags: ReadonlyArray<TagSummary>;
  message: MessageDetail;
  hasPrevious: boolean;
  hasNext: boolean;
  onPrevious: () => void;
  onNext: () => void;
  onToggleSeen: () => void;
  onUpdateTags: (nextTags: string[]) => void;
  onDelete: () => void;
  isUpdatingSeen: boolean;
  isUpdatingTags: boolean;
  tagEditorOpen: boolean;
  onTagEditorOpenChange: (open: boolean) => void;
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
  projectRole,
  projectTags,
  message,
  hasPrevious,
  hasNext,
  onPrevious,
  onNext,
  onToggleSeen,
  onUpdateTags,
  onDelete,
  isUpdatingSeen,
  isUpdatingTags,
  tagEditorOpen,
  onTagEditorOpenChange,
}: MessageHeaderProps) {
  const [clock24h] = useClock24hPreference();

  const canEditTags = projectRole !== "viewer";
  const tagColorByLower = useMemo(() => {
    const map = new Map<string, string>();
    for (const tag of projectTags) {
      map.set(
        tag.name.toLowerCase(),
        PROJECT_COLOR_DOT_CLASS[tag.color] ?? PROJECT_COLOR_DOT_CLASS.slate,
      );
    }
    return map;
  }, [projectTags]);

  const handleToggleTag = (tagName: string) => {
    const targetLower = tagName.toLowerCase();
    const exists = message.tags.some((t) => t.toLowerCase() === targetLower);
    const next = exists
      ? message.tags.filter((t) => t.toLowerCase() !== targetLower)
      : [...message.tags, tagName];
    onUpdateTags(next);
  };

  const subject = message.subject.trim() || "(no subject)";
  const relative = formatRelativeTime(message.received_at, new Date(), clock24h);
  const exact = formatExactTimestamp(message.received_at, clock24h).combined;
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
            title="Previous message (k)"
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
            title="Next message (j)"
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
            title={message.seen ? "Mark unread (u)" : "Mark read (u)"}
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
            title="Delete message (e)"
            onClick={onDelete}
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

      {message.credential_label || message.tags.length > 0 || canEditTags ? (
        <div className="flex flex-wrap items-center gap-1.5">
          {message.credential_label ? (
            <Badge variant="outline">via {message.credential_label}</Badge>
          ) : null}
          {message.tags.map((tag) => {
            const dot = tagColorByLower.get(tag.toLowerCase()) ?? PROJECT_COLOR_DOT_CLASS.slate;
            return (
              <Badge key={tag} variant="secondary" className="gap-1.5 pr-1">
                <span className={cn("size-1.5 shrink-0 rounded-full", dot)} aria-hidden="true" />
                <span>{tag}</span>
                {canEditTags ? (
                  <button
                    type="button"
                    aria-label={`Remove tag ${tag}`}
                    disabled={isUpdatingTags}
                    onClick={() => handleToggleTag(tag)}
                    className="inline-flex size-3.5 cursor-pointer items-center justify-center rounded-xs text-muted-foreground hover:text-foreground"
                  >
                    <X className="size-2.5" aria-hidden="true" />
                  </button>
                ) : null}
              </Badge>
            );
          })}
          {canEditTags ? (
            <TagPickerPopover
              mode="message"
              open={tagEditorOpen}
              onOpenChange={onTagEditorOpenChange}
              projectTags={projectTags}
              selectedTags={message.tags}
              disabled={isUpdatingTags}
              onToggleTag={handleToggleTag}
              triggerLabel={message.tags.length > 0 ? "Edit tags" : "Add tag"}
              triggerTitle="Edit tags (t)"
            />
          ) : null}
        </div>
      ) : null}
    </header>
  );
}
