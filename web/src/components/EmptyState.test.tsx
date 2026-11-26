import { cleanup, render, screen } from "@testing-library/react";
import { Inbox } from "lucide-react";
import { afterEach, describe, expect, it } from "vitest";
import { EmptyState } from "@/components/EmptyState";

describe("EmptyState", () => {
  afterEach(() => {
    cleanup();
  });

  it("renders a heading, one line of help, one action and a decorative icon", () => {
    const { container } = render(
      <EmptyState
        icon={Inbox}
        title="No messages yet"
        description="New messages appear here."
        action={<button type="button">Send test email</button>}
      />,
    );

    expect(screen.getByRole("heading", { level: 2, name: "No messages yet" })).toBeTruthy();
    expect(screen.getByText("New messages appear here.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Send test email" })).toBeTruthy();
    const icon = container.querySelector("svg");
    expect(icon?.closest('[aria-hidden="true"]')).not.toBeNull();
  });

  it("supports a level 3 heading without description or action", () => {
    render(<EmptyState icon={Inbox} title="Waiting for mail" headingLevel={3} size="sm" />);

    expect(screen.getByRole("heading", { level: 3, name: "Waiting for mail" })).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
