// Plan 173 (rules page), shared with the tunnel page in plan 178: keep a
// tapped group header in view after it opened / closed. Runs once per click
// from a layout effect — no scroll listeners.

export interface PendingGroupScroll {
  key: string;
  open: boolean;
  toTop: boolean;
  anchorTop: number | null;
}

// The group wrapper carries data-group-key, its header data-group-header.
export const findGroupElement = (groupKey: string): HTMLElement | null =>
  Array.from(document.querySelectorAll<HTMLElement>("[data-group-key]")).find(
    (node) => node.dataset.groupKey === groupKey,
  ) || null;

export const findGroupHeader = (groupKey: string): HTMLElement | null =>
  findGroupElement(groupKey)?.querySelector<HTMLElement>(
    "[data-group-header]",
  ) || null;

// Nearest ancestor that actually scrolls (desktop: the layout <main>); null
// means the page itself scrolls (phone layout).
export const findScrollParent = (el: HTMLElement): HTMLElement | null => {
  for (
    let node = el.parentElement;
    node && node !== document.body && node !== document.documentElement;
    node = node.parentElement
  ) {
    const { overflowY } = window.getComputedStyle(node);

    if (
      (overflowY === "auto" || overflowY === "scroll") &&
      node.scrollHeight > node.clientHeight
    ) {
      return node;
    }
  }

  return null;
};

// Captures where the tapped header is now (anchor) before the re-render.
export const buildPendingGroupScroll = (
  groupKey: string,
  open: boolean,
  toTop: boolean,
  anchor: boolean,
): PendingGroupScroll => {
  const header = anchor ? findGroupHeader(groupKey) : null;

  return {
    key: groupKey,
    open,
    toTop,
    anchorTop: header ? header.getBoundingClientRect().top : null,
  };
};

// After the click has been rendered: first keep the tapped header where it
// was (a long group above may just have collapsed), then scroll an opened
// group so its header sits at the top; a closed one only if it left the
// screen.
export const settleGroupScroll = (pending: PendingGroupScroll) => {
  const wrapper = findGroupElement(pending.key);
  const header = wrapper?.querySelector<HTMLElement>("[data-group-header]");

  if (!wrapper || !header) return;
  const scroller = findScrollParent(wrapper);
  const viewTop = scroller
    ? scroller.getBoundingClientRect().top + scroller.clientTop
    : 0;
  const viewBottom = scroller
    ? viewTop + scroller.clientHeight
    : window.innerHeight;
  // The sticky top (safe area on phones) is mirrored in scroll-margin-top.
  const restTop =
    viewTop +
    (parseFloat(window.getComputedStyle(wrapper).scrollMarginTop) || 0);
  const scrollByY = (dy: number, smooth: boolean) => {
    if (Math.abs(dy) < 1) return;
    // "auto" = instant here (no CSS scroll-behavior is set anywhere).
    (scroller ?? window).scrollBy({
      top: dy,
      behavior: smooth ? "smooth" : "auto",
    });
  };
  // Plan 178: an open header sticks, so while its group starts above the
  // screen (a long group above just collapsed) its box reports the pinned
  // spot. Measure where it really sits: the group's top edge (the header is
  // the group's first child).
  const headerTop = () =>
    window.getComputedStyle(header).position === "sticky"
      ? wrapper.getBoundingClientRect().top + wrapper.clientTop
      : header.getBoundingClientRect().top;

  if (pending.anchorTop !== null) {
    scrollByY(headerTop() - pending.anchorTop, false);
  }
  const top = headerTop();

  if (pending.open && pending.toTop) {
    const reduceMotion = window.matchMedia?.(
      "(prefers-reduced-motion: reduce)",
    ).matches;

    scrollByY(top - restTop, !reduceMotion);
  } else if (top + header.offsetHeight <= viewTop || top >= viewBottom) {
    // Only when it has left the screen completely (a tapped header is
    // kept where it was by the anchor above).
    scrollByY(top - restTop, false);
  }
};
