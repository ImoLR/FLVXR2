// Page chunks (plan 209). Each page is its own chunk so the login page and
// 主页 no longer download every other page first. This table holds only
// `import()` calls (no React), so the tiny boot entry (main.tsx) can start the
// current route's chunk in parallel with the app shell.

export const pageLoaders = {
  changePassword: () => import("@/pages/change-password"),
  dashboard: () => import("@/pages/dashboard"),
  monitor: () => import("@/pages/monitor"),
  forward: () => import("@/pages/forward"),
  tunnel: () => import("@/pages/tunnel"),
  leikwan: () => import("@/pages/leikwan"),
  landing: () => import("@/pages/landing"),
  subLink: () => import("@/pages/sub-link"),
  node: () => import("@/pages/node"),
  nodeMonitor: () => import("@/pages/node-monitor"), // plan 255 线路监测 (admin)
  tls: () => import("@/pages/tls"),
  user: () => import("@/pages/user"),
  group: () => import("@/pages/group"),
  profile: () => import("@/pages/profile"),
  config: () => import("@/pages/config"),
  panelSharing: () => import("@/pages/panel-sharing"),
  shop: () => import("@/pages/shop"),
  myhome: () => import("@/pages/myhome"),
  adminPlans: () => import("@/pages/admin-plans"),
  adminOrders: () => import("@/pages/admin-orders"),
  adminPayment: () => import("@/pages/admin-payment"),
};

export type PageKey = keyof typeof pageLoaders;

export const routePages: Record<string, PageKey> = {
  "/change-password": "changePassword",
  "/dashboard": "dashboard",
  "/monitor": "monitor",
  "/forward": "forward",
  "/tunnel": "tunnel",
  "/leikwan": "leikwan",
  "/landing": "landing",
  "/sub": "subLink",
  "/node": "node",
  "/node-monitor": "nodeMonitor",
  "/tls": "tls",
  "/user": "user",
  "/group": "group",
  "/profile": "profile",
  "/config": "config",
  "/panel-sharing": "panelSharing",
  "/shop": "shop",
  "/myhome": "myhome",
  "/admin/plans": "adminPlans",
  "/admin/orders": "adminOrders",
  "/admin/payment": "adminPayment",
};

// Page key for a pathname ("/forward/" → forward), or null.
export const pageKeyForPath = (pathname: string): PageKey | null => {
  const path =
    pathname.length > 1 && pathname.endsWith("/")
      ? pathname.slice(0, -1)
      : pathname;

  return routePages[path] ?? null;
};

// The page a signed-in visitor sees at the current URL ("/" redirects to 主页,
// or 我的 for restricted accounts), or null (signed out / not a page route).
export const currentPageKey = (): PageKey | null => {
  try {
    const signedIn =
      localStorage.getItem("role_id") !== null ||
      Boolean(localStorage.getItem("token"));

    if (!signedIn) {
      return null;
    }
    const path = window.location.pathname;

    if (path === "/") {
      return localStorage.getItem("restricted") === "true"
        ? "myhome"
        : "dashboard";
    }

    return pageKeyForPath(path);
  } catch {
    return null;
  }
};
