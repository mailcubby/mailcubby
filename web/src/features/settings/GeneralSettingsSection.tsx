import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { AlertTriangle, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useId, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ProjectSettingsLayout } from "@/features/settings/ProjectSettingsLayout";
import {
  ApiError,
  api,
  getApiErrorMessage,
  handleApiError,
  type ProjectColor,
  type ProjectDetail,
  unwrap,
  unwrapVoid,
} from "@/lib/api";
import { PROJECT_COLOR_DOT_CLASS, PROJECT_COLORS } from "@/lib/format";
import { metaQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

function GeneralSettingsForm({ project }: { project: ProjectDetail }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: meta } = useQuery(metaQueryOptions());

  const nameId = useId();
  const slugId = useId();
  const confirmSlugId = useId();

  const [name, setName] = useState(project.name);
  const [slug, setSlug] = useState(project.slug);
  const [color, setColor] = useState<ProjectColor>(project.color);
  const [formError, setFormError] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [confirmSlug, setConfirmSlug] = useState("");
  const [deleteError, setDeleteError] = useState<string | null>(null);

  useEffect(() => {
    setName(project.name);
    setSlug(project.slug);
    setColor(project.color);
    setFormError(null);
  }, [project.name, project.slug, project.color]);

  useEffect(() => {
    if (project.role !== "owner") {
      void navigate({
        to: "/p/$project/settings/members",
        params: { project: project.slug },
        replace: true,
      });
    }
  }, [project.role, project.slug, navigate]);

  const updateMutation = useMutation({
    mutationFn: async (vars: { name: string; slug: string; color: ProjectColor }) =>
      unwrap(
        await api.PATCH("/api/v1/projects/{project}", {
          params: { path: { project: project.slug } },
          body: vars,
        }),
      ),
    onSuccess: async (updated) => {
      setFormError(null);
      const slugChanged = updated.slug !== project.slug;
      queryClient.setQueryData(queryKeys.project(updated.slug), updated);
      if (slugChanged) {
        queryClient.removeQueries({ queryKey: queryKeys.project(project.slug) });
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.projects() }),
        queryClient.invalidateQueries({ queryKey: queryKeys.authMe() }),
      ]);
      toast.success("Project settings saved");
      if (slugChanged) {
        await navigate({
          to: "/p/$project/settings/general",
          params: { project: updated.slug },
          replace: true,
        });
      }
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details) {
        const first = Object.values(err.details)[0];
        if (first) {
          setFormError(first);
          return;
        }
      }
      setFormError(getApiErrorMessage(err, "Failed to update project"));
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (confirmValue: string) =>
      unwrapVoid(
        await api.DELETE("/api/v1/projects/{project}", {
          params: { path: { project: project.slug } },
          body: { confirm_slug: confirmValue },
        }),
      ),
    onSuccess: async () => {
      setDeleteOpen(false);
      setConfirmSlug("");
      setDeleteError(null);
      queryClient.removeQueries({ queryKey: queryKeys.project(project.slug) });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.projects() }),
        queryClient.invalidateQueries({ queryKey: queryKeys.authMe() }),
      ]);
      toast.success(`Deleted project "${project.name}"`);
      await navigate({ to: "/", replace: true });
    },
    onError: (err) => {
      setDeleteError(getApiErrorMessage(err, "Failed to delete project"));
      handleApiError(err, "Failed to delete project");
    },
  });

  if (project.role !== "owner") {
    return null;
  }

  const trimmedName = name.trim();
  const trimmedSlug = slug.trim();
  const slugChanged = trimmedSlug !== "" && trimmedSlug !== project.slug;
  const hasChanges =
    trimmedName !== project.name || trimmedSlug !== project.slug || color !== project.color;

  const handleSave = (e: FormEvent) => {
    e.preventDefault();
    if (!trimmedName) {
      setFormError("Project name is required.");
      return;
    }
    if (!trimmedSlug) {
      setFormError("Project slug is required.");
      return;
    }
    setFormError(null);
    updateMutation.mutate({ name: trimmedName, slug: trimmedSlug, color });
  };

  const handleDeleteSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (confirmSlug.trim() !== project.slug) {
      setDeleteError(`Type "${project.slug}" to confirm deletion.`);
      return;
    }
    setDeleteError(null);
    deleteMutation.mutate(confirmSlug.trim());
  };

  const inboxDomain = meta?.inbox_domain;

  return (
    <>
      <Card aria-labelledby="general-heading">
        <CardHeader
          titleId="general-heading"
          title="General"
          description="Update the project name, URL slug, and colour badge."
        />

        <form onSubmit={handleSave}>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <label htmlFor={nameId} className="text-xs font-medium text-foreground">
                Project name
              </label>
              <Input
                id={nameId}
                required
                maxLength={80}
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  if (formError) {
                    setFormError(null);
                  }
                }}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor={slugId} className="text-xs font-medium text-foreground">
                Slug
              </label>
              <Input
                id={slugId}
                required
                maxLength={63}
                className="font-mono"
                value={slug}
                onChange={(e) => {
                  setSlug(e.target.value);
                  if (formError) {
                    setFormError(null);
                  }
                }}
              />
              <p className="text-xs text-muted-foreground">
                Lowercase letters, numbers, and hyphens (1–63 characters). Used in URLs and routing
                addresses.
              </p>
            </div>

            {slugChanged ? (
              <div
                role="status"
                className="flex items-start gap-2.5 rounded-md border border-warning/30 bg-warning-subtle p-3 text-xs text-foreground"
              >
                <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden="true" />
                <div className="min-w-0 space-y-1">
                  <p className="font-medium">
                    Renaming the slug changes URLs and routing addresses
                  </p>
                  <p className="text-muted-foreground">
                    Old slugs are not reserved and existing links to{" "}
                    <code className="font-mono text-foreground">/p/{project.slug}</code> will stop
                    working.
                  </p>
                  {inboxDomain ? (
                    <div className="mt-1.5 space-y-0.5 font-mono text-[11px] text-muted-foreground">
                      <p>
                        Old:{" "}
                        <span className="line-through">
                          *@{project.slug}.{inboxDomain}
                        </span>
                        ,{" "}
                        <span className="line-through">
                          {project.slug}@{inboxDomain}
                        </span>
                      </p>
                      <p className="text-foreground">
                        New: *@{trimmedSlug}.{inboxDomain}, {trimmedSlug}@{inboxDomain}
                      </p>
                    </div>
                  ) : null}
                </div>
              </div>
            ) : null}

            <fieldset className="flex flex-col gap-2 border-0 p-0">
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

            {formError ? (
              <p role="alert" className="text-xs font-medium text-destructive">
                {formError}
              </p>
            ) : null}
          </CardContent>

          <CardFooter>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!hasChanges || updateMutation.isPending}
              onClick={() => {
                setName(project.name);
                setSlug(project.slug);
                setColor(project.color);
                setFormError(null);
              }}
            >
              Reset
            </Button>
            <Button
              type="submit"
              variant="primary"
              size="sm"
              disabled={!hasChanges || updateMutation.isPending}
            >
              {updateMutation.isPending ? "Saving..." : "Save changes"}
            </Button>
          </CardFooter>
        </form>
      </Card>

      <Card aria-labelledby="danger-zone-heading" className="border-destructive/40">
        <CardHeader
          titleId="danger-zone-heading"
          tone="destructive"
          title="Danger zone"
          description={
            <>
              Permanently delete{" "}
              <strong className="font-medium text-foreground">{project.name}</strong> and all of its
              captured messages, attachments, tags, credentials, and memberships. This action cannot
              be undone.
            </>
          }
          actions={
            <Button
              variant="destructive"
              size="sm"
              onClick={() => {
                setConfirmSlug("");
                setDeleteError(null);
                setDeleteOpen(true);
              }}
            >
              <Trash2 className="size-3.5" aria-hidden="true" />
              <span>Delete project</span>
            </Button>
          }
        />
      </Card>

      <Dialog
        open={deleteOpen}
        onOpenChange={(open) => {
          setDeleteOpen(open);
          if (!open) {
            setConfirmSlug("");
            setDeleteError(null);
          }
        }}
        title={`Delete "${project.name}"?`}
        description="All messages, attachments, tags, and project credentials will be permanently deleted."
      >
        <form onSubmit={handleDeleteSubmit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <label htmlFor={confirmSlugId} className="text-xs font-medium text-foreground">
              Type <code className="rounded-sm bg-muted px-1 py-0.5 font-mono">{project.slug}</code>{" "}
              to confirm
            </label>
            <Input
              id={confirmSlugId}
              autoFocus
              required
              placeholder={project.slug}
              className="font-mono"
              value={confirmSlug}
              onChange={(e) => {
                setConfirmSlug(e.target.value);
                if (deleteError) {
                  setDeleteError(null);
                }
              }}
            />
            {deleteError ? (
              <p role="alert" className="text-xs font-medium text-destructive">
                {deleteError}
              </p>
            ) : null}
          </div>

          <div className="flex items-center justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={deleteMutation.isPending}
              onClick={() => setDeleteOpen(false)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              variant="destructive"
              size="sm"
              disabled={confirmSlug.trim() !== project.slug || deleteMutation.isPending}
            >
              {deleteMutation.isPending ? "Deleting..." : "Delete project"}
            </Button>
          </div>
        </form>
      </Dialog>
    </>
  );
}

export function GeneralSettingsPage({ projectSlug }: { projectSlug: string }) {
  return (
    <ProjectSettingsLayout
      projectSlug={projectSlug}
      section="general"
      title="General settings"
      description="Configure project identity, slug, and lifecycle."
    >
      {(project) => <GeneralSettingsForm project={project} />}
    </ProjectSettingsLayout>
  );
}
