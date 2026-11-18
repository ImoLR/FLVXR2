import Network from "./network";

// Plan 177 「外部节点」 / 「链式代理」. Credentials never come back from the
// server: only 名称 · 协议 · 地址:端口.

export interface ExternalNodeUse {
  id: number;
  name: string;
  landingServerId: number;
  /** Plan 276: "leikwan" = a 利群 rule landing on it (id = rule id).
   *  Plan 284: "forward" = a 转发 targeting it (id = forward id). */
  kind?: "leikwan" | "forward";
}

/** Plan 276: the 链式代理 uses vs the 利群 rules using an 外部节点. */
export const chainUses = (uses: ExternalNodeUse[]) =>
  uses.filter((u) => !u.kind);
export const leiKwanUses = (uses: ExternalNodeUse[]) =>
  uses.filter((u) => u.kind === "leikwan");
/** Plan 284: the 转发 targeting an 外部节点 (「N 条转发在用」). */
export const forwardUses = (uses: ExternalNodeUse[]) =>
  uses.filter((u) => u.kind === "forward");

/** Why an 外部节点 cannot be deleted ('' = it can). */
export function externalInUseNote(uses: ExternalNodeUse[]) {
  const c = chainUses(uses);
  const lk = leiKwanUses(uses);
  const fw = forwardUses(uses);
  const parts: string[] = [];

  if (c.length) parts.push("链式代理在用，要先在落地节点里改回直连");
  if (lk.length)
    parts.push(
      `利群规则 ${lk.map((u) => `「${u.name}」`).join("、")} 用它做落地，要先换落地或删除规则`,
    );
  if (fw.length)
    parts.push(
      `${fw.length} 条转发在用（${fw
        .slice(0, 3)
        .map((u) => `「${u.name}」`)
        .join("、")}${fw.length > 3 ? " 等" : ""}），要先给转发换目标或删除转发`,
    );

  return parts.join("；");
}

export interface ExternalNode {
  id: number;
  name: string;
  protocol: string;
  protocolLabel: string;
  host: string;
  port: number;
  summary: string;
  udpOnly: boolean;
  // mmwx-shared servers take SOCKS5 / HTTP / Shadowsocks / VLESS Reality only.
  sharedOk: boolean;
  sharedReason: string;
  inUse: ExternalNodeUse[];
  /** plan 256: how many of the owner's subscriptions contain it */
  subUses?: number;
  /** plan 284: what a tunnel entry filter sees (tls / http / socks / quic / other) */
  filterFeature?: string;
}

export interface ExternalList {
  items: ExternalNode[];
  cap: number; // 0 = unlimited (admin)
  count: number;
  userId: number;
}

export interface ExternalPreview {
  protocol: string;
  protocolLabel: string;
  host: string;
  port: number;
  summary: string;
  name: string;
  udpOnly: boolean;
  sharedOk: boolean;
  sharedReason: string;
  resolved?: string;
}

export interface SubItem {
  index: number;
  name: string;
  protocol: string;
  protocolLabel: string;
  host: string;
  port: number;
  summary: string;
  supported: boolean;
  reason: string;
  sharedOk?: boolean;
  sharedReason?: string;
  udpOnly?: boolean;
  duplicate?: string;
}

export interface SubFetchResult {
  fetchId: string;
  items: SubItem[];
  info: number;
  invalid: number;
  truncated: boolean;
  cap: number;
  count: number;
  expiresIn: number;
}

export interface SubImportResult {
  imported: number;
  items: ExternalNode[];
  duplicates: string[];
  overCap: number;
  failed: { name: string; reason: string }[];
  cap: number;
}

export interface ChainProbeResult {
  probed: boolean;
  ok: boolean;
  latencyMs?: number;
  server: string;
  target: string;
  message?: string;
}

// Owner = the landing node's owner (admins act for them via userId).
const owner = (userId?: number) => (userId ? { userId } : {});

export const externalList = (userId?: number) =>
  Network.post<ExternalList>("/landing/external/list", owner(userId));

export const externalParse = (link: string) =>
  Network.post<ExternalPreview>("/landing/external/parse", { link });

export const externalCreate = (link: string, name: string, userId?: number) =>
  Network.post<ExternalNode>("/landing/external/create", {
    link,
    name,
    ...owner(userId),
  });

export const externalRename = (id: number, name: string, userId?: number) =>
  Network.post<{ id: number; name: string }>("/landing/external/rename", {
    id,
    name,
    ...owner(userId),
  });

export const externalDelete = (id: number, userId?: number) =>
  Network.post<{ id: number }>("/landing/external/delete", {
    id,
    ...owner(userId),
  });

export const subscriptionFetch = (url: string, userId?: number) =>
  Network.post<SubFetchResult>(
    "/landing/external/sub/fetch",
    { url, ...owner(userId) },
    { timeout: 20000 },
  );

export const subscriptionImport = (
  fetchId: string,
  indexes: number[],
  userId?: number,
) =>
  Network.post<SubImportResult>(
    "/landing/external/sub/import",
    { fetchId, indexes, ...owner(userId) },
    { timeout: 40000 },
  );

export const chainSave = (id: number, externalId: number) =>
  Network.post<{
    node: unknown;
    probe?: ChainProbeResult | null;
    syncError?: string;
  }>("/landing/node/chain/save", { id, externalId }, { timeout: 40000 });

export const chainProbe = (id: number, externalId: number) =>
  Network.post<ChainProbeResult>(
    "/landing/node/chain/probe",
    { id, externalId },
    { timeout: 20000 },
  );
