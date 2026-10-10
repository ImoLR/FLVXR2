// Keeps long-lived tabs on the deployed frontend build.
//
// Two mechanisms share the same few triggers (no polling while visible):
// - the service worker update check (`registration.update()`), as before;
// - a build-id check: the bundle knows its own id (__APP_BUILD_ID__) and
//   compares it with the tiny static /version.json served by nginx. On a
//   mismatch the client drops its service workers + CacheStorage and reloads
//   once (the login token is kept: this is a refresh, not a logout).
//
// Triggers: app start (login does a full navigation, so this also covers
// "after login"), tab becoming visible and bfcache restore (both rate-limited
// by one shared 5-min gap). While the tab is hidden, only the existing 30-min
// service worker check runs. Nothing runs per API request.

const CHECK_MIN_GAP_MS = 5 * 60 * 1000;
const SW_HIDDEN_CHECK_INTERVAL_MS = 30 * 60 * 1000;
const RELOAD_GUARD_WINDOW_MS = 10 * 60 * 1000;
const VERSION_FETCH_TIMEOUT_MS = 10 * 1000;
const STEP_TIMEOUT_MS = 5 * 1000;
const RELOAD_GUARD_KEY = "flvx-build-reload";
const VERSION_URL = "/version.json";
const BUILD_ID_PATTERN = /^[0-9A-Za-z._-]{1,128}$/;

const CURRENT_BUILD =
  typeof __APP_BUILD_ID__ === "string" ? __APP_BUILD_ID__ : "";
const BUILD_CHECK_ENABLED =
  !import.meta.env.DEV && BUILD_ID_PATTERN.test(CURRENT_BUILD);

let swRegistration: ServiceWorkerRegistration | null = null;
let swHiddenIntervalStarted = false;
let watchStarted = false;
let lastForegroundCheckAt = 0;
let buildCheckInFlight: Promise<void> | null = null;

const isOffline = (): boolean =>
  typeof navigator !== "undefined" && navigator.onLine === false;

// Resolves with the promise's value, or undefined on error / after `ms`.
const settle = <T>(
  promise: Promise<T> | undefined,
  ms = STEP_TIMEOUT_MS,
): Promise<T | undefined> => {
  if (!promise) {
    return Promise.resolve(undefined);
  }

  return new Promise((resolve) => {
    const timer = window.setTimeout(() => resolve(undefined), ms);

    promise.then(
      (value) => {
        window.clearTimeout(timer);
        resolve(value);
      },
      () => {
        window.clearTimeout(timer);
        resolve(undefined);
      },
    );
  });
};

const updateServiceWorker = (): void => {
  const registration = swRegistration;

  if (!registration || registration.installing) {
    return;
  }
  try {
    void registration.update().catch(() => undefined);
  } catch {
    // ignore
  }
};

// Returns the deployed build id, or null when it cannot be determined
// (network error, 404, HTML fallback, malformed JSON, timeout).
const fetchDeployedBuild = async (): Promise<string | null> => {
  if (typeof fetch !== "function") {
    return null;
  }
  const controller =
    typeof AbortController === "function" ? new AbortController() : null;
  const timer = controller
    ? window.setTimeout(() => controller.abort(), VERSION_FETCH_TIMEOUT_MS)
    : 0;

  try {
    const response = await fetch(`${VERSION_URL}?t=${Date.now()}`, {
      cache: "no-store",
      credentials: "same-origin",
      signal: controller?.signal,
    });

    if (response.status !== 200) {
      return null;
    }
    const text = await response.text();

    if (text.length > 1024) {
      return null;
    }
    const data: unknown = JSON.parse(text);
    const build =
      data && typeof data === "object"
        ? (data as { build?: unknown }).build
        : undefined;

    return typeof build === "string" && BUILD_ID_PATTERN.test(build)
      ? build
      : null;
  } catch {
    return null;
  } finally {
    if (timer) {
      window.clearTimeout(timer);
    }
  }
};

