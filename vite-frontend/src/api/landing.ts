import type { MachineView } from "./types";
import type { ScanGuardNodeState, ServerEgressView } from "./landing-egress";

import Network from "./network";

export type LandingProtocol =
  | "ss2022"
  | "vless_reality_vision"
  | "anytls"
  | "snell"
  | "mieru"
  | "vless_enc";
// Plan 121: AnyTLS / Snell / Mieru exist only on 妙妙屋共享 servers that
// advertise them (contract landing-protocols-v1 `protocol_caps`).
export const LANDING_EXTRA_PROTOCOLS = ["anytls", "snell", "mieru"] as const;
export type LandingExtraProtocol = (typeof LANDING_EXTRA_PROTOCOLS)[number];
export const isLandingExtraProtocol = (
  value: string,
): value is LandingExtraProtocol =>
  (LANDING_EXTRA_PROTOCOLS as readonly string[]).includes(value);
export interface LandingProtocolCap {
  udp: boolean;
  versions?: number[];
  obfs?: string[];
  transports?: string[];
}
// Plan 253: VLESS Encryption runs on native landings (agent landing_vless_enc)
// and on shares advertising protocol_caps.protocols.vless_enc.
export const VLESS_ENC_HINT =
  "需较新的 Xray / Mihomo 客户端（v2rayN、v2rayNG、Clash Meta 等）；Surge、sing-box 暂不支持";
export interface SharedLandingCaps {
  status: string;
  port_min: number;
  port_max: number;
  traffic_limit_bytes: number;
  traffic_used_bytes: number;
  reset_day: number;
  max_connections: number;
  share_until: number | null;
  protocols: LandingProtocol[];
  dual_install: boolean;
  protocol_caps?: {
    versions: number[];
    protocols: Partial<
      Record<LandingExtraProtocol | "vless_enc", LandingProtocolCap>
    >;
  } | null;
  server_mode?: "embedded" | "external" | "";
}
/** The share's capability for an extra protocol (undefined = not offered). */
export const sharedProtocolCap = (
  server: { sharedCaps?: SharedLandingCaps } | undefined,
  protocol: string,
): LandingProtocolCap | undefined => {
  const caps = server?.sharedCaps?.protocol_caps;

  if (
    !caps ||
    !caps.versions?.includes(1) ||
    !(isLandingExtraProtocol(protocol) || protocol === "vless_enc")
  )
    return undefined;

  return caps.protocols?.[protocol] || undefined;
};
/** Plan 253: why VLESS Encryption is unavailable on a server ("" = usable). */
export const vlessEncUnavailableReason = (server: {
  source: string;
  sharedCaps?: SharedLandingCaps;
  vlessEncAvailable?: boolean;
  vlessEncReason?: string;
}) => {
  if (server.vlessEncAvailable === false)
    return server.vlessEncReason || "该服务器暂不支持 VLESS Encryption";
  if (server.vlessEncAvailable === undefined && server.source === "shared")
    return sharedProtocolCap(server, "vless_enc")
      ? ""
      : "该共享服务器暂不支持 VLESS Encryption（需升级妙妙屋 Helper）";

  return "";
};
/** Protocols a node can be created with right now on this server. */
export const landingCreatableProtocols = (server: {
  source: string;
  protocols: LandingProtocol[];
  sharedCaps?: SharedLandingCaps;
  vlessEncAvailable?: boolean;
  vlessEncReason?: string;
}) =>
  (server.protocols || []).filter((protocol) =>
    protocol === "vless_enc"
      ? !vlessEncUnavailableReason(server)
      : !isLandingExtraProtocol(protocol) ||
        (server.source === "shared" &&
          Boolean(sharedProtocolCap(server, protocol))),
  );
// Plan 224: a tunnel with 屏蔽 TLS / 屏蔽 HTTP closes connections that show
// that feature. Which landing protocols show it is decided by the backend
// (lab-tested) and sent as filterFeatures / protocolFilterFeatures; tunnels
// send blockedFeatures. The UI only intersects them.
const filterFeatureLabel = (feature: string) => feature.toUpperCase();

export const landingFeatureBlockedReason = (
  blocked?: string[],
  features?: string[],
) => {
  const hit = (features || []).find((feature) => blocked?.includes(feature));

  return hit
    ? `该节点协议带 ${filterFeatureLabel(hit)} 特征，此隧道无法使用`
    : "";
};

