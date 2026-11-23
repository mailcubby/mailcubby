import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { isEditableTarget, type KeyShortcutBinding, useKeyShortcuts } from "@/lib/shortcuts";

function pressKey(
  key: string,
  options: KeyboardEventInit = {},
  target: EventTarget = document.body,
) {
  const event = new KeyboardEvent("keydown", {
    key,
    bubbles: true,
    cancelable: true,
    ...options,
  });
  target.dispatchEvent(event);
  return event;
}

describe("useKeyShortcuts", () => {
  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
    vi.useRealTimers();
  });

  it("fires single-key shortcuts and ignores them inside form inputs", () => {
    const onNext = vi.fn();
    const onDelete = vi.fn();
    const onHelp = vi.fn();

    const bindings: KeyShortcutBinding[] = [
      { keys: "j", handler: onNext },
      { keys: "#", handler: onDelete },
      { keys: "?", handler: onHelp },
    ];

    renderHook(() => useKeyShortcuts(bindings));

    pressKey("j");
    expect(onNext).toHaveBeenCalledTimes(1);

    pressKey("#", { shiftKey: true });
    expect(onDelete).toHaveBeenCalledTimes(1);

    pressKey("?", { shiftKey: true });
    expect(onHelp).toHaveBeenCalledTimes(1);

    const input = document.createElement("input");
    document.body.appendChild(input);
    expect(isEditableTarget(input)).toBe(true);

    pressKey("j", {}, input);
    pressKey("#", { shiftKey: true }, input);
    pressKey("?", { shiftKey: true }, input);

    expect(onNext).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onHelp).toHaveBeenCalledTimes(1);
  });

  it("respects the singleKeyEnabled preference while keeping Mod+k and Escape active", () => {
    const onNext = vi.fn();
    const onPalette = vi.fn();
    const onEscape = vi.fn();

    const bindings: KeyShortcutBinding[] = [
      { keys: "j", handler: onNext },
      { keys: "Mod+k", handler: onPalette },
      { keys: "Escape", handler: onEscape },
    ];

    renderHook(() => useKeyShortcuts(bindings, { singleKeyEnabled: false }));

    pressKey("j");
    expect(onNext).not.toHaveBeenCalled();

    const input = document.createElement("input");
    document.body.appendChild(input);
    pressKey("k", { ctrlKey: true }, input);
    expect(onPalette).toHaveBeenCalledTimes(1);

    pressKey("k", { metaKey: true }, input);
    expect(onPalette).toHaveBeenCalledTimes(2);

    pressKey("Escape");
    expect(onEscape).toHaveBeenCalledTimes(1);
  });

  it("distinguishes Shift+j from plain j", () => {
    const onNext = vi.fn();
    const onExtend = vi.fn();

    renderHook(() =>
      useKeyShortcuts([
        { keys: "j", handler: onNext },
        { keys: "Shift+j", handler: onExtend },
      ]),
    );

    pressKey("j");
    expect(onNext).toHaveBeenCalledTimes(1);
    expect(onExtend).not.toHaveBeenCalled();

    pressKey("J", { shiftKey: true });
    expect(onNext).toHaveBeenCalledTimes(1);
    expect(onExtend).toHaveBeenCalledTimes(1);
  });

  it("supports two-key chords and expires them after the timeout", () => {
    vi.useFakeTimers();
    const onGoProjects = vi.fn();
    const onGoPrefs = vi.fn();

    renderHook(() =>
      useKeyShortcuts(
        [
          { keys: "g p", handler: onGoProjects },
          { keys: "g a", handler: onGoPrefs },
        ],
        { chordTimeoutMs: 500 },
      ),
    );

    pressKey("g");
    pressKey("p");
    expect(onGoProjects).toHaveBeenCalledTimes(1);

    pressKey("g");
    vi.advanceTimersByTime(600);
    pressKey("a");
    expect(onGoPrefs).not.toHaveBeenCalled();

    pressKey("g");
    vi.advanceTimersByTime(200);
    pressKey("a");
    expect(onGoPrefs).toHaveBeenCalledTimes(1);
  });

  it("blocks regular shortcuts while a native modal dialog is open", () => {
    const onNext = vi.fn();
    const onPalette = vi.fn();

    renderHook(() =>
      useKeyShortcuts([
        { keys: "j", handler: onNext },
        { keys: "Mod+k", allowInModal: true, handler: onPalette },
      ]),
    );

    const dialog = document.createElement("dialog");
    dialog.setAttribute("open", "");
    document.body.appendChild(dialog);

    pressKey("j");
    expect(onNext).not.toHaveBeenCalled();

    pressKey("k", { metaKey: true });
    expect(onPalette).toHaveBeenCalledTimes(1);
  });

  it("does not preventDefault when a binding's when predicate returns false", () => {
    const onEnter = vi.fn();
    const button = document.createElement("button");
    document.body.appendChild(button);

    renderHook(() =>
      useKeyShortcuts([
        {
          keys: "Enter",
          when: (event) => event.target === document.body,
          handler: onEnter,
        },
      ]),
    );

    const buttonEvent = pressKey("Enter", {}, button);
    expect(onEnter).not.toHaveBeenCalled();
    expect(buttonEvent.defaultPrevented).toBe(false);

    const bodyEvent = pressKey("Enter", {}, document.body);
    expect(onEnter).toHaveBeenCalledTimes(1);
    expect(bodyEvent.defaultPrevented).toBe(true);
  });
});
