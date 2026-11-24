import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ResetPasswordPage } from "@/features/auth/ResetPasswordPage";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, className }: { children: ReactNode; to: string; className?: string }) => (
    <a href={to} className={className}>
      {children}
    </a>
  ),
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

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe("ResetPasswordPage (FR-046)", () => {
  beforeEach(() => {
    mockGet.mockReset();
    mockPost.mockReset();
    mockGet.mockResolvedValue({
      data: {
        mode: "server",
        version: "dev",
        instance_name: "Acme Mailcubby",
        setup_required: false,
        base_url: "http://localhost:8025",
        features: { remote_images: "opt-in", recipient_routing: false },
        inbox_domain: "",
        smtp: { host: "localhost", port: 587, tls_mode: "none" },
      },
      response: new Response(null, { status: 200 }),
    });
  });

  afterEach(() => {
    cleanup();
  });

  it("rejects short passwords client-side and submits valid reset token", async () => {
    mockPost.mockResolvedValue({
      response: new Response(null, { status: 204 }),
    });

    renderWithQuery(<ResetPasswordPage token="reset-tok-1" />);

    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "short" } });
    fireEvent.click(screen.getByRole("button", { name: "Reset password" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(mockPost).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("New password"), {
      target: { value: "brand-new-secret-pass" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Reset password" }));

    await waitFor(() => {
      expect(mockPost).toHaveBeenCalledWith("/api/v1/password-reset/{token}", {
        params: { path: { token: "reset-tok-1" } },
        body: { password: "brand-new-secret-pass" },
      });
    });

    expect(await screen.findByRole("link", { name: "Sign in" })).toBeTruthy();
  });

  it("shows clear message when reset link is expired or invalid (410)", async () => {
    mockPost.mockResolvedValue({
      error: { error: { code: "gone", message: "reset link invalid" } },
      response: new Response(null, { status: 410 }),
    });

    renderWithQuery(<ResetPasswordPage token="bad-tok" />);
    fireEvent.change(screen.getByLabelText("New password"), {
      target: { value: "brand-new-secret-pass" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Reset password" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("invalid or has expired");
  });
});