export const tunnelBlockedHint = (features: string[]) =>
  features.length
    ? `此隧道屏蔽了 ${features.map(filterFeatureLabel).join(" / ")} 协议特征`
    : "";

export const landingServerModeLabel = (mode?: string) =>
  mode === "embedded" ? "内置 Xray" : mode === "external" ? "外置 Xray" : "";
export interface LandingIntegration {
  baseUrl: string;
  hasToken: boolean;
  lastSyncAt: number;
  syncError: string;
}
export const getLandingIntegration = () =>
  Network.post<LandingIntegration>("/landing/integration/get", {});
export const saveLandingIntegration = (data: {
  baseUrl: string;
  token: string;
}) => Network.post<LandingIntegration>("/landing/integration/save", data);
export const testLandingIntegration = (data: {
  baseUrl: string;
  token: string;
}) => Network.post<{ shareCount: number }>("/landing/integration/test", data);
export const syncLandingIntegration = () =>
  Network.post<LandingIntegration>("/landing/integration/sync", {});
export interface LandingServer {
  /** Plan 253: VLESS Encryption usable now (native agent / share caps). */
  vlessEncAvailable?: boolean;
  vlessEncReason?: string;
  /** Plan 224: entry-filter features per protocol a new node would have. */
  protocolFilterFeatures?: Record<string, string[]>;
  sharedCaps?: SharedLandingCaps;
  sharedNotice?: string;
  directUserCount?: number;
  sharedLastSyncAt?: number;
  sharedSyncError?: string;
  sharedAvailable?: boolean;
  /** Plan 243: admin view — share held by the mass-vanish guard (⚠). */
  sharedHold?: string;
  defaultUserQuotaBytes?: number;
  // plan 108: exit landing set up only for 「我自己的节点」 (admin view)
  chainOnly?: boolean;
  chainNodeCount?: number;
  id: number;
  source: "native" | "shared";
  nodeId: number;
  name: string;
  publicHost: string;
  portMin: number;
  portMax: number;
  flowType: number;
  trafficRatio: number;
  maxConnections: number;
  defaultMaxConnections: number;
  /** plan 211: 新节点默认 节点总连接数 (入站+出站 incl. TIME_WAIT/CLOSE_WAIT; 0 = 不限). Admin only. */
  defaultMaxTotalConnections?: number;
  defaultMaxOnlineIps: number;
  defaultTrafficLimitBytes: number;
  defaultExpiryMode: string;
  defaultExpiryDays: number;
  defaultExpiresAt: number | null;
  maxNodesPerUser: number;
  protocols: LandingProtocol[];
  realityDest: string;
  realityServerNames: string[];
  /** plan 227: 伪装域名 自动（最快） / 手动. Admin fields below. */
  realityMode?: RealityMode;
  realityProbeSupported?: boolean;
  realityProbeUnsupportedReason?: string;
  realityProbedAt?: number;
  realityProbeError?: string;
  realityProbe?: RealityProbeRow[];
  realityProbeStats?: RealityProbeStats | null;
  realityTrialRule?: RealityTrialRule;
  realityPinSupported?: boolean;
  realityPins?: RealityPin[];
  realityPinsAt?: number;
  /** set by a 测速 response when 自动 picked a new default */
  realityPicked?: string;
  /** a 测速 still runs (shared servers: the Helper picks it up with its next upload) */
  realityProbePending?: boolean;
  availableUntil: number | null;
  coreMemoryMb: number;
  coreCpuPercent: number;
  status: string;
  userGroupIds: number[];
  online?: boolean;
  // Admin: all users' nodes on the server. Non-admin (plan 132): the caller's
  // own nodes, plus myRemaining = how many more the caller can create here.
  nodeCount?: number;
  myRemaining?: number;
  // Plan 205 (non-admin): set while a node of the caller is (or was, until
  // an admin lifts it) disabled by 禁止端口扫描 — creating is refused.
  scanCreateBlocked?: string;
  connections?: number;
  usedBytes?: number;
  routeProfileId?: number;
  egress?: ServerEgressView;
  /** plan 218 (admin): this server's 禁止端口扫描 state, same as the 内置 per-server row. */
  scanGuard?: ScanGuardNodeState;
  egressLabel?: string;
  egressWarnings?: string[];
  // plan 121 defaults for new AnyTLS / Snell / Mieru nodes (shared servers)
  anytlsServerName?: string;
  snellVersion?: number;
  snellObfs?: string;
  snellObfsHost?: string;
  mieruTransport?: string;
  busyPortCount?: number;
  // plan 141: how 节点连接数 (connMode, also for nodes following the server)
  // and the native 服务器总连接数 (maxConnMode) count; admin view only.
  connMode?: LandingConnMode;
  maxConnMode?: LandingConnMode;
  /** false = the agent cannot apply 仅入站 yet (stays 入站+出站). */
  connModeSupported?: boolean;
  /** plan 147: shared (妙妙屋) server — no 计算方式 choice, fixed 入站+出站. */
  connModeFixed?: boolean;
  /** how `connections` counts: each node in its own effective mode. */
  connectionsMode?: LandingConnSumMode;
  /** effective mode of `maxConnections` (server total). */
  maxConnectionsMode?: LandingConnMode;
  /**
   * plan 158: the whole-machine protection cap (整机保护上限: TCP+UDP, all
   * ports, all states) of the server's machine, with its owner (flvx / mmwx);
   * absent = unknown or off. Admin view only.
   */
  machineMaxTotal?: number;
  machineCapOwner?: string;
  /** plan 158: the native total was stored as 仅入站 (plan 141) — it becomes 入站+出站 on the next save. */
  maxConnModeLegacy?: boolean;
  /**
   * plan 211 (admin): live sums of the server's nodes — 入站 (client
   * connections; inboundKnown = the runtime reports them) and 总 (every socket,
   * 入站+出站 incl. TIME_WAIT/CLOSE_WAIT); whether the runtime applies a
   * 节点总连接数 next to a 入站 limit; shared: the 妙妙屋 share cap (read-only).
   */
  inboundConnections?: number;
  totalConnections?: number;
  inboundKnown?: boolean;
  nodeTotalSupported?: boolean;
  shareMaxConnections?: number;
}
/** plan 141: 仅入站 (client connections, like rule 连接数) / 入站+出站 */
export type LandingConnMode = "total" | "inbound";
export type LandingConnSumMode = LandingConnMode | "mixed";
export interface LandingProtocolOpts {
  sni?: string;
  version?: number;
  obfs?: string;
  obfsHost?: string;
  transport?: string;
  /** plan 253 vless_enc: native | xorpub | random */
  mode?: string;
}
// Plan 202: why a node is disabled by 禁止端口扫描.
export interface LandingScanBlock {
  type: string;
  typeLabel: string;
  count: number;
  port?: number;
  host?: string;
  windowS: number;
  at: number;
  blockedAt: number;
  reason: string;
  message: string;
}

