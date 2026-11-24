import Network from "./network";

// 用户专属域名 (plan 303): one private CNAME per user per entry node, pointing
// at the node's DDNS main domain. Admin only (all routes under /node/).

export type UserSubState = "active" | "paused" | "retired";
export type UserSubSync = "pending" | "synced" | "failed";

export interface UserSubForward {
  id: number;
  name: string;
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
  counts: Partial<Record<"active" | "paused" | "retired" | "failed", number>>;
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
  waiting: number;
  wildcard: boolean;
  zoneError: string;
  attention: boolean;
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
  unused: "未使用",
  node: "节点已关闭",
};

export const RETIRE_REASON: Record<string, string> = {
  user_deleted: "用户已删除",
  node_deleted: "节点已删除",
  unused: "不再使用此节点",
  renamed: "已换新名",
  admin: "管理员删除",
};
