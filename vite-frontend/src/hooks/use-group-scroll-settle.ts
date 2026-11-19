import { useCallback, useLayoutEffect, useRef, useState } from "react";

import {
  buildPendingGroupScroll,
  settleGroupScroll,
  type PendingGroupScroll,
} from "@/utils/group-accordion";

/**
 * Plan 287: the 转发 / 隧道 page position correction (utils/group-accordion,
 * plans 173 / 178) for other group lists. Call `queue(key, open)` in the
 * header / footer tap handler, right before the open state changes. After
 * that render the tapped header first stays where it was (a long open group
 * above may just have collapsed — accordion), then an opened group's header
 * glides to the top (instant with prefers-reduced-motion); a closed one only
 * scrolls when it left the screen. The group wrapper carries data-group-key
 * and its header data-group-header. Runs once per tap, no scroll listeners.
 * Jumps / restored state just don't call it and keep their own scrolling.
 */
export function useGroupScrollSettle() {
  const pending = useRef<PendingGroupScroll | null>(null);
  const [seq, setSeq] = useState(0);

  useLayoutEffect(() => {
    const next = pending.current;

    if (!next) return;
    pending.current = null;
    settleGroupScroll(next);
  }, [seq]);

  return useCallback((key: string, open: boolean) => {
    pending.current = buildPendingGroupScroll(key, open, true, true);
    setSeq((value) => value + 1);
  }, []);
}
