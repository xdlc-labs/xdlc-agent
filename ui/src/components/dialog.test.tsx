import { useState } from "react";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Dialog } from "./dialog";

/**
 * Smoke tests for the shared Dialog component in both of its rendered
 * shapes: variant="modal" (centered — used by routes/actions.tsx for
 * confirm prompts) and variant="drawer" (right panel — used by
 * routes/repos.tsx for row detail).
 */

function ControlledDialog(props: { variant?: "modal" | "drawer"; initialOpen?: boolean }) {
  const { variant = "modal", initialOpen = true } = props;
  const [open, setOpen] = useState(initialOpen);
  return (
    <Dialog open={open} onClose={() => setOpen(false)} title="Test dialog" variant={variant}>
      <button type="button">first</button>
      <button type="button">second</button>
    </Dialog>
  );
}

describe.each([["modal"], ["drawer"]] as const)("Dialog variant=%s", (variant) => {
  it("renders nothing when closed", () => {
    render(
      <Dialog open={false} onClose={vi.fn()} title="Hidden" variant={variant}>
        content
      </Dialog>,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("renders the dialog with its title when open", () => {
    render(
      <Dialog open onClose={vi.fn()} title="My Title" variant={variant}>
        body content
      </Dialog>,
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog).toBeInTheDocument();
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(screen.getByText("My Title")).toBeInTheDocument();
    expect(screen.getByText("body content")).toBeInTheDocument();
  });

  it("calls onClose when Escape is pressed", () => {
    const onClose = vi.fn();
    render(
      <Dialog open onClose={onClose} title="Escaping" variant={variant}>
        <button type="button">only</button>
      </Dialog>,
    );
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("calls onClose when the backdrop is clicked but not when the panel is clicked", () => {
    const onClose = vi.fn();
    render(
      <Dialog open onClose={onClose} title="Backdrop" variant={variant}>
        <button type="button">inside</button>
      </Dialog>,
    );
    fireEvent.click(screen.getByRole("dialog"));
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("Backdrop").closest(".fixed")!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("traps Tab focus inside the panel, cycling from last back to first", async () => {
    const user = userEvent.setup();
    render(<ControlledDialog variant={variant} />);

    // Drawer variant renders its own "close" button ahead of the children,
    // so the focusable set differs per variant — read it from the DOM
    // rather than hardcoding it.
    const dialog = screen.getByRole("dialog");
    const buttons = within(dialog).getAllByRole("button");
    const first = buttons[0]!;
    const last = buttons[buttons.length - 1]!;

    // Dialog auto-focuses the first focusable element on open (async via
    // queueMicrotask), so wait for it rather than asserting synchronously.
    await waitFor(() => expect(first).toHaveFocus());

    // Tab through every focusable to reach the last one.
    for (let i = 1; i < buttons.length; i++) {
      await user.tab();
      expect(buttons[i]).toHaveFocus();
    }

    // Tab from the last focusable wraps back to the first.
    await user.tab();
    expect(first).toHaveFocus();

    // Shift+Tab from the first wraps back to the last.
    await user.tab({ shift: true });
    expect(last).toHaveFocus();
  });

  it("closes and unmounts on Escape when used with real state", () => {
    render(<ControlledDialog variant={variant} />);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("Dialog drawer-specific chrome", () => {
  it("renders a close button that calls onClose", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(
      <Dialog open onClose={onClose} title="Drawer" variant="drawer">
        drawer body
      </Dialog>,
    );
    await user.click(screen.getByRole("button", { name: /close/i }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
