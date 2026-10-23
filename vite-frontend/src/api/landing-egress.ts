import Network from "./network";

export type EgressIPVersion =
  | "system"
  | "prefer_ipv4"
  | "prefer_ipv6"
  | "force_ipv4"
  | "force_ipv6";
export type EgressOutboundType = "direct" | "warp" | "proxy" | "landing_node";
export type EgressProxyProtocol = "socks" | "http" | "shadowsocks" | "vless";

export interface EgressSettings {
  warpMode?: "auto" | "manual";
  warpLicense?: string;
  privateKey?: string;
  peerPublicKey?: string;
  endpoint?: string;
  addressV4?: string;
  addressV6?: string;
  reserved?: number[];
  mtu?: number;
  protocol?: EgressProxyProtocol;
  server?: string;
  port?: number;
  username?: string;
  password?: string;
  method?: string;
  uuid?: string;
  flow?: string;
  realityServerName?: string;
  realityPublicKey?: string;
  realityShortId?: string;
  realityFingerprint?: string;
  landingNodeId?: number;
  viaForwardId?: number;
}

export interface EgressOutbound {
  id: number;
  name: string;
  type: EgressOutboundType;
  ipVersion: EgressIPVersion;
  settings: EgressSettings;
  remark?: string;
  usedBy?: string[];
  warnings?: string[];
  // Plan 134: users may use it in their node rules; global / node usage.
  userAllowed?: boolean;
  usedByGlobal?: boolean;
  usedByNodes?: number;
}

export interface EgressRule {
  id: string;
  name: string;
  enabled: boolean;
  domains: string[];
  ips: string[];
  ports: string;
  sourceIps: string[];
  sourcePorts: string;
  network: "" | "tcp" | "udp";
  protocols: string[];
  landingNodeIds: number[];
  action: string;
  preset?: string;
}

export interface EgressProfile {
  id: number;
  name: string;
  defaultAction: string;
  rules: EgressRule[];
  // Plan 127: built-in anti-abuse rules (TCP 25 + BT); undefined = on.
  abuseDefaults?: boolean;
  serverCount?: number;
  nodeCount?: number;
  warnings?: string[];
}

export interface EgressLandingNodeChoice {
  id: number;
  name: string;
  serverId: number;
  serverName: string;
  protocol: string;
  userName: string;
  status: string;
}

export interface EgressForwardChoice {
  id: number;
  name: string;
  landingNodeId: number;
  entries: string[];
}

export interface EgressWarpAccount {
  serverId: number;
  serverName: string;
  outboundId: number;
  status: string;
  accountType: string;
  addressV4: string;
  addressV6: string;
  lastError: string;
  updatedTime: number;
}

export interface EgressOverview {
  outbounds: EgressOutbound[];
  profiles: EgressProfile[];
  whitelist: string[];
  exemptNodeCount: number;
  exemptPreview: string[];
  landingNodes: EgressLandingNodeChoice[];
  forwards: EgressForwardChoice[];
  warpAccounts: EgressWarpAccount[];
  geo: { release: string; geoip: string[]; geosite: string[] };
  ssMethods: string[];
  global?: EgressGlobal;
}

// Plan 134: per-server sync state of the global group.
export interface LayerServerStatus {
  serverId: number;
  serverName: string;
  source: string;
  state: "synced" | "syncing" | "failed" | "restart" | "unsupported";
  reason?: string;
  notes?: string[];
}

export interface EgressGlobal {
  forced: EgressRule[];
  default: EgressRule[];
  defaultAction: string;
  updatedTime: number;
  userCaps: { perNode: number; total: number };
  nodesWithRules: number;
  servers: LayerServerStatus[];
  warnings?: string[];
  // Plan 140: the 内置 list (safety CIDRs + global anti-abuse switches).
  builtin?: EgressBuiltin;
}

// Plan 140 / 143: 内置 防滥用 rows, in display order: SMTP 25, 发信端口
// 465/587/2525, Telnet/SMB, BT.
export type AbuseKey = "smtp" | "mail" | "telnet" | "bt";

export const abuseKeys: AbuseKey[] = ["smtp", "mail", "telnet", "bt"];

