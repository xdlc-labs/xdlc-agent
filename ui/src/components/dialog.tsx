import { useEffect, useId, useRef, type ReactNode } from "react";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";

const FOCUSABLE =
  'a[href],button:not([disabled]),textarea:not([disabled]),input:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])';

type DialogProps = {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  /** modal = centered; drawer = right panel */
  variant?: "modal" | "drawer";
  className?: string;
};

export function Dialog({
  open,
  onClose,
  title,
  children,
  footer,
  variant = "modal",
  className,
}: DialogProps) {
  const titleId = useId();
  const panelRef = useRef<HTMLDivElement>(null);
  const prevFocus = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return;

    prevFocus.current = document.activeElement as HTMLElement | null;
    const panel = panelRef.current;

    const focusables = () =>
      [...(panel?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [])].filter(
        (el) => el.offsetParent !== null || el === document.activeElement,
      );

    queueMicrotask(() => {
      const list = focusables();
      (list[0] ?? panel)?.focus();
    });

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
        return;
      }
      if (e.key !== "Tab" || !panel) return;
      const list = focusables();
      if (list.length === 0) {
        e.preventDefault();
        panel.focus();
        return;
      }
      const first = list[0]!;
      const last = list[list.length - 1]!;
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };

    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      prevFocus.current?.focus();
    };
  }, [open, onClose]);

  if (!open) return null;

  const isDrawer = variant === "drawer";

  return (
    <div
      className={cn(
        "fixed inset-0 z-30 bg-background/80",
        isDrawer ? "flex justify-end" : "flex items-center justify-center px-4",
      )}
      onClick={onClose}
    >
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className={cn(
          "border border-border-strong bg-card outline-none",
          isDrawer
            ? "h-full w-full max-w-lg overflow-y-auto border-y-0 border-r-0"
            : "w-full max-w-md p-5",
          className,
        )}
        onClick={(e) => e.stopPropagation()}
      >
        {isDrawer ? (
          <div className="flex items-center justify-between border-b border-border px-5 py-4">
            <h2 id={titleId} className="font-mono text-[13px] text-foreground">
              {title}
            </h2>
            <button
              type="button"
              onClick={onClose}
              aria-label={t("dialog.close")}
              className="border border-border px-2 py-0.5 font-mono text-[11px] text-muted-foreground hover:text-foreground"
            >
              close
            </button>
          </div>
        ) : (
          <h3 id={titleId} className="font-mono text-[13px] text-foreground">
            {title}
          </h3>
        )}
        <div className={isDrawer ? undefined : "mt-2"}>{children}</div>
        {footer ? <div className={isDrawer ? "px-5 py-4" : "mt-5 flex justify-end gap-2"}>{footer}</div> : null}
      </div>
    </div>
  );
}