export interface LandingNode {
  /** Plan 224: entry-filter features of the node's traffic ("tls" / "http"). */
  filterFeatures?: string[];
  deletePending?: boolean;
  /** disabled by the panel (user / server stopped, no permission), not by an admin. */
  autoDisabled?: boolean;
  scanBlock?: LandingScanBlock;
  // Plan 134: number of the owner's node rules.
  routeRuleCount?: number;
  forwardCount?: number;
  serverAvailable?: boolean;
  id: number;
  userId: number;
  userName?: string;
  landingServerId: number;
  serverName?: string;
  syncError?: string;
  sharedSyncError?: string;
  pendingReason?: string;
  name: string;
  protocol: LandingProtocol;
  port: number;
  inboundTag: string;
  maxConnections: number;
  /** plan 211: 节点总连接数 (入站+出站 incl. TIME_WAIT/CLOSE_WAIT; 0 = 不限). */
  maxTotalConnections?: number;
  maxOnlineIps: number;
  trafficLimitBytes: number;
  flowType: number | null;
  trafficRatio: number | null;
  expiryMode: "follow_user" | "fixed";
  expiresAt: number | null;
  effectiveExpiresAt?: number | null;
  effectiveFlowType?: number;
  effectiveTrafficRatio?: number;
  status: string;
  expiredAt?: number | null;
  expiringSoon?: boolean;
  upBytes: number;
  downBytes: number;
  billedIn: number;
  billedOut: number;
  connections?: number;
  routeProfileId?: number | null;
  effectiveRouteProfileId?: number;
  effectiveRouteProfileName?: string;
  egressLabel?: string;
  protocolOpts?: LandingProtocolOpts;
  certSha256?: string;
  // plan 141: own 计算方式 (null = follow the server; admin only), the
  // configured / applied mode and whether 仅入站 waits for an upgrade.
  connMode?: LandingConnMode | null;
  connModeConfigured?: LandingConnMode;
  connModeEffective?: LandingConnMode;
  connModePending?: boolean;
  /** plan 147: node of a shared server — fixed 入站+出站 (admin view). */
  connModeFixed?: boolean;
  /**
   * plan 211: live 入站 (client connections; inboundKnown = reported) and 总
   * (every socket of the port); admin: the 节点总连接数 waits for an upgrade.
   */
  inboundConnections?: number;
  totalConnections?: number;
  inboundKnown?: boolean;
  nodeTotalPending?: boolean;
  /** plan 177 「链式代理」: the owner's external node (0 = 直连). */
  chainExternalId?: number;
  chain?: LandingNodeChain;
  chainSupport?: { supported: boolean; reason: string; shared: boolean };
}
export interface LandingNodeChain {
  externalId: number;
  name?: string;
  protocolLabel?: string;
  summary?: string;
  /** applied = 已生效, syncing = 正在同步, error = 未生效 (reason) */
  state: "applied" | "syncing" | "error";
  reason?: string;
}
export interface LandingLink {
  name: string;
  url: string;
  kind: "direct" | "forward";
  forwardId?: number;
  entryNodeId?: number;
  /** plan 121: Clash (mihomo) proxy entry for AnyTLS / Snell / Mieru */
  clash?: string;
  /** plan 121: Surge proxy line (Snell) */
  surge?: string;
  /** plan 193: why a Snell node has no Surge line (v4/v5 + TLS obfs) */
  surgeNote?: string;
}
export interface ServerQuota {
  serverKind: "node" | "landing";
  serverRefId: number;
  serverName?: string;
  limitBytes: number;
  limitOverride?: boolean;
  usedIn: number;
  usedOut: number;
  paused?: boolean;
  /** plan 158: 入站连接数 of this user on this server (0 = 不限). */
  maxInboundConnections?: number;
  /**
   * plan 159: 速率 (Mbps) of this user on this server — one limit for all of
   * the user's rules entering the machine. rateOverride false = follows the
   * user's 默认速率; true = rateMbps (0 = 不限).
   */
  rateMbps?: number;
  rateOverride?: boolean;
}
/** Plan 157: one server in a 共享流量池 / related-servers picker. */
export interface PoolServer {
  serverKind: "node" | "landing";
  serverRefId: number;
  serverName?: string;
}
/** Plan 157: a server related to the user (入口 = tunnel/rule entry, 落地 = landing). */
export interface RelatedServer extends PoolServer {
  entry: boolean;
  landing: boolean;
  /** plan 159: entry node whose agent predates the shared 速率 limiter. */
  rateOldAgent?: boolean;
  /** plan 159: shared server applying a per-user 速率 (妙妙屋 contract §7). */
  sharedRate?: boolean;
  /** plan 161: own landing server whose agent cannot limit its landing nodes yet. */
  landingRateOldAgent?: boolean;
  /** plan 161: why the agent could not apply the 速率 to the landing nodes. */
  landingRateError?: string;
  /** plan 168: entry node still limiting the old way (agent = not upgraded, kernel = no tc). */
  entryRateFallback?: "agent" | "kernel";
  /** plan 168: probe reason (kernel) or why tc could not fully apply. */
  entryRateError?: string;
}
/** Plan 157: 共享流量池 — member servers share one billed-bytes limit. */
export interface TrafficPool {
  id?: number;
  name: string;
  limitBytes: number;
  usedIn?: number;
  usedOut?: number;
  paused?: boolean;
  members: PoolServer[];
}
export interface LandingUserSetting {
  // Effective total node cap; maxNodesSource says whether it is the user's
  // own value or the global default (plan 131).
  maxNodes: number;
  maxNodesSource?: "global" | "custom" | "admin";
  globalMaxNodes?: number;
  customMaxNodes?: number;
  quotas: ServerQuota[];
  fixedNodeCount?: number;
  serverIds?: number[];
  // Plan 134 node-rule caps (每个节点 / 每个用户总计), same 跟随全局 / 自定义
  // pattern as maxNodes.
  ruleMaxNode?: number;
  ruleMaxNodeSource?: "global" | "custom" | "admin";
  globalRuleMaxNode?: number;
  ruleMaxTotal?: number;
  ruleMaxTotalSource?: "global" | "custom" | "admin";
  globalRuleMaxTotal?: number;
  ruleUsed?: number;
  /** Plan 157 (absent from older panels). */
  pools?: TrafficPool[];
  relatedServers?: RelatedServer[];
  /** Plan 159: the user's 默认速率 (Mbps, 0 = 不限; absent = older panel). */
  serverRateMbps?: number;
}
export const landingServers = (userId?: number) =>
  Network.post<LandingServer[]>(
    "/landing/server/list",
    userId ? { userId } : {},
  );