export type AbuseValues = Record<AbuseKey, boolean>;

// Why a row is not in force on a node: switched off globally, by the node's
// routing profile, or the server cannot apply rules yet.
export type AbuseOffReason = "global" | "profile" | "pending";

// Plan 140: 内置 rules. Abuse keys: admin view = global switch; node view =
// whether the rule applies to that node (offReasons says why not).
export interface EgressBuiltin extends AbuseValues {
  safety: string[];
  profilesOff?: { id: number; name: string }[];
  offReasons?: Partial<Record<AbuseKey, AbuseOffReason>>;
}

export interface RuleSummary {
  id: string;
  name: string;
  action: string;
  actionLabel: string;
  domains: number;
  ips: number;
  ports: string;
  sourceIps: number;
  network: string;
  protocols: string[] | null;
}

export interface NodeRuleOutbound {
  id: number;
  name: string;
  type: EgressOutboundType;
  ipVersion: EgressIPVersion;
  allowed: boolean;
}

// The 路由规则 dialog of one landing node (owner or admin).
export interface NodeRulesView {
  nodeId: number;
  nodeName: string;
  serverId: number;
  serverName: string;
  rules: EgressRule[];
  defaultAction: string;
  inheritedAction: string;
  inheritedLabel: string;
  inheritedFrom: "server" | "global";
  outbounds: NodeRuleOutbound[];
  supported: boolean;
  state: "applied" | "syncing" | "queued" | "failed" | "unsupported";
  stateReason: string;
  notes: string[] | null;
  global: {
    active: boolean;
    forced: RuleSummary[];
    default: RuleSummary[];
    defaultLabel: string;
  };
  globalPending?: string;
  builtin?: EgressBuiltin;
  server: {
    label: string;
    profileName?: string;
    rules?: RuleSummary[];
    ruleCount?: number;
    defaultLabel?: string;
  };
  caps: {
    perNode: number;
    total: number;
    used: number;
    unlimited: boolean;
    perNodeSource: string;
    totalSource: string;
  };
  admin: boolean;
  warnings?: string[];
}

export interface ServerEgressView {
  profileId: number;
  profileName: string;
  label: string;
  supported: boolean;
  unsupportedReason: string;
  restartRequired: boolean;
  restartConfirmed: boolean;
  state: string;
  error: string;
  warnings: string[];
  checkedAt: number;
  // Plan 127: active | off | global_off | restart | unsupported.
  abuseDefaults?: string;
  // Plan 140 / 143: default rules switched on globally, e.g. ["SMTP 25", "BT"].
  abuseRules?: string[];
  realityGuard?: RealityGuardView;
}

// Reality 防盗 (plan 127), native servers only. state: shared | off |
// agent_old | idle | on | restart | error.
export interface RealityGuardView {
  enabled: boolean;
  state: string;
  nodes: number;
}

// The built-in anti-abuse rules the panel adds right after the safety rule
// (compiled server-side; shown as locked rows).
export const abuseDefaultRules: { key: AbuseKey; name: string }[] = [
  { key: "smtp", name: "默认：屏蔽 SMTP 25" },
  { key: "mail", name: "默认：屏蔽发信端口" },
  { key: "telnet", name: "默认：屏蔽 Telnet/SMB" },
  { key: "bt", name: "默认：屏蔽 BT" },
];

export const abuseDefaultsOn = (p?: Partial<EgressProfile> | null) =>
  p?.abuseDefaults !== false;

export const egressOverview = () =>
  Network.post<EgressOverview>("/landing/egress/overview", {});
export const saveEgressOutbound = (o: Partial<EgressOutbound>) =>
  Network.post<EgressOutbound>("/landing/egress/outbound/save", {
    id: o.id,
    name: o.name,
    type: o.type,
    ipVersion: o.ipVersion,
    settings: o.settings,
    remark: o.remark,
    userAllowed: Boolean(o.userAllowed),
  });
export const deleteEgressOutbound = (id: number) =>
  Network.post("/landing/egress/outbound/delete", { id });
export const parseEgressLink = (link: string) =>
  Network.post<{ name: string; settings: EgressSettings }>(
    "/landing/egress/outbound/parse",
    { link },
  );
