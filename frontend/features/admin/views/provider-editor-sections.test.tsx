import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ProviderHealthField } from "./provider-editor-sections";

describe("ProviderHealthField", () => {
  it("allows an unhealthy provider to be explicitly restored", () => {
    const onUpdate = vi.fn();
    render(<ProviderHealthField values={{ healthy: "false" }} onUpdate={onUpdate} />);

    const control = screen.getByRole("checkbox");
    expect(control).not.toBeChecked();
    fireEvent.click(control);
    expect(onUpdate).toHaveBeenCalledWith("healthy", "true");
  });
});
