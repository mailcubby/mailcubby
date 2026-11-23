import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type FormEvent, useId, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ApiError, api, getApiErrorMessage, type ProjectColor, unwrap } from "@/lib/api";
import { PROJECT_COLOR_DOT_CLASS, PROJECT_COLORS } from "@/lib/format";
import { queryKeys } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

export interface CreateProjectDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function CreateProjectDialog({ open, onOpenChange }: CreateProjectDialogProps) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const nameId = useId();
  const slugId = useId();
  const errorId = useId();

  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [color, setColor] = useState<ProjectColor>("blue");
  const [fieldError, setFieldError] = useState<string | null>(null);

  const createMutation = useMutation({
    mutationFn: async () => {
      const trimmedName = name.trim();
      const trimmedSlug = slug.trim();
      const body = trimmedSlug
        ? { name: trimmedName, slug: trimmedSlug, color }
        : { name: trimmedName, color };
      return unwrap(
        await api.POST("/api/v1/projects", {
          body,
        }),
      );
    },
    onSuccess: (created) => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.projects() });
      toast.success(`Created project "${created.name}"`);
      setName("");
      setSlug("");
      setColor("blue");
      setFieldError(null);
      onOpenChange(false);
      void navigate({ to: "/p/$project", params: { project: created.slug } });
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details) {
        const firstDetail = Object.values(err.details)[0];
        if (firstDetail) {
          setFieldError(firstDetail);
          return;
        }
      }
      setFieldError(getApiErrorMessage(err, "Could not create project"));
    },
  });

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setFieldError("Project name is required");
      return;
    }
    setFieldError(null);
    createMutation.mutate();
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setFieldError(null);
        }
        onOpenChange(next);
      }}
      title="New project"
      description="Create an isolated inbox for an application or environment."
    >
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <label htmlFor={nameId} className="text-xs font-medium text-foreground">
            Name
          </label>
          <Input
            id={nameId}
            autoFocus
            required
            maxLength={80}
            placeholder="Shop Staging"
            value={name}
            onChange={(e) => setName(e.target.value)}
            aria-invalid={fieldError ? "true" : undefined}
            aria-describedby={fieldError ? errorId : undefined}
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <label htmlFor={slugId} className="text-xs font-medium text-foreground">
            Slug <span className="font-normal text-muted-foreground">(optional)</span>
          </label>
          <Input
            id={slugId}
            maxLength={63}
            placeholder="shop-staging"
            className="font-mono"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
          />
          <p className="text-xs text-muted-foreground">
            Used in URLs and as the SMTP username in local mode. Auto-generated from name if left
            blank.
          </p>
        </div>

        <fieldset className="flex flex-col gap-2">
          <legend className="text-xs font-medium text-foreground">Color</legend>
          <div className="flex flex-wrap gap-2">
            {PROJECT_COLORS.map((token) => {
              const selected = color === token;
              return (
                <button
                  key={token}
                  type="button"
                  aria-label={token}
                  aria-pressed={selected}
                  onClick={() => setColor(token)}
                  className={cn(
                    "flex size-6 cursor-pointer items-center justify-center rounded-full border transition-transform",
                    selected
                      ? "scale-110 border-foreground ring-2 ring-accent ring-offset-2 ring-offset-surface"
                      : "border-transparent hover:scale-105",
                  )}
                >
                  <span
                    className={cn("size-4 rounded-full", PROJECT_COLOR_DOT_CLASS[token])}
                    aria-hidden="true"
                  />
                </button>
              );
            })}
          </div>
        </fieldset>

        {fieldError ? (
          <p id={errorId} role="alert" className="text-xs font-medium text-destructive">
            {fieldError}
          </p>
        ) : null}

        <div className="flex items-center justify-end gap-2 pt-1">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" variant="default" disabled={createMutation.isPending}>
            {createMutation.isPending ? "Creating..." : "Create project"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
