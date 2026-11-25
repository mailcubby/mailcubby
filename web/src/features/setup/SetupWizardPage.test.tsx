import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SetupWizardPage } from "@/features/setup/SetupWizardPage";

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  useRouterState: () => "/setup",
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

describe("SetupWizardPage step 1 (FR-040, UI 3.2)", () => {
  beforeEach(() => {
    mockGet.mockReset();
    mockPost.mockReset();
    mockGet.mockResolvedValue({
      data: {
        mode: "server",
        version: "dev",
        instance_name: "Mailcubby",
        setup_required: true,
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

  it("shows the Docker Compose, docker logs and binary ways to find the token", () => {
    renderWithQuery(<SetupWizardPage />);

    expect(
      screen.getByText(
        'docker compose -f deploy/compose.server.yml logs mailcubby | grep "setup token" | tail -1',
      ),
    ).toBeTruthy();
    expect(
      screen.getByText('docker logs <container> 2>&1 | grep "setup token" | tail -1'),
    ).toBeTruthy();
    expect(screen.getByText("mailcubby serve")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy Docker Compose command" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy Docker command" })).toBeTruthy();
  });

  it("shows the empty-token error only after a submit attempt", () => {
    renderWithQuery(<SetupWizardPage />);
    const input = screen.getByLabelText("Setup token");

    expect(screen.queryByRole("alert")).toBeNull();
    expect(input.getAttribute("aria-invalid")).toBeNull();

    // Typing and clearing the field is not a submit attempt.
    fireEvent.change(input, { target: { value: "abc" } });
    fireEvent.change(input, { target: { value: "" } });
    expect(screen.queryByRole("alert")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Continue to admin account" }));
    expect(screen.getByRole("alert").textContent).toContain("Paste the setup token");
    expect(input.getAttribute("aria-invalid")).toBe("true");

    // Typing clears the error.
    fireEvent.change(input, { target: { value: "tok" } });
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("returns to step 1 with the invalid-token error when setup is rejected (403)", async () => {
    mockPost.mockResolvedValue({
      error: { error: { code: "forbidden", message: "invalid setup token" } },
      response: new Response(null, { status: 403 }),
    });
    renderWithQuery(<SetupWizardPage />);

    fireEvent.change(screen.getByLabelText("Setup token"), { target: { value: "wrong-token" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to admin account" }));

    fireEvent.change(screen.getByLabelText("Full name"), { target: { value: "Agency Admin" } });
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "admin@agency.test" } });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "super-secret-pass-1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Continue to instance" }));
    fireEvent.click(await screen.findByRole("button", { name: "Complete setup" }));

    expect((await screen.findByRole("alert")).textContent).toContain("Invalid setup token");
    expect(screen.getByRole("heading", { name: "Step 1: Setup token" })).toBeTruthy();
  });
});
