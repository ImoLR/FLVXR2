import type { ReactNode } from "react";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Link2, Minus, Plug, Plus, RefreshCw, Settings2 } from "lucide-react";
import toast from "react-hot-toast";

import {
  ChipLine,
  ChipLineItem,
  SharedDetailsButton,
  SharedDetailsDialog,
} from "./landing/server-card";
import { LandingLinkCard } from "./landing/link-card";
import { LandingTone, Pill, ProtocolPill } from "./landing/pill";
import {
  NodeProblemKey,
  NodeProblemPills,
  NodeServerGroup,
  groupNodesByServer,
  LandingCardTree,
  useGroupOpenState,
} from "./landing/admin-groups";
import {
  ExternalNodeCard,
  KID_LIMIT,
  TreeExternalCard,
  TreeForward,
  TreeKid,
  TreeKids,
} from "./landing/my-tree";

import {
  LandingLink,
  LandingNode,
  LANDING_EXTRA_PROTOCOLS,
  LandingServer,
  MIERU_UDP_HINT,
  deleteLandingNode,
  isMieruUdp,
  landingCreatableProtocols,
  landingDate,
  landingGB,
  landingLinks,
  landingNodes,
  landingProtocolLabel,
  landingProtocolShortLabel,
  landingProtocolSummary,
  VLESS_ENC_HINT,
  landingServerModeLabel,
  landingServers,
  landingStatus,
} from "@/api/landing";
import { getForwardList } from "@/api";
import { LandingTreeData, landingTree } from "@/api/landing-tree";
import { AnimatedPage } from "@/components/animated-page";
import {
  OnboardingRing,
  useOnboardingHint,
} from "@/components/onboarding-ring";
import {
  TreeGroupChevron,
  TreeGroupFooter,
  TreeGroupHeader,
  treeGroupHeaderClass,
  treeGroupWrapperClass,
} from "@/components/tree-group";
import { useH5Mode } from "@/hooks/useH5Mode";
import { usePullToRefresh } from "@/hooks/usePullToRefresh";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import { isAdmin } from "@/utils/auth";
import { JwtUtil } from "@/utils/jwt";
import { compareRegions, regionFlag, regionName } from "@/utils/region";
import { CreateLandingNodeDialog } from "@/pages/landing/create-node";
import { NodeDetails } from "@/pages/landing/node-details";
import {
  NODE_TOTAL_NAME,
  NODE_TOTAL_UNIT,
  connLevel,
  nodeTotalPendingText,
} from "@/pages/landing/conn-mode";
import { NodeRulesDialog } from "@/pages/landing/node-rules";
import { ScanBlockNote } from "@/pages/landing/scan-guard";
import {
  ChainNote,
  ChainPill,
  ChainProxyDialog,
} from "@/pages/landing/chain-proxy";
import { ServerSettings } from "@/pages/landing/server-settings";
import { SubExternalsDialog } from "@/pages/sub-link/externals";
import {
  AdminAttention,
  AttentionCorner,
  AttentionNameMarker,
  attentionTitle,
  serverAdminAttention,
  useAttentionPeek,
} from "@/pages/landing/admin-attention";

// Plan 218: the card's 「设置」 button; with an admin-attention state it gets
// the corner ⚠, the reasons as hover title and a long-press bubble. Without
// one it renders exactly the original button.
// Plan 327: on the 我的节点 server group header; compact = phones (icon only).
function ServerSettingsButton({
  attention,
  onPress,
  compact = false,
}: {
  attention: AdminAttention | null;
  onPress: () => void;
  compact?: boolean;
}) {
  const { handlers, bubble } = useAttentionPeek(attention);
  const size = compact ? " h-7 min-w-7 px-1.5" : " h-7";

  return (
    <>
      <Button
        aria-label="服务器设置"
        className={`${attention ? "relative select-none overflow-visible [-webkit-touch-callout:none]" : ""}${size}`}
        color="warning"
        data-server-settings=""
        size="sm"
        startContent={<Settings2 size={15} />}
        variant="flat"
        onPress={onPress}
        {...(attention
          ? {
              title: attentionTitle(attention),
              "data-attention-settings": attention.level,
              ...handlers,
            }
          : {})}
      >
        {compact ? null : "设置"}
        {attention && <AttentionCorner level={attention.level} />}
      </Button>
      {bubble}
    </>
  );
}

// Plan 211: the server card's live connection line — Σ 入站 and Σ 总 of the
// nodes, both labelled (the hidden server total no longer appears).
function serverConnRow(server: LandingServer): [string, string] {
  const total = server.totalConnections ?? server.connections ?? 0;

  if (!server.inboundKnown) return ["实时连接 · 总", String(total)];

  return [
    "实时连接 · 入站 / 总",
    `${server.inboundConnections ?? 0} / ${total}`,
  ];
}

// Plan 211: the node's live 入站 / 总 and (admins) its 节点总连接数, e.g.
// 「入站 12 · 总 87/400」; amber from 80 % of the cap, red at the cap.
function nodeConnText(node: LandingNode, admin: boolean) {
  const total = node.totalConnections ?? node.connections ?? 0;
  const cap = node.maxTotalConnections ?? 0;
  const totalText = admin
    ? `总 ${total}/${cap > 0 ? cap : "不限"}`
    : `总 ${total}`;

  return node.inboundKnown
    ? `入站 ${node.inboundConnections ?? 0} · ${totalText}`
    : totalText;
}

function nodeConnTitle(node: LandingNode, admin: boolean) {
  const cap = node.maxTotalConnections ?? 0;

  return `${node.inboundKnown ? `入站 ${node.inboundConnections ?? 0}（客户端连接）· ` : ""}总 ${node.totalConnections ?? node.connections ?? 0}（${NODE_TOTAL_UNIT}）${
    admin
      ? `\n${NODE_TOTAL_NAME} ${cap > 0 ? cap : "不限"}${node.maxConnections > 0 ? ` · 入站上限 ${node.maxConnections}` : ""}`
      : ""
  }`;
}

const connTone = (node: LandingNode, admin: boolean): LandingTone => {
  if (!admin) return "purple";
  const level = connLevel(
    node.totalConnections ?? node.connections ?? 0,
    node.maxTotalConnections,
  );

  return level === "full" ? "red" : level === "warn" ? "amber" : "purple";
};

// Plan 211: admins see when the 节点总连接数 waits for an agent / 妙妙屋 upgrade.
function NodeTotalPending({
  node,
  shared,
}: {
  node: LandingNode;
  shared: boolean;
}) {
  if (!node.nodeTotalPending) return null;

  return (
    <span
      className="ml-1 text-xs text-warning"
      title={nodeTotalPendingText(shared)}
    >
      总上限待升级
    </span>
  );
}

const DAY_MS = 86_400_000;
const GRACE_MS = 7 * DAY_MS;

function nodeStatusTone(status: string): LandingTone {
  if (status === "active") return "green";
  if (status === "disabled") return "slate";

  return "amber";
}

function NodeStatusPill({ node }: { node: LandingNode }) {
  // Plan 202: disabled by 禁止端口扫描 (only an admin lifts it).
  if (node.scanBlock) return <Pill tone="red">因端口扫描已禁用</Pill>;

  return (
    <Pill tone={nodeStatusTone(node.status)}>
      {landingStatus[node.status] || node.status}
    </Pill>
  );
}

// Plan 149: 「剩余 N 天」 (green > 7 days, amber ≤ 7), 「长期有效」, red
// 「宽限期 N 天」 / 「已过期」; the exact date (and 跟随用户) in the title.
// Plan 286: the pill's state as data, so the 全部节点 user-group header reads
// the same decision (red = 已过期 / 宽限期, days = 剩余 N 天).
function expiryView(
  node: LandingNode,
  now: number,
): { tone: LandingTone; text: string; title: string; days?: number } {
  const expires = node.effectiveExpiresAt;
  const follow = node.expiryMode === "follow_user" ? " · 跟随用户" : "";

  if (node.expiredAt) {
    const left = Math.ceil((node.expiredAt + GRACE_MS - now) / DAY_MS);

    return {
      tone: "red",
      text: left > 0 ? `宽限期 ${left} 天` : "已过期",
      title: `已到期：${landingDate(expires || node.expiredAt)}；宽限期结束：${landingDate(node.expiredAt + GRACE_MS)}${follow}`,
    };
  }
  if (!expires) {
    return {
      tone: "green",
      text: "长期有效",
      title: `长期有效，不会到期${follow}`,
    };
  }
  if (expires <= now) {
    return {
      tone: "red",
      text: "已过期",
      title: `到期：${landingDate(expires)}${follow}`,
    };
  }
  const days = Math.ceil((expires - now) / DAY_MS);

  return {
    tone: days <= 7 ? "amber" : "green",
    text: `剩余 ${days} 天`,
    title: `到期：${landingDate(expires)}${follow}`,
    days,
  };
}

