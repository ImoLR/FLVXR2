import { useCallback, useEffect, useRef, useState } from "react";

// Keep the latest snapshot in a ref, including while hidden. Only publishing it
// causes a render; superseded values never accumulate in a message queue.
export function useBatchedRealtimeState<T>(initial: T) {
  const [state, setState] = useState(initial);
  const pending = useRef(initial);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastFlush = useRef<number | null>(null);

  const flush = useCallback(() => {
    timer.current = null;
    if (document.visibilityState === "hidden") return;
    lastFlush.current = performance.now();
    setState(pending.current);
  }, []);

  const update = useCallback(
    (updater: (prev: T) => T) => {
      const next = updater(pending.current);

      if (Object.is(next, pending.current)) return;
      pending.current = next;
      if (document.visibilityState === "hidden" || timer.current !== null)
        return;
      const delay =
        lastFlush.current === null
          ? 0
          : Math.max(0, 1000 - (performance.now() - lastFlush.current));

      if (delay === 0) flush();
      else timer.current = setTimeout(flush, delay);
    },
    [flush],
  );

  useEffect(() => {
    const onVisibility = () => {
      if (timer.current !== null) clearTimeout(timer.current);
      timer.current = null;
      if (document.visibilityState !== "hidden") flush();
    };

    document.addEventListener("visibilitychange", onVisibility);

    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      if (timer.current !== null) clearTimeout(timer.current);
    };
  }, [flush]);

  return [state, update] as const;
}
