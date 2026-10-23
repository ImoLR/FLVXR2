import { useEffect } from "react";

export const PULL_TO_REFRESH_EVENT = "flvx:pulltorefresh";
export const PULL_TO_REFRESH_DONE_EVENT = "flvx:pulltorefresh:done";

// The global H5 pull-to-refresh passes this detail; every page handler counts
// itself in synchronously so the spinner knows whom to wait for.
export type PullToRefreshDetail = { claims: number };

export function usePullToRefresh(callback: () => unknown) {
  useEffect(() => {
    const handler = (event: Event) => {
      const detail = (event as CustomEvent<PullToRefreshDetail | undefined>)
        .detail;

      if (detail) detail.claims += 1;
      // "done" only after the reload finished (or failed), never before.
      void (async () => {
        try {
          await callback();
        } catch {
          // The page shows its own load errors.
        } finally {
          window.dispatchEvent(new CustomEvent(PULL_TO_REFRESH_DONE_EVENT));
        }
      })();
    };

    window.addEventListener(PULL_TO_REFRESH_EVENT, handler);

    return () => window.removeEventListener(PULL_TO_REFRESH_EVENT, handler);
  }, [callback]);
}
