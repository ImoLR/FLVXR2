import type { UserSubNodeView, UserSubZone } from "./user-subdomain";

import Network from "./network";

// Node DDNS (plan 142). The API token is never sent back by the panel: the
// provider list only says whether one is set.

export interface DNSProviderView {
  id: number;
  name: string;
  providerType: "cloudflare";
  tokenSet: boolean;
  tokenStatus: "" | "active" | "invalid" | "error" | string;
  tokenExpiresOn: number; // unix ms, 0 = no expiry
  tokenCheckedAt: number;
  tokenError: string;
  zones: string[];
  usedBy: number;
  /** plan 303: only with listDNSProviders(true) (the DNS 服务商 card) */
  userSubdomain?: { zones: UserSubZone[]; names: number; withRecord: number };
}

export interface DNSProviderTestResult {
  status: string;
  expiresOn: number;
  checkedAt: number;
  error: string;
  zones: { name: string; canEdit: boolean; status: string }[];
}

export type DDNSRecordType = "A" | "AAAA";
export type DDNSRole = "main" | "extra";
export type DDNSSource =
  | "auto"
  | "ct"
  | "cu"
  | "cm"
  | "prefix"
  | "panel"
  | "iface"
  /** plan 307: 固定 IP (the IP is in `prefix`) */
  | "static";
export type DDNSStatus = "pending" | "synced" | "failed" | "upgrade" | "noaddr";

export interface DDNSRecordPayload {
  domain: string;
  type: DDNSRecordType;
  role: DDNSRole;
  source: DDNSSource;
  prefix: string;
  takeover: boolean;
}

export interface NodeDDNSPayload {
  enabled: boolean;
  providerId: number;
  records: DDNSRecordPayload[];
  /** plan 303 「用户专属域名」 */
  userSubdomain?: boolean;
  /** plan 305 「已有转发保持原地址」 when the toggle goes on (default true). */
  userSubdomainKeep?: boolean;
  /** plan 307 「保持节点地址」: never switch the node address to the domain. */
  keepAddress?: boolean;
}

export interface DDNSRecordView extends DDNSRecordPayload {
  id: number;
  currentIp: string;
  ttl: number;
  applied: boolean;
  status: DDNSStatus;
  lastError: string;
  lastSyncAt: number;
  lastCheckAt: number;
}

export interface NodeDDNSView {
  enabled: boolean;
  providerId: number;
  providerName: string;
  status: "" | DDNSStatus;
  online: boolean;
  addrReport: boolean;
  agentAddrs: { v4: string[]; v6: string[] } | null;
  panelIp: string;
  records: DDNSRecordView[];
  /** plan 307 「保持节点地址」 */
  keepAddress?: boolean;
  /** plan 303: 「专属域名 N」 pill; absent when unused */
  userSubdomain?: UserSubNodeView | null;
}

export const listDNSProviders = (stats = false) =>
  Network.post<DNSProviderView[]>(
    "/node/ddns/provider/list",
    stats ? { stats: true } : {},
    stats ? { timeout: 40000 } : undefined,
  );
export const createDNSProvider = (data: {
  name: string;
  providerType: "cloudflare";
  token: string;
}) => Network.post<DNSProviderView>("/node/ddns/provider/create", data);
export const updateDNSProvider = (data: {
  id: number;
  name: string;
  token: string;
}) => Network.post<DNSProviderView>("/node/ddns/provider/update", data);
export const deleteDNSProvider = (id: number) =>
  Network.post("/node/ddns/provider/delete", { id });
export const testDNSProvider = (data: { id?: number; token?: string }) =>
  Network.post<DNSProviderTestResult>("/node/ddns/provider/test", data, {
    timeout: 30000,
  });
export const syncNodeDDNS = (id: number) =>
  Network.post<NodeDDNSView>("/node/ddns/sync", { id }, { timeout: 60000 });

/** 「永久有效」/「剩余 N 天」 and whether to warn (< 14 days). */
export const tokenValidity = (
  expiresOn: number,
  now = Date.now(),
): { label: string; warn: boolean; expired: boolean } => {
  if (!expiresOn) return { label: "永久有效", warn: false, expired: false };
  const days = Math.floor((expiresOn - now) / 86400000);

  if (expiresOn <= now) return { label: "已过期", warn: true, expired: true };

  return {
    label: `剩余 ${Math.max(days, 0)} 天`,
    warn: days < 14,
    expired: false,
  };
};
