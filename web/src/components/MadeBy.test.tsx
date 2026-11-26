import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PublicFooter } from "@/components/MadeBy";

describe("PublicFooter", () => {
  it("credits the author and the company with followable links", () => {
    render(<PublicFooter />);
    const author = screen.getByRole("link", { name: "Fábio Ferreira" });
    const company = screen.getByRole("link", { name: "NOOP" });
    expect(author.getAttribute("href")).toBe("https://x.com/fabiomlferreira");
    expect(company.getAttribute("href")).toBe("https://noop.pt");
    // No nofollow: public instances should pass the credit on to search engines.
    expect(author.getAttribute("rel")).toBe("noopener");
    expect(company.getAttribute("rel")).toBe("noopener");
    expect(screen.queryByText(/Powered by/)).toBeNull();
  });

  it("adds Powered by Mailcubby when the instance has its own name (audit V6)", () => {
    render(<PublicFooter poweredBy />);
    expect(screen.getByText(/Powered by/).textContent).toBe("Powered by Mailcubby");
    expect(screen.getByRole("link", { name: "Mailcubby" }).getAttribute("href")).toBe(
      "https://github.com/mailcubby/mailcubby",
    );
  });
});