export const saveEgressProfile = (p: Partial<EgressProfile>) =>
  Network.post<EgressProfile>("/landing/egress/profile/save", {
    id: p.id,
    name: p.name,
    defaultAction: p.defaultAction,
    rules: p.rules,
  });
export const deleteEgressProfile = (id: number) =>
  Network.post("/landing/egress/profile/delete", { id });
export const saveEgressWhitelist = (whitelist: string[]) =>
  Network.post<{ whitelist: string[] }>("/landing/egress/whitelist/save", {
    whitelist,
  });
export const getEgressGlobal = () =>
  Network.post<EgressGlobal>("/landing/egress/global/get", {});
export const saveEgressGlobal = (g: {
  forced: EgressRule[];
  default: EgressRule[];
  defaultAction: string;
}) => Network.post<EgressGlobal>("/landing/egress/global/save", g);
export const saveEgressUserCaps = (perNode: number, total: number) =>
  Network.post<{ perNode: number; total: number }>(
    "/landing/egress/user-caps/save",
    { perNode, total },
  );
export const saveEgressBuiltin = (values: AbuseValues) =>
  Network.post<EgressGlobal>("/landing/egress/builtin/save", values);
export const getNodeRules = (id: number) =>
  Network.post<NodeRulesView>("/landing/node/rules/get", { id });
export const saveNodeRules = (
  id: number,
  rules: EgressRule[],
  defaultAction: string,
) =>
  Network.post<NodeRulesView>("/landing/node/rules/save", {
    id,
    rules,
    defaultAction,
  });

// The layer legend (匹配顺序), top to bottom.
export const layerOrder = [
  { key: "builtin", label: "内置", hint: "安全规则 + 默认防滥用（锁定）" },
  { key: "forced", label: "全局·强制", hint: "所有落地节点，用户不能覆盖" },
  { key: "node", label: "节点规则", hint: "节点所属用户为自己的节点添加" },
  { key: "server", label: "服务器", hint: "落地服务器的路由方案" },
  { key: "default", label: "全局·默认", hint: "前面都没命中时使用" },
  { key: "outbound", label: "默认出站", hint: "节点 > 服务器方案 > 全局" },
] as const;

export const registerEgressWarp = (serverId: number, outboundId: number) =>
  Network.post<{ status: string; accountType?: string; lastError?: string }>(
    "/landing/egress/warp/register",
    { serverId, outboundId },
  );

export const ipVersionOptions: {
  key: EgressIPVersion;
  label: string;
  hint: string;
}[] = [
  {
    key: "system",
    label: "跟随系统",
    hint: "跟随系统：由服务器解析，IPv4、IPv6 都可能使用",
  },
  {
    key: "prefer_ipv4",
    label: "IPv4 优先",
    hint: "IPv4 优先：先用 IPv4，目标没有 IPv4 时改用 IPv6",
  },
  {
    key: "prefer_ipv6",
    label: "IPv6 优先",
    hint: "IPv6 优先：先用 IPv6，目标没有 IPv6 时改用 IPv4",
  },
  {
    key: "force_ipv4",
    label: "强制 IPv4",
    hint: "强制 IPv4：只走 IPv4，目标没有 IPv4 时连接失败",
  },
  {
    key: "force_ipv6",
    label: "强制 IPv6",
    hint: "强制 IPv6：只走 IPv6，目标没有 IPv6 时连接失败",
  },
];

export const ipVersionLabel = (v?: string) =>
  ipVersionOptions.find((o) => o.key === v)?.label || "跟随系统";

export const ipVersionScope: Record<EgressOutboundType, string> = {
  direct: "直连：决定访问目标网站时用 IPv4 还是 IPv6",
  warp: "WARP：决定 WARP 隧道内使用 IPv4 还是 IPv6",
  proxy: "代理：只决定用 IPv4 还是 IPv6 连接上游服务器",
  landing_node: "FLVX 落地节点：只决定用 IPv4 还是 IPv6 连接上游节点",
};

export const outboundTypeLabel: Record<EgressOutboundType, string> = {
  direct: "直连",
  warp: "WARP",
  proxy: "代理",
  landing_node: "FLVX 落地节点",
};

