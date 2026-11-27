import { X } from "lucide-react";
import { type ReactNode, useEffect, useId, useRef } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface DialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  titleClassName?: string;
  description?: string;
  headerActions?: ReactNode;
  children: ReactNode;
  className?: string;
  containerClassName?: string;
  bodyClassName?: string;
  closeOnBackdropClick?: boolean;
}

/**
 * Native modal <dialog> wrapper following modern-web-guidance:
 * - Uses showModal() for built-in top-layer, inert background, Esc dismissal, and focus return.
 * - Omits redundant role="dialog" and aria-modal.
 * - Uses closedby="any" with a pointer bounding-box fallback for browsers lacking closedBy.
 */
export function Dialog({
  open,
  onOpenChange,
  title,
  titleClassName,
  description,
  headerActions,
  children,
  className,
  containerClassName,
  bodyClassName,
  closeOnBackdropClick = true,
}: DialogProps) {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const titleId = useId();
  const descriptionId = useId();

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) {
      return;
    }
    if (open && !dialog.open) {
      dialog.showModal();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) {
      return;
    }

    const handleClose = () => {
      dialog.returnValue = "";
      onOpenChange(false);
    };

    dialog.addEventListener("close", handleClose);

    if (!closeOnBackdropClick || "closedBy" in HTMLDialogElement.prototype) {
      return () => {
        dialog.removeEventListener("close", handleClose);
      };
    }

    let pressedOutside = false;
    const isOutside = (e: PointerEvent) => {
      const rect = dialog.getBoundingClientRect();
      return (
        e.clientX < rect.left ||
        e.clientX > rect.right ||
        e.clientY < rect.top ||
        e.clientY > rect.bottom
      );
    };
    const handlePointerDown = (e: PointerEvent) => {
      pressedOutside = isOutside(e);
    };
    const handlePointerUp = (e: PointerEvent) => {
      if (pressedOutside && isOutside(e)) {
        dialog.close();
      }
      pressedOutside = false;
    };

    dialog.addEventListener("pointerdown", handlePointerDown);
    dialog.addEventListener("pointerup", handlePointerUp);
    return () => {
      dialog.removeEventListener("close", handleClose);
      dialog.removeEventListener("pointerdown", handlePointerDown);
      dialog.removeEventListener("pointerup", handlePointerUp);
    };
  }, [onOpenChange, closeOnBackdropClick]);

  const closedByProp = closeOnBackdropClick ? ({ closedby: "any" } as Record<string, string>) : {};

  return (
    <dialog
      ref={dialogRef}
      aria-labelledby={titleId}
      aria-describedby={description ? descriptionId : undefined}
      {...closedByProp}
      className={cn(
        "m-auto w-[min(92vw,28rem)] rounded-lg border border-border bg-surface p-0 text-foreground shadow-xl",
        className,
      )}
    >
      {open ? (
        <div className={cn("flex flex-col", containerClassName)}>
          <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border px-5 py-4">
            <div className="min-w-0 flex-1">
              <h2
                id={titleId}
                title={titleClassName ? title : undefined}
                className={cn(
                  "text-[15px] font-semibold leading-snug text-foreground",
                  titleClassName,
                )}
              >
                {title}
              </h2>
              {description ? (
                <p id={descriptionId} className="mt-1 text-[13px] text-muted-foreground">
                  {description}
                </p>
              ) : null}
            </div>
            <div className="flex shrink-0 items-center gap-1.5">
              {headerActions}
              <Button
                variant="ghost"
                size="icon"
                aria-label="Close dialog"
                onClick={() => {
                  const dialog = dialogRef.current;
                  if (dialog?.open) {
                    // Close through native dialog.close() while focus is still inside the dialog
                    // so the browser restores focus to the trigger and fires `close` exactly once.
                    dialog.close();
                  } else {
                    onOpenChange(false);
                  }
                }}
                className={cn("-mr-1 size-7", !headerActions && "-mt-1")}
              >
                <X className="size-4" aria-hidden="true" />
              </Button>
            </div>
          </div>
          <div className={cn("px-5 py-4", bodyClassName)}>{children}</div>
        </div>
      ) : null}
    </dialog>
  );
}

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
  pending?: boolean;
  onConfirm: () => void;
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  destructive = false,
  pending = false,
  onConfirm,
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={title} description={description}>
      <div className="flex items-center justify-end gap-2">
        <Button
          variant="outline"
          size="sm"
          autoFocus={destructive}
          disabled={pending}
          onClick={() => onOpenChange(false)}
        >
          {cancelLabel}
        </Button>
        <Button
          variant={destructive ? "destructive" : "primary"}
          size="sm"
          disabled={pending}
          onClick={onConfirm}
        >
          {pending ? "Working..." : confirmLabel}
        </Button>
      </div>
    </Dialog>
  );
}