export const enableLandingServer = (nodeId: number) =>
  Network.post<LandingServer>("/landing/server/enable", { nodeId });
export const saveLandingServer = (
  server: LandingServer,
  egressRestartConfirmed = false,
  realityGuard?: boolean,
  applyConnModeToNodes = false,
  coreUpgradeConfirmed = false,
  applyNodeTotalToNodes = false,
) => {
  const {
    id,
    nodeId,
    name,
    publicHost,
    portMin,
    portMax,
    flowType,
    trafficRatio,
    maxConnections,
    defaultMaxConnections,
    defaultMaxOnlineIps,
    defaultTrafficLimitBytes,
    defaultUserQuotaBytes,
    defaultExpiryMode,
    defaultExpiryDays,
    defaultExpiresAt,
    maxNodesPerUser,
    protocols,
    realityDest,
    realityServerNames,
    availableUntil,
    coreMemoryMb,
    coreCpuPercent,
    status,
    userGroupIds,
    anytlsServerName,
    snellVersion,
    snellObfs,
    snellObfsHost,
    mieruTransport,
  } = server;

  return Network.post<LandingServer>("/landing/server/save", {
    id,
    nodeId,
    name,
    publicHost,
    portMin,
    portMax,
    flowType,
    trafficRatio,
    maxConnections,
    defaultMaxConnections,
    defaultMaxOnlineIps,
    defaultTrafficLimitBytes,
    defaultUserQuotaBytes,
    defaultExpiryMode,
    defaultExpiryDays,
    defaultExpiresAt,
    maxNodesPerUser,
    protocols,
    realityDest,
    realityServerNames,
    availableUntil,
    coreMemoryMb,
    coreCpuPercent,
    status,
    userGroupIds,
    anytlsServerName,
    snellVersion,
    snellObfs,
    snellObfsHost,
    mieruTransport,
    ...(server.routeProfileId !== undefined
      ? { routeProfileId: server.routeProfileId, egressRestartConfirmed }
      : {}),
    ...(realityGuard !== undefined ? { realityGuard } : {}),
    // Plan 227: absent = the panel keeps the stored mode.
    ...(server.realityMode !== undefined
      ? { realityMode: server.realityMode }
      : {}),
    ...(coreUpgradeConfirmed ? { coreUpgradeConfirmed } : {}),
    // Plan 158: the 计算方式 selectors are retired — no connMode/maxConnMode is
    // sent (the server keeps its stored node mode; the total becomes
    // 入站+出站). The checkbox still copies 节点连接数 to existing nodes.
    ...(server.connMode !== undefined && !server.connModeFixed
      ? { applyConnModeToNodes }
      : {}),
    // Plan 211: absent = the panel keeps the stored default.
    ...(server.defaultMaxTotalConnections !== undefined
      ? {
          defaultMaxTotalConnections: server.defaultMaxTotalConnections,
          applyNodeTotalToNodes,
        }
      : {}),
  });
};
export const landingNodes = () =>
  Network.post<LandingNode[]>("/landing/node/list", {});
