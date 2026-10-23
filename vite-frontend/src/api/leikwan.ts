import Network from "@/api/network";

export interface LeiKwanAccount {
  id: number;
  name: string;
  serviceId: number;
  tokenConfigured: boolean;
}

export interface LeiKwanPipe {
  id: number;
  accountId: number;
  forwardId: number;
  tunnelId?: number | null;
  landingNodeId: number;
  landingPort: number;
  listenPort: number;
  routeId: number;
  routeLabel: string;
  transport: string;
  targetHost: string;
  etag: string;
  updatedTime: number;
}

export interface LeiKwanForward {
  id: number;
  route: { id: number; label: string };
  listen_port: number;
  transport: string;
  target: { host: string; port: number };
  state: string;
  updated_at: string;
  pipe?: LeiKwanPipe | null;
  tunnelName?: string;
}

export interface LeiKwanRoute {
  id: number;
  label: string;
  description?: string;
  badge?: string;
  transports: Record<
    string,
    {
      available: boolean;
      healthy_node_count: number;
      unavailable_reason?: string | null;
    }
  >;
}

export interface LeiKwanOverview {
  account: LeiKwanAccount;
  options: {
    forward_limit: number;
    active_forward_count: number;
    routes: LeiKwanRoute[];
    transports: string[];
  };
  forwards: LeiKwanForward[];
  // Pipes FLVX still stores whose forward LeiKwan no longer lists.
  missingPipes?: Array<{ pipe: LeiKwanPipe; tunnelName?: string }>;
}

export interface LeiKwanCheck {
  platformSaved: boolean;
  entries: Array<{
    nodeId: number;
    nodeName: string;
    reachable: boolean;
    message: string;
  }>;
  endToEnd: { success: boolean; message: string };
}

export const getLeiKwanAccounts = () =>
  Network.post<LeiKwanAccount[]>("/leikwan/account/list");
export const saveLeiKwanAccount = (data: {
  id?: number;
  name: string;
  serviceId: number;
  token?: string;
}) => Network.post<LeiKwanAccount>("/leikwan/account/save", data);
export const deleteLeiKwanAccount = (id: number) =>
  Network.post("/leikwan/account/delete", { id });
export const getLeiKwanOverview = (accountId: number) =>
  Network.post<LeiKwanOverview>("/leikwan/overview", { accountId });
export const getLeiKwanForward = (accountId: number, forwardId: number) =>
  Network.post<{ forward: LeiKwanForward; etag: string }>(
    "/leikwan/forward/detail",
    { accountId, forwardId },
  );

export interface LeiKwanTargetChange {
  landingNodeId: number;
  landingPort: number;
  expectedTarget: { host: string; port: number };
  etag: string;
  confirmed: boolean;
}
export const takeoverLeiKwanPipe = (
  data: LeiKwanTargetChange & { accountId: number; forwardId: number },
) => Network.post<LeiKwanPipe>("/leikwan/pipe/takeover", data);
export const retargetLeiKwanPipe = (
  data: LeiKwanTargetChange & { pipeId: number },
) => Network.post<LeiKwanPipe>("/leikwan/pipe/retarget", data);
export const createLeiKwanPipe = (data: {
  accountId: number;
  routeId: number;
  transport: string;
  landingNodeId: number;
  landingPort: number;
  requestId: string;
}) => Network.post<LeiKwanPipe>("/leikwan/pipe/create", data);
export const deleteLeiKwanPipe = (data: {
  pipeId: number;
  etag: string;
  confirmed: boolean;
}) => Network.post("/leikwan/pipe/delete", data);
export const checkLeiKwanPipe = (pipeId: number, signal: AbortSignal) =>
  Network.post<LeiKwanCheck>(
    "/leikwan/pipe/check",
    { pipeId },
    { timeout: 20000, signal },
  );

export const formatLeiKwanTarget = (host: string, port: number | string) =>
  `${host.includes(":") ? `[${host}]` : host}:${port}`;

// Match the panel's public literal-IP selection so confirmations show its exact target.
export function leiKwanNodeHost(node?: {
  id: number;
  serverIpV4?: unknown;
  serverIpV6?: unknown;
  serverIp?: unknown;
}): string {
  const publicIPv4 = (host: string) => {
    const parts = host.split(".");

    if (
      parts.length !== 4 ||
      parts.some(
        (part) => !/^(0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255,
      )
    )
      return false;
    const [a, b, c, d] = parts.map(Number);

    return (
      !(a === 0 && b === 0 && c === 0 && d === 0) &&
      a !== 10 &&
      a !== 127 &&
      a < 224 &&
      !(a === 169 && b === 254) &&
      !(a === 172 && b >= 16 && b <= 31) &&
      !(a === 192 && b === 168) &&
      !(a === 100 && b >= 64 && b <= 127) &&
      !(a === 192 && b === 0 && (c === 0 || c === 2)) &&
      !(a === 198 && (b === 18 || b === 19 || (b === 51 && c === 100))) &&
      !(a === 203 && b === 0 && c === 113)
    );
  };

  for (const raw of [node?.serverIpV4, node?.serverIpV6, node?.serverIp]) {
    const host = String(raw || "").trim();

    if (publicIPv4(host)) return host;
    if (!host.includes(":")) continue;
    try {
      const canonical = new URL(`http://[${host}]/`).hostname
        .slice(1, -1)
        .toLowerCase();
      const mapped = canonical.match(/^::ffff:([a-f0-9]+):([a-f0-9]+)$/);

      if (mapped) {
        const high = parseInt(mapped[1], 16);
        const low = parseInt(mapped[2], 16);
        const ipv4 = `${high >> 8}.${high & 255}.${low >> 8}.${low & 255}`;

        if (publicIPv4(ipv4)) return ipv4;
        continue;
      }
      const first = parseInt(canonical.split(":")[0] || "0", 16);

      if (
        canonical !== "::" &&
        canonical !== "::1" &&
        (first & 0xff00) !== 0xff00 &&
        (first & 0xfe00) !== 0xfc00 &&
        (first & 0xffc0) !== 0xfe80 &&
        !/^2001:db8(?::|$)/.test(canonical)
      )
        return canonical;
    } catch {
      // A domain or malformed literal cannot be a LeiKwan landing target.
    }
  }

  return "";
}