function ExpiryPill({ node, now }: { node: LandingNode; now: number }) {
  const view = expiryView(node, now);

  return (
    <Pill title={view.title} tone={view.tone}>
      {view.text}
    </Pill>
  );
}

// Plan 149: plug + current connections. Users see the count only (plan 132
// hid the limits); admins also see the limit and the 计算方式 tag.
function ConnPill({
  node,
  admin,
  shared = false,
}: {
  node: LandingNode;
  admin: boolean;
  shared?: boolean;
}) {
  const icon = <Plug aria-hidden className="h-3 w-3 shrink-0" />;

  return (
    <span className="inline-flex items-center">
      <Pill
        icon={icon}
        title={nodeConnTitle(node, admin)}
        tone={connTone(node, admin)}
      >
        {nodeConnText(node, admin)}
      </Pill>
      {admin && <NodeTotalPending node={node} shared={shared} />}
    </span>
  );
}

// Plan 149: 已用 / 上限 text plus a thin bar when there is a limit (amber
// ≥ 80 %, red at 100 %).
function UsageBar({ node }: { node: LandingNode }) {
  const limit = node.trafficLimitBytes;

  if (!limit) return null;
  const used = node.billedIn + node.billedOut;
  const ratio = Math.min(1, Math.max(0, used / limit));
  const color =
    ratio >= 1 ? "bg-red-500" : ratio >= 0.8 ? "bg-amber-500" : "bg-green-500";

  return (
    <div
      aria-label={`已用 ${Math.round(ratio * 100)}%`}
      className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-slate-500/15 dark:bg-slate-400/20"
      role="img"
    >
      <div
        className={`h-full rounded-full ${color}`}
        style={{ width: `${Math.max(ratio * 100, used > 0 ? 2 : 0)}%` }}
      />
    </div>
  );
}

// Plan 286: what a 全部节点 user-group header counts for one node — read from
// the same helpers as the row (status pill, sync notes, expiry pill, usage
// bar, connection tone); 即将到期 = the expiry pill's 剩余 ≤ 3 天.
// Round 2: users' headers skip 连接满 (they never see the caps).
function nodeProblems(
  node: LandingNode,
  now: number,
  admin: boolean,
): NodeProblemKey[] {
  const out: NodeProblemKey[] = [];

  if (node.deletePending) return out;
  if (node.scanBlock) out.push("scan");
  else if (node.status === "error") out.push("error");
  else if (node.status === "pending") out.push("pending");
  else if (
    node.status === "paused_quota" ||
    node.status === "paused_server_quota"
  )
    out.push("quota");
  else if (node.status === "paused_expired") out.push("expired");
  // A sync error explains 节点异常; count the node once.
  if ((node.syncError || node.sharedSyncError) && !out.includes("error"))
    out.push("sync");
  const expiry = expiryView(node, now);

  if (expiry.tone === "red") out.push("expired");
  else if (expiry.days !== undefined && expiry.days <= 3) out.push("expiring");
  const limit = node.trafficLimitBytes;

  if (limit > 0) {
    const ratio = (node.billedIn + node.billedOut) / limit;

    if (ratio >= 1) out.push("quota");
    else if (ratio >= 0.9) out.push("quota90");
  }
  if (admin && connTone(node, true) === "red") out.push("conn");

  return out;
}

// Plan 286: the phone row's status dot = the status pill's colour.
const NODE_DOT_CLASS: Record<string, string> = {
  green: "bg-green-500",
  slate: "bg-slate-400",
  amber: "bg-amber-500",
  red: "bg-red-500",
};

function NodeStatusDot({ node }: { node: LandingNode }) {
  const tone = node.scanBlock ? "red" : nodeStatusTone(node.status);

  return (
    <span
      aria-label={
        node.scanBlock
          ? "因端口扫描已禁用"
          : landingStatus[node.status] || node.status
      }
      className={`inline-block h-2 w-2 shrink-0 rounded-full ${NODE_DOT_CLASS[tone] || "bg-slate-400"}`}
      role="img"
    />
  );
}

// Plan 327: the server group header's server facts (what the 落地服务器 card
// showed) — chips like the card, one compact facts line.
function serverChipItems(server: LandingServer, admin: boolean) {
  const sourceItems: ChipLineItem[] = [];
  const chipItems: ChipLineItem[] = [];

  if (admin) {
    const shared = server.source === "shared";
    const source = shared ? "妙妙屋共享" : "FLVX 自有落地";

    sourceItems.push({
      key: "source",
      label: source,
      node: <Pill tone={shared ? "purple" : "cyan"}>{source}</Pill>,
      short: (
        <Pill title={source} tone={shared ? "purple" : "cyan"}>
          {shared ? "共享" : "自有"}
        </Pill>
      ),
    });
    const mode = shared
      ? landingServerModeLabel(server.sharedCaps?.server_mode)
      : "";
    const modeTitle =
      "妙妙屋官方给这台服务器设置的运行模式，仅供参考；FLVX 节点运行在 FLVX 独立的 Core 中，连接数、在线 IP、流量限制照常生效";

    if (mode)
      sourceItems.push({
        key: "mode",
        label: `妙妙屋 · ${mode}`,
        node: (
          <Pill title={modeTitle} tone="slate">
            妙妙屋 · {mode}
          </Pill>
        ),
        short: (
          <Pill title={`妙妙屋 · ${mode}：${modeTitle}`} tone="slate">
            {mode}
          </Pill>
        ),
      });
    const chainNodes = server.chainNodeCount ?? 0;

    if (server.chainOnly)
      sourceItems.push({
        key: "chain",
        label: chainNodes ? `链式 · ${chainNodes}` : "链式",
        node: (
          <Pill
            title={`自动安装，只承载「我的外部节点」线路机加速，不对用户开放；在服务器页「启用为落地」可转为公开落地${chainNodes ? `；链式出口 ${chainNodes} 个` : ""}`}
            tone="purple"
          >
            {chainNodes ? `链式 · ${chainNodes}` : "链式"}
          </Pill>
        ),
      });
    else if (chainNodes > 0)
      sourceItems.push({
        key: "chain-nodes",
        label: `链式节点 ${chainNodes}`,
        node: <Pill tone="slate">链式节点 {chainNodes}</Pill>,
        short: (
          <Pill title={`链式节点 ${chainNodes}`} tone="slate">
            链式 {chainNodes}
          </Pill>
        ),
      });
    if (server.egress?.coreUpgradeRequired)
      sourceItems.push({
        key: "core-upgrade",
        pin: true,
        label: "落地 Core 待升级",
        node: (
          <Pill
            title="新版落地 Core 已下载，在「设置」里确认重启一次后生效"
            tone="amber"
          >
            落地 Core 待升级
          </Pill>
        ),
        short: (
          <Pill
            title="落地 Core 待升级：新版落地 Core 已下载，在「设置」里确认重启一次后生效"
            tone="amber"
          >
            Core 待升级
          </Pill>
        ),
      });
  }
  const flow = `${server.flowType === 2 ? "双向" : "单向"} · ${server.trafficRatio}×`;

  chipItems.push({
    key: "flow",
    label: flow,
    node: <Pill tone="purple">{flow}</Pill>,
  });
  if (server.egressLabel)
    chipItems.push({
      key: "egress",
      label: `出站：${server.egressLabel}`,
      node: <Pill tone="teal">出站：{server.egressLabel}</Pill>,
      short: (
        <Pill title={`出站：${server.egressLabel}`} tone="teal">
          {server.egressLabel}
        </Pill>
      ),
    });
  const protocols = server.protocols || [];

  if (protocols.length)
    chipItems.push({
      key: "protocols",
      label: `协议：${protocols.map(landingProtocolLabel).join("、")}`,
      node: <ProtocolPill protocols={protocols} />,
      short: <ProtocolPill compact protocols={protocols} />,
    });
  if (admin && server.egress?.state === "error")
    chipItems.push({
      key: "egress-error",
      pin: true,
      label: "出站与路由应用失败",
      node: <Pill tone="red">出站与路由应用失败</Pill>,
      short: (
        <Pill title="出站与路由应用失败" tone="red">
          路由应用失败
        </Pill>
      ),
    });
  if (admin && server.egress?.state === "restart_required")
    chipItems.push({
      key: "egress-restart",
      pin: true,
      label: "出站与路由待重启生效",
      node: <Pill tone="amber">出站与路由待重启生效</Pill>,
      short: (
        <Pill title="出站与路由待重启生效" tone="amber">
          路由待重启
        </Pill>
      ),
    });

  return [...sourceItems, ...chipItems];
}