export const createLandingNode = (data: {
  landingServerId: number;
  name: string;
  protocol: LandingProtocol;
  userId?: number;
}) => Network.post<LandingNode>("/landing/node/create", data);
export const updateLandingNode = (node: LandingNode) => {
  const {
    id,
    name,
    maxConnections,
    maxOnlineIps,
    trafficLimitBytes,
    flowType,
    trafficRatio,
    expiryMode,
    expiresAt,
  } = node;

  return Network.post("/landing/node/update", {
    id,
    name,
    maxConnections,
    maxOnlineIps,
    trafficLimitBytes,
    flowType,
    trafficRatio,
    expiryMode,
    expiresAt,
    status: node.status === "disabled" ? "disabled" : "active",
    ...(node.routeProfileId !== undefined
      ? { routeProfileId: node.routeProfileId }
      : {}),
  });
};
// Plan 211 follow-up: 节点详情 停用 / 启用 only — same /landing/node/update
// path as before, but just the status so no other field is rewritten.
export const setLandingNodeStatus = (id: number, disabled: boolean) =>
  Network.post<LandingNode>("/landing/node/update", {
    id,
    status: disabled ? "disabled" : "active",
  });
// Plan 227: Reality 伪装域名 candidates (admin) and per-server 测速.
export type RealityMode = "auto" | "manual";
export type RealityDomainSource = "builtin" | "custom" | "pool";
export interface RealityDomain {
  id: number;
  domain: string;
  source: RealityDomainSource;
  inPool: boolean;
  blocked: boolean;
  note: string;
  // Plan 229: 妙妙屋池 small site — auto-picked only once stable on a machine.
  trial?: boolean;
}
// Plan 229: the 试用 rule and the re-sample pacing (help text).
export interface RealityTrialRule {
  minSamples: number;
  minSpanHours: number;
  jitterMs: number;
  jitterPct: number;
  marginMs: number;
  resampleTop: number;
  resampleTrial: number;
  resampleRounds: number;
  resampleGapSec: number;
  keep: number;
  // Plan 233: 自动 changes the pick only when ≥ this many ms faster.
  switchMarginMs?: number;
}
export interface RealityProbeStats {
  at: number;
  passes: number;
  resampled: number;
  sockets?: number;
  maxOpen?: number;
  durationMs: number;
  linger0: boolean;
  measured: boolean;
}
export interface RealityDomainsView {
  domains: RealityDomain[];
  trialRule?: RealityTrialRule;
  probeCount: number;
  probeMax: number;
  customMax: number;
  poolFetchedAt: number;
  poolError: string;
  poolAvailable: boolean;
}
export interface RealityProbeRow {
  target: string;
  domain: string;
  feasible: boolean;
  reason?: string;
  latencyMs: number;
  bestIp?: string;
  ips?: { ip: string; tcp_ms: number }[];
  serverNames: string[];
  source: RealityDomainSource | "current";
  inPool: boolean;
  blocked: boolean;
  // Plan 229: this 测速's samples (ms, -1 = not usable), worst / median, the
  // 试用 verdict and the machine's history of the domain.
  samples?: number[];
  worstMs?: number;
  medianMs?: number;
  trial?: boolean;
  trialOk?: boolean;
  trialPending?: boolean;
  trialReason?: string;
  historyCount?: number;
  historyJitterMs?: number;
  // Plan 233: current 自动 pick kept although pickHeldBy is pickHeldGapMs
  // faster (< switchMarginMs).
  pickHeld?: boolean;
  pickHeldBy?: string;
  pickHeldGapMs?: number;
  pickHeldMs?: number;
  pickHeldByMs?: number;
}
export interface RealityPin {
  host: string;
  port?: number;
  pinned_ip?: string;
  pinned_ms?: number;
  best_ip?: string;
  best_ms?: number;
  ips?: number;
  checked_at: number;
  error?: string;
}
export const realityDomains = () =>
  Network.post<RealityDomainsView>("/landing/reality/domains", {});
