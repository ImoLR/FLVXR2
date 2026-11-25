import Network from "./network";

// 用户专属域名 (plan 303): one private CNAME per user per entry node, pointing
// at the node's DDNS main domain. Admin only (all routes under /node/).

export type UserSubState = "active" | "paused" | "retired";
/** capped (round 3): waits for room in the zone — no Cloudflare call. */
export type UserSubSync = "pending" | "synced" | "failed" | "capped";

export interface UserSubForward {
  id: number;
  name: string;
  port: number;
  /** plan 305: 保持原地址 — keeps the node address (no name). */
  kept?: boolean;
}

/** Plan 305: one DDNS domain user names point at (several → one name each). */
export interface UserSubRecord {
  key: string;
  domain: string;
  /** 电信 / 联通 / 移动 / 主地址 / 附加地址 */
  label: string;
  synced: boolean;
}

/** Plan 305: a forward kept on its old address (已有转发保持原地址). */
export interface UserSubKeptForward {
  forwardId: number;
  name: string;
  userId: number;
  userName: string;
  port: number;
}

export interface UserSubItem {
  id: number;
  userId: number;
  userName: string;
  label: string;
  domain: string;
  state: UserSubState;
  /** paused: expired | disabled | unused | node; retired: user_deleted | node_deleted | unused | renamed | admin */
  reason: string;
  status: UserSubSync;
  lastError: string;
  lastSyncAt: number;
  nodeId: number;
  nodeName: string;
  originNodeId: number;
  originNodeName: string;
  /** node view: "in" = re-pointed here from another node, "out" = re-pointed away */
  moved: "" | "in" | "out";
  target: string;
  blocked: boolean;
  forwards: UserSubForward[];
  landings: number;
  createdTime: number;
  retiredAt: number;
  unusedSince: number;
  /** Round 3: first verified in Cloudflare (0 = 正在就绪). */
  readyAt: number;
  failCount: number;
  /** Round 3: withheld from the user — no address at all (名额已满 / 创建失败). */
  hidden: boolean;
  /** Plan 305: the DDNS record ("" = main), its label on multi-record nodes, its domain. */
  recordDomain?: string;
  recordLabel?: string;
  targetDomain?: string;
}

export interface UserSubZone {
  providerId: number;
  name: string;
  used: number;
  quota: number;
  quotaGuess: boolean;
  wildcard: boolean;
  userRecords: number;
  foreign: number;
  checkedAt: number;
  error: string;
}

export interface UserSubTarget {
  id: number;
  name: string;
  mainDomain: string;
}

export interface UserSubList {
  items: UserSubItem[];
  history: UserSubItem[];
  counts: Partial<
    Record<
      | "active"
      | "paused"
      | "retired"
      | "failed"
      | "paused_expired"
      | "paused_disabled"
      | "capped"
      | "hidden"
      | "readying",
      number
    >
  >;
  node?: {
    id: number;
    name: string;
    toggle: boolean;
    ddnsEnabled: boolean;
    mainDomain: string;
    mainIps: string[] | null;
    mainSynced: boolean;
    v6Only: boolean;
    effective: boolean;
    /** plan 305 */
    records?: UserSubRecord[];
    keptForwards?: UserSubKeptForward[];
    keptLandings?: number;
  };
  zone?: UserSubZone | null;
  capped?: number;
  waiting?: number;
  targets: UserSubTarget[];
}

/** Node list pill (inside the node's DDNS view). */
export interface UserSubNodeView {
  toggle: boolean;
  effective: boolean;
  mainDomain: string;
  active: number;
  paused: number;
  failed: number;
  pending: number;
  capped: number;
  /** Round 3: fresh names withheld from their users (capped / keeps failing). */
  hidden: number;
  waiting: number;
  wildcard: boolean;
  zoneError: string;
  attention: boolean;
  /** plan 305: DDNS domains (only when several) and kept forwards / landings. */
  records?: UserSubRecord[];
  kept?: number;
}

export const listUserSubdomains = (q: {
  nodeId?: number;
  userId?: number;
  retired?: boolean;
}) => Network.post<UserSubList>("/node/user-subdomain/list", q);
export const repointUserSubdomain = (id: number, nodeId: number) =>
  Network.post(
    "/node/user-subdomain/repoint",
    { id, nodeId },
    { timeout: 60000 },
  );
export const renameUserSubdomain = (id: number) =>
  Network.post("/node/user-subdomain/rename", { id }, { timeout: 60000 });
export const deleteUserSubdomain = (id: number) =>
  Network.post("/node/user-subdomain/delete", { id }, { timeout: 60000 });
export const allowUserSubdomain = (id: number) =>
  Network.post("/node/user-subdomain/allow", { id }, { timeout: 60000 });
export const syncUserSubdomains = (q: { nodeId?: number; userId?: number }) =>
  Network.post<UserSubList>("/node/user-subdomain/sync", q, {
    timeout: 150000,
  });

export const PAUSE_REASON: Record<string, string> = {
  expired: "到期",
  disabled: "停用",
  unused: "同步中",
  node: "节点已关闭",
};

/**
 * Plan 303 round 2: tag next to a handed-out name (node dialog, user edit
 * section, forward address). 到期 / 停用 = record deleted on purpose, the
 * name stays and comes back on 续期 / 启用. Round 3: 「正在就绪」 = the name
 * is final, its record is being created (usable in a few seconds); users see
 * it too. The main domain is never handed out (no 「暂用主域名」 any more).
 */
export const ENTRY_TAG_TEXT: Record<string, string> = {
  expired: "已过期 · 记录已删除",
  disabled: "已停用 · 记录已删除",
  deleted: "已删除 · 不再解析",
  pending: "正在就绪",
  capped: "名额已满 · 请联系管理员",
  failed: "创建失败 · 请联系管理员",
};

/** Hint after 「正在就绪」. */
export const ENTRY_PENDING_HINT = "几秒后即可使用";

/**
 * Round 3: an entry without any address (its name cannot be handed out
 * yet). The forward list sends `entryNotices` instead of the address.
 */
export interface EntryNotice {
  nodeId: number;
  port: number;
  tag: "capped" | "failed" | "pending" | string;
  /** admins only: the withheld name and the Cloudflare error */
  domain?: string;
  reason?: string;
}

export const ENTRY_NOTICE_TEXT: Record<string, string> = {
  capped: "入口域名名额已满，请联系管理员",
  failed: "入口域名创建失败，请联系管理员",
  pending: "入口域名正在就绪，几秒后自动显示",
};

/** Admin wording for the same notices (red ⚠, with the reason). */
export const ENTRY_NOTICE_ADMIN_TEXT: Record<string, string> = {
  capped: "Cloudflare 名额已满 · 用户看不到这条入口地址",
  failed: "专属域名创建失败 · 用户看不到这条入口地址",
  pending: "专属域名正在分配 · 用户暂时看不到这条入口地址",
};

export const RETIRE_REASON: Record<string, string> = {
  user_deleted: "用户已删除",
  node_deleted: "节点已删除",
  unused: "不再使用此节点",
  renamed: "已换新名",
  admin: "管理员删除",
};
