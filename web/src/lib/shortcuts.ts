import { useEffect, useRef } from "react";

export interface KeyShortcutBinding {
  /**
   * Key descriptor:
   * - Single key: "j", "k", "e", "#", "Delete", "Escape", "Enter", "1", "?", "[", "/"
   * - Shift + letter: "Shift+j", "Shift+k"
   * - Mod + key (Cmd on macOS, Ctrl elsewhere): "Mod+k"
   * - Two-key chord: "g p", "g a", "g s"
   */
  keys: string;
  handler: (event: KeyboardEvent) => void;
  /**
   * When true, fires even if focus is inside an editable input/textarea/select.
   * Defaults to true for "Mod+" combos, false for everything else.
   */
  allowInInput?: boolean;
  /**
   * When true, fires even when a native modal <dialog open> is open.
   * Defaults to false.
   */
  allowInModal?: boolean;
  /**
   * When false, the binding is inactive.
   */
  enabled?: boolean;
  /**
   * Optional predicate checked before `event.preventDefault()` and `handler`.
   * Return false to let the browser handle the key natively (e.g. Enter on a focused button).
   */
  when?: (event: KeyboardEvent) => boolean;
}

export interface UseKeyShortcutsOptions {
  /**
   * Master toggle for single-key shortcuts (UI 4 item 2, WCAG 2.1.4).
   * Modifier combos (Mod+K) and Escape always remain active.
   */
  singleKeyEnabled?: boolean;
  /**
   * Timeout window in milliseconds for two-key chords (e.g. `g p`).
   */
  chordTimeoutMs?: number;
}

export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  const tag = target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") {
    return true;
  }
  if (target.isContentEditable) {
    return true;
  }
  if (target.closest("[contenteditable=''], [contenteditable='true'], [role='textbox']")) {
    return true;
  }
  return false;
}

export function isOpenModalPresent(): boolean {
  if (typeof document === "undefined") {
    return false;
  }
  return document.querySelector("dialog[open]") !== null;
}

function isSingleKeyDescriptor(descriptor: string): boolean {
  if (descriptor.startsWith("Mod+")) {
    return false;
  }
  if (descriptor === "Escape" || descriptor === "Enter") {
    return false;
  }
  return true;
}

function matchesSingleEvent(descriptor: string, event: KeyboardEvent): boolean {
  if (descriptor.startsWith("Mod+")) {
    const key = descriptor.slice(4).toLowerCase();
    return (event.metaKey || event.ctrlKey) && !event.altKey && event.key.toLowerCase() === key;
  }

  if (descriptor.startsWith("Shift+")) {
    const key = descriptor.slice(6).toLowerCase();
    return (
      event.shiftKey &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.altKey &&
      event.key.toLowerCase() === key
    );
  }

  // Non-modifier keys must not match when Ctrl/Meta/Alt are held.
  if (event.metaKey || event.ctrlKey || event.altKey) {
    return false;
  }

  // Special characters that require Shift on standard keyboards (`?`, `#`).
  if (descriptor === "?" || descriptor === "#") {
    return event.key === descriptor;
  }

  if (descriptor.length === 1) {
    return !event.shiftKey && event.key === descriptor;
  }

  return !event.shiftKey && event.key === descriptor;
}

/**
 * Registers document-level keyboard shortcuts with input-focus guarding,
 * modal awareness, two-key chord support (`g p`, `g a`, `g s`), and the
 * user's single-key shortcuts preference (WCAG 2.1.4).
 */
export function useKeyShortcuts(
  bindings: ReadonlyArray<KeyShortcutBinding>,
  options: UseKeyShortcutsOptions = {},
): void {
  const { singleKeyEnabled = true, chordTimeoutMs = 800 } = options;
  const bindingsRef = useRef(bindings);
  bindingsRef.current = bindings;

  const pendingChordRef = useRef<{ prefix: string; expiresAt: number } | null>(null);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing) {
        return;
      }

      const activeBindings = bindingsRef.current;
      const inInput = isEditableTarget(event.target);
      const modalOpen = isOpenModalPresent();
      const now = Date.now();

      // Check active chord completion first.
      const pending = pendingChordRef.current;
      if (pending) {
        pendingChordRef.current = null;
        if (now <= pending.expiresAt && !inInput && !modalOpen && singleKeyEnabled) {
          const chordKey = `${pending.prefix} ${event.key.toLowerCase()}`;
          for (const binding of activeBindings) {
            if (binding.enabled === false) {
              continue;
            }
            if (binding.keys === chordKey) {
              event.preventDefault();
              binding.handler(event);
              return;
            }
          }
        }
      }

      // Check if this key starts a registered two-key chord.
      if (
        !inInput &&
        !modalOpen &&
        singleKeyEnabled &&
        !event.metaKey &&
        !event.ctrlKey &&
        !event.altKey &&
        !event.shiftKey &&
        event.key.length === 1
      ) {
        const lower = event.key.toLowerCase();
        const hasChordWithPrefix = activeBindings.some(
          (b) => b.enabled !== false && b.keys.startsWith(`${lower} `),
        );
        if (hasChordWithPrefix) {
          pendingChordRef.current = {
            prefix: lower,
            expiresAt: now + chordTimeoutMs,
          };
          return;
        }
      }

      for (const binding of activeBindings) {
        if (binding.enabled === false || binding.keys.includes(" ")) {
          continue;
        }
        const isMod = binding.keys.startsWith("Mod+");
        if (!singleKeyEnabled && isSingleKeyDescriptor(binding.keys)) {
          continue;
        }
        const allowInInput = binding.allowInInput ?? isMod;
        if (inInput && !allowInInput) {
          continue;
        }
        const allowInModal = binding.allowInModal ?? false;
        if (modalOpen && !allowInModal) {
          continue;
        }
        if (matchesSingleEvent(binding.keys, event)) {
          if (binding.when && !binding.when(event)) {
            continue;
          }
          event.preventDefault();
          binding.handler(event);
          return;
        }
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [singleKeyEnabled, chordTimeoutMs]);
}
