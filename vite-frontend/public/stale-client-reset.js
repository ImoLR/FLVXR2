// Only served in place of missing legacy workbox-window chunks. Old bundles
// import { Workbox } and chain register().then(...), even while we reset.
export class Workbox {
  async register() {}
  addEventListener() {}
  messageSkipWaiting() {}
  async update() {}
}

async function resetStaleClient() {
  if (typeof window === "undefined") return;

  // Without a working per-tab guard, never force a reload.
  try {
    const storage = window.sessionStorage;
    const key = "flvx-stale-reset";
    const previousRaw = storage.getItem(key);
    const previous = previousRaw ? JSON.parse(previousRaw) : null;
    const now = Date.now();

    if (
      previous &&
      typeof previous.at === "number" &&
      now - previous.at < 10 * 60 * 1000
    ) {
      return;
    }
    const value = JSON.stringify({ at: now });

    storage.setItem(key, value);
    if (storage.getItem(key) !== value) return;
  } catch {
    return;
  }

  try {
    const container = window.navigator?.serviceWorker;

    if (typeof container?.getRegistrations === "function") {
      const registrations = await container.getRegistrations();

      await Promise.all(
        registrations.map(async (registration) => {
          try {
            if (typeof registration.unregister === "function") {
              await registration.unregister();
            }
          } catch {
            // Continue clearing the other registrations and caches.
          }
        }),
      );
    }
  } catch {
    // Storage APIs may be unavailable or denied.
  }

  try {
    const storage = window.caches;

    if (
      typeof storage?.keys === "function" &&
      typeof storage.delete === "function"
    ) {
      const keys = await storage.keys();

      await Promise.all(
        keys.map(async (key) => {
          try {
            await storage.delete(key);
          } catch {
            // Continue clearing the other caches.
          }
        }),
      );
    }
  } catch {
    // Still allow the guarded navigation if cleanup is denied.
  }

  if (typeof window.location?.reload === "function") window.location.reload();
}

void resetStaleClient().catch(() => {});
