import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Copy, KeyRound } from "lucide-react";
import { useMemo } from "react";
import { Button } from "@/components/ui/button";
import { ProjectSettingsLayout } from "@/features/settings/ProjectSettingsLayout";
import { generateApiCurlExamples } from "@/features/settings/snippets";
import type { ProjectDetail } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { metaQueryOptions } from "@/lib/queries";

function ApiSettingsContent({ project }: { project: ProjectDetail }) {
  const { data: meta } = useQuery(metaQueryOptions());
  const mode = meta?.mode ?? "local";
  const baseUrl =
    meta?.base_url ||
    (typeof window !== "undefined" ? window.location.origin : "http://localhost:8025");

  const examples = useMemo(
    () =>
      generateApiCurlExamples({
        mode,
        baseUrl,
        projectSlug: project.slug,
      }),
    [mode, baseUrl, project.slug],
  );

  return (
    <div className="flex flex-col gap-6">
      <section className="rounded-lg border border-border bg-surface p-5">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="max-w-xl">
            <h2 className="text-sm font-semibold text-foreground">REST API access</h2>
            {mode === "server" ? (
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
                Automate end-to-end tests and scripts with personal API tokens. Tokens are created
                under <span className="font-medium text-foreground">Account &gt; API tokens</span>{" "}
                and can be restricted to this project with{" "}
                <code className="font-mono text-foreground">read</code> or{" "}
                <code className="font-mono text-foreground">write</code> scope.
              </p>
            ) : (
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
                Local mode has no authentication — call the REST API endpoints directly without an{" "}
                <code className="font-mono text-foreground">Authorization</code> header.
              </p>
            )}
          </div>

          {mode === "server" ? (
            <Link
              to="/account/tokens"
              search={{ project: project.slug }}
              className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground hover:opacity-90"
            >
              <KeyRound className="size-3.5" aria-hidden="true" />
              <span>Create token for this project</span>
            </Link>
          ) : null}
        </div>
      </section>

      <section className="flex flex-col gap-4">
        <div>
          <h2 className="text-sm font-semibold text-foreground">cURL examples</h2>
          <p className="text-xs text-muted-foreground">
            Common endpoints for <code className="font-mono text-foreground">{project.slug}</code>.
            Full schema available at{" "}
            <a
              href="/api/v1/openapi.yaml"
              target="_blank"
              rel="noopener noreferrer"
              className="font-mono text-accent underline underline-offset-2"
            >
              /api/v1/openapi.yaml
            </a>
            .
          </p>
        </div>

        {examples.map((ex) => (
          <div key={ex.id} className="overflow-hidden rounded-lg border border-border bg-surface">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2.5">
              <div>
                <h3 className="text-xs font-semibold text-foreground">{ex.title}</h3>
                <p className="text-[11px] text-muted-foreground">{ex.description}</p>
              </div>
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={() => void copyToClipboard(ex.code, `${ex.title} command`)}
              >
                <Copy className="size-3" aria-hidden="true" />
                <span>Copy</span>
              </Button>
            </div>
            <pre
              // biome-ignore lint/a11y/noNoninteractiveTabindex: scrollable code region needs keyboard focus (WCAG 2.1.1).
              tabIndex={0}
              className="overflow-x-auto p-4 font-mono text-xs leading-5 text-foreground"
            >
              <code>{ex.code}</code>
            </pre>
          </div>
        ))}
      </section>
    </div>
  );
}

export function ApiSettingsSection({ projectSlug }: { projectSlug: string }) {
  return (
    <ProjectSettingsLayout
      projectSlug={projectSlug}
      section="api"
      title="API"
      description="Automate message inspection and assertions using the Mailcubby REST API."
    >
      {(project) => <ApiSettingsContent project={project} />}
    </ProjectSettingsLayout>
  );
}
