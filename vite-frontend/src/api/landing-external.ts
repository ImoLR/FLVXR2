import Network from "./network";

// Plan 177 「外部节点」 / 「链式代理」. Credentials never come back from the
// server: only 名称 · 协议 · 地址:端口.

export interface ExternalNodeUse {
  id: number;
  name: string;
  landingServerId: number;
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