function serverStatusPill(server: LandingServer) {
  const online = server.online && server.status === "active";

  return (
    <Pill
      tone={online ? "green" : server.status !== "active" ? "slate" : "red"}
    >
      {server.status !== "active" ? "已停用" : server.online ? "在线" : "离线"}
    </Pill>
  );
}

/** Admin: 计费流量 · 实时连接 · 节点数 · 可用至; users: their own count, room and end date. */
function serverFacts(server: LandingServer, admin: boolean, mineOnly: boolean) {
  if (admin) {
    const [connLabel, connValue] = serverConnRow(server);

    return [
      `计费流量 ${landingGB(server.usedBytes)} GB`,
      `${connLabel} ${connValue}`,
      // 只看我的: the header counts my nodes; this is everyone's.
      `${mineOnly ? `全部用户共 ${server.nodeCount ?? 0} 个节点 · ` : ""}每人最多 ${server.maxNodesPerUser}`,
      `可用至 ${landingDate(server.availableUntil)}`,
    ];
  }
  const room = server.scanCreateBlocked
    ? "暂不能新建"
    : server.myRemaining === undefined
      ? ""
      : server.myRemaining === 0
        ? "已达上限"
        : `还可创建 ${server.myRemaining}`;

  return [
    ...(room ? [room] : []),
    `可用至 ${landingDate(server.availableUntil)}`,
  ];
}

type FocusTarget =
  | { kind: "forward"; id: number }
  | { kind: "node"; id: number }
  | { kind: "server"; id: number }
  | { kind: "external"; id: number };

const parseFocus = (value: string | null): FocusTarget | null => {
  const match = /^(forward|node|server|external):(\d+)$/.exec(value || "");

  if (!match) return null;

  return { kind: match[1] as FocusTarget["kind"], id: Number(match[2]) };
};

const EXTERNAL_GROUP_KEY = "externals";
const GLOW_MS = 15000;

