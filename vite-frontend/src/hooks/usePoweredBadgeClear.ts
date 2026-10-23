import { useEffect, useState } from "react";

// The fixed 「Powered by FLVX」 badge sits over the bottom of the viewport. On a
// page taller than the screen it covered whatever was there on first load
// (e.g. the 24 h chart axis, QA F09). It is now only shown while nothing can
// be under it: the page fits the screen, or it is scrolled to the end where
// the layout keeps an empty spacer for it.
const BOTTOM_SLACK_PX = 40;

const scrollTarget = (el: HTMLElement | null): Element | null => {
  if (el && el.scrollHeight > el.clientHeight + 1) {
    const overflowY = window.getComputedStyle(el).overflowY;

    if (overflowY === "auto" || overflowY === "scroll") return el;
  }

  return document.scrollingElement || document.documentElement;
};

export const usePoweredBadgeClear = (
  getContainer: () => HTMLElement | null,
  enabled: boolean,
  routeKey: string,
): boolean => {
  const [clear, setClear] = useState(true);

  useEffect(() => {
    if (!enabled || typeof window === "undefined") return;
    let frame = 0;
    const check = () => {
      frame = 0;
      const target = scrollTarget(getContainer());

      if (!target) return;
      const remaining =
        target.scrollHeight - target.scrollTop - target.clientHeight;

      setClear(remaining <= BOTTOM_SLACK_PX);
    };
    const schedule = () => {
      if (!frame) frame = window.requestAnimationFrame(check);
    };
    const container = getContainer();
    const observer =
      typeof ResizeObserver !== "undefined"
        ? new ResizeObserver(schedule)
        : null;

    const observeChildren = () => {
      if (!container || !observer) return;
      Array.from(container.children).forEach((child) =>
        observer.observe(child),
      );
    };
    // Route transitions replace the page element; watch the new one too.
    const mutations =
      container && typeof MutationObserver !== "undefined"
        ? new MutationObserver(() => {
            observeChildren();
            schedule();
          })
        : null;

    if (container && observer) {
      observer.observe(container);
      observeChildren();
    }
    if (container) mutations?.observe(container, { childList: true });
    container?.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("resize", schedule);
    // Route transitions animate in; check again once they settle.
    const settle = window.setTimeout(schedule, 400);

    schedule();

    return () => {
      if (frame) window.cancelAnimationFrame(frame);
      window.clearTimeout(settle);
      observer?.disconnect();
      mutations?.disconnect();
      container?.removeEventListener("scroll", schedule);
      window.removeEventListener("scroll", schedule);
      window.removeEventListener("resize", schedule);
    };
  }, [enabled, getContainer, routeKey]);

  return clear;
};