// Reload-loop guard: at most one forced reload per target build per tab within
// RELOAD_GUARD_WINDOW_MS. Without working sessionStorage we never force-reload.
const claimReloadGuard = (target: string): boolean => {
  try {
    const storage = window.sessionStorage;
    const previousRaw = storage.getItem(RELOAD_GUARD_KEY);

    if (previousRaw) {
      const previous = JSON.parse(previousRaw) as {
        target?: unknown;
        at?: unknown;
      } | null;

      if (
        previous &&
        previous.target === target &&
        typeof previous.at === "number" &&
        Date.now() - previous.at < RELOAD_GUARD_WINDOW_MS
      ) {
        return false;
      }
    }
    const value = JSON.stringify({ target, at: Date.now() });

    storage.setItem(RELOAD_GUARD_KEY, value);

    return storage.getItem(RELOAD_GUARD_KEY) === value;
  } catch {
    return false;
  }
};

const refreshToBuild = async (target: string): Promise<void> => {
  if (!claimReloadGuard(target)) {
    return;
  }

  if (swRegistration) {
    await settle(swRegistration.update().catch(() => undefined));
  }

  const container =
    typeof navigator !== "undefined" && "serviceWorker" in navigator
      ? navigator.serviceWorker
      : undefined;

  if (container && typeof container.getRegistrations === "function") {
    const registrations = await settle(container.getRegistrations());

    if (registrations) {
      await settle(
        Promise.all(
          registrations.map((registration) =>
            registration.unregister().catch(() => false),
          ),
        ),
      );
    }
  }

  if (typeof caches !== "undefined" && typeof caches.keys === "function") {
    const keys = await settle(caches.keys());

    if (keys) {
      await settle(
        Promise.all(keys.map((key) => caches.delete(key).catch(() => false))),
      );
    }
  }

  window.location.reload();
};

// One check at a time per page; resolves when done (never rejects).
export const checkFrontendBuild = (): Promise<void> => {
  if (!BUILD_CHECK_ENABLED || isOffline()) {
    return Promise.resolve();
  }
  if (buildCheckInFlight) {
    return buildCheckInFlight;
  }

  buildCheckInFlight = (async () => {
    try {
      const deployed = await fetchDeployedBuild();

      if (deployed && deployed !== CURRENT_BUILD) {
        await refreshToBuild(deployed);
      }
    } catch {
      // ignore
    } finally {
      buildCheckInFlight = null;
    }
  })();

  return buildCheckInFlight;
};

const foregroundCheck = (): void => {
  if (isOffline() || Date.now() - lastForegroundCheckAt < CHECK_MIN_GAP_MS) {
    return;
  }
  lastForegroundCheckAt = Date.now();
  updateServiceWorker();
  void checkFrontendBuild();
};

// Called once the service worker is registered (production builds only).
export const attachServiceWorkerRegistration = (
  registration: ServiceWorkerRegistration,
): void => {
  swRegistration = registration;

  if (swHiddenIntervalStarted) {
    return;
  }
  swHiddenIntervalStarted = true;
  // Long-lived hidden tabs never navigate, so the browser never re-checks
  // sw.js on its own; the autoUpdate reload is invisible while hidden.
  window.setInterval(() => {
    if (document.visibilityState === "hidden" && !isOffline()) {
      updateServiceWorker();
    }
  }, SW_HIDDEN_CHECK_INTERVAL_MS);
};

// Called once at app start: initial build check + foreground triggers.
export const startFrontendFreshnessWatch = (): void => {
  if (watchStarted || typeof window === "undefined") {
    return;
  }
  watchStarted = true;
  lastForegroundCheckAt = Date.now();

  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      foregroundCheck();
    }
  });
  window.addEventListener("pageshow", (event) => {
    if (event.persisted) {
      foregroundCheck();
    }
  });

  void checkFrontendBuild();
};
