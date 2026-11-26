import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { AlertTriangle, Copy } from "lucide-react";
import { useEffect, useId } from "react";
import { toast } from "sonner";
import { ProjectSettingsLayout } from "@/features/settings/ProjectSettingsLayout";
import { api, handleApiError, type ProjectDetail, type ProjectSettings, unwrap } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { metaQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";

function isOwnerSettings(settings: ProjectDetail["settings"]): settings is ProjectSettings {
  return "recipient_routing_enabled" in settings && "members_can_reveal_smtp" in settings;
}

function RoutingSettingsForm({ project }: { project: ProjectDetail }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: meta } = useQuery(metaQueryOptions());
  const isServer = meta?.mode === "server";

  const recipientRoutingId = useId();
  const recipientRoutingDescId = useId();
  const plusTagsId = useId();
  const plusTagsDescId = useId();
  const hideTechId = useId();
  const hideTechDescId = useId();
  const membersRevealId = useId();
  const membersRevealDescId = useId();

  useEffect(() => {
    if (project.role !== "owner") {
      void navigate({
        to: "/p/$project/settings/smtp",
        params: { project: project.slug },
        replace: true,
      });
    }
  }, [project.role, project.slug, navigate]);

  const toggleMutation = useMutation({
    mutationFn: async (
      patch: Partial<{
        recipient_routing_enabled: boolean;
        plus_address_tags: boolean;
        viewer_hide_technical: boolean;
        members_can_reveal_smtp: boolean;
      }>,
    ) =>
      unwrap(
        await api.PATCH("/api/v1/projects/{project}", {
          params: { path: { project: project.slug } },
          body: { settings: patch },
        }),
      ),
    onMutate: async (patch) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.project(project.slug) });
      const previous = queryClient.getQueryData<ProjectDetail>(queryKeys.project(project.slug));
      if (previous) {
        queryClient.setQueryData<ProjectDetail>(queryKeys.project(project.slug), {
          ...previous,
          settings: {
            ...previous.settings,
            ...patch,
          },
        });
      }
      return { previous };
    },
    onSuccess: (updated) => {
      queryClient.setQueryData(queryKeys.project(project.slug), updated);
      toast.success("Project setting saved");
    },
    onError: (err, _patch, context) => {
      if (context?.previous) {
        queryClient.setQueryData(queryKeys.project(project.slug), context.previous);
      }
      handleApiError(err, "Failed to update setting");
    },
  });

  if (project.role !== "owner" || !isOwnerSettings(project.settings)) {
    return null;
  }

  const settings = project.settings;
  const inboxDomain = meta?.inbox_domain;

  return (
    <div className="flex flex-col gap-6">
      {isServer ? (
        <section className="rounded-lg border border-border bg-surface p-5">
          <div className="flex items-start justify-between gap-4">
            <div>
              <label htmlFor={recipientRoutingId} className="text-sm font-semibold text-foreground">
                Recipient routing (unauthenticated SMTP)
              </label>
              <p id={recipientRoutingDescId} className="mt-1 text-xs text-muted-foreground">
                Accept mail without SMTP AUTH when addressed to this project&apos;s inbox domain
                routes.
              </p>
            </div>
            <input
              id={recipientRoutingId}
              type="checkbox"
              role="switch"
              aria-checked={settings.recipient_routing_enabled}
              aria-describedby={recipientRoutingDescId}
              checked={settings.recipient_routing_enabled}
              disabled={toggleMutation.isPending}
              onChange={(e) =>
                toggleMutation.mutate({ recipient_routing_enabled: e.target.checked })
              }
              className="mt-1 size-4 shrink-0 cursor-pointer rounded-sm border border-border accent-accent"
            />
          </div>

          <div
            role="note"
            className="mt-3 flex items-start gap-2 rounded-md border border-warning/30 bg-warning-subtle px-3 py-2 text-xs text-warning-foreground"
          >
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
            <span>
              When enabled, any sender that can reach the SMTP port can deliver messages into this
              project without credentials. Firewall the SMTP port if exposed beyond your internal
              network.
            </span>
          </div>

          {inboxDomain ? (
            <div className="mt-4">
              <div className="text-[11px] font-medium text-muted-foreground">
                Resulting routing addresses
              </div>
              <div className="mt-2 flex flex-wrap gap-2 font-mono text-xs">
                {[
                  `*@${project.slug}.${inboxDomain}`,
                  `${project.slug}@${inboxDomain}`,
                  `${project.slug}+tag@${inboxDomain}`,
                ].map((addr) => (
                  <button
                    key={addr}
                    type="button"
                    onClick={() => void copyToClipboard(addr, "routing address")}
                    className="inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-border bg-background px-2.5 py-1 text-foreground hover:bg-muted"
                  >
                    <span>{addr}</span>
                    <Copy className="size-3 text-muted-foreground" aria-hidden="true" />
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <p className="mt-3 text-xs text-muted-foreground">
              Set <code className="font-mono text-foreground">MAILCUBBY_INBOX_DOMAIN</code> on the
              server to enable address-based routing.
            </p>
          )}
        </section>
      ) : null}

      <section className="flex flex-col divide-y divide-border rounded-lg border border-border bg-surface">
        <div className="flex items-start justify-between gap-4 p-5">
          <div>
            <label htmlFor={plusTagsId} className="text-sm font-semibold text-foreground">
              Plus-addressing tags
            </label>
            <p id={plusTagsDescId} className="mt-1 text-xs text-muted-foreground">
              Automatically tag incoming messages from envelope recipients using{" "}
              <code className="font-mono text-foreground">local+tag@domain</code>.
            </p>
          </div>
          <input
            id={plusTagsId}
            type="checkbox"
            role="switch"
            aria-checked={settings.plus_address_tags}
            aria-describedby={plusTagsDescId}
            checked={settings.plus_address_tags}
            disabled={toggleMutation.isPending}
            onChange={(e) => toggleMutation.mutate({ plus_address_tags: e.target.checked })}
            className="mt-1 size-4 shrink-0 cursor-pointer rounded-sm border border-border accent-accent"
          />
        </div>

        {isServer ? (
          <>
            <div className="flex items-start justify-between gap-4 p-5">
              <div>
                <label htmlFor={hideTechId} className="text-sm font-semibold text-foreground">
                  Hide technical views from viewers
                </label>
                <p id={hideTechDescId} className="mt-1 text-xs text-muted-foreground">
                  Hide Raw source, HTML source, Headers, <code className="font-mono">.eml</code>{" "}
                  downloads, and the Tech tab from client viewers so they only see Preview, Text,
                  and Attachments.
                </p>
              </div>
              <input
                id={hideTechId}
                type="checkbox"
                role="switch"
                aria-checked={settings.viewer_hide_technical}
                aria-describedby={hideTechDescId}
                checked={settings.viewer_hide_technical}
                disabled={toggleMutation.isPending}
                onChange={(e) => toggleMutation.mutate({ viewer_hide_technical: e.target.checked })}
                className="mt-1 size-4 shrink-0 cursor-pointer rounded-sm border border-border accent-accent"
              />
            </div>

            <div className="flex items-start justify-between gap-4 p-5">
              <div>
                <label htmlFor={membersRevealId} className="text-sm font-semibold text-foreground">
                  Members can reveal SMTP passwords
                </label>
                <p id={membersRevealDescId} className="mt-1 text-xs text-muted-foreground">
                  Allow project members (in addition to owners) to reveal and copy SMTP credential
                  passwords. Every reveal is recorded in the audit log.
                </p>
              </div>
              <input
                id={membersRevealId}
                type="checkbox"
                role="switch"
                aria-checked={settings.members_can_reveal_smtp}
                aria-describedby={membersRevealDescId}
                checked={settings.members_can_reveal_smtp}
                disabled={toggleMutation.isPending}
                onChange={(e) =>
                  toggleMutation.mutate({ members_can_reveal_smtp: e.target.checked })
                }
                className="mt-1 size-4 shrink-0 cursor-pointer rounded-sm border border-border accent-accent"
              />
            </div>
          </>
        ) : null}
      </section>
    </div>
  );
}

export function RoutingSettingsSection({ projectSlug }: { projectSlug: string }) {
  return (
    <ProjectSettingsLayout
      projectSlug={projectSlug}
      section="routing"
      title="Routing & client view"
      description="Configure SMTP recipient routing, plus-address tags, and client viewer visibility."
    >
      {(project) => <RoutingSettingsForm project={project} />}
    </ProjectSettingsLayout>
  );
}
