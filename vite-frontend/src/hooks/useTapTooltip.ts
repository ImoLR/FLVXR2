import { useCallback, useEffect, useRef, useState } from "react";

// Click/tap-only chart tooltips (plan 156).
//
// Recharts' default tooltip follows the mouse and also follows a finger that
// merely scrolls the page across the chart. With `trigger="click"` hover and
// touchmove only update recharts' hover state, which the click-mode tooltip,
// cursor and active dot ignore. This hook adds the closing rules recharts does
// not have: tapping the same point again, tapping outside the chart or any
// scroll closes it. Recharts keeps the last clicked index in its own store, so
// "closed" is enforced with `Tooltip active={false}` + `Line activeDot={false}`.
//
// Usage:
//   const tap = useTapTooltip();
//   <div ref={tap.containerRef}>
//     <LineChart onClick={tap.onChartClick}>
//       <Tooltip {...tap.tooltipProps} />
//       <Line activeDot={tap.activeDot()} />

interface ChartClickState {
  activeIndex?: unknown;
  activeTooltipIndex?: unknown;
}

interface ChartClickEvent {
  clientX?: number;
  clientY?: number;
}

// Recharts only moves the click index when the click lands inside the plot
// area; outside it (axes, margins) the old index would come back, so such a
// click just closes the tooltip.
const isInsidePlot = (
  container: HTMLElement | null,
  event: ChartClickEvent | undefined,
): boolean => {
  if (!container || event?.clientX == null || event.clientY == null) {
    return true;
  }
  const grid = container.querySelector(".recharts-cartesian-grid");

  if (!grid) return true;
  const rect = grid.getBoundingClientRect();

  if (rect.width === 0 || rect.height === 0) return true;

  return (
    event.clientX >= rect.left &&
    event.clientX <= rect.right &&
    event.clientY >= rect.top &&
    event.clientY <= rect.bottom
  );
};

export const useTapTooltip = <T extends HTMLElement = HTMLDivElement>() => {
  const containerRef = useRef<T | null>(null);
  const [open, setOpenState] = useState(false);
  const openRef = useRef(false);
  const lastIndexRef = useRef<string | null>(null);

  const setOpen = useCallback((next: boolean) => {
    openRef.current = next;
    setOpenState(next);
  }, []);

  const onChartClick = useCallback(
    (state?: ChartClickState | null, event?: ChartClickEvent) => {
      const raw = state?.activeTooltipIndex ?? state?.activeIndex;
      const index = raw == null ? null : String(raw);

      if (index == null || !isInsidePlot(containerRef.current, event)) {
        setOpen(false);

        return;
      }
      const sameAsOpen = openRef.current && lastIndexRef.current === index;

      lastIndexRef.current = index;
      setOpen(!sameAsOpen);
    },
    [setOpen],
  );

  // Close on a press outside the chart or on any scroll. Attached only while
  // a tooltip is open and removed again on close/unmount.
  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onPointerDown = (event: PointerEvent) => {
      const container = containerRef.current;

      if (container && event.target instanceof Node) {
        if (container.contains(event.target)) return;
      }
      close();
    };

    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("scroll", close, true);

    return () => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("scroll", close, true);
    };
  }, [open, setOpen]);

  const activeDot = useCallback(
    <D>(dot?: D): D | boolean => (open ? (dot ?? true) : false),
    [open],
  );

  return {
    activeDot,
    containerRef,
    onChartClick,
    open,
    tooltipProps: {
      trigger: "click" as const,
      // undefined = let recharts show the clicked point; false = hidden.
      active: open ? undefined : false,
    },
  };
};
