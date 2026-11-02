import axios from "axios";

import Network from "./network";

// Plan 185 「订阅」: the user's own subscription links. A subscription stores
// references; the server renders nodes on every pull of /s/<token>.

export type SubFormat = "auto" | "clash" | "singbox" | "base64";
export type SubRuleMode = "cn_direct" | "global";

export interface SubRef {
  kind:
    | "forward_link"
    | "landing_direct"
    | "external_node"
    | "source_node"
    | "source_all";
  forwardId?: number;
  entry?: string;
  landingId?: number;
  externalId?: number;
  sourceId?: number;
  name?: string;
}

export interface SubSourceCount {
  id: number;
  name: string;
  all: boolean;
  count: number;
}

export interface SubCounts {
  panel: number;
  external: number;
  missing: number;
  sources: SubSourceCount[];
  total: number;
}

export interface SubItem {
  id: number;
  name: string;
  token: string;
  format: SubFormat;
  ruleMode: SubRuleMode;
  counts: SubCounts;
  items: number;
  lastPullAt: number;
  lastPullClient: string;
  lastPullNodes: number;
}

export interface SubSource {
  id: number;
  name: string;
  urlHint: string;
  nodeCount: number;
  lastOkAt: number;
  lastFetchAt: number;
  lastError: string;
  usedBy: number;
  error?: string;
}

export interface SubListData {
  enabled: boolean;
  subs: SubItem[];
  sources: SubSource[];
  cap: number;
  sourceCap: number;
  maxNodes: number;
}

export interface SubPoolNode {
  ref: SubRef;
  key: string;
  name: string;
  protocol: string;
  detail: string;
  chain?: boolean;
}

export interface SubPoolSource extends SubSource {
  nodes: SubPoolNode[];
  followKey: string;
}

export interface SubSelected {
  ref: SubRef;
  key: string;
  name?: string;
  protocol?: string;
  group: "rule" | "landing" | "external" | "source" | "source_all";
  sourceName?: string;
  count?: number;
  chain?: boolean;
  missing?: boolean;
}

export interface SubPool {
  rules: SubPoolNode[];
  landing: SubPoolNode[];
  external: SubPoolNode[];
  sources: SubPoolSource[];
  portForwards: number;
  selected: SubSelected[];
  maxNodes: number;
}

export interface SubSaveBody {
  id?: number;
  name: string;
  format: SubFormat;
  ruleMode: SubRuleMode;
  items: SubRef[];
}

export interface SubPreviewItem {
  name: string;
  protocol: string;
  detail: string;
  supported: boolean;
  reason: string;
}

export interface SubPreview {
  fetchId: string;
  items: SubPreviewItem[];
  count: number;
  info: number;
  invalid: number;
  truncated: boolean;
  /** plan 193: nodes in the subscription (when truncated) and the cap */
  total?: number;
  maxNodes?: number;
  urlHint: string;
}

export const subList = () => Network.post<SubListData>("/sub/list", {});
export const subPool = (id?: number) =>
  Network.post<SubPool>("/sub/pool", id ? { id } : {});
export const subCreate = (body: SubSaveBody) =>
  Network.post<SubItem>("/sub/create", body);
export const subUpdate = (body: SubSaveBody) =>
  Network.post<SubItem>("/sub/update", body);
export const subDelete = (id: number) => Network.post("/sub/delete", { id });
export const subReset = (id: number) =>
  Network.post<SubItem>("/sub/reset", { id });
export const subSourceFetch = (url: string) =>
  Network.post<SubPreview>("/sub/source/fetch", { url });
export const subSourceCreate = (name: string, fetchId: string) =>
  Network.post<SubSource>("/sub/source/create", { name, fetchId });
export const subSourceRefresh = (id: number) =>
  Network.post<SubSource>("/sub/source/refresh", { id });
export const subSourceDelete = (id: number) =>
  Network.post<{ removedFrom: number }>("/sub/source/delete", { id });

/** The public link (the panel origin, also inside the mobile app WebView). */
export function subLinkURL(token: string) {
  let origin = window.location.origin;

  try {
    origin = new URL(
      String(axios.defaults.baseURL || "/"),
      window.location.href,
    ).origin;
  } catch {
    // keep the page origin
  }

  return `${origin}/s/${token}`;
}

/** Token hidden in the link box (the full link is only copied). */
export function maskedSubLink(token: string) {
  const full = subLinkURL(token);

  if (token.length < 10) return full;

  return full.replace(token, `${token.slice(0, 4)}••••••${token.slice(-3)}`);
}

export const subFormatLabel: Record<SubFormat, string> = {
  auto: "自动识别客户端",
  clash: "Clash",
  singbox: "sing-box",
  base64: "通用",
};

export const subModeLabel: Record<SubRuleMode, string> = {
  cn_direct: "国内直连",
  global: "全部走代理",
};

/** 「3 分钟前」/「昨天 22:14」 style. */
export function subAgo(ms: number) {
  if (!ms) return "";
  const diff = Date.now() - ms;

  if (diff < 60_000) return "刚刚";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
  if (diff < 6 * 3_600_000) return `${Math.floor(diff / 3_600_000)} 小时前`;
  const d = new Date(ms);
  const hm = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  const day = (t: Date) => new Date(t).setHours(0, 0, 0, 0);
  const days = Math.round((day(new Date()) - day(d)) / 86_400_000);

  if (days === 0) return `今天 ${hm}`;
  if (days === 1) return `昨天 ${hm}`;

  return `${d.getMonth() + 1}月${d.getDate()}日 ${hm}`;
}

// 「新」 badge on the menu until the page was opened once.
const SEEN_KEY = "flvx_sub_link_seen";

export function subMenuSeen() {
  try {
    return localStorage.getItem(SEEN_KEY) === "1";
  } catch {
    return true;
  }
}

export function markSubMenuSeen() {
  try {
    if (localStorage.getItem(SEEN_KEY) !== "1") {
      localStorage.setItem(SEEN_KEY, "1");
      window.dispatchEvent(new Event("flvxSubLinkSeen"));
    }
  } catch {
    // ignore
  }
}

// Admin switch (设置 → 订阅链接), cached like the other config switches.
const SWITCH_KEY = "vite_config_sub_link_enabled";

export function subLinkEnabledCached() {
  try {
    return localStorage.getItem(SWITCH_KEY) !== "false";
  } catch {
    return true;
  }
}

let switchCheck: Promise<boolean> | null = null;

/** One config/get per page load; updates the cache and tells the menus. */
export function refreshSubLinkEnabled(): Promise<boolean> {
  if (!switchCheck) {
    switchCheck = Network.post<{ value?: string }>("/config/get", {
      name: "sub_link_enabled",
    })
      .then((res) => {
        const on = !(res.code === 0 && res.data?.value === "false");

        try {
          localStorage.setItem(SWITCH_KEY, on ? "true" : "false");
        } catch {
          // ignore
        }
        window.dispatchEvent(new Event("flvxSubLinkSwitch"));

        return on;
      })
      .catch(() => subLinkEnabledCached());
  }

  return switchCheck;
}

export function resetSubLinkSwitchCheck() {
  switchCheck = null;
}