export const addRealityDomain = (domain: string) =>
  Network.post<RealityDomainsView>("/landing/reality/domain/add", { domain });
export const deleteRealityDomain = (id: number) =>
  Network.post<RealityDomainsView>("/landing/reality/domain/delete", { id });
export const blockRealityDomain = (id: number, blocked: boolean) =>
  Network.post<RealityDomainsView>("/landing/reality/domain/block", {
    id,
    blocked,
  });
export const refreshRealityPool = () =>
  Network.post<RealityDomainsView>("/landing/reality/pool/refresh", {});
export const landingRealityProbeStatus = (id: number) =>
  Network.post<LandingServer>("/landing/server/reality-probe/status", { id });
export const probeLandingReality = (id: number) =>
  Network.post<LandingServer>(
    "/landing/server/reality-probe",
    { id },
    { timeout: 100000 },
  );
// Plan 131: admin-only global landing settings and node owner transfer.
export interface LandingGlobalSettings {
  defaultMaxNodes: number;
  customUserCount: number;
}
export const getLandingSettings = () =>
  Network.post<LandingGlobalSettings>("/landing/settings/get", {});
export const saveLandingSettings = (defaultMaxNodes: number) =>
  Network.post<LandingGlobalSettings>("/landing/settings/save", {
    defaultMaxNodes,
  });
// userId 0 = the calling admin.
export const transferLandingNode = (id: number, userId: number) =>
  Network.post<LandingNode>("/landing/node/transfer", { id, userId });
// Plan 202: 「解除禁用」 of a node disabled by 禁止端口扫描 (admin).
export const unblockLandingScan = (id: number) =>
  Network.post<LandingNode>("/landing/node/scan/unblock", { id });
