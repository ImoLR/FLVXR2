// Idle-time prefetch of page chunks (plan 209): login page → 主页 chunks,
// 主页 → 规则 / 隧道 / 落地. Each group runs once per page load, one chunk at
// a time, only while the browser is idle; skipped on Data Saver and 2G. A
// failed prefetch is ignored (the page loads its chunk itself when opened).
// No timers or requests beyond the listed chunks.
//
// The router defines the groups (definePrefetch) and pages only name them, so
// a page chunk never imports the route table: a release that changes one page
// does not change (and re-download) the others.

type NetworkInformationLike = { saveData?: boolean; effectiveType?: string };
type IdleWindow = Window & {
  requestIdleCallback?: (
    callback: () => void,
    options?: { timeout: number },
  ) => number;
};

const IDLE_TIMEOUT_MS = 3000;
// After the visitor opens another page, its own requests go first.
const PAUSE_AFTER_NAVIGATION_MS = 2000;
let pausedUntil = 0;
const groups = new Map<string, Array<() => Promise<unknown>>>();
const started = new Set<string>();

export const definePrefetch = (
  key: string,
  loaders: Array<() => Promise<unknown>>,
): void => {
  groups.set(key, loaders);
};

const prefetchAllowed = (): boolean => {
  const connection = (
    navigator as Navigator & { connection?: NetworkInformationLike }
  ).connection;

  if (!connection) {
    return true;
  }
  if (connection.saveData) {
    return false;
  }

  // effectiveType: "slow-2g" | "2g" | "3g" | "4g"
  return !/2g$/.test(connection.effectiveType ?? "");
};

const whenIdle = (callback: () => void): void => {
  const idleWindow = window as IdleWindow;

  if (typeof idleWindow.requestIdleCallback === "function") {
    idleWindow.requestIdleCallback(callback, { timeout: IDLE_TIMEOUT_MS });
  } else {
    window.setTimeout(callback, 300);
  }
};

// Called on every in-app page change: the next queued chunk waits a moment.
export const pausePrefetch = (): void => {
  pausedUntil = Date.now() + PAUSE_AFTER_NAVIGATION_MS;
};

export const prefetchOnIdle = (key: string): void => {
  const loaders = groups.get(key);

  if (
    !loaders ||
    typeof window === "undefined" ||
    started.has(key) ||
    !prefetchAllowed()
  ) {
    return;
  }
  started.add(key);

  const run = (index: number): void => {
    if (index >= loaders.length) {
      return;
    }
    const wait = pausedUntil - Date.now();

    if (wait > 0) {
      window.setTimeout(() => run(index), wait);

      return;
    }
    whenIdle(() => {
      let next: Promise<unknown>;

      try {
        next = loaders[index]();
      } catch {
        next = Promise.resolve();
      }
      void next.catch(() => undefined).then(() => run(index + 1));
    });
  };

  run(0);
};
