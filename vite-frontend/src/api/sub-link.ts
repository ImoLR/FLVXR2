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
  /** plan 256: how many 分流 entries (0 = none) */
  routing?: number;
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
  /** plan 256: the user's 外部节点 count */
  externals?: number;
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
  // Plan 256 「分流」 (absent on a panel before plan 256).
  routing?: SubRouteEntry[];
  routingCatalog?: SubRouteCategory[];
  routingDefaults?: string[];
  routingLimits?: SubRouteLimits;
  /** plan 261: the stored 规则集链接 sets of this subscription, by id */
  routingLinks?: Record<string, SubLinkSet>;
}

/**
 * Plan 256 「分流」: one row of a subscription's list = one group in the
 * client. exit: select (节点选择) | auto (自动选择) | direct | reject |
 * node:<item key> (a node of this subscription).
 */
export interface SubRouteEntry {
  type: "builtin" | "custom" | "link";
  key?: string;
  name?: string;
  lines?: string[];
  exit: string;
  /** plan 261 link entry: the stored rule set (SubLinkSet.id) */
  ref?: number;
}

/** Plan 261 「规则集链接」: what the owner sees of a stored set (never the URL). */
export interface SubLinkCounts {
  domain: number;
  suffix: number;
  keyword: number;
  ip: number;
  ignored: number;
}

export interface SubLinkSet {
  id: number;
  urlHint: string;
  ruleCount: number;
  counts: SubLinkCounts;
  fetchedAt: number;
  lastTryAt: number;
  lastError: string;
  failCount: number;
  /** first rules (获取 / 立即更新 only) */
  sample?: { kind: "full" | "suffix" | "keyword" | "ip"; value: string }[];
  /** 立即更新 failed (the last good copy stays) */
  error?: string;
}

export interface SubRouteCategory {
  key: string;
  icon: string;
  name: string;
  desc: string;
  exit: string;
}

export interface SubRouteLimits {
  entries: number;
  lines: number;
  lineLen: number;
  nameLen: number;
  /** plan 261: link entries per subscription / stored sets per account */
  links?: number;
  linkSets?: number;
  linkRules?: number;
}

export interface SubSaveBody {
  id?: number;
  name: string;
  format: SubFormat;
  ruleMode: SubRuleMode;
  items: SubRef[];
  routing?: SubRouteEntry[];
}

export interface SubProbeResult {
  id: number;
  ok: boolean;
  ms?: number;
  udp?: boolean;
  error?: string;
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
/** Plan 261 「规则集链接」 获取: the panel downloads, validates and stores it. */
export const subLinkFetch = (url: string) =>
  Network.post<SubLinkSet>("/sub/ruleset/fetch", { url }, { timeout: 40000 });
/** Plan 261 立即更新 (a failure keeps the last good copy, see error). */
export const subLinkRefresh = (id: number) =>
  Network.post<SubLinkSet>("/sub/ruleset/refresh", { id }, { timeout: 40000 });
/** Plan 256 「检测全部」: TCP connect time from the panel to each 外部节点. */
export const subExternalProbe = () =>
  Network.post<{ items: SubProbeResult[] }>(
    "/sub/external/probe",
    {},
    { timeout: 40000 },
  );

// Plan 260 「全部用户」: the admin's read-only view of every user's
// subscriptions (no tokens, source URLs, credentials or addresses).

/** none = 未设置 (old output), default = 默认 8 组, custom = 自定义 N 组 */
export type SubAdminRoutingKind = "none" | "default" | "custom";

export interface SubAdminSub {
  id: number;
  name: string;
  format: SubFormat;
  ruleMode: SubRuleMode;
  nodes: number;
  routing: SubAdminRoutingKind;
  routingCount: number;
  lastPullAt: number;
  lastPullClient: string;
  lastPullNodes: number;
  createdTime: number;
}

export interface SubAdminSource {
  id: number;
  name: string;
  nodes: number;
  lastOkAt: number;
  lastFetchAt: number;
  error: string;
}

export interface SubAdminUser {
  id: number;
  name: string;
  lastPullAt: number;
  subs: SubAdminSub[];
  sources: SubAdminSource[];
}

export interface SubAdminListData {
  enabled: boolean;
  users: SubAdminUser[];
}

export interface SubAdminItem {
  group: SubSelected["group"];
  name: string;
  protocol?: string;
  chain?: boolean;
  sourceName?: string;
  count?: number;
  missing?: boolean;
}

/** One 分流 row; a node exit comes as exit "node" + the node's name. */
export interface SubAdminRoute {
  type: "builtin" | "custom" | "link";
  key?: string;
  name?: string;
  lines?: string[];
  exit: string;
  exitName?: string;
  exitMissing?: boolean;
  /** plan 261 link entry: count + status only (never the URL) */
  ruleCount?: number;
  fetchedAt?: number;
  linkFailed?: boolean;
  linkMissing?: boolean;
}

export interface SubAdminDetail {
  id: number;
  name: string;
  userId: number;
  user: string;
  format: SubFormat;
  ruleMode: SubRuleMode;
  createdTime: number;
  lastPullAt: number;
  lastPullClient: string;
  lastPullNodes: number;
  items: SubAdminItem[];
  /** null = 未设置分流 (NULL or empty list: the output from before 分流) */
  routing: SubAdminRoute[] | null;
  routingKind: SubAdminRoutingKind;
  routingCatalog: SubRouteCategory[];
}

export const subAdminList = () =>
  Network.post<SubAdminListData>("/sub/admin/list", {});
export const subAdminDetail = (id: number) =>
  Network.post<SubAdminDetail>("/sub/admin/detail", { id });

/** 「刚刚」「2 小时前」「昨天」「7 天前」; 0 → 「从未拉取」. */
export function subRelative(ms: number, never = "从未拉取") {
  if (!ms) return never;
  const diff = Date.now() - ms;

  if (diff < 60_000) return "刚刚";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`;
  const day = (t: number) => new Date(t).setHours(0, 0, 0, 0);
  const days = Math.max(
    1,
    Math.round((day(Date.now()) - day(ms)) / 86_400_000),
  );

  return days === 1 ? "昨天" : `${days} 天前`;
}

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
  base64: "节点链接",
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