export const deleteLandingNode = (id: number) =>
  Network.post<{ pending?: boolean }>("/landing/node/delete", { id });
export const landingLinks = (id: number) =>
  Network.post<LandingLink[]>("/landing/node/links", { id });
export const getLandingUser = (userId: number) =>
  Network.post<LandingUserSetting>("/landing/user/get", { userId });
export const saveLandingUser = (
  userId: number,
  data: LandingUserSetting & { extendFixedByMs?: number },
) =>
  Network.post("/landing/user/save", {
    userId,
    maxNodes: data.maxNodes,
    ...(data.maxNodesSource === "global" || data.maxNodesSource === "custom"
      ? { maxNodesSource: data.maxNodesSource }
      : {}),
    // Rows following the server default keep following it: only their
    // 入站连接数 (plan 158) and 速率 are sent (followDefault).
    quotas: data.quotas.map(
      ({
        serverKind,
        serverRefId,
        limitBytes,
        limitOverride,
        maxInboundConnections,
        rateOverride,
        rateMbps,
      }) => {
        // Plan 159/161: the row's 速率 (rateOverride false = follows the
        // user's 默认速率). Without these fields the panel keeps the stored
        // rate (older panels send none, so nothing is sent back to them).
        const rate =
          rateOverride === undefined
            ? {}
            : { rateOverride, rateMbps: rateOverride ? (rateMbps ?? 0) : 0 };

        return limitOverride === false
          ? {
              serverKind,
              serverRefId,
              limitBytes: 0,
              maxInboundConnections: maxInboundConnections || 0,
              followDefault: true,
              ...rate,
            }
          : {
              serverKind,
              serverRefId,
              limitBytes,
              maxInboundConnections: maxInboundConnections || 0,
              ...rate,
            };
      },
    ),
    ...(data.extendFixedByMs ? { extendFixedByMs: data.extendFixedByMs } : {}),
    ...(data.pools
      ? {
          pools: data.pools.map(({ id, name, limitBytes, members }) => ({
            id: id || 0,
            name,
            limitBytes,
            members: members.map(({ serverKind, serverRefId }) => ({
              serverKind,
              serverRefId,
            })),
          })),
        }
      : {}),
    ...(data.serverIds ? { serverIds: data.serverIds } : {}),
    ...(data.ruleMaxNodeSource === "global" ||
    data.ruleMaxNodeSource === "custom"
      ? {
          ruleMaxNodeSource: data.ruleMaxNodeSource,
          ruleMaxNode: data.ruleMaxNode,
        }
      : {}),
    ...(data.ruleMaxTotalSource === "global" ||
    data.ruleMaxTotalSource === "custom"
      ? {
          ruleMaxTotalSource: data.ruleMaxTotalSource,
          ruleMaxTotal: data.ruleMaxTotal,
        }
      : {}),
  });
const LANDING_PROTOCOL_LABELS: Record<string, string> = {
  ss2022: "SS2022",
  anytls: "AnyTLS",
  snell: "Snell",
  mieru: "Mieru",
  vless_enc: "VLESS Encryption",
};

export const landingProtocolLabel = (value: string) =>
  LANDING_PROTOCOL_LABELS[value] ?? "VLESS Reality Vision";
/** Short label for the narrow H5 protocol column on server cards. */
export const landingProtocolShortLabel = (value: string) =>
  value === "vless_enc"
    ? "VLESS Enc"
    : (LANDING_PROTOCOL_LABELS[value] ?? "VLESS");
/** Plan 126: Mieru over UDP costs noticeably more server CPU and uploads slower. */
export const MIERU_UDP_HINT = "UDP 更耗服务器 CPU、上传较慢，一般建议用 TCP";
export const isMieruUdp = (protocol: string, transport?: string) =>
  protocol === "mieru" && (transport || "").toUpperCase() === "UDP";
