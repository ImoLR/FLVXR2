// Keeps long-lived tabs on the deployed frontend build.
//
// Two mechanisms share the same few triggers (no polling while visible):
// - the service worker update check (`registration.update()`), as before;
// - a build-id check: the page knows its own build id and
//   compares it with the tiny static /version.json served by nginx (plan 209:
//   the id comes from index.html's <meta name="flvx-build">, see build-id.ts). On a
//   mismatch the client reloads once (the login token is kept: this is a
//   refresh, not a logout). Plan 209: the service worker serves page loads
//   network-first, so a plain reload already lands on the deployed build and
//   the new build is downloaded once. Only if that reload still shows a stale
//   build (a stuck service worker) does the second step drop service workers +
//   CacheStorage and reload again — the old one-step behaviour.
//
// Triggers: app start, a successful login (the login page moves to 主页
// in-app since plan 209, so it calls checkFrontendBuild() itself), tab
// becoming visible and bfcache restore (both rate-limited by one shared 5-min
// gap), and a new service worker taking over. While the tab is hidden, only
// the existing 30-min service worker check runs. Nothing runs per API request.
// A page chunk that fails to load (files of an older build are gone after a
// release) also checks the build and reloads onto the deployed one.

import { showBuildUpdating } from "@/components/build-update-feedback";
import { currentBuildId } from "@/utils/build-id";

const CHECK_MIN_GAP_MS = 5 * 60 * 1000;
const SW_HIDDEN_CHECK_INTERVAL_MS = 30 * 60 * 1000;
const RELOAD_GUARD_WINDOW_MS = 10 * 60 * 1000;
const VERSION_FETCH_TIMEOUT_MS = 10 * 1000;
const STEP_TIMEOUT_MS = 5 * 1000;
const RELOAD_GUARD_KEY = "flvx-build-reload";
const VERSION_URL = "/version.json";
const BUILD_ID_PATTERN = /^[0-9A-Za-z._-]{1,128}$/;

const CURRENT_BUILD = currentBuildId();
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

type ReloadStage = "soft" | "hard";

// Reload-loop guard, per target build and tab within RELOAD_GUARD_WINDOW_MS:
// first a soft reload (keep service worker + caches), then — only if the page
// is still stale — one hard reload (drop them), then give up. A record written
// by an older build (no stage) counts as the hard step. Without working
// sessionStorage we never force-reload.
const claimReloadStage = (target: string): ReloadStage | null => {
  try {
    const storage = window.sessionStorage;
    const previousRaw = storage.getItem(RELOAD_GUARD_KEY);
    let stage: ReloadStage = "soft";

    if (previousRaw) {
      const previous = JSON.parse(previousRaw) as {
        target?: unknown;
        at?: unknown;
        stage?: unknown;
      } | null;

      if (
        previous &&
        previous.target === target &&
        typeof previous.at === "number" &&
        Date.now() - previous.at < RELOAD_GUARD_WINDOW_MS
      ) {
        if (previous.stage !== "soft") {
          return null;
        }
        stage = "hard";
      }
    }
    const value = JSON.stringify({ target, at: Date.now(), stage });

    storage.setItem(RELOAD_GUARD_KEY, value);

    return storage.getItem(RELOAD_GUARD_KEY) === value ? stage : null;
  } catch {
    return null;
  }
};

// The old one-step refresh: unregister every service worker and empty
// CacheStorage (never Clear-Site-Data: it logged users out, fork.43).
const dropServiceWorkersAndCaches = async (): Promise<void> => {
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
};

// Resolves true when the page is reloading onto `target`.
const refreshToBuild = async (target: string): Promise<boolean> => {
  const stage = claimReloadStage(target);

  if (!stage) {
    return false;
  }

  showBuildUpdating();

  const registration = swRegistration;

  if (registration) {
    let updating: Promise<unknown> | undefined;

    try {
      updating = registration.update();
    } catch {
      updating = undefined;
    }
    await settle(updating);
  }

  if (stage === "hard") {
    await dropServiceWorkersAndCaches();
  }

  window.location.reload();

  return true;
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

// A new service worker took over (another tab or the 30-min check found a
// release). The page itself only needs a reload when it is a stale build, so
// ask version.json instead of reloading unconditionally (plan 209: that was a
// second reload right after the freshness reload).
export const onServiceWorkerTakeover = (): void => {
  void checkFrontendBuild();
};

const CHUNK_RELOAD_KEY = "flvx-chunk-reload";

// At most one "chunk failed twice" reload per page path per tab within
// RELOAD_GUARD_WINDOW_MS.
const claimChunkReload = (): boolean => {
  try {
    const storage = window.sessionStorage;
    const path = window.location.pathname;
    const previous = JSON.parse(
      storage.getItem(CHUNK_RELOAD_KEY) || "null",
    ) as {
      path?: unknown;
      at?: unknown;
    } | null;

    if (
      previous &&
      previous.path === path &&
      typeof previous.at === "number" &&
      Date.now() - previous.at < RELOAD_GUARD_WINDOW_MS
    ) {
      return false;
    }
    storage.setItem(CHUNK_RELOAD_KEY, JSON.stringify({ path, at: Date.now() }));

    return true;
  } catch {
    return false;
  }
};

// A page chunk failed to load. After a release the files of the build this
// tab runs are gone: reload onto the deployed build (same guard as above).
// Otherwise (a network blip) retry once after a second; if that fails too,
// reload the page once (guarded) rather than leave it empty; only then give
// up and rethrow.
export const recoverChunkLoad = async <T>(
  error: unknown,
  retry: () => Promise<T>,
): Promise<T> => {
  const reloading = () => new Promise<T>(() => undefined);

  if (BUILD_CHECK_ENABLED && !isOffline()) {
    const deployed = await fetchDeployedBuild();

    if (
      deployed &&
      deployed !== CURRENT_BUILD &&
      (await refreshToBuild(deployed))
    ) {
      // Keep the page in its loading state until it unloads.
      return reloading();
    }
  }
  await new Promise((resolve) => window.setTimeout(resolve, 1000));

  try {
    return await retry();
  } catch {
    if (!isOffline() && claimChunkReload()) {
      window.location.reload();

      return reloading();
    }
    throw error;
  }
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