export const outboundTypeColor: Record<
  EgressOutboundType,
  "success" | "secondary" | "warning" | "primary"
> = {
  direct: "success",
  warp: "secondary",
  proxy: "success",
  landing_node: "primary",
};

export const proxyProtocolLabel: Record<EgressProxyProtocol, string> = {
  socks: "SOCKS5",
  http: "HTTP",
  shadowsocks: "Shadowsocks",
  vless: "VLESS Reality",
};

export const actionLabel = (action: string, outbounds: EgressOutbound[]) => {
  if (action === "block") return "封锁";
  if (!action || action === "direct") return "直连（默认）";
  const id = Number(action.replace("outbound:", ""));
  const o = outbounds.find((item) => item.id === id);

  return o ? o.name : "已删除的出站";
};

export const actionColor = (
  action: string,
): "danger" | "success" | "secondary" | "default" =>
  action === "block"
    ? "danger"
    : action.startsWith("outbound:")
      ? "success"
      : action === "ask"
        ? "secondary"
        : "default";

export const newRuleId = () =>
  `r-${Math.random().toString(16).slice(2, 10)}${Date.now().toString(16).slice(-4)}`;

export const emptyRule = (action = "block"): EgressRule => ({
  id: newRuleId(),
  name: "",
  enabled: true,
  domains: [],
  ips: [],
  ports: "",
  sourceIps: [],
  sourcePorts: "",
  network: "",
  protocols: [],
  landingNodeIds: [],
  action,
});

// The attribute chips shown on a rule row.
export const ruleAttributeChips = (rule: EgressRule) => {
  const chips: string[] = [];

  if (rule.domains.length) chips.push(`目标域名 ${rule.domains.length}`);
  if (rule.ips.length) chips.push(`目标 IP ${rule.ips.length}`);
  if (rule.ports) chips.push(`目标端口 ${rule.ports}`);
  if (rule.sourceIps.length) chips.push(`来源 IP ${rule.sourceIps.length}`);
  if (rule.sourcePorts) chips.push(`来源端口 ${rule.sourcePorts}`);
  if (rule.network) chips.push(rule.network === "tcp" ? "TCP" : "UDP");
  if (rule.protocols.length) chips.push(`协议 ${rule.protocols.join("/")}`);
  if (rule.landingNodeIds.length)
    chips.push(`适用节点 ${rule.landingNodeIds.length}`);

  return chips;
};

// Plan 140: the preset library lives in the panel database (admin CRUD,
// seeded once with the former built-in list). Rules of an askOutbound preset
// carry the action "ask": the outbound picked when inserting replaces it.
export const presetAskAction = "ask";

export type EgressPresetRule = Omit<EgressRule, "id">;

export interface EgressPreset {
  id: number;
  name: string;
  description: string;
  rules: EgressPresetRule[];
  askOutbound: boolean;
  insertTop: boolean;
  sortOrder: number;
  seedKey: string;
  // Admin list only: a fixed outbound of the preset was deleted.
  missingOutbound?: boolean;
}

export const listEgressPresets = () =>
  Network.post<{ presets: EgressPreset[]; admin: boolean }>(
    "/landing/preset/list",
    {},
  );
export const saveEgressPreset = (p: Partial<EgressPreset>) =>
  Network.post<EgressPreset>("/landing/egress/preset/save", {
    id: p.id,
    name: p.name,
    description: p.description || "",
    rules: p.rules,
    askOutbound: Boolean(p.askOutbound),
    insertTop: Boolean(p.insertTop),
  });
export const deleteEgressPreset = (id: number) =>
  Network.post("/landing/egress/preset/delete", { id });
export const reorderEgressPresets = (ids: number[]) =>
  Network.post<{ presets: EgressPreset[] }>("/landing/egress/preset/reorder", {
    ids,
  });
export const restoreEgressPresets = () =>
  Network.post<{ presets: EgressPreset[]; added: number }>(
    "/landing/egress/preset/restore",
    {},
  );

