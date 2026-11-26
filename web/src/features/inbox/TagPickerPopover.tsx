import { Check, Plus, Tag } from "lucide-react";
import { type KeyboardEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { isValidTagName } from "@/features/inbox/searchQuery";
import type { TagSummary } from "@/lib/api";
import { PROJECT_COLOR_DOT_CLASS } from "@/lib/format";
import { cn } from "@/lib/utils";

export interface TagPickerPopoverProps {
  mode: "message" | "bulk";
  open: boolean;
  onOpenChange: (open: boolean) => void;
  projectTags: ReadonlyArray<TagSummary>;
  selectedTags?: ReadonlyArray<string>;
  disabled?: boolean;
  onToggleTag?: (tagName: string) => void;
  onBulkApply?: (action: "add_tags" | "remove_tags", tagName: string) => void;
  align?: "left" | "right";
  triggerLabel?: string;
  triggerClassName?: string;
  triggerTitle?: string;
}

export function TagPickerPopover({
  mode,
  open,
  onOpenChange,
  projectTags,
  selectedTags = [],
  disabled = false,
  onToggleTag,
  onBulkApply,
  align = "left",
  triggerLabel = "Add tag",
  triggerClassName,
  triggerTitle = "Edit tags (t)",
}: TagPickerPopoverProps) {
  const [filter, setFilter] = useState("");
  const containerRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const panelId = useId();
  const inputId = useId();

  useEffect(() => {
    if (!open) {
      setFilter("");
      return;
    }
    requestAnimationFrame(() => {
      inputRef.current?.focus();
    });
    const handlePointerDown = (e: PointerEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        onOpenChange(false);
      }
    };
    const handleKeyDown = (e: globalThis.KeyboardEvent) => {
      if (e.key === "Escape") {
        onOpenChange(false);
      }
    };
    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open, onOpenChange]);

  const selectedLower = useMemo(
    () => new Set(selectedTags.map((t) => t.toLowerCase())),
    [selectedTags],
  );

  const trimmed = filter.trim();
  const lowerFilter = trimmed.toLowerCase();

  const matchingTags = useMemo(() => {
    if (!lowerFilter) {
      return projectTags;
    }
    return projectTags.filter((t) => t.name.toLowerCase().includes(lowerFilter));
  }, [projectTags, lowerFilter]);

  const exactExists = useMemo(
    () => projectTags.some((t) => t.name.toLowerCase() === lowerFilter),
    [projectTags, lowerFilter],
  );

  const canCreate = trimmed.length > 0 && !exactExists && isValidTagName(trimmed);
  const invalidTyped = trimmed.length > 0 && !exactExists && !isValidTagName(trimmed);

  const handleInputKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key !== "Enter") {
      return;
    }
    e.preventDefault();
    if (mode === "message" && onToggleTag) {
      const exactMatch = projectTags.find((t) => t.name.toLowerCase() === lowerFilter);
      if (exactMatch) {
        onToggleTag(exactMatch.name);
        setFilter("");
        return;
      }
      if (canCreate) {
        onToggleTag(trimmed);
        setFilter("");
        return;
      }
      if (matchingTags.length === 1 && matchingTags[0]) {
        onToggleTag(matchingTags[0].name);
        setFilter("");
      }
    } else if (mode === "bulk" && onBulkApply) {
      const target =
        projectTags.find((t) => t.name.toLowerCase() === lowerFilter)?.name ??
        (canCreate ? trimmed : matchingTags[0]?.name);
      if (target) {
        onBulkApply("add_tags", target);
        setFilter("");
        onOpenChange(false);
      }
    }
  };

  return (
    <div ref={containerRef} className="relative inline-flex">
      <Button
        variant="outline"
        size="xs"
        disabled={disabled}
        aria-expanded={open}
        aria-controls={panelId}
        title={triggerTitle}
        onClick={() => onOpenChange(!open)}
        className={triggerClassName}
      >
        <Tag className="size-3" aria-hidden="true" />
        <span>{triggerLabel}</span>
      </Button>

      {open ? (
        <div
          id={panelId}
          className={cn(
            "absolute top-full z-30 mt-1 w-60 rounded-md border border-border bg-surface p-2 shadow-lg",
            align === "right" ? "right-0" : "left-0",
          )}
        >
          <label htmlFor={inputId} className="sr-only">
            Tag name
          </label>
          <Input
            id={inputId}
            ref={inputRef}
            placeholder="Find or create tag..."
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            onKeyDown={handleInputKeyDown}
            className="h-7 text-xs"
          />

          {invalidTyped ? (
            <p className="mt-1.5 px-1 text-[11px] text-destructive">
              Use 1–40 letters, numbers, _, . or -
            </p>
          ) : null}

          <div className="mt-1.5 flex max-h-48 flex-col gap-0.5 overflow-y-auto">
            {canCreate ? (
              <button
                type="button"
                onClick={() => {
                  if (mode === "message" && onToggleTag) {
                    onToggleTag(trimmed);
                    setFilter("");
                  } else if (mode === "bulk" && onBulkApply) {
                    onBulkApply("add_tags", trimmed);
                    setFilter("");
                    onOpenChange(false);
                  }
                }}
                className="flex w-full cursor-pointer items-center gap-1.5 rounded-sm px-2 py-1.5 text-left text-xs font-medium text-accent hover:bg-muted"
              >
                <Plus className="size-3.5 shrink-0" aria-hidden="true" />
                <span className="truncate">Create &ldquo;{trimmed}&rdquo;</span>
              </button>
            ) : null}

            {matchingTags.map((tag) => {
              const dot = PROJECT_COLOR_DOT_CLASS[tag.color] ?? PROJECT_COLOR_DOT_CLASS.slate;
              if (mode === "bulk" && onBulkApply) {
                return (
                  <div
                    key={tag.id}
                    className="flex items-center justify-between gap-1 rounded-sm px-1.5 py-1 text-xs hover:bg-muted"
                  >
                    <span className="flex min-w-0 items-center gap-1.5">
                      <span
                        className={cn("size-2 shrink-0 rounded-full", dot)}
                        aria-hidden="true"
                      />
                      <span className="truncate text-foreground">{tag.name}</span>
                    </span>
                    <span className="flex shrink-0 items-center gap-1">
                      <button
                        type="button"
                        onClick={() => {
                          onBulkApply("add_tags", tag.name);
                          onOpenChange(false);
                        }}
                        className="cursor-pointer rounded-xs px-1.5 py-0.5 text-[11px] font-medium text-accent hover:bg-accent-subtle"
                      >
                        Add
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          onBulkApply("remove_tags", tag.name);
                          onOpenChange(false);
                        }}
                        className="cursor-pointer rounded-xs px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-destructive"
                      >
                        Remove
                      </button>
                    </span>
                  </div>
                );
              }

              const active = selectedLower.has(tag.name.toLowerCase());
              return (
                <button
                  key={tag.id}
                  type="button"
                  onClick={() => onToggleTag?.(tag.name)}
                  className="flex w-full cursor-pointer items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-xs text-foreground hover:bg-muted"
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <span className={cn("size-2 shrink-0 rounded-full", dot)} aria-hidden="true" />
                    <span className="truncate">{tag.name}</span>
                  </span>
                  {active ? (
                    <Check className="size-3.5 shrink-0 text-accent" aria-hidden="true" />
                  ) : null}
                </button>
              );
            })}

            {matchingTags.length === 0 && !canCreate && !invalidTyped ? (
              <p className="px-2 py-2 text-xs text-muted-foreground">
                No tags yet. Type a name to create one.
              </p>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}
