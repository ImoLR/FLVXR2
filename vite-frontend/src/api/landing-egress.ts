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
  // Plan 127: active | off | restart | unsupported.
  abuseDefaults?: string;
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
export const abuseDefaultRules = [
  {
    name: "默认：屏蔽 SMTP 25",
    chips: ["目标端口 25", "TCP"],
    hint: "防止落地被用来群发垃圾邮件；只影响经代理连 25 端口，不影响落地本机",
  },
  {
    name: "默认：屏蔽 BT",
    chips: ["协议 bittorrent"],
    hint: "按流量特征识别 TCP 上的 BT 握手；uTP（UDP）、加密 BT 和 DHT 识别不了",
  },
];

export const abuseDefaultsOn = (p?: Partial<EgressProfile> | null) =>
  p?.abuseDefaults !== false;

// Mirrors the backend dedupe: a block rule that only repeats a default rule
// is not sent while the defaults are on.
export const isAbuseDuplicate = (r: EgressRule) => {
  if (
    r.action !== "block" ||
    r.domains.length ||
    r.ips.length ||
    r.sourceIps.length ||
    r.sourcePorts
  )
    return false;
  const network = r.network;

  return (
    (r.ports === "25" && network === "tcp" && !r.protocols.length) ||
    (!r.ports &&
      !network &&
      r.protocols.length === 1 &&
      r.protocols[0] === "bittorrent")
  );
};

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
): "danger" | "success" | "default" =>
  action === "block"
    ? "danger"
    : action.startsWith("outbound:")
      ? "success"
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

export interface EgressPreset {
  key: string;
  label: string;
  description: string;
  needsOutbound: boolean;
  top?: boolean;
  rules: (action: string) => Omit<EgressRule, "id">[];
}

const presetRule = (
  name: string,
  preset: string,
  part: Partial<EgressRule>,
  action: string,
): Omit<EgressRule, "id"> => ({
  name,
  preset,
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
  ...part,
});

export const egressPresets: EgressPreset[] = [
  {
    key: "block-cn",
    label: "屏蔽中国大陆",
    description:
      "三条规则插在最前：屏蔽大陆 IP、屏蔽大陆网站、禁止大陆客户端直连（FLVX 节点与来源白名单除外）",
    needsOutbound: false,
    top: true,
    rules: () => [
      presetRule("屏蔽大陆 IP", "block-cn", { ips: ["geoip:cn"] }, "block"),
      presetRule(
        "屏蔽大陆网站",
        "block-cn",
        { domains: ["geosite:cn"] },
        "block",
      ),
      presetRule(
        "禁止大陆直连",
        "block-cn",
        { sourceIps: ["geoip:cn"] },
        "block",
      ),
    ],
  },
  {
    key: "anti-google-cn",
    label: "防止送中",
    description: "Google 走所选出站（默认 WARP），避免落地 IP 被判定在中国",
    needsOutbound: true,
    rules: (action) => [
      presetRule(
        "防止送中",
        "anti-google-cn",
        { domains: ["geosite:google"] },
        action,
      ),
    ],
  },
  {
    key: "speedtest",
    label: "测速分流",
    description: "Speedtest 测速走所选出站",
    needsOutbound: true,
    rules: (action) => [
      presetRule(
        "测速分流",
        "speedtest",
        { domains: ["geosite:speedtest", "geosite:ookla-speedtest"] },
        action,
      ),
    ],
  },
  {
    key: "ai",
    label: "AI 解锁",
    description: "OpenAI、Claude、Gemini 等走所选出站",
    needsOutbound: true,
    rules: (action) => [
      presetRule(
        "AI 解锁",
        "ai",
        {
          domains: [
            "geosite:openai",
            "geosite:anthropic",
            "geosite:google-gemini",
            "geosite:perplexity",
            "geosite:xai",
          ],
        },
        action,
      ),
    ],
  },
  {
    key: "tiktok",
    label: "抖音 / TikTok 解锁",
    description: "TikTok 走所选出站",
    needsOutbound: true,
    rules: (action) => [
      presetRule(
        "TikTok 解锁",
        "tiktok",
        { domains: ["geosite:tiktok"] },
        action,
      ),
    ],
  },
  {
    key: "streaming",
    label: "流媒体解锁",
    description: "Netflix、Disney+、HBO、Prime Video 等走所选出站",
    needsOutbound: true,
    rules: (action) => [
      presetRule(
        "流媒体解锁",
        "streaming",
        {
          domains: [
            "geosite:netflix",
            "geosite:disney",
            "geosite:hbo",
            "geosite:primevideo",
            "geosite:hulu",
            "geosite:dazn",
            "geosite:abema",
            "geosite:bahamut",
          ],
        },
        action,
      ),
    ],
  },
  {
    key: "block-bt",
    label: "屏蔽 BT",
    description: "按流量特征识别 BitTorrent 并封锁",
    needsOutbound: false,
    rules: () => [
      presetRule("屏蔽 BT", "block-bt", { protocols: ["bittorrent"] }, "block"),
    ],
  },
  {
    key: "anti-abuse",
    label: "防滥用",
    description:
      "封锁 TCP 465/587/2525（发信）和 23、135-139、445（Telnet/SMB，TCP+UDP）。封 465/587 只影响用邮件客户端通过代理发信，收信（IMAP 993/143、POP3 995/110）和网页邮箱不受影响；不封 22 和 3389",
    needsOutbound: false,
    rules: () => [
      presetRule(
        "屏蔽发信端口",
        "anti-abuse",
        { ports: "465,587,2525", network: "tcp" },
        "block",
      ),
      presetRule(
        "屏蔽 Telnet/SMB",
        "anti-abuse",
        { ports: "23,135-139,445" },
        "block",
      ),
    ],
  },
  {
    key: "block-ads",
    label: "屏蔽广告",
    description: "常见广告域名（geosite:category-ads）封锁",
    needsOutbound: false,
    rules: () => [
      presetRule(
        "屏蔽广告",
        "block-ads",
        { domains: ["geosite:category-ads"] },
        "block",
      ),
    ],
  },
];

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
// rules were skipped as duplicates.
export const applyPreset = (
  rules: EgressRule[],
  preset: EgressPreset,
  action: string,
): { rules: EgressRule[]; added: number; skipped: number } => {
  const existing = new Set(rules.map(ruleSignature));
  const fresh: EgressRule[] = [];
  let skipped = 0;

  for (const rule of preset.rules(action)) {
    const signature = ruleSignature(rule);

    if (existing.has(signature)) {
      skipped++;
      continue;
    }
    existing.add(signature);
    fresh.push({ ...rule, id: newRuleId() });
  }

  return {
    rules: preset.top ? [...fresh, ...rules] : [...rules, ...fresh],
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
