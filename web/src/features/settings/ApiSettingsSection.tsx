import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Copy, KeyRound } from "lucide-react";
import { useMemo } from "react";
import { Button, buttonClassName } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
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
    <>
      <Card aria-labelledby="api-access-heading">
        <CardHeader
          titleId="api-access-heading"
          title="REST API access"
          description={
            mode === "server" ? (
              <>
                Automate end-to-end tests and scripts with personal API tokens. Tokens are created
                under <span className="font-medium text-foreground">Account &gt; API tokens</span>{" "}
                and can be restricted to this project with{" "}
                <code className="font-mono text-foreground">read</code> or{" "}
                <code className="font-mono text-foreground">write</code> scope.
              </>
            ) : (
              <>
                Local mode has no authentication — call the REST API endpoints directly without an{" "}
                <code className="font-mono text-foreground">Authorization</code> header.
              </>
            )
          }
          actions={
            mode === "server" ? (
              <Link
                to="/account/tokens"
                search={{ project: project.slug }}
                className={buttonClassName({ variant: "primary" })}
              >
                <KeyRound className="size-3.5" aria-hidden="true" />
                <span>Create token for this project</span>
              </Link>
            ) : null
          }
        />
      </Card>

      <section aria-labelledby="api-curl-heading" className="flex flex-col gap-3 pt-2">
        <div>
          <h2
            id="api-curl-heading"
            className="text-[15px] leading-[22px] font-semibold text-foreground"
          >
            cURL examples
          </h2>
          <p className="mt-0.5 text-[13px] text-muted-foreground">
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
          <div
            key={ex.id}
            className="overflow-hidden rounded-lg border border-border bg-surface shadow-xs"
          >
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-5 py-3">
              <div>
                <h3 className="text-[13px] font-semibold text-foreground">{ex.title}</h3>
                <p className="text-xs text-muted-foreground">{ex.description}</p>
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
              className="overflow-x-auto px-5 py-4 font-mono text-xs leading-5 text-foreground"
            >
              <code>{ex.code}</code>
            </pre>
          </div>
        ))}
      </section>
    </>
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
