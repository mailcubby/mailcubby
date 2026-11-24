import { Search, X } from "lucide-react";
import {
  type KeyboardEvent,
  type RefObject,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  type AutocompleteSuggestion,
  applyAutocompleteSuggestion,
  extractOperatorChips,
  getAutocompleteSuggestions,
  removeChipFromQuery,
} from "@/features/inbox/searchQuery";
import type { SMTPCredential, TagSummary } from "@/lib/api";
import { cn } from "@/lib/utils";

const SEARCH_DEBOUNCE_MS = 180;

export interface SearchBarProps {
  inputRef: RefObject<HTMLInputElement | null>;
  value: string;
  activeTag?: string;
  activeCredentialLabel?: string;
  tags: ReadonlyArray<TagSummary>;
  credentials?: ReadonlyArray<SMTPCredential>;
  recentSenders: ReadonlyArray<string>;
  onChangeQuery: (next: string) => void;
  onClearTag: () => void;
  onClearCredential?: () => void;
  onClearAll: () => void;
}

export function SearchBar({
  inputRef,
  value,
  activeTag,
  activeCredentialLabel,
  tags,
  credentials,
  recentSenders,
  onChangeQuery,
  onClearTag,
  onClearCredential,
  onClearAll,
}: SearchBarProps) {
  const [draft, setDraft] = useState(value);
  const [cursorPos, setCursorPos] = useState(value.length);
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);

  const containerRef = useRef<HTMLDivElement | null>(null);
  const debounceRef = useRef<number | null>(null);
  const listboxId = useId();

  // Keep draft synchronized when URL query changes externally (e.g. sidebar or command palette).
  useEffect(() => {
    setDraft(value);
  }, [value]);

  useEffect(() => {
    return () => {
      if (debounceRef.current !== null) {
        window.clearTimeout(debounceRef.current);
      }
    };
  }, []);

  const commitQueryImmediate = (next: string) => {
    if (debounceRef.current !== null) {
      window.clearTimeout(debounceRef.current);
      debounceRef.current = null;
    }
    onChangeQuery(next);
  };

  const scheduleCommit = (next: string) => {
    if (debounceRef.current !== null) {
      window.clearTimeout(debounceRef.current);
    }
    debounceRef.current = window.setTimeout(() => {
      debounceRef.current = null;
      onChangeQuery(next);
    }, SEARCH_DEBOUNCE_MS);
  };

  const chips = useMemo(() => extractOperatorChips(draft), [draft]);

  const autocomplete = useMemo(
    () =>
      getAutocompleteSuggestions(draft, cursorPos, {
        tags,
        senders: recentSenders,
        ...(credentials ? { credentials } : {}),
      }),
    [draft, cursorPos, tags, recentSenders, credentials],
  );

  const dropdownOpen = focused && !dismissed && autocomplete.suggestions.length > 0;

  useEffect(() => {
    if (activeIndex >= autocomplete.suggestions.length) {
      setActiveIndex(0);
    }
  }, [activeIndex, autocomplete.suggestions.length]);

  const selectSuggestion = (suggestion: AutocompleteSuggestion) => {
    const { nextQuery, nextCursor } = applyAutocompleteSuggestion(
      draft,
      autocomplete.replaceStart,
      autocomplete.replaceEnd,
      suggestion.insertText,
    );
    setDraft(nextQuery);
    setCursorPos(nextCursor);
    setActiveIndex(0);
    if (suggestion.complete) {
      commitQueryImmediate(nextQuery.trim());
    } else {
      scheduleCommit(nextQuery);
    }
    requestAnimationFrame(() => {
      const el = inputRef.current;
      if (el) {
        el.focus();
        el.setSelectionRange(nextCursor, nextCursor);
      }
    });
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (dropdownOpen) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveIndex((prev) => (prev + 1) % autocomplete.suggestions.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveIndex(
          (prev) => (prev - 1 + autocomplete.suggestions.length) % autocomplete.suggestions.length,
        );
        return;
      }
      if (e.key === "Tab") {
        setDismissed(true);
        return;
      }
      if (e.key === "Enter") {
        const target = autocomplete.suggestions[activeIndex];
        // Apply suggestion on Enter only when typing a partial operator/value; otherwise submit search.
        const currentToken = draft.slice(autocomplete.replaceStart, cursorPos);
        if (target && currentToken !== "" && target.label !== currentToken) {
          e.preventDefault();
          selectSuggestion(target);
          return;
        }
        e.preventDefault();
        setDismissed(true);
        commitQueryImmediate(draft.trim());
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setDismissed(true);
        return;
      }
    } else if (e.key === "Enter") {
      e.preventDefault();
      commitQueryImmediate(draft.trim());
    } else if (e.key === "Escape") {
      e.preventDefault();
      inputRef.current?.blur();
    }
  };

  const activeOptionId =
    dropdownOpen && autocomplete.suggestions[activeIndex]
      ? `${listboxId}-opt-${activeIndex}`
      : undefined;

  const hasFilters = Boolean(draft.trim() || activeTag || activeCredentialLabel);

  return (
    <div ref={containerRef} className="relative border-b border-border bg-surface px-2.5 py-2">
      <div className="relative flex items-center">
        <Search
          className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground"
          aria-hidden="true"
        />
        <input
          ref={inputRef}
          type="text"
          role="combobox"
          aria-label="Search messages"
          aria-expanded={dropdownOpen}
          aria-controls={listboxId}
          aria-activedescendant={activeOptionId}
          aria-autocomplete="list"
          placeholder="Search messages (from:, subject:, has:, tag:)..."
          value={draft}
          onFocus={(e) => {
            setFocused(true);
            setDismissed(false);
            setCursorPos(e.currentTarget.selectionStart ?? e.currentTarget.value.length);
          }}
          onBlur={(e) => {
            // Keep open briefly when clicking an option inside containerRef.
            if (containerRef.current?.contains(e.relatedTarget as Node | null)) {
              return;
            }
            setFocused(false);
          }}
          onClick={(e) => {
            setDismissed(false);
            setCursorPos(e.currentTarget.selectionStart ?? e.currentTarget.value.length);
          }}
          onKeyUp={(e) => {
            setCursorPos(e.currentTarget.selectionStart ?? e.currentTarget.value.length);
          }}
          onChange={(e) => {
            const next = e.target.value;
            setDraft(next);
            setCursorPos(e.target.selectionStart ?? next.length);
            setDismissed(false);
            setActiveIndex(0);
            scheduleCommit(next);
          }}
          onKeyDown={handleKeyDown}
          className="h-8 w-full rounded-md border border-border bg-background pr-14 pl-8 text-xs text-foreground placeholder:text-muted-foreground focus-visible:outline-2 focus-visible:outline-accent"
        />
        <div className="absolute right-2 flex items-center gap-1">
          {hasFilters ? (
            <button
              type="button"
              aria-label="Clear search"
              title="Clear search"
              onClick={() => {
                setDraft("");
                setCursorPos(0);
                setDismissed(true);
                if (debounceRef.current !== null) {
                  window.clearTimeout(debounceRef.current);
                  debounceRef.current = null;
                }
                onClearAll();
              }}
              className="inline-flex size-5 cursor-pointer items-center justify-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              <X className="size-3.5" aria-hidden="true" />
            </button>
          ) : !focused ? (
            <kbd className="pointer-events-none rounded-sm border border-border bg-surface px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
              /
            </kbd>
          ) : null}
        </div>
      </div>

      {chips.length > 0 || activeTag || activeCredentialLabel ? (
        <ul
          aria-label="Active search filters"
          className="mt-1.5 flex list-none flex-wrap items-center gap-1 p-0"
        >
          {activeTag ? (
            <li
              data-testid="search-chip"
              className="inline-flex items-center gap-1 rounded-sm border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[11px] font-medium text-foreground"
            >
              <span>tag:{activeTag}</span>
              <button
                type="button"
                aria-label={`Remove tag filter ${activeTag}`}
                onClick={onClearTag}
                className="cursor-pointer rounded-xs text-muted-foreground hover:text-foreground"
              >
                <X className="size-3" aria-hidden="true" />
              </button>
            </li>
          ) : null}
          {activeCredentialLabel ? (
            <li
              data-testid="search-chip"
              className="inline-flex items-center gap-1 rounded-sm border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[11px] font-medium text-foreground"
            >
              <span>credential:{activeCredentialLabel}</span>
              <button
                type="button"
                aria-label={`Remove credential filter ${activeCredentialLabel}`}
                onClick={onClearCredential}
                className="cursor-pointer rounded-xs text-muted-foreground hover:text-foreground"
              >
                <X className="size-3" aria-hidden="true" />
              </button>
            </li>
          ) : null}
          {chips.map((chip) => (
            <li
              key={`${chip.start}-${chip.raw}`}
              data-testid="search-chip"
              className="inline-flex items-center gap-1 rounded-sm border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-foreground"
            >
              <span>{chip.raw}</span>
              <button
                type="button"
                aria-label={`Remove filter ${chip.raw}`}
                onClick={() => {
                  const next = removeChipFromQuery(draft, chip);
                  setDraft(next);
                  setCursorPos(next.length);
                  commitQueryImmediate(next);
                }}
                className="cursor-pointer rounded-xs text-muted-foreground hover:text-foreground"
              >
                <X className="size-3" aria-hidden="true" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      {dropdownOpen ? (
        <div
          id={listboxId}
          role="listbox"
          aria-label="Search operator suggestions"
          className="absolute right-2.5 left-2.5 z-30 mt-1 max-h-60 overflow-y-auto rounded-md border border-border bg-surface p-1 shadow-lg"
        >
          {autocomplete.suggestions.map((suggestion, idx) => {
            const selected = idx === activeIndex;
            return (
              <button
                key={suggestion.id}
                id={`${listboxId}-opt-${idx}`}
                type="button"
                role="option"
                tabIndex={-1}
                aria-selected={selected}
                onMouseDown={(e) => {
                  e.preventDefault();
                  selectSuggestion(suggestion);
                }}
                onMouseEnter={() => setActiveIndex(idx)}
                className={cn(
                  "flex w-full cursor-pointer items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-xs transition-colors",
                  selected ? "bg-accent/12 text-foreground" : "text-foreground hover:bg-muted/60",
                )}
              >
                <span className="truncate font-mono font-medium">{suggestion.label}</span>
                <span className="shrink-0 text-[11px] text-muted-foreground">
                  {suggestion.description}
                </span>
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
