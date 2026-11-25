import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { InviteAcceptPage } from "@/features/auth/InviteAcceptPage";

const mockNavigate = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  Link: ({
    children,
    to,
    search,
    className,
  }: {
    children: ReactNode;
    to: string;
    search?: Record<string, unknown>;
    className?: string;
  }) => {
    const qs =
      search && typeof search.returnTo === "string"
        ? `?returnTo=${encodeURIComponent(search.returnTo)}`
        : "";
    return (
      <a href={`${to}${qs}`} className={className}>
        {children}
      </a>
    );
  },
  useNavigate: () => mockNavigate,
}));

const mockGet = vi.fn();
const mockPost = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      GET: (...args: unknown[]) => mockGet(...args),
      POST: (...args: unknown[]) => mockPost(...args),
    },
  };
});

function ok<T>(data: T) {
  return { data, response: new Response(null, { status: 200 }) };
}

function err(status: number, code: string, message: string) {
  return {
    error: { error: { code, message } },
    response: new Response(null, { status }),
  };
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

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return {
    queryClient: client,
    ...render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>),
  };
}

describe("InviteAcceptPage (FR-056)", () => {
  beforeEach(() => {
    mockNavigate.mockReset();
    mockGet.mockReset();
    mockPost.mockReset();
  });

  afterEach(() => {
    cleanup();
  });

  it("renders unauthenticated project invite and creates account on submit", async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === "/api/v1/meta") {
        return ok(SERVER_META);
      }
      if (path === "/api/v1/invites/{token}") {
        return ok({
          project: { name: "Shop Staging", slug: "shop" },
          role: "viewer",
          email: "client@shop.test",
          invited_by: "Agency Admin",
          expires_at: "2026-12-01T00:00:00.000Z",
        });
      }
      if (path === "/api/v1/auth/me") {
        return err(401, "unauthorized", "authentication required");
      }
      throw new Error(`unexpected GET ${path}`);
    });

    mockPost.mockImplementation(async (path: string) => {
      if (path === "/api/v1/invites/{token}/accept") {
        // After accept, subsequent GET /auth/me succeeds.
        mockGet.mockImplementation(async (nextPath: string) => {
          if (nextPath === "/api/v1/auth/me") {
            return ok({
              user: {
                id: 2,
                email: "client@shop.test",
                name: "Client Viewer",
                is_admin: false,
                can_create_projects: false,
              },
              csrf_token: "csrf-1",
              memberships: [{ project: "shop", role: "viewer" }],
            });
          }
          return ok(SERVER_META);
        });
        return ok({ project: { name: "Shop Staging", slug: "shop" } });
      }
      throw new Error(`unexpected POST ${path}`);
    });

    renderWithQuery(<InviteAcceptPage token="tok-proj-1" />);

    expect(await screen.findByRole("heading", { name: "Join Shop Staging" })).toBeTruthy();
    expect(screen.getByText("client@shop.test")).toBeTruthy();
    expect(screen.getByText("viewer")).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Full name"), { target: { value: "Client Viewer" } });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "client-pass-12345" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create account & join" }));

    await waitFor(() => {
      expect(mockPost).toHaveBeenCalledWith("/api/v1/invites/{token}/accept", {
        params: { path: { token: "tok-proj-1" } },
        body: { name: "Client Viewer", password: "client-pass-12345" },
      });
      expect(mockNavigate).toHaveBeenCalledWith({
        to: "/p/$project",
        params: { project: "shop" },
        replace: true,
      });
    });
  });

  it("renders unauthenticated instance invite with optional name and navigates home", async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === "/api/v1/meta") {
        return ok(SERVER_META);
      }
      if (path === "/api/v1/invites/{token}") {
        return ok({
          role: null,
          email: "newdev@agency.test",
          invited_by: "Agency Admin",
          expires_at: "2026-12-01T00:00:00.000Z",
        });
      }
      if (path === "/api/v1/auth/me") {
        return err(401, "unauthorized", "authentication required");
      }
      throw new Error(`unexpected GET ${path}`);
    });

    mockPost.mockImplementation(async () => {
      mockGet.mockImplementation(async (nextPath: string) => {
        if (nextPath === "/api/v1/auth/me") {
          return ok({
            user: {
              id: 3,
              email: "newdev@agency.test",
              name: "New Dev",
              is_admin: false,
              can_create_projects: true,
            },
            csrf_token: "csrf-2",
            memberships: [],
          });
        }
        return ok(SERVER_META);
      });
      return ok({});
    });

    renderWithQuery(<InviteAcceptPage token="tok-inst-1" />);

    expect(
      await screen.findByRole("heading", { name: "Set up your Acme Mailcubby account" }),
    ).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "dev-secret-pass-99" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Set password & sign in" }));

    await waitFor(() => {
      expect(mockPost).toHaveBeenCalledWith("/api/v1/invites/{token}/accept", {
        params: { path: { token: "tok-inst-1" } },
        body: { password: "dev-secret-pass-99" },
      });
      expect(mockNavigate).toHaveBeenCalledWith({ to: "/", replace: true });
    });
  });

  it("prompts sign in on 409 login_required with returnTo pointing back to invite", async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === "/api/v1/meta") {
        return ok(SERVER_META);
      }
      if (path === "/api/v1/invites/{token}") {
        return ok({
          project: { name: "Shop Staging", slug: "shop" },
          role: "member",
          email: "existing@agency.test",
          invited_by: "Admin",
          expires_at: "2026-12-01T00:00:00.000Z",
        });
      }
      return err(401, "unauthorized", "authentication required");
    });

    mockPost.mockResolvedValue(
      err(409, "login_required", "an account with this email already exists"),
    );

    renderWithQuery(<InviteAcceptPage token="tok-existing" />);
    expect(await screen.findByRole("heading", { name: "Join Shop Staging" })).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Full name"), { target: { value: "Existing User" } });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "some-password-123" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create account & join" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("already exists");
    // FR-056: project invites for admin-created accounts without a password also
    // return login_required, so the copy must point them to account setup first.
    expect(alert.textContent).toContain("If you have not set a password yet");
    expect(alert.textContent).toContain("password reset link");
    const signInLinks = screen.getAllByRole("link", { name: "Sign in to accept" });
    expect(signInLinks[0]?.getAttribute("href")).toBe("/login?returnTo=%2Finvite%2Ftok-existing");
  });

  it("allows logged-in user to accept project invite in one click", async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === "/api/v1/meta") {
        return ok(SERVER_META);
      }
      if (path === "/api/v1/invites/{token}") {
        return ok({
          project: { name: "Shop Staging", slug: "shop" },
          role: "member",
          email: "dev@agency.test",
          invited_by: "Admin",
          expires_at: "2026-12-01T00:00:00.000Z",
        });
      }
      if (path === "/api/v1/auth/me") {
        return ok({
          user: {
            id: 5,
            email: "dev@agency.test",
            name: "Dev",
            is_admin: false,
            can_create_projects: false,
          },
          csrf_token: "csrf-5",
          memberships: [],
        });
      }
      throw new Error(`unexpected GET ${path}`);
    });

    mockPost.mockResolvedValue(ok({ project: { name: "Shop Staging", slug: "shop" } }));

    renderWithQuery(<InviteAcceptPage token="tok-logged-in" />);
    const acceptBtn = await screen.findByRole("button", { name: "Accept invitation" });
    fireEvent.click(acceptBtn);

    await waitFor(() => {
      expect(mockPost).toHaveBeenCalledWith("/api/v1/invites/{token}/accept", {
        params: { path: { token: "tok-logged-in" } },
        body: {},
      });
    });
  });

  it("shows sign-out prompt for logged-in user opening an instance invite", async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === "/api/v1/meta") {
        return ok(SERVER_META);
      }
      if (path === "/api/v1/invites/{token}") {
        return ok({
          role: null,
          email: "other@agency.test",
          invited_by: "Admin",
          expires_at: "2026-12-01T00:00:00.000Z",
        });
      }
      return ok({
        user: {
          id: 1,
          email: "admin@agency.test",
          name: "Admin",
          is_admin: true,
          can_create_projects: true,
        },
        csrf_token: "csrf-1",
        memberships: [],
      });
    });

    renderWithQuery(<InviteAcceptPage token="tok-inst-signed-in" />);
    expect(await screen.findByRole("button", { name: "Sign out to accept" })).toBeTruthy();
  });

  it("clears all cached tenant queries when signing out from an invite page", async () => {
    let signedIn = true;
    mockGet.mockImplementation(async (path: string) => {
      if (path === "/api/v1/meta") {
        return ok(SERVER_META);
      }
      if (path === "/api/v1/invites/{token}") {
        return ok({
          role: null,
          email: "other@agency.test",
          invited_by: "Admin",
          expires_at: "2026-12-01T00:00:00.000Z",
        });
      }
      if (signedIn) {
        return ok({
          user: {
            id: 1,
            email: "admin@agency.test",
            name: "Admin",
            is_admin: true,
            can_create_projects: true,
          },
          csrf_token: "csrf-1",
          memberships: [],
        });
      }
      return err(401, "unauthorized", "authentication required");
    });
    mockPost.mockImplementation(async (path: string) => {
      if (path === "/api/v1/auth/logout") {
        signedIn = false;
        return { data: undefined, response: new Response(null, { status: 204 }) };
      }
      throw new Error(`unexpected POST ${path}`);
    });

    const { queryClient } = renderWithQuery(<InviteAcceptPage token="tok-clear-cache" />);
    queryClient.setQueryData(["projects", "secret-tenant", "messages", {}], { pages: [] });

    const signOutBtn = await screen.findByRole("button", { name: "Sign out to accept" });
    fireEvent.click(signOutBtn);

    await waitFor(() => {
      expect(
        queryClient.getQueryData(["projects", "secret-tenant", "messages", {}]),
      ).toBeUndefined();
    });
  });

  it("renders unavailable state when invite preview returns 410 gone", async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === "/api/v1/meta") {
        return ok(SERVER_META);
      }
      if (path === "/api/v1/invites/{token}") {
        return err(410, "gone", "invite link is invalid or has expired");
      }
      return err(401, "unauthorized", "authentication required");
    });

    renderWithQuery(<InviteAcceptPage token="expired-tok" />);
    expect(await screen.findByRole("heading", { name: "Invitation unavailable" })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain(
      "invalid, already used, or has expired",
    );
  });
});
