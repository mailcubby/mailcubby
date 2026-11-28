import { Link } from "@tanstack/react-router";
import { Dialog } from "@/components/ui/dialog";
import { useSingleKeyShortcutsPreference } from "@/lib/preferences";

interface ShortcutGroup {
  title: string;
  items: ReadonlyArray<{
    keys: ReadonlyArray<string>;
    description: string;
  }>;
}

const SHORTCUT_GROUPS: ReadonlyArray<ShortcutGroup> = [
  {
    title: "Navigation",
    items: [
      { keys: ["j", "k"], description: "Next / previous message or project" },
      { keys: ["Enter", "o"], description: "Open focused message / focus reading pane" },
      { keys: ["Esc"], description: "Close dialog, clear selection, or return to list" },
      { keys: ["["], description: "Toggle sidebar" },
      { keys: ["g", "p"], description: "Go to Projects" },
      { keys: ["g", "s"], description: "Go to Project settings" },
      { keys: ["g", "a"], description: "Go to Preferences" },
    ],
  },
  {
    title: "Message actions",
    items: [
      { keys: ["e", "#", "Del"], description: "Delete message (with undo)" },
      { keys: ["u"], description: "Toggle read / unread" },
      { keys: ["t"], description: "Edit tags on message or selection" },
      { keys: ["x"], description: "Select message" },
      { keys: ["Shift+j", "Shift+k"], description: "Extend selection down / up" },
    ],
  },
  {
    title: "Message views",
    items: [
      { keys: ["1–7"], description: "Switch message tab (Preview through Tech)" },
      { keys: ["9"], description: "Switch to HTML check tab" },
      { keys: ["d"], description: "Cycle preview device (Desktop, Tablet, Mobile)" },
      { keys: ["i"], description: "Toggle remote images" },
    ],
  },
  {
    title: "Global",
    items: [
      { keys: ["⌘/Ctrl", "K"], description: "Open command palette" },
      { keys: ["/"], description: "Focus search" },
      { keys: ["n"], description: "New project (on Projects home)" },
      { keys: ["?"], description: "Open keyboard shortcuts help" },
    ],
  },
];

export interface ShortcutsHelpDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function ShortcutsHelpDialog({ open, onOpenChange }: ShortcutsHelpDialogProps) {
  const [singleKeyEnabled] = useSingleKeyShortcutsPreference();

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Keyboard shortcuts"
      description="Single-key shortcuts pause automatically while typing in inputs."
      className="w-[min(94vw,42rem)]"
    >
      <div className="flex flex-col gap-5">
        <div className="grid gap-5 sm:grid-cols-2">
          {SHORTCUT_GROUPS.map((group) => (
            <section key={group.title} aria-label={group.title} className="flex flex-col gap-2">
              <h3 className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
                {group.title}
              </h3>
              <dl className="flex flex-col gap-1.5 text-xs">
                {group.items.map((item) => (
                  <div
                    key={item.description}
                    className="flex items-center justify-between gap-3 py-0.5"
                  >
                    <dt className="text-foreground">{item.description}</dt>
                    <dd className="flex shrink-0 items-center gap-1">
                      {item.keys.map((keyLabel) => (
                        <kbd
                          key={keyLabel}
                          className="inline-flex min-w-5 items-center justify-center rounded-sm border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] font-medium text-foreground"
                        >
                          {keyLabel}
                        </kbd>
                      ))}
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
        </div>

        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3 text-xs text-muted-foreground">
          <span>
            Single-key shortcuts are currently{" "}
            <strong className="font-medium text-foreground">
              {singleKeyEnabled ? "enabled" : "disabled"}
            </strong>
            .
          </span>
          <Link
            to="/account/preferences"
            onClick={() => onOpenChange(false)}
            className="font-medium text-accent hover:underline"
          >
            Manage in Preferences
          </Link>
        </div>
      </div>
    </Dialog>
  );
}
