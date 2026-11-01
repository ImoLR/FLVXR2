// Entry split (plan 187): the public probe page (/tz) loads only its own small
// chunk; everything else loads the admin / user app. On the 探针专用域名 every
// route goes to the probe page.
import "@/styles/globals.css";

const PROBE_PATH = /^\/tz(\/|$)/;
const apiBase = (
  (import.meta.env.VITE_API_BASE as string | undefined) || ""
).replace(/\/$/, "");

function startProbe() {
  // First paint: the snapshot request starts before the page chunk loads.
  window.__flvxProbeFirst = fetch(`${apiBase}/api/v1/probe/snapshot`, {
    cache: "no-cache",
  });
  void import("./probe/entry").then((m) => m.mountProbe());
}

function probeHostCheck(): Promise<boolean | null> {
  return fetch(`${apiBase}/api/v1/probe/host`, { cache: "no-store" })
    .then(async (r) =>
      r.ok ? (await r.json())?.data?.probeOnly === true : false,
    )
    .catch(() => null);
}

function bootAdmin() {
  const key = `flvx_probe_host:${window.location.hostname}`;
  let cached: string | null = null;

  try {
    cached = localStorage.getItem(key);
  } catch {
    /* private mode */
  }
  const check = probeHostCheck().then((probeOnly) => {
    if (probeOnly !== null) {
      try {
        localStorage.setItem(key, probeOnly ? "1" : "0");
      } catch {
        /* ignore */
      }
    }

    return probeOnly;
  });
  const toProbe = () => window.location.replace("/tz");
  const admin = import("./admin-main");

  if (cached === "0") {
    // Known panel host: start at once, re-check in the background.
    void admin.then((m) => m.startAdmin());
    void check.then((probeOnly) => probeOnly && toProbe());

    return;
  }
  // First visit on this host (or known probe host): decide before rendering
  // (the admin chunk downloads meanwhile; at most 1.5 s wait).
  const timeout = new Promise<null>((ok) => setTimeout(() => ok(null), 1500));

  void Promise.race([check, timeout]).then((probeOnly) => {
    if (probeOnly === true || (probeOnly === null && cached === "1")) {
      toProbe();

      return;
    }
    void admin.then((m) => m.startAdmin());
  });
}

if (PROBE_PATH.test(window.location.pathname)) {
  startProbe();
} else {
  bootAdmin();
}