// A preset rule as stored: no id, no node scope.
export const toPresetRule = (rule: EgressRule): EgressPresetRule => {
  const rest: Partial<EgressRule> = { ...rule };

  delete rest.id;

  return { ...(rest as EgressPresetRule), landingNodeIds: [] };
};

// Plan 140: which built-in rule already covers a rule (same match, block).
// active = built-in rules in force where the rule would be added; the safety
// rule (private / loopback / link-local / metadata / multicast) always is.
export type BuiltinActive = AbuseValues;

export const allAbuse = (on: boolean): AbuseValues => ({
  smtp: on,
  mail: on,
  telnet: on,
  bt: on,
});

export const builtinCover = (
  r: EgressPresetRule,
  active: BuiltinActive,
  safety: string[] = [],
): string => {
  if (
    r.action !== "block" ||
    r.domains.length ||
    r.sourceIps.length ||
    r.sourcePorts ||
    r.landingNodeIds.length
  )
    return "";
  if (r.ips.length) {
    const inSafety = r.ips.every(
      (ip) => ip === "geoip:private" || safety.includes(ip),
    );

    return inSafety && !r.ports && !r.network && !r.protocols.length
      ? "安全规则"
      : "";
  }
  if (
    active.smtp &&
    r.ports === "25" &&
    r.network === "tcp" &&
    !r.protocols.length
  )
    return "屏蔽 SMTP 25";
  if (
    active.mail &&
    r.ports === "465,587,2525" &&
    r.network === "tcp" &&
    !r.protocols.length
  )
    return "屏蔽发信端口";
  if (
    active.telnet &&
    r.ports === "23,135-139,445" &&
    !r.network &&
    !r.protocols.length
  )
    return "屏蔽 Telnet/SMB";
  if (
    active.bt &&
    !r.ports &&
    !r.network &&
    r.protocols.length === 1 &&
    r.protocols[0] === "bittorrent"
  )
    return "屏蔽 BT";

  return "";
};

// Every rule of the preset repeats an active built-in rule.
export const presetCoveredByBuiltin = (
  p: EgressPreset,
  active: BuiltinActive,
  safety: string[] = [],
) =>
  p.rules.length > 0 && p.rules.every((r) => builtinCover(r, active, safety));

// Rules are compared by matchers + action; the name does not matter.
export const ruleSignature = (r: Omit<EgressRule, "id">) =>
  JSON.stringify([
    [...r.domains].sort(),
    [...r.ips].sort(),
    r.ports,
    [...r.sourceIps].sort(),
    r.sourcePorts,
    r.network,
    [...r.protocols].sort(),
    [...r.landingNodeIds].sort((a, b) => a - b),
    r.action,
  ]);

// applyPreset inserts preset rules (at the top or the end) without
// duplicating rules that already exist. Returns the new list and how many
// rules were skipped as duplicates. action replaces the "ask" placeholder.
export const applyPreset = (
  rules: EgressRule[],
  preset: EgressPreset,
  action: string,
): { rules: EgressRule[]; added: number; skipped: number } => {
  const existing = new Set(rules.map(ruleSignature));
  const fresh: EgressRule[] = [];
  let skipped = 0;
  const filled = preset.rules.map((r) => ({
    ...r,
    enabled: r.enabled !== false,
    domains: r.domains || [],
    ips: r.ips || [],
    sourceIps: r.sourceIps || [],
    protocols: r.protocols || [],
    landingNodeIds: [],
    action: r.action === presetAskAction ? action : r.action,
  }));

  for (const rule of filled) {
    const signature = ruleSignature(rule);

    if (existing.has(signature)) {
      skipped++;
      continue;
    }
    existing.add(signature);
    fresh.push({ ...rule, id: newRuleId() });
  }

  return {
    rules: preset.insertTop ? [...fresh, ...rules] : [...rules, ...fresh],
    added: fresh.length,
    skipped,
  };
};

// One entry per line (domain entries may contain commas, e.g. regexps).
export const splitLines = (value: string) =>
  value
    .split("\n")
    .map((item) => item.trim())
    .filter(Boolean);

// Lines or commas (IP/CIDR lists).
export const splitList = (value: string) =>
  value
    .split(/[\n,，\s]+/)
    .map((item) => item.trim())
    .filter(Boolean);