/** One-line description of a node's (or a server default's) protocol options. */
export const landingProtocolSummary = (
  protocol: string,
  opts?: LandingProtocolOpts,
) => {
  if (protocol === "anytls")
    return `TLS 自签证书（客户端固定证书指纹）${opts?.sni ? ` · SNI ${opts.sni}` : ""}`;
  if (protocol === "snell")
    return `v${opts?.version || 4} · ${
      !opts?.obfs || opts.obfs === "none"
        ? "无混淆"
        : `${opts.obfs.toUpperCase()} 混淆 ${opts.obfsHost || ""}`
    }`;
  if (protocol === "mieru")
    return `客户端 ${opts?.transport || "TCP"} 传输（同一端口同时支持 TCP 与 UDP）`;
  if (protocol === "vless_enc")
    return "抗量子加密，无需 TLS / 伪装域名（流量全随机）";

  return "";
};
export const landingGB = (bytes: number = 0) =>
  (bytes / 1073741824).toLocaleString("zh-CN", { maximumFractionDigits: 2 });
export const landingDate = (value?: number | null) =>
  value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "永久";
export const landingStatus: Record<string, string> = {
  active: "正常",
  error: "节点异常",
  pending: "正在同步，请稍后",
  paused_quota: "流量已用完",
  paused_server_quota: "服务器额度已用完",
  paused_expired: "已到期（宽限期）",
  disabled: "已停用",
};

// 监控 「落地」 tab (plan 111). Rates are bytes per second; null = no rate yet
// (fewer than two samples, a counter reset, or data older than staleAfterMs).
export interface LandingMonitorNode {
  id: number;
  name: string;
  userId?: number;
  userName?: string;
  protocol: LandingProtocol;
  port: number;
  status: string;
  pendingReason?: string;
  syncError?: string;
  deletePending?: boolean;
  connections: number;
  onlineIps: number | null;
  maxConnections: number;
  /** plan 141: how connections / maxConnections count (absent = 入站+出站). */
  connMode?: LandingConnMode;
  /**
   * plan 211: 入站 (client connections, inboundKnown = reported), 总 (every
   * socket, 入站+出站 incl. TIME_WAIT/CLOSE_WAIT), the 节点总连接数 cap (0 = 不限),
   * the 入站 cap, whether the cap waits for an upgrade, and the latest socket
   * breakdown (absent = the runtime reports none).
   */
  inboundConnections?: number;
  totalConnections?: number;
  inboundKnown?: boolean;
  maxTotalConnections?: number;
  maxInboundConnections?: number;
  totalPending?: boolean;
  tcp?: LandingTcpCounts | null;
  maxOnlineIps: number;
  upRate: number | null;
  downRate: number | null;
  sampleAt: number;
  // Per item 「数据 N 秒前」 threshold (shared servers collect once a minute);
  // absent on older panels → LandingMonitorData.ageHintMs.
  ageHintMs?: number;
  billedBytes: number;
  trafficLimitBytes: number;
}
export interface LandingMonitorServer {
  id: number;
  name: string;
  source: "native" | "shared";
  status: string;
  online: boolean;
  coreState?: string;
  notice?: string;
  syncError?: string;
  nodeCount: number;
  activeNodeCount: number;
  connections: number;
  maxConnections: number;
  /** plan 141: label of the node sum / of the server limit. */
  connectionsMode?: LandingConnSumMode;
  maxConnectionsMode?: LandingConnMode;
  /** plan 211: Σ 入站 / Σ 总 of the nodes; admin, shared: the 妙妙屋 share cap. */
  inboundConnections?: number;
  totalConnections?: number;
  inboundKnown?: boolean;
  shareMaxConnections?: number;
  upRate: number | null;
  downRate: number | null;
  sampleAt: number;
  ageHintMs?: number;
  billedBytes: number;
  shareUsedBytes?: number;
  shareLimitBytes?: number;
  quotaUsedBytes?: number;
  quotaLimitBytes?: number;
  quotaPaused?: boolean;
  nodes: LandingMonitorNode[];
  /** Whole-machine connections + 整机兜底 (shared: 妙妙屋; native: agent, plan 211). */
  machine?: MachineView | null;
}
/** plan 211: a landing node's sockets by direction and state. */
export interface LandingTcpCounts {
  inbound: number;
  outbound: number;
  established: number;
  time_wait: number;
  close_wait: number;
}
export interface LandingMonitorData {
  generatedAt: number;
  admin: boolean;
  ageHintMs: number;
  staleAfterMs: number;
  servers: LandingMonitorServer[];
}
export const landingMonitor = () =>
  Network.post<LandingMonitorData>("/landing/monitor", {});
export interface LandingMonitorIps {
  onlineIps: number | null;
  ips: { ip: string; connections: number }[];
  sampleAt: number;
}
export const landingMonitorIps = (nodeId: number) =>
  Network.post<LandingMonitorIps>("/landing/monitor/ips", { nodeId });
