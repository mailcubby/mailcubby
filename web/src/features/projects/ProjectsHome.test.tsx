import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ProjectLoadError } from "@/components/PageStates";
import { ProjectsHome } from "@/features/projects/ProjectsHome";
import { ApiError, type ProjectSummary } from "@/lib/api";
import { queryKeys } from "@/lib/queryKeys";

const mockNavigateTo = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  Link: ({
    children,
    to,
    params,
    className,
  }: {
    children: ReactNode;
    to: string;
    params?: Record<string, string>;
    className?: string;
  }) => (
    <a href={params?.project ? to.replace("$project", params.project) : to} className={className}>
      {children}
    </a>
  ),
  Navigate: ({ to, params }: { to: string; params?: Record<string, string> }) => {
    mockNavigateTo(params?.project ? to.replace("$project", params.project) : to);
    return null;
  },
  useRouterState: () => "/",
}));

const mockGet = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      GET: (...args: unknown[]) => mockGet(...args),
    },
  };
});

function ok<T>(data: T) {
  return { data, response: new Response(null, { status: 200 }) };
}

const SERVER_META = {
  mode: "server" as const,
  version: "dev",
  instance_name: "Acme Mailcubby",
  setup_required: false,
  base_url: "http://localhost:8025",
  features: { remote_images: "opt-in" as const, recipient_routing: false },
  inbox_domain: "",
  smtp: { host: "localhost", port: 587, tls_mode: "starttls" as const },
};

const MEMBER = {
  user: {
    id: 2,
    email: "client@portal.test",
    name: "Client Viewer",
    is_admin: false,
    can_create_projects: false,
  },
  csrf_token: "csrf-1",
  memberships: [],
};

const CLIENT_PORTAL: ProjectSummary = {
  id: 1,
  slug: "client-portal",
  name: "Client Portal Staging",
  color: "teal",
  role: "viewer",
  message_count: 0,
  unread_count: 0,
  size_bytes: 0,
  last_message_at: null,
  sparkline: [],
};

function newClient() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(queryKeys.meta(), SERVER_META);
  client.setQueryData(queryKeys.authMe(), MEMBER);
  return client;
}

function renderWith(client: QueryClient, ui: ReactNode) {
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

function deferred<T>() {
  let resolve: (value: T) => void = () => {};
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

describe("single-project redirect after losing the project (audit V1, FR-035)", () => {
  beforeEach(() => {
    mockNavigateTo.mockReset();
    mockGet.mockReset();
    window.localStorage.clear();
  });

  afterEach(() => {
    cleanup();
  });

  it("drops a project that returned 404 from the cached list", () => {
    const client = newClient();
    client.setQueryData(queryKeys.projects(), [CLIENT_PORTAL]);

    renderWith(
      client,
      <ProjectLoadError
        projectSlug="client-portal"
        error={new ApiError(404, "not_found", "project not found")}
        onRetry={() => {}}
      />,
    );

    expect(client.getQueryData(queryKeys.projects())).toEqual([]);
    expect(client.getQueryState(queryKeys.projects())?.isInvalidated).toBe(true);
    expect(screen.getByRole("heading", { name: /Project not found/ })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Back to projects" }).getAttribute("href")).toBe("/");
  });

  it("waits for a refetch of a stale single-project list instead of redirecting", async () => {
    const client = newClient();
    client.setQueryData(queryKeys.projects(), [CLIENT_PORTAL], {
      updatedAt: Date.now() - 60_000,
    });
    const projects = deferred<ReturnType<typeof ok<ProjectSummary[]>>>();
    mockGet.mockImplementation(async (path: string) => {
      if (path === "/api/v1/projects") {
        return projects.promise;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    const { container } = renderWith(client, <ProjectsHome />);

    // While the refetch runs, the neutral pane shows and nothing redirects.
    expect(container.querySelector('main[aria-busy="true"]')).not.toBeNull();
    expect(mockNavigateTo).not.toHaveBeenCalled();

    projects.resolve(ok([]));

    expect(await screen.findByText("No projects yet")).toBeTruthy();
    expect(mockNavigateTo).not.toHaveBeenCalled();
  });

  it("still opens the only project once the list is settled", async () => {
    const client = newClient();
    mockGet.mockImplementation(async (path: string) => {
      if (path === "/api/v1/projects") {
        return ok([CLIENT_PORTAL]);
      }
      throw new Error(`unexpected GET ${path}`);
    });

    renderWith(client, <ProjectsHome />);

    await waitFor(() => expect(mockNavigateTo).toHaveBeenCalledWith("/p/client-portal"));
  });
});
