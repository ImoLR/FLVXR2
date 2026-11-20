// Plan 289 (user): open groups are no longer remembered anywhere — every page
// visit starts with every group collapsed. This removes the keys older builds
// stored (any account on this browser). Runs once per app start; a no-op once
// they are gone. Other remembered choices (转发 group order, grouping / view
// modes, filters, sidebar) are not touched.
const OPEN_STATE_KEY_PREFIXES = [
  // 落地 page (plans 286 / 287: v2 open sets, v3 accordion keys).
  "flvx:landing:region-open:",
  "flvx:landing:user-open:",
  "flvx:landing:my-server-open:",
  // 节点 page 分组 view (plan 287) + its pre-287 per-group collapse map.
  "flvx:node:group-open:",
  // 隧道 page (plan 178).
  "tunnel-group-open:",
  // 转发 page (plan 163/173) + the pre-163 collapsed map.
  "forward-group-expanded:",
  "forward-group-collapsed:",
];
const OPEN_STATE_EXACT_KEYS = [
  "flvx:landing:region-groups",
  "flvx:landing:user-groups",
  "node-group-collapsed-state",
];

export function forgetStoredGroupOpenState() {
  try {
    const stale: string[] = [];

    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);

      if (
        key &&
        (OPEN_STATE_EXACT_KEYS.includes(key) ||
          OPEN_STATE_KEY_PREFIXES.some((prefix) => key.startsWith(prefix)))
      ) {
        stale.push(key);
      }
    }
    stale.forEach((key) => localStorage.removeItem(key));
  } catch {
    /* private mode */
  }
}
