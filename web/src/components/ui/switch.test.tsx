import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { Switch } from "@/components/ui/switch";

function Harness({ disabled = false }: { disabled?: boolean }) {
  const [on, setOn] = useState(false);
  return (
    <div>
      <label htmlFor="sw">Plus-addressing tags</label>
      <Switch
        id="sw"
        checked={on}
        disabled={disabled}
        onChange={(e) => setOn(e.target.checked)}
        className="mt-1"
      />
    </div>
  );
}

describe("Switch", () => {
  afterEach(() => {
    cleanup();
  });

  it("is a labelled native checkbox with role switch that toggles", () => {
    const { container } = render(<Harness />);
    const sw = screen.getByRole("switch", { name: "Plus-addressing tags" });
    expect(sw.tagName).toBe("INPUT");
    expect(sw.getAttribute("type")).toBe("checkbox");
    expect(sw.getAttribute("aria-checked")).toBe("false");

    fireEvent.click(sw);
    expect(sw.getAttribute("aria-checked")).toBe("true");
    expect((sw as HTMLInputElement).checked).toBe(true);

    // Clicking the label toggles it too (native label behaviour).
    fireEvent.click(screen.getByText("Plus-addressing tags"));
    expect(sw.getAttribute("aria-checked")).toBe("false");

    // className goes on the wrapper; the knob is a decorative sibling element.
    const wrapper = sw.parentElement;
    expect(wrapper?.className).toContain("mt-1");
    const knob = sw.nextElementSibling;
    expect(knob?.getAttribute("aria-hidden")).toBe("true");
    expect(container.querySelectorAll('[role="switch"]')).toHaveLength(1);
  });

  it("passes disabled to the native input", () => {
    render(<Harness disabled />);
    const sw = screen.getByRole("switch", { name: "Plus-addressing tags" });
    expect((sw as HTMLInputElement).disabled).toBe(true);
  });
});