export default function LandingPage() {
  const admin = isAdmin();
  const h5 = useH5Mode();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [servers, setServers] = useState<LandingServer[]>([]);
  const [nodes, setNodes] = useState<LandingNode[]>([]);
  const [forwards, setForwards] = useState<TreeForward[]>([]);
  const [tree, setTree] = useState<LandingTreeData | null>(null);
  const [loading, setLoading] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  // Plan 327 (admin): 只看我的 (default on every visit) / 全部用户.
  const [scope, setScope] = useState<"mine" | "all">("mine");
  const [search, setSearch] = useState("");
  const [settings, setSettings] = useState<LandingServer | null>(null);
  const [details, setDetails] = useState<LandingNode | null>(null);
  const [creating, setCreating] = useState<LandingServer | null>(null);
  const [deleting, setDeleting] = useState<LandingNode | null>(null);
  const [deleteError, setDeleteError] = useState("");
  const [linksFor, setLinksFor] = useState<LandingNode | null>(null);
  const [links, setLinks] = useState<LandingLink[]>([]);
  const [linkError, setLinkError] = useState("");
  const [linksLoading, setLinksLoading] = useState(false);
  const [qr, setQR] = useState("");
  const [saving, setSaving] = useState(false);
  const [rulesFor, setRulesFor] = useState<LandingNode | null>(null);
  const [chainFor, setChainFor] = useState<LandingNode | null>(null);
  const [externalsOpen, setExternalsOpen] = useState(false);
  const [sharedDetails, setSharedDetails] = useState<LandingServer | null>(
    null,
  );
  const [focusNote, setFocusNote] = useState("");
  const currentUserId = JwtUtil.getUserIdFromToken() ?? 0;
  // Plan 218: which servers need the admin (one helper decides).
  const attentionById = useMemo(
    () =>
      new Map(
        admin
          ? servers.map((server) => [server.id, serverAdminAttention(server)])
          : [],
      ),
    [admin, servers],
  );
  // Plan 287/289: one open group, never remembered, no position correction.
  const groupOpen = useGroupOpenState();
  const openGroup = groupOpen.open;
  const rulesStale = useRef(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    const [serverRes, nodeRes, forwardRes, treeRes] = await Promise.all([
      landingServers(),
      landingNodes(),
      getForwardList(),
      // Admins read every user's 外部节点 once; the scope filters locally.
      landingTree(admin),
    ]);

    if (serverRes.code === 0) setServers(serverRes.data || []);
    else setError(serverRes.msg);
    if (nodeRes.code === 0) setNodes(nodeRes.data || []);
    else setError(nodeRes.msg);
    if (forwardRes.code === 0)
      setForwards(
        ((forwardRes.data?.items || []) as unknown as TreeForward[]).filter(
          (forward) => forward.mode !== "wg_path",
        ),
      );
    if (treeRes.code === 0 && treeRes.data) setTree(treeRes.data);
    setLoading(false);
    setLoaded(true);
  }, [admin]);

  useEffect(() => {
    void load();
  }, [load]);
  usePullToRefresh(load);
  // While a node shows 「正在同步，请稍后」, re-read only the node list every 5 s
  // so it flips to 正常 by itself. Bounded: stops when nothing is pending, after
  // 2 min per set of pending nodes, on unmount, and skips ticks while hidden.
  const pendingKey = nodes
    .filter((node) => node.status === "pending")
    .map((node) => node.id)
    .join(",");

  useEffect(() => {
    if (!pendingKey) return;
    const until = Date.now() + 2 * 60 * 1000;
    let busy = false;
    let disposed = false;
    const timer = window.setInterval(() => {
      if (Date.now() > until) {
        window.clearInterval(timer);

        return;
      }
      if (busy || document.hidden) return;
      busy = true;
      void landingNodes()
        .then((res) => {
          if (!disposed && res.code === 0) setNodes(res.data || []);
        })
        .catch(() => undefined)
        .finally(() => {
          busy = false;
        });
    }, 5000);

    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [pendingKey]);
  useEffect(() => {
    if (!linksFor) return;
    let disposed = false;

    setLinks([]);
    setLinkError("");
    setQR("");
    setLinksLoading(true);
    void landingLinks(linksFor.id).then((res) => {
      if (disposed) return;
      if (res.code === 0) setLinks(res.data || []);
      else setLinkError(res.msg);
      setLinksLoading(false);
    });

    return () => {
      disposed = true;
    };
  }, [linksFor]);

  // ------------------------------------------------------------ tree data
  const mineOnly = !admin || scope === "mine";
  const ownerOf = (userId?: number) => !mineOnly || userId === currentUserId;
  const query = search.trim().toLowerCase();
  const scopedNodes = useMemo(
    () => nodes.filter((node) => !admin || ownerOf(node.userId)),
    [nodes, admin, scope, currentUserId],
  );
  const chainExternal = useMemo(
    () =>
      new Map(
        (tree?.chains || []).map((chain) => [
          chain.forwardId,
          chain.externalId,
        ]),
      ),
    [tree],
  );
  const externalsById = useMemo(
    () => new Map((tree?.externals || []).map((ext) => [ext.id, ext])),
    [tree],
  );
  // Node children: forwards to the node (+ 链式出口 when 链式代理 is on).
  const kidsByNode = useMemo(() => {
    const map = new Map<number, TreeKid[]>();

    for (const forward of forwards) {
      if (!forward.landingNodeId || forward.chain) continue;
      const list = map.get(forward.landingNodeId) || [];

      list.push({ key: `forward:${forward.id}`, kind: "forward", forward });
      map.set(forward.landingNodeId, list);
    }
    for (const node of nodes) {
      if (!node.chain) continue;
      const ext = externalsById.get(node.chain.externalId);
      const list = map.get(node.id) || [];

      list.push({
        key: `exit:${node.id}`,
        kind: "chain-exit",
        exit: {
          id: node.chain.externalId,
          name: node.chain.name || ext?.name || "外部节点",
          protocolLabel: node.chain.protocolLabel || ext?.protocolLabel,
          state: node.chain.state,
        },
      });
      map.set(node.id, list);
    }

    return map;
  }, [forwards, nodes, externalsById]);
  // 外部节点 cards + children (线路机加速 / 端口转发); pasted 线路机 links
  // that are not in the list get one card per address.
  const externalCards = useMemo(() => {
    const cards = new Map<string, TreeExternalCard>();
    const kids = new Map<string, TreeKid[]>();
    const add = (key: string, kid: TreeKid) =>
      kids.set(key, [...(kids.get(key) || []), kid]);

    for (const ext of tree?.externals || []) {
      if (!ownerOf(ext.userId)) continue;
      cards.set(`ext:${ext.id}`, { ...ext, key: `ext:${ext.id}` });
    }
    for (const forward of forwards) {
      if (!ownerOf(forward.userId)) continue;
      if (forward.chain) {
        const extId = chainExternal.get(forward.id) || 0;
        let key = `ext:${extId}`;

        if (!extId || !cards.has(key)) {
          const c = forward.chain;

          key = `paste:${forward.userId}:${c.upstreamProtocol}:${c.upstreamHost}:${c.upstreamPort}`;
          if (!cards.has(key))
            cards.set(key, {
              key,
              id: 0,
              userId: forward.userId || 0,
              userName: forward.userName,
              name: `${c.upstreamHost}:${c.upstreamPort}`,
              protocol: c.upstreamProtocol,
              protocolLabel: c.upstreamProtocol,
              host: c.upstreamHost,
              port: c.upstreamPort,
              summary: c.summary,
              virtual: true,
            });
        }
        add(key, { key: `forward:${forward.id}`, kind: "line", forward });
      } else if (forward.externalNode && !forward.landingNodeId) {
        const key = `ext:${forward.externalNode.id}`;

        if (cards.has(key))
          add(key, { key: `forward:${forward.id}`, kind: "port", forward });
      }
    }

    return { cards: [...cards.values()], kids };
  }, [tree, forwards, chainExternal, scope, currentUserId, admin]);
  const chainUsersOf = (extId: number) =>
    scopedNodes
      .filter((node) => node.chain && node.chain.externalId === extId)
      .map((node) => ({ id: node.id, name: node.name }));
  // Search (admin): node / owner / server / child names.
  const nodeMatches = (node: LandingNode) =>
    !query ||
    [
      node.name,
      node.userName,
      node.serverName,
      ...(kidsByNode.get(node.id) || []).map(
        (kid) => kid.forward?.name || kid.exit?.name,
      ),
    ].some((text) => (text || "").toLowerCase().includes(query));
  const externalMatches = (card: TreeExternalCard) =>
    !query ||
    [
      card.name,
      card.userName,
      card.host,
      ...(externalCards.kids.get(card.key) || []).map(
        (kid) => `${kid.forward?.name} ${kid.forward?.tunnelName}`,
      ),
    ].some((text) => (text || "").toLowerCase().includes(query));
  const visibleNodes = scopedNodes.filter(nodeMatches);
  const visibleExternals = externalCards.cards.filter(externalMatches);
  // Server groups: servers with (visible) nodes first, then the empty ones
  // (users: granted servers → 「＋ 创建节点」; admin: every server; chain-only
  // exits only in 全部用户).
  const serverGroups = (() => {
    const withNodes = groupNodesByServer(visibleNodes, servers, (node) =>
      nodeProblems(node, Date.now(), admin),
    );
    const used = new Set(withNodes.map((group) => group.serverId));
    const empty = query
      ? []
      : servers
          .filter(
            (server) =>
              !used.has(server.id) && (!server.chainOnly || !mineOnly),
          )
          .map(
            (server): NodeServerGroup => ({
              key: `server:${server.id}`,
              serverId: server.id,
              name: server.name,
              region: (server.region || "").toUpperCase(),
              nodes: [],
              used: 0,
              problems: new Map(),
            }),
          )
          .sort(
            (a, b) =>
              compareRegions(a.region, b.region) ||
              a.name.localeCompare(b.name, "zh-CN"),
          );

    return { withNodes, empty };
  })();

  // --------------------------------------------------------------- focus
  const [glowKey, setGlowKey] = useState("");
  const glowTimer = useRef(0);
  const glow = useCallback((key: string, ms = GLOW_MS) => {
    window.clearTimeout(glowTimer.current);
    setGlowKey(key);
    glowTimer.current = window.setTimeout(() => setGlowKey(""), ms);
  }, []);
  const endGlow = useCallback(() => {
    window.clearTimeout(glowTimer.current);
    setGlowKey("");
  }, []);

  useEffect(() => () => window.clearTimeout(glowTimer.current), []);
  // The glow ends after its time or at the next click / tap anywhere.
  useEffect(() => {
    if (!glowKey) return;
    const end = () => endGlow();
    const arm = window.setTimeout(
      () => document.addEventListener("pointerdown", end, { once: true }),
      300,
    );

    return () => {
      window.clearTimeout(arm);
      document.removeEventListener("pointerdown", end);
    };
  }, [glowKey, endGlow]);
  // Opens the target's group, then (after render) scrolls it to the middle.
  const reveal = useCallback(
    (groupKey: string, selector: string, key: string, ms = GLOW_MS) => {
      openGroup([groupKey]);
      glow(key, ms);
      let tries = 0;
      const tick = () => {
        const element = document.querySelector(selector);

        if (element) {
          element.scrollIntoView({ behavior: "smooth", block: "center" });

          return;
        }
        if (++tries < 20) window.setTimeout(tick, 60);
      };

      window.setTimeout(tick, 60);
    },
    [openGroup, glow],
  );
  const groupKeyOfNode = (node: LandingNode) =>
    `server:${node.landingServerId}`;
  const locateNode = (nodeId: number, ms = 2400) => {
    const node = nodes.find((item) => item.id === nodeId);

    if (!node) return;
    if (admin && node.userId !== currentUserId) setScope("all");
    setSearch("");
    reveal(
      groupKeyOfNode(node),
      `[data-landing-node-row="${node.id}"]`,
      `node:${node.id}`,
      ms,
    );
  };
  const locateExternal = (extId: number, ms = 2400) => {
    const ext = externalsById.get(extId);

    if (admin && ext && ext.userId !== currentUserId) setScope("all");
    setSearch("");
    reveal(
      EXTERNAL_GROUP_KEY,
      `[data-mytree-external="ext:${extId}"]`,
      `ext:${extId}`,
      ms,
    );
  };
  const focusParam = params.get("focus");

  useEffect(() => {
    const target = parseFocus(focusParam);

    if (!target || !loaded) return;
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);

        next.delete("focus");

        return next;
      },
      { replace: true },
    );
    setFocusNote("");
    if (target.kind === "node") {
      locateNode(target.id, GLOW_MS);

      return;
    }
    if (target.kind === "external") {
      locateExternal(target.id, GLOW_MS);

      return;
    }
    if (target.kind === "server") {
      const server = servers.find((item) => item.id === target.id);

      if (!server) return;
      if (admin && server.chainOnly) setScope("all");
      setSearch("");
      reveal(
        `server:${server.id}`,
        `[data-landing-server-group="server:${server.id}"]`,
        `server:${server.id}`,
      );

      return;
    }
    const forward = forwards.find((item) => item.id === target.id);
    const key = `forward:${target.id}`;

    if (!forward) {
      setFocusNote("没有找到这条转发，可能已被删除。");

      return;
    }
    if (admin && forward.userId !== currentUserId) setScope("all");
    setSearch("");
    if (forward.landingNodeId && !forward.chain) {
      const node = nodes.find((item) => item.id === forward.landingNodeId);

      if (!node) {
        setFocusNote("这条转发的落地节点不在列表里。");

        return;
      }
      reveal(groupKeyOfNode(node), `[data-mytree-kid="${key}"]`, key);

      return;
    }
    if (forward.chain || forward.externalNode) {
      reveal(EXTERNAL_GROUP_KEY, `[data-mytree-kid="${key}"]`, key);

      return;
    }
    setFocusNote(
      "这条转发是手动填写的目标，没有节点链接，请在转发页「复制地址」。",
    );
  }, [focusParam, loaded]);
  // 设置 from the 服务器 page (?server=id): open its settings + group.
  useEffect(() => {
    const serverId = Number(params.get("server"));

    if (!admin || !serverId || !servers.length) return;
    const server = servers.find((item) => item.id === serverId);

    if (server) {
      setSettings(server);
      if (server.chainOnly) setScope("all");
      openGroup([`server:${server.id}`]);
      setParams({}, { replace: true });
    }
  }, [admin, servers, params, setParams, openGroup]);
  // A server whose group must open once the reloaded list arrives (设置 saved).
  const revealServer = useRef(0);

  useEffect(() => {
    const id = revealServer.current;

    if (!id || !servers.some((item) => item.id === id)) return;
    revealServer.current = 0;
    openGroup([`server:${id}`]);
  }, [servers, openGroup]);
  // Search: the first matching group opens.
  useEffect(() => {
    if (!query) return;
    const first =
      serverGroups.withNodes[0]?.key ??
      (visibleExternals.length ? EXTERNAL_GROUP_KEY : "");

    if (first) openGroup([first]);
  }, [query]);

  // Plan 146/169: hint on 「添加转发」 of the user's newest own active node until clicked once.
  const hintNode = nodes.reduce<LandingNode | null>(
    (newest, node) =>
      node.userId === currentUserId &&
      !node.deletePending &&
      (!newest || node.id > newest.id)
        ? node
        : newest,
    null,
  );
  const useNodeHint = useOnboardingHint("useNode", {
    enabled: hintNode?.status === "active",
  });
  const [openRow, setOpenRow] = useState(0);
  const nodeServer = (node: LandingNode) =>
    servers.find((server) => server.id === node.landingServerId);
  const actions = (node: LandingNode) => (
    <div className="flex flex-wrap gap-2">
      <Button
        color="success"
        size="sm"
        title="这个节点自己的链接（不经转发）；经转发的链接在下面各条转发里"
        variant="flat"
        onPress={() => setLinksFor(node)}
      >
        直连链接 / 二维码
      </Button>
      {admin && !node.deletePending && (
        <Button
          color="primary"
          size="sm"
          variant="flat"
          onPress={() => setDetails(node)}
        >
          节点详情
        </Button>
      )}
      {!node.deletePending && (
        <Button
          color="warning"
          size="sm"
          variant="flat"
          onPress={() => setRulesFor(node)}
        >
          {node.routeRuleCount
            ? `设置路由（${node.routeRuleCount}）`
            : "设置路由"}
        </Button>
      )}
      {/* Plan 177: 链式代理; muted (but tappable, shows the reason) where the
          server cannot carry it and the node is not chained. */}
      {!node.deletePending && node.chainSupport && (
        <Button
          data-chain-button
          color={
            node.chainSupport.supported || node.chain ? "primary" : "default"
          }
          size="sm"
          variant="flat"
          onPress={() => setChainFor(node)}
        >
          链式代理
        </Button>
      )}
      <Button
        ref={node.id === hintNode?.id ? useNodeHint.ref : undefined}
        className="relative"
        color="secondary"
        isDisabled={node.status !== "active"}
        size="sm"
        variant="flat"
        onPress={() => {
          useNodeHint.complete();
          navigate(
            `/forward?landingNodeId=${node.id}&landingUserId=${node.userId}`,
          );
        }}
      >
        添加转发
        {node.id === hintNode?.id && (
          <OnboardingRing active={useNodeHint.active} />
        )}
      </Button>
      <Button
        color="danger"
        size="sm"
        variant="flat"
        onPress={() => {
          setDeleteError("");
          setDeleting(node);
        }}
      >
        删除
      </Button>
    </div>
  );
  // Sync errors / pending reason lines (unchanged wording).
  const nodeNotes = (node: LandingNode) => (
    <>
      {node.syncError && (
        <span className="w-full text-xs text-danger">{node.syncError}</span>
      )}
      {node.status === "pending" && node.pendingReason && (
        <span className="w-full text-xs text-warning">
          {node.pendingReason}
        </span>
      )}
      {node.sharedSyncError && (
        <span className="w-full text-xs text-warning">
          同步异常 · {node.sharedSyncError}
        </span>
      )}
    </>
  );
  const status = (node: LandingNode) => (
    <div className="flex flex-wrap gap-1">
      <NodeStatusPill node={node} />
      {(node.forwardCount || 0) > 0 && (
        <Pill tone="purple">转发 {node.forwardCount}</Pill>
      )}
      {node.deletePending && <Pill tone="amber">删除中</Pill>}
      {nodeNotes(node)}
      <ScanBlockNote
        compact
        admin={admin}
        node={node}
        onUnblocked={() => void load()}
      />
    </div>
  );
  const now = Date.now();
  const usage = (node: LandingNode) =>
    `${landingGB(node.billedIn + node.billedOut)} / ${node.trafficLimitBytes ? landingGB(node.trafficLimitBytes) : "不限"} GB`;
  // Admin 全部用户: whose node / 外部节点 (the signed-in admin = 我).
  const ownerChip = (userId?: number, name?: string, userAdmin?: boolean) => {
    if (mineOnly) return null;
    const me = userId === currentUserId;

    return (
      <Pill
        title={me ? "我的" : `用户：${name || userId}`}
        tone={me ? "green" : userAdmin ? "cyan" : "violet"}
      >
        {me ? "我" : name || `用户 ${userId}`}
      </Pill>
    );
  };
  const nodeConn = (node: LandingNode) => (
    <>
      <span
        className={`inline-flex items-center gap-1 ${connTone(node, admin) === "red" ? "font-semibold text-danger" : connTone(node, admin) === "amber" ? "text-warning" : ""}`}
        title={nodeConnTitle(node, admin)}
      >
        <Plug
          aria-hidden
          className="h-3.5 w-3.5 shrink-0 text-purple-600 dark:text-purple-300"
        />
        {nodeConnText(node, admin)}
      </span>
      {admin && (
        <NodeTotalPending
          node={node}
          shared={nodeServer(node)?.source === "shared"}
        />
      )}
    </>
  );

  // Plan 327: node card (plan 149 content) laid out for a full-width tree
  // row — chips / usage on the left, actions on the right.
  const renderNodeCard = (node: LandingNode) => (
    <Card
      data-landing-node-card
      className={glowKey === `node:${node.id}` ? "flvx-mytree-glow" : undefined}
      data-landing-node-row={node.id}
    >
      <CardHeader className="flex flex-row items-start justify-between gap-2 pb-1 md:pb-1">
        <div className="min-w-0">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <h2 className="break-all text-sm font-bold">{node.name}</h2>
            <Pill title={landingProtocolLabel(node.protocol)} tone="pink">
              {landingProtocolLabel(node.protocol)}
            </Pill>
            {admin && (
              <span className="text-xs tabular-nums text-default-500">
                端口 {node.port}
              </span>
            )}
            {ownerChip(node.userId, node.userName, node.userAdmin)}
          </div>
          {node.protocolOpts && (
            <p className="mt-1 text-xs text-default-500">
              {landingProtocolSummary(node.protocol, node.protocolOpts)}
            </p>
          )}
          {isMieruUdp(node.protocol, node.protocolOpts?.transport) && (
            <p className="mt-1 text-xs text-warning-600 dark:text-warning-400">
              {MIERU_UDP_HINT}
            </p>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap justify-end gap-1">
          <NodeStatusPill node={node} />
          {node.deletePending && <Pill tone="amber">删除中</Pill>}
        </div>
      </CardHeader>
      <CardBody className="pt-1 md:pt-1">
        <div className="flex flex-col gap-3 md:flex-row md:items-end md:justify-between">
          <div className="min-w-0 space-y-2 md:max-w-[44%] md:flex-1">
            <div
              data-landing-node-chips
              className="flex flex-wrap items-center gap-1"
            >
              <ExpiryPill node={node} now={now} />
              <ConnPill
                admin={admin}
                node={node}
                shared={nodeServer(node)?.source === "shared"}
              />
              {(node.forwardCount || 0) > 0 && (
                <Pill tone="purple">转发 {node.forwardCount}</Pill>
              )}
              {node.chain ? (
                <ChainPill node={node} />
              ) : (
                <Pill title={`出站：${node.egressLabel || "直连"}`} tone="teal">
                  出站：{node.egressLabel || "直连"}
                </Pill>
              )}
            </div>
            <ScanBlockNote
              admin={admin}
              node={node}
              onUnblocked={() => void load()}
            />
            {(node.syncError ||
              node.sharedSyncError ||
              node.chain?.state === "error" ||
              (node.status === "pending" && node.pendingReason)) && (
              <div className="flex flex-wrap gap-1">
                {nodeNotes(node)}
                <ChainNote node={node} />
              </div>
            )}
            <div className="text-sm">
              <p className="flex flex-wrap items-baseline justify-between gap-x-2">
                <span className="text-xs text-default-500">已用 / 上限</span>
                <span className="tabular-nums">{usage(node)}</span>
              </p>
              <UsageBar node={node} />
              {node.expiredAt && (
                <p className="mt-2 text-warning">
                  宽限期结束：
                  {landingDate(node.expiredAt + GRACE_MS)}
                  ；期内续期可恢复原链接。
                </p>
              )}
            </div>
          </div>
          <div className="md:flex md:max-w-[56%] md:shrink-0 md:justify-end [&>div]:md:justify-end">
            {actions(node)}
          </div>
        </div>
      </CardBody>
    </Card>
  );
  const nodeDetail = (label: string, value: ReactNode) => (
    <>
      <dt className="pt-0.5 text-xs text-default-500">{label}</dt>
      <dd className="min-w-0 break-words text-sm">{value}</dd>
    </>
  );
  // Phones / H5: name + protocol + ＋/− row (memory h5-expand-row), details
  // and actions on expand; the children always show under the row.
  const renderNodeRow = (node: LandingNode) => {
    const open = openRow === node.id;
    const toggle = () => setOpenRow(open ? 0 : node.id);

    return (
      <div
        className={`rounded-lg border border-divider bg-content1${glowKey === `node:${node.id}` ? " flvx-mytree-glow" : ""}`}
        data-landing-node-row={node.id}
        data-open={open ? "true" : "false"}
      >
        <div
          aria-expanded={open}
          className="flex min-h-[50px] cursor-pointer select-none items-center gap-2 pl-3 pr-2"
          role="button"
          tabIndex={0}
          onClick={toggle}
          onKeyDown={(event) => {
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              toggle();
            }
          }}
        >
          <NodeStatusDot node={node} />
          <span
            className="min-w-0 flex-1 truncate text-sm font-medium"
            title={node.name}
          >
            {node.name}
          </span>
          {ownerChip(node.userId, node.userName, node.userAdmin)}
          <Pill title={landingProtocolLabel(node.protocol)} tone="pink">
            {landingProtocolShortLabel(node.protocol)}
          </Pill>
          <span
            aria-label={open ? "收起详情" : "展开详情"}
            className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full ${open ? "bg-slate-500/15 text-slate-600 dark:bg-slate-400/20 dark:text-slate-300" : "bg-teal-500/15 text-teal-700 dark:bg-teal-400/15 dark:text-teal-300"}`}
            data-landing-node-toggle={open ? "minus" : "plus"}
            role="img"
          >
            {open ? <Minus size={16} /> : <Plus size={16} />}
          </span>
        </div>
        {open && (
          <div
            className="space-y-3 border-t border-divider px-3 pb-3 pt-2.5"
            data-landing-node-details=""
          >
            <dl className="grid grid-cols-[4.5rem_minmax(0,1fr)] gap-x-2 gap-y-2">
              {admin &&
                nodeDetail(
                  "端口",
                  <span className="tabular-nums">{node.port}</span>,
                )}
              {nodeDetail(
                "协议",
                <>
                  <div>{landingProtocolLabel(node.protocol)}</div>
                  {node.protocolOpts && (
                    <div className="text-xs text-default-500">
                      {landingProtocolSummary(node.protocol, node.protocolOpts)}
                    </div>
                  )}
                  {isMieruUdp(node.protocol, node.protocolOpts?.transport) && (
                    <div className="text-xs text-warning-600 dark:text-warning-400">
                      {MIERU_UDP_HINT}
                    </div>
                  )}
                </>,
              )}
              {node.chain
                ? nodeDetail(
                    "链式",
                    <div className="flex flex-wrap gap-1">
                      <ChainPill node={node} />
                      <ChainNote node={node} />
                    </div>,
                  )
                : nodeDetail(
                    "出站",
                    <Pill
                      title={`出站：${node.egressLabel || "直连"}`}
                      tone="teal"
                    >
                      {node.egressLabel || "直连"}
                    </Pill>,
                  )}
              {nodeDetail("状态", status(node))}
              {nodeDetail(
                "已用 / 上限",
                <>
                  <span className="tabular-nums">{usage(node)}</span>
                  <UsageBar node={node} />
                </>,
              )}
              {nodeDetail("连接", nodeConn(node))}
              {nodeDetail(
                "到期",
                <>
                  <ExpiryPill node={node} now={now} />
                  {node.expiredAt && (
                    <p className="mt-1 text-xs text-warning">
                      宽限期结束：
                      {landingDate(node.expiredAt + GRACE_MS)}
                      ；期内续期可恢复原链接。
                    </p>
                  )}
                </>,
              )}
            </dl>
            {actions(node)}
          </div>
        )}
      </div>
    );
  };
  const kidsOf = (
    key: string,
    kids: TreeKid[],
    tone: "forward" | "external",
  ) => (
    <TreeKids
      key={key}
      compact={h5}
      forceOpen={
        Boolean(glowKey) &&
        kids.findIndex((kid) => kid.key === glowKey) >= KID_LIMIT
      }
      glowKey={glowKey}
      kids={kids}
      tone={tone}
      onGlowEnd={() => {
        if (glowKey.startsWith("forward:")) endGlow();
      }}
      onLocateExit={tone === "forward" ? (id) => locateExternal(id) : undefined}
    />
  );
  // One node = its card (desktop) / row (phone) + the child rows below it.
  const renderNodeItem = (node: LandingNode) => (
    <div className="min-w-0" data-mytree-node={node.id}>
      {h5 ? renderNodeRow(node) : renderNodeCard(node)}
      {kidsOf(`kids-${node.id}`, kidsByNode.get(node.id) || [], "forward")}
    </div>
  );

  // ---------------------------------------------------------- group header
  const groupMeta = (text: string) => (
    <span className="whitespace-nowrap text-xs text-default-500">{text}</span>
  );
  const stop = (event: { stopPropagation: () => void }) =>
    event.stopPropagation();
  const serverButtons = (server: LandingServer | undefined) => {
    if (!server) return null;
    const attention = attentionById.get(server.id) ?? null;
    const createDisabled =
      server.status !== "active" ||
      server.sharedAvailable === false ||
      Boolean(server.chainOnly) ||
      (!admin && server.myRemaining === 0) ||
      (!admin && Boolean(server.scanCreateBlocked));

    return (
      <div
        className="flex shrink-0 items-center gap-1.5"
        data-server-actions=""
        role="presentation"
        onClick={stop}
        onKeyDown={stop}
      >
        {!server.chainOnly && (
          <Button
            aria-label="创建节点"
            className={h5 ? "h-7 min-w-7 px-1.5" : "h-7"}
            color="success"
            isDisabled={createDisabled}
            size="sm"
            startContent={<Plus size={15} />}
            variant="flat"
            onPress={() => setCreating(server)}
          >
            {h5 ? null : "创建节点"}
          </Button>
        )}
        {admin && (
          <ServerSettingsButton
            attention={attention}
            compact={h5}
            onPress={() => setSettings(server)}
          />
        )}
        {admin && server.source === "shared" && !h5 && (
          <SharedDetailsButton
            server={server}
            onPress={() => setSharedDetails(server)}
          />
        )}
      </div>
    );
  };
  const renderServerHeader = (
    group: NodeServerGroup,
    server: LandingServer | undefined,
    collapsed: boolean,
  ) => {
    const flag = regionFlag(group.region);
    const attention = server ? (attentionById.get(server.id) ?? null) : null;
    const facts = server ? serverFacts(server, admin, mineOnly) : [];
    const meta =
      group.nodes.length > 0
        ? `${group.nodes.length} 个节点 · 已用 ${landingGB(group.used)} GB`
        : "还没有节点";
    const name = (
      <div className="flex min-w-0 items-center gap-1.5">
        {flag && (
          <span
            aria-hidden
            className="shrink-0 text-base leading-none"
            title={regionName(group.region)}
          >
            {flag}
          </span>
        )}
        <span
          className={`min-w-0 truncate text-sm ${collapsed ? "font-semibold" : "font-bold"} text-foreground`}
          title={group.name}
        >
          {group.name}
        </span>
        {attention && server && (
          <span
            className="flex shrink-0"
            role="presentation"
            onClick={stop}
            onKeyDown={stop}
          >
            <AttentionNameMarker
              attention={attention}
              onOpen={() => setSettings(server)}
            />
          </span>
        )}
        {server && serverStatusPill(server)}
      </div>
    );
    // The 协议 / 「+N」 chips open menus: their taps (also inside the menu
    // portal, which bubbles through React) must not toggle the group.
    const chips = server ? (
      <div
        className="min-w-0"
        role="presentation"
        onClick={(event) => {
          const target = event.target as HTMLElement;

          if (!event.currentTarget.contains(target) || target.closest("button"))
            event.stopPropagation();
        }}
      >
        <ChipLine items={serverChipItems(server, admin)} />
      </div>
    ) : null;
    const factLine = (
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-default-500">
        {facts.map((fact) => (
          <span
            key={fact}
            className={`whitespace-nowrap${fact === "已达上限" || fact === "暂不能新建" ? " text-warning" : ""}`}
          >
            {fact}
          </span>
        ))}
        {group.problems.size > 0 && <NodeProblemPills group={group} />}
      </div>
    );

    if (h5)
      return (
        <div className="flex min-w-0 flex-1 items-start gap-2">
          <TreeGroupChevron collapsed={collapsed} />
          <div className="min-w-0 flex-1 space-y-1">
            <div className="flex min-w-0 items-center gap-1.5">
              <div className="min-w-0 flex-1">{name}</div>
              {serverButtons(server)}
            </div>
            {chips}
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-default-500">
              <span className="whitespace-nowrap">{meta}</span>
              {facts.map((fact) => (
                <span
                  key={fact}
                  className={`whitespace-nowrap${fact === "已达上限" || fact === "暂不能新建" ? " text-warning" : ""}`}
                >
                  {fact}
                </span>
              ))}
              {group.problems.size > 0 && <NodeProblemPills group={group} />}
            </div>
          </div>
        </div>
      );

    return (
      <div className="flex min-w-0 flex-1 items-start gap-2">
        <TreeGroupChevron collapsed={collapsed} />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex min-w-0 items-center gap-3">
            <div className="min-w-0 shrink">{name}</div>
            <div className="min-w-0 flex-1">{chips}</div>
            {groupMeta(meta)}
            {serverButtons(server)}
          </div>
          {factLine}
        </div>
      </div>
    );
  };
  const renderServerGroup = (group: NodeServerGroup) => {
    const server = servers.find((item) => item.id === group.serverId);
    const collapsed = !groupOpen.isOpen(group.key);
    const toggle = () => groupOpen.toggle(group.key);

    return (
      <div
        key={group.key}
        className={`${treeGroupWrapperClass(collapsed)} flvx-landing-user-group${glowKey === group.key ? " flvx-mytree-glow" : ""}`}
        data-group-key={group.key}
        data-landing-server-group={group.key}
        {...(server && attentionById.get(server.id)
          ? { "data-attention-card": String(server.id) }
          : {})}
      >
        <TreeGroupHeader
          className={`${treeGroupHeaderClass(collapsed)} !items-start`}
          collapsed={collapsed}
          onToggle={toggle}
        >
          {renderServerHeader(group, server, collapsed)}
        </TreeGroupHeader>
        {!collapsed &&
          (group.nodes.length ? (
            <LandingCardTree
              cols="one"
              gridClassName="grid gap-4"
              items={group.nodes.map((node) => ({
                key: node.id,
                node: renderNodeItem(node),
              }))}
            />
          ) : (
            <div className="px-4 py-3 text-sm text-default-500">
              {server?.chainOnly
                ? "这台服务器只承载「我的外部节点」的线路机加速（链式出口），不对用户开放；这些链式出口显示在下面「外部节点」里各个节点的「线路机加速」。"
                : server?.scanCreateBlocked && !admin
                  ? server.scanCreateBlocked
                  : `${admin && !mineOnly ? "这台服务器上还没有节点" : "你在这台服务器上还没有节点"}，点「创建节点」开始。`}
            </div>
          ))}
        {!collapsed && <TreeGroupFooter name={group.name} onToggle={toggle} />}
      </div>
    );
  };
  // 我的外部节点 (last group, indigo tone).
  const renderExternalGroup = () => {
    if (!visibleExternals.length) return null;
    const collapsed = !groupOpen.isOpen(EXTERNAL_GROUP_KEY);
    const toggle = () => groupOpen.toggle(EXTERNAL_GROUP_KEY);
    const title = mineOnly ? "我的外部节点" : "外部节点 · 全部用户";
    const kidCount = visibleExternals.reduce(
      (sum, card) => sum + (externalCards.kids.get(card.key)?.length || 0),
      0,
    );

    return (
      <div
        className={`${treeGroupWrapperClass(collapsed)} flvx-mytree-ext`}
        data-group-key={EXTERNAL_GROUP_KEY}
        data-mytree-external-group=""
      >
        <TreeGroupHeader
          className={treeGroupHeaderClass(collapsed)}
          collapsed={collapsed}
          onToggle={toggle}
        >
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <TreeGroupChevron collapsed={collapsed} />
            <Link2
              aria-hidden
              className="h-4 w-4 shrink-0 text-indigo-600 dark:text-indigo-300"
            />
            <span
              className={`min-w-0 truncate text-sm ${collapsed ? "font-semibold" : "font-bold"} text-foreground`}
            >
              {title}
            </span>
            {groupMeta(
              `${visibleExternals.length} 个${kidCount ? ` · ${kidCount} 条转发` : ""}`,
            )}
          </div>
          <div
            className="flex shrink-0 items-center"
            role="presentation"
            onClick={stop}
            onKeyDown={stop}
          >
            <Button
              className="h-7"
              color="secondary"
              size="sm"
              variant="flat"
              onPress={() => setExternalsOpen(true)}
            >
              管理
            </Button>
          </div>
        </TreeGroupHeader>
        {!collapsed && (
          <LandingCardTree
            cols="one"
            gridClassName="grid gap-4"
            items={visibleExternals.map((card) => ({
              key: card.key,
              node: (
                <div className="min-w-0">
                  <ExternalNodeCard
                    card={card}
                    chainUsers={card.id ? chainUsersOf(card.id) : []}
                    compact={h5}
                    glow={glowKey === card.key}
                    owner={ownerChip(card.userId, card.userName)}
                    onLocateNode={(id) => locateNode(id)}
                  />
                  {kidsOf(
                    `kids-${card.key}`,
                    externalCards.kids.get(card.key) || [],
                    "external",
                  )}
                </div>
              ),
            }))}
          />
        )}
        {!collapsed && <TreeGroupFooter name={title} onToggle={toggle} />}
      </div>
    );
  };

  const extraProtocolsHint = (() => {
    if (admin) return "";
    // Plan 253: VLESS Encryption joins the same hint; plan 274: only the
    // extras some server really offers.
    const extras = LANDING_EXTRA_PROTOCOLS.filter((protocol) =>
      servers.some((server) =>
        landingCreatableProtocols(server).includes(protocol),
      ),
    ).map(landingProtocolLabel);
    const more = [
      ...(extras.length ? [extras.join(" / ")] : []),
      ...(servers.some((server) =>
        landingCreatableProtocols(server).includes("vless_enc"),
      )
        ? ["VLESS Encryption"]
        : []),
    ];

    return more.length ? `（部分服务器还支持 ${more.join(" / ")}）` : "";
  })();
  const noTree =
    !loading &&
    !serverGroups.withNodes.length &&
    !serverGroups.empty.length &&
    !visibleExternals.length;
  // Plan 272: 利群汇聚 rule links (kind leikwan) after the direct link.
  const directLinks = links.filter((link) => link.kind === "direct");
  const leikwanLinks = links.filter((link) => link.kind === "leikwan");
  const forwardLinkCount = links.filter(
    (link) => link.kind === "forward",
  ).length;

  return (
    <AnimatedPage className="px-3 py-8 lg:px-6">
      <div className="flvx-landing space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <h1 className="text-xl font-semibold">我的节点</h1>
            <p className="mt-1 text-sm text-default-500">
              {admin
                ? "落地节点、挂在节点下的转发和外部节点；服务器的设置在每台服务器右侧「设置」"
                : "在获授权的服务器上创建自己的 SS2022 或 VLESS 节点，转发和链式出口挂在节点下面"}
              {extraProtocolsHint}
            </p>
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            {admin && (
              <div
                aria-label="显示范围"
                className="inline-flex h-8 items-center rounded-lg bg-default-100 p-0.5 dark:bg-default-50/20"
                data-mytree-scope={scope}
                role="radiogroup"
              >
                {(
                  [
                    ["mine", "只看我的"],
                    ["all", "全部用户"],
                  ] as const
                ).map(([key, label]) => (
                  <button
                    key={key}
                    aria-checked={scope === key}
                    className={`h-7 rounded-md px-3 text-xs transition-colors ${scope === key ? "bg-content1 font-semibold text-foreground shadow-sm" : "font-medium text-default-500 hover:text-foreground"}`}
                    role="radio"
                    type="button"
                    onClick={() => setScope(key)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            )}
            {admin && (
              <input
                aria-label="搜索节点"
                className="h-8 w-44 rounded-lg border border-divider bg-content1 px-2.5 text-sm outline-none placeholder:text-default-400 focus:border-primary"
                data-mytree-search=""
                placeholder="搜索节点 / 用户 / 转发"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
            )}
            <Button
              isIconOnly
              aria-label="刷新"
              isLoading={loading}
              size="sm"
              variant="flat"
              onPress={load}
            >
              <RefreshCw size={16} />
            </Button>
          </div>
        </div>
        {error && (
          <p className="rounded-lg bg-danger-50 p-2 text-sm text-danger">
            {error}
          </p>
        )}
        {focusNote && (
          <p
            className="flex items-center gap-2 rounded-lg bg-amber-500/15 px-3 py-2 text-sm text-amber-700 dark:bg-amber-400/15 dark:text-amber-300"
            data-mytree-focus-note=""
          >
            <span className="min-w-0 flex-1">{focusNote}</span>
            <Button
              className="h-7"
              color="danger"
              size="sm"
              variant="flat"
              onPress={() => setFocusNote("")}
            >
              关闭
            </Button>
          </p>
        )}
        <div className="space-y-3" data-mytree="">
          {serverGroups.withNodes.length > 0 && (
            <p className="px-1 text-xs text-default-500">
              {serverGroups.withNodes.length} 台服务器 · {visibleNodes.length}{" "}
              个节点
              {!mineOnly && " · 全部用户"}
            </p>
          )}
          {serverGroups.withNodes.map(renderServerGroup)}
          {serverGroups.empty.length > 0 && (
            <p className="px-1 pt-2 text-xs text-default-500">
              {admin
                ? `还没有${mineOnly ? "我的" : ""}节点的服务器 · ${serverGroups.empty.length} 台`
                : `可以创建节点的服务器 · ${serverGroups.empty.length} 台`}
            </p>
          )}
          {serverGroups.empty.map(renderServerGroup)}
          {renderExternalGroup()}
        </div>
        {noTree && (
          <Card>
            <CardBody className="py-12 text-center text-sm text-default-500">
              {query
                ? "没有符合搜索的节点。"
                : admin
                  ? "暂无落地服务器，请在「服务器」页选择「启用为落地」。节点程序需要支持落地功能。"
                  : "暂无获授权的落地服务器，请联系管理员授权。"}
            </CardBody>
          </Card>
        )}
        {loading && !loaded && (
          <div className="flex justify-center p-4">
            <Spinner />
          </div>
        )}
        <SharedDetailsDialog
          server={sharedDetails}
          onClose={() => setSharedDetails(null)}
        />
        {settings && (
          <ServerSettings
            key={settings.id}
            server={settings}
            onClose={() => setSettings(null)}
            onProbed={() => void load()}
            onSaved={() => {
              revealServer.current = settings.id;
              void load();
            }}
          />
        )}
        {details && (
          <NodeDetails
            key={details.id}
            node={details}
            server={servers.find((item) => item.id === details.landingServerId)}
            onClose={() => setDetails(null)}
            onSaved={() => void load()}
          />
        )}
        {chainFor && (
          <ChainProxyDialog
            key={chainFor.id}
            admin={admin}
            node={chainFor}
            onClose={() => setChainFor(null)}
            onSaved={() => void load()}
          />
        )}
        {externalsOpen && (
          <SubExternalsDialog
            isH5={h5}
            onChanged={() => void load()}
            onClose={() => setExternalsOpen(false)}
          />
        )}
        {rulesFor && (
          <NodeRulesDialog
            key={rulesFor.id}
            chainName={rulesFor.chain?.name}
            nodeId={rulesFor.id}
            onClose={() => {
              setRulesFor(null);
              if (rulesStale.current) {
                rulesStale.current = false;
                void load();
              }
            }}
            onSaved={() => void load()}
            onScanState={(state) => {
              const card = servers.find(
                (server) => server.id === rulesFor.landingServerId,
              );

              if (
                admin &&
                card?.scanGuard &&
                state &&
                state !== card.scanGuard.state
              )
                rulesStale.current = true;
            }}
          />
        )}
        {creating && (
          <CreateLandingNodeDialog
            initialServer={creating}
            servers={servers}
            onClose={() => setCreating(null)}
            onCreated={async (node) => {
              toast.success("节点已创建");
              await load();
              // Open its server group and flash the new card.
              if (node?.id)
                reveal(
                  `server:${node.landingServerId}`,
                  `[data-landing-node-row="${node.id}"]`,
                  `node:${node.id}`,
                  1600,
                );
            }}
          />
        )}
        <Modal
          isOpen={Boolean(linksFor)}
          scrollBehavior="inside"
          size="lg"
          onClose={() => setLinksFor(null)}
        >
          <ModalContent className="flvx-landing">
            <ModalHeader>直连链接 · {linksFor?.name}</ModalHeader>
            <ModalBody className="space-y-4">
              {linksFor &&
                isMieruUdp(
                  linksFor.protocol,
                  linksFor.protocolOpts?.transport,
                ) && (
                  <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10">
                    该节点客户端配置使用 UDP 传输：{MIERU_UDP_HINT}。
                  </p>
                )}
              {linksLoading && <Spinner />}
              {linkError && <p className="text-danger">{linkError}</p>}
              {[...directLinks, ...leikwanLinks].map((link, index) => (
                <LandingLinkCard
                  key={`${link.kind}-${index}`}
                  link={link}
                  qrOpen={qr === link.url}
                  onToggleQR={() => setQR(qr === link.url ? "" : link.url)}
                >
                  {linksFor?.protocol === "anytls" && link.url && (
                    <p className="text-xs text-default-500">
                      链接导入（v2rayN、NekoBox、Shadowrocket
                      等）需允许自签证书；下方 Clash / mihomo
                      配置已固定证书指纹，更安全。
                    </p>
                  )}
                  {linksFor?.protocol === "vless_enc" && link.url && (
                    <p className="text-xs text-default-500">
                      {VLESS_ENC_HINT}。链接已包含加密参数，Clash Meta 请用
                      mihomo 1.19.13 或更新版本。
                    </p>
                  )}
                  {linksFor?.protocol === "snell" && !link.url && (
                    <p className="text-xs text-default-500">
                      Snell 没有通用的分享链接，请使用下方
                      {link.surge ? " Surge 或 " : " "}Clash / mihomo 配置。
                    </p>
                  )}
                </LandingLinkCard>
              ))}
              {!linksLoading && !linkError && (
                <p data-links-summary className="text-xs text-default-500">
                  {forwardLinkCount > 0
                    ? `经转发的链接（${forwardLinkCount} 条）在「我的节点」里这个节点下面，每条转发右侧「复制链接」。`
                    : "用此节点添加转发后，转发会显示在这个节点下面，每条转发都有自己的「复制链接」。"}
                </p>
              )}
            </ModalBody>
            <ModalFooter>
              <Button
                color="danger"
                variant="flat"
                onPress={() => setLinksFor(null)}
              >
                关闭
              </Button>
            </ModalFooter>
          </ModalContent>
        </Modal>
        <Modal isOpen={Boolean(deleting)} onClose={() => setDeleting(null)}>
          <ModalContent className="flvx-landing">
            <ModalHeader>删除落地节点</ModalHeader>
            <ModalBody>
              <p>
                删除「{deleting?.name}」将停用入站并释放端口，原链接无法再使用。
              </p>
              {deleteError && <p className="mt-3 text-danger">{deleteError}</p>}
            </ModalBody>
            <ModalFooter>
              <Button
                color="danger"
                variant="flat"
                onPress={() => setDeleting(null)}
              >
                取消
              </Button>
              <Button
                color="danger"
                isLoading={saving}
                onPress={async () => {
                  if (!deleting) return;
                  setSaving(true);
                  setDeleteError("");
                  const res = await deleteLandingNode(deleting.id);

                  setSaving(false);
                  if (res.code !== 0) {
                    setDeleteError(res.msg);
                    toast.error(res.msg);

                    return;
                  }
                  setDeleting(null);
                  toast.success(res.data?.pending ? res.msg : "节点已删除");
                  void load();
                }}
              >
                删除节点
              </Button>
            </ModalFooter>
          </ModalContent>
        </Modal>
      </div>
    </AnimatedPage>
  );
}
