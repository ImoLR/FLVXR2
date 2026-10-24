import { Suspense, lazy, useCallback, useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Copy, Plus, QrCode, RefreshCw, Settings2 } from "lucide-react";
import toast from "react-hot-toast";

import {
  ChipLine,
  ChipLineItem,
  SharedDetailsButton,
  SharedDetailsDialog,
} from "./landing/server-card";
import { IntegrationSettings } from "./landing/integration-settings";

import {
  LandingLink,
  LandingNode,
  LandingServer,
  MIERU_UDP_HINT,
  deleteLandingNode,
  isLandingExtraProtocol,
  isMieruUdp,
  landingCreatableProtocols,
  landingDate,
  landingGB,
  landingLinks,
  landingNodes,
  landingProtocolLabel,
  landingProtocolShortLabel,
  landingProtocolSummary,
  landingServerModeLabel,
  landingServers,
  landingStatus,
} from "@/api/landing";
import { AnimatedPage } from "@/components/animated-page";
import {
  OnboardingRing,
  useOnboardingHint,
} from "@/components/onboarding-ring";
import { usePullToRefresh } from "@/hooks/usePullToRefresh";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import { Tab, Tabs } from "@/shadcn-bridge/heroui/tabs";
import { isAdmin } from "@/utils/auth";
import { JwtUtil } from "@/utils/jwt";
import { CreateLandingNodeDialog } from "@/pages/landing/create-node";
import { LandingGlobalSettings } from "@/pages/landing/global-settings";
import { NodeDetails } from "@/pages/landing/node-details";
import { connModeTag } from "@/pages/landing/conn-mode";
import { NodeRulesDialog } from "@/pages/landing/node-rules";
import { ServerSettings } from "@/pages/landing/server-settings";
import { OfflineQRCode } from "@/pages/node/node-deploy-modal";

// Admin-only tab, loaded on demand to keep the main bundle unchanged.
const LandingEgressTab = lazy(() => import("@/pages/landing/egress"));

async function copyToClipboard(value: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);

      return true;
    }
    const input = document.createElement("textarea");

    input.value = value;
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.appendChild(input);
    input.select();
    const copied = document.execCommand("copy");

    input.remove();

    return copied;
  } catch {
    return false;
  }
}

// Plan 121: client config block (Clash / mihomo entry, Surge line) shown only
// for AnyTLS / Snell / Mieru links.
function LandingClientConfig({
  label,
  value,
}: {
  label: string;
  value?: string;
}) {
  if (!value) return null;

  return (
    <div className="space-y-2">
      <p className="text-xs font-medium text-default-600">{label}</p>
      <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded bg-default-100 p-2 text-xs">
        {value}
      </pre>
      <Button
        color="success"
        size="sm"
        startContent={<Copy size={15} />}
        variant="flat"
        onPress={() =>
          void copyToClipboard(value).then((ok) =>
            ok ? toast.success(`${label} 配置已复制`) : toast.error("复制失败"),
          )
        }
      >
        复制 {label} 配置
      </Button>
    </div>
  );
}

// Plan 132: a non-admin's server card shows only their own node count, how
// many more they can create there (both caps) and the server's end date.
function UserServerSummary({ server }: { server: LandingServer }) {
  const full = server.myRemaining === 0;

  return (
    <>
      <dl className="grid grid-cols-2 gap-x-3 gap-y-2">
        <div>
          <dt className="text-xs text-default-500">我的节点</dt>
          <dd className="mt-0.5 text-sm">{server.nodeCount ?? 0}</dd>
        </div>
        <div>
          <dt className="text-xs text-default-500">还可创建</dt>
          <dd
            className={full ? "mt-0.5 text-sm text-warning" : "mt-0.5 text-sm"}
          >
            {server.myRemaining === undefined
              ? "—"
              : full
                ? "已达上限"
                : server.myRemaining}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-default-500">可用至</dt>
          <dd className="mt-0.5 text-sm">
            {landingDate(server.availableUntil)}
          </dd>
        </div>
      </dl>
      {full && (
        <p className="text-xs text-default-500">
          已达到可创建的节点数，删除不用的节点后可再创建，或联系管理员调整。
        </p>
      )}
    </>
  );
}

// Plan 141: the server card's connection line. The node sum counts each node
// in its own 计算方式; the limit is the server total (native: own mode,
// shared: 妙妙屋's 入站+出站). One label when both count the same way.
function serverConnRow(server: LandingServer): [string, string] {
  const sum = server.connectionsMode ?? "total";
  const limit = server.maxConnectionsMode ?? "total";
  const value = `${server.connections ?? 0} / ${server.maxConnections || "不限"}`;

  if (!server.maxConnections || sum === limit) {
    return [`连接数 / 上限 ${connModeTag(sum)}`, value];
  }

  return [`连接数 ${connModeTag(sum)} / 上限 ${connModeTag(limit)}`, value];
}

// Plan 141: 「(入站)」 / 「(入站+出站)」 after a node's connection count; admins
// also see when a 仅入站 setting waits for an agent / 妙妙屋 upgrade.
function NodeConnTag({ node }: { node: LandingNode }) {
  if (!node.connModeEffective) return null;

  return (
    <span className="ml-1 text-xs text-default-500">
      {connModeTag(node.connModeEffective)}
      {node.connModePending && (
        <span
          className="ml-1 text-warning"
          title="仅入站需要升级节点程序（或妙妙屋侧升级）后生效，目前仍按入站+出站计算"
        >
          仅入站待升级
        </span>
      )}
    </span>
  );
}

export default function LandingPage() {
  const admin = isAdmin();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [servers, setServers] = useState<LandingServer[]>([]);
  const [nodes, setNodes] = useState<LandingNode[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [tab, setTab] = useState("servers");
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
  const [globalSettings, setGlobalSettings] = useState(false);
  const [rulesFor, setRulesFor] = useState<LandingNode | null>(null);
  const [sharedDetails, setSharedDetails] = useState<LandingServer | null>(
    null,
  );

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    const [serverRes, nodeRes] = await Promise.all([
      landingServers(),
      landingNodes(),
    ]);

    if (serverRes.code === 0) setServers(serverRes.data || []);
    else setError(serverRes.msg);
    if (nodeRes.code === 0) setNodes(nodeRes.data || []);
    else setError(nodeRes.msg);
    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);
  // H5 pull-to-refresh: reload the active tab (the egress tab remounts and
  // loads its own data).
  const [egressReload, setEgressReload] = useState(0);
  const refresh = useCallback(async () => {
    if (tab === "egress") {
      setEgressReload((value) => value + 1);

      return;
    }
    await load();
  }, [tab, load]);

  usePullToRefresh(refresh);
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
    const serverId = Number(params.get("server"));

    if (!admin || !serverId || !servers.length) return;
    const server = servers.find((item) => item.id === serverId);

    if (server) {
      setSettings(server);
      setParams({}, { replace: true });
    }
  }, [admin, servers, params, setParams]);
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
  // Plan 146: first-time hint on 「用于新规则」 of the user's newest own node, once it is active.
  const currentUserId = JwtUtil.getUserIdFromToken();
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
    enabled: tab === "nodes" && hintNode?.status === "active",
  });
  const actions = (node: LandingNode) => (
    <div className="flex flex-wrap gap-2">
      <Button
        color="success"
        size="sm"
        variant="flat"
        onPress={() => setLinksFor(node)}
      >
        链接 / 二维码
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
            ? `路由规则（${node.routeRuleCount}）`
            : "路由规则"}
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
        用于新规则
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
  const status = (node: LandingNode) => (
    <div className="flex flex-wrap gap-1">
      <Chip
        className={node.status === "disabled" ? "text-default-600" : undefined}
        color={
          node.status === "active"
            ? "success"
            : node.status === "disabled"
              ? "default"
              : "warning"
        }
        size="sm"
        variant={node.status === "disabled" ? "bordered" : "flat"}
      >
        {landingStatus[node.status] || node.status}
      </Chip>
      {(node.forwardCount || 0) > 0 && (
        <Chip
          className="text-default-600"
          color="default"
          size="sm"
          variant="bordered"
        >
          规则 {node.forwardCount}
        </Chip>
      )}
      {node.deletePending && (
        <Chip color="warning" size="sm" variant="flat">
          删除中
        </Chip>
      )}
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
      {node.expiringSoon && (
        <Chip color="warning" size="sm" variant="flat">
          即将到期
        </Chip>
      )}
    </div>
  );
  const usage = (node: LandingNode) =>
    `${landingGB(node.billedIn + node.billedOut)} / ${node.trafficLimitBytes ? landingGB(node.trafficLimitBytes) : "不限"} GB`;

  return (
    <AnimatedPage className="px-3 py-8 lg:px-6">
      <div className="flvx-landing space-y-4">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <h1 className="text-xl font-semibold">落地</h1>
            <p className="mt-1 text-sm text-default-500">
              {admin
                ? "管理自有与共享落地服务器、节点和独立限制"
                : "选择获授权的服务器，创建自己的 SS2022 或 VLESS 节点"}
              {!admin &&
                servers.some((server) =>
                  landingCreatableProtocols(server).some(
                    isLandingExtraProtocol,
                  ),
                ) &&
                "（部分服务器还支持 AnyTLS / Snell / Mieru）"}
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {admin && (
              <Button
                color="warning"
                size="sm"
                startContent={<Settings2 size={16} />}
                variant="flat"
                onPress={() => setGlobalSettings(true)}
              >
                全局设置
              </Button>
            )}
            <Button
              isIconOnly
              aria-label="刷新落地"
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
        {admin && <IntegrationSettings onSynced={() => void load()} />}
        <Tabs
          aria-label="落地管理"
          selectedKey={tab}
          onSelectionChange={(key) => setTab(String(key))}
        >
          <Tab key="servers" title={admin ? "落地服务器" : "可用落地"}>
            {/* Plan 145: same grid as 隧道/节点 cards; auto-rows-fr + h-full
                cards = every card the same size, also across rows. */}
            <div className="flvx-card-grid mt-4 grid auto-rows-fr gap-4">
              {servers.map((server) => {
                const online = server.online && server.status === "active";
                const sourceItems: ChipLineItem[] = [];
                const chipItems: ChipLineItem[] = [];

                if (admin) {
                  const source =
                    server.source === "shared" ? "妙妙屋共享" : "FLVX 自有落地";

                  sourceItems.push({
                    key: "source",
                    label: source,
                    node: (
                      <Chip
                        className="text-default-600"
                        size="sm"
                        variant="bordered"
                      >
                        {source}
                      </Chip>
                    ),
                  });
                  const mode =
                    server.source === "shared"
                      ? landingServerModeLabel(server.sharedCaps?.server_mode)
                      : "";

                  if (mode)
                    sourceItems.push({
                      key: "mode",
                      label: `妙妙屋 · ${mode}`,
                      node: (
                        <Chip
                          className="text-default-600"
                          size="sm"
                          title="妙妙屋官方给这台服务器设置的运行模式，仅供参考；FLVX 节点运行在 FLVX 独立的 Core 中，连接数、在线 IP、流量限制照常生效"
                          variant="bordered"
                        >
                          妙妙屋 · {mode}
                        </Chip>
                      ),
                    });
                  if (server.chainOnly)
                    sourceItems.push({
                      key: "chain",
                      label: "链式",
                      node: (
                        <Chip
                          color="secondary"
                          size="sm"
                          title="自动安装，只承载「我自己的节点」链式规则，不对用户开放；在节点页「启用为落地」可转为公开落地"
                          variant="flat"
                        >
                          链式
                        </Chip>
                      ),
                    });
                  if ((server.chainNodeCount ?? 0) > 0)
                    sourceItems.push({
                      key: "chain-nodes",
                      label: `链式节点 ${server.chainNodeCount}`,
                      node: (
                        <Chip
                          className="text-default-600"
                          size="sm"
                          variant="bordered"
                        >
                          链式节点 {server.chainNodeCount}
                        </Chip>
                      ),
                    });
                }
                const flow = `${server.flowType === 2 ? "双向" : "单向"} · ${server.trafficRatio}×`;

                chipItems.push({
                  key: "flow",
                  label: flow,
                  node: (
                    <Chip color="secondary" size="sm" variant="flat">
                      {flow}
                    </Chip>
                  ),
                });
                for (const protocol of server.protocols || [])
                  chipItems.push({
                    key: `p-${protocol}`,
                    label: landingProtocolLabel(protocol),
                    node: (
                      <Chip
                        className="text-default-600"
                        size="sm"
                        title={landingProtocolLabel(protocol)}
                        variant="bordered"
                      >
                        {landingProtocolShortLabel(protocol)}
                      </Chip>
                    ),
                  });
                if (server.egressLabel)
                  chipItems.push({
                    key: "egress",
                    label: `出站：${server.egressLabel}`,
                    node: (
                      <Chip color="success" size="sm" variant="flat">
                        出站：{server.egressLabel}
                      </Chip>
                    ),
                  });
                if (admin && server.egress?.state === "error")
                  chipItems.push({
                    key: "egress-error",
                    label: "出站与路由应用失败",
                    node: (
                      <Chip color="danger" size="sm" variant="flat">
                        出站与路由应用失败
                      </Chip>
                    ),
                  });
                if (admin && server.egress?.state === "restart_required")
                  chipItems.push({
                    key: "egress-restart",
                    label: "出站与路由待重启生效",
                    node: (
                      <Chip color="warning" size="sm" variant="flat">
                        出站与路由待重启生效
                      </Chip>
                    ),
                  });
                if (admin && server.egress?.coreUpgradeRequired)
                  chipItems.push({
                    key: "core-upgrade",
                    label: "落地 Core 待升级",
                    node: (
                      <Chip
                        color="warning"
                        size="sm"
                        title="新版落地 Core 已下载，在「设置」里确认重启一次后生效"
                        variant="flat"
                      >
                        落地 Core 待升级
                      </Chip>
                    ),
                  });

                return (
                  <Card
                    key={server.id}
                    className="flex h-full min-w-0 flex-col"
                    data-landing-server-card={server.source}
                  >
                    <CardHeader className="flex flex-row items-center justify-between gap-2 pb-1.5 md:pb-1.5 md:pt-[18px]">
                      <h2
                        className="min-w-0 truncate text-sm font-bold"
                        title={server.name}
                      >
                        {server.name}
                      </h2>
                      <Chip
                        className={
                          online ? "shrink-0" : "shrink-0 text-default-600"
                        }
                        color={online ? "success" : "default"}
                        size="sm"
                        variant={online ? "flat" : "bordered"}
                      >
                        {server.status !== "active"
                          ? "已停用"
                          : server.online
                            ? "在线"
                            : "离线"}
                      </Chip>
                    </CardHeader>
                    <CardBody className="flex flex-1 flex-col gap-3 pb-3.5 md:pb-3.5">
                      {admin && <ChipLine items={sourceItems} />}
                      <ChipLine items={chipItems} />
                      {admin ? (
                        <dl className="grid grid-cols-2 gap-x-3 gap-y-2">
                          {(
                            [
                              ["计费流量", `${landingGB(server.usedBytes)} GB`],
                              serverConnRow(server),
                              [
                                "节点数",
                                `${server.nodeCount ?? 0} · 每人最多 ${server.maxNodesPerUser}`,
                              ],
                              ["可用至", landingDate(server.availableUntil)],
                            ] as const
                          ).map(([label, value]) => (
                            <div key={label} className="min-w-0">
                              <dt className="text-xs text-default-500">
                                {label}
                              </dt>
                              <dd
                                className="mt-0.5 truncate text-sm"
                                title={value}
                              >
                                {value}
                              </dd>
                            </div>
                          ))}
                        </dl>
                      ) : (
                        <UserServerSummary server={server} />
                      )}
                      <div className="mt-auto flex flex-nowrap gap-2 pt-1">
                        <Button
                          color="success"
                          isDisabled={
                            server.status !== "active" ||
                            server.sharedAvailable === false ||
                            (!admin && server.myRemaining === 0)
                          }
                          size="sm"
                          startContent={<Plus size={16} />}
                          onPress={() => setCreating(server)}
                        >
                          创建节点
                        </Button>
                        {admin && (
                          <Button
                            color="warning"
                            size="sm"
                            startContent={<Settings2 size={16} />}
                            variant="flat"
                            onPress={() => setSettings(server)}
                          >
                            设置
                          </Button>
                        )}
                        {admin && server.source === "shared" && (
                          <SharedDetailsButton
                            server={server}
                            onPress={() => setSharedDetails(server)}
                          />
                        )}
                      </div>
                    </CardBody>
                  </Card>
                );
              })}
            </div>
            {!loading && !servers.length && (
              <Card className="mt-4">
                <CardBody className="py-12 text-center text-sm text-default-500">
                  {admin
                    ? "暂无落地服务器，请在「节点」页选择「启用为落地」。节点程序需要支持落地功能。"
                    : "暂无获授权的落地服务器，请联系管理员授权。"}
                </CardBody>
              </Card>
            )}
          </Tab>
          <Tab key="nodes" title={admin ? "全部节点" : "我的节点"}>
            <div className="mt-4">
              {admin && nodes.length > 0 ? (
                <Card>
                  <CardBody>
                    <div className="overflow-x-auto">
                      <table className="w-full text-sm">
                        <thead>
                          <tr className="border-b text-left text-default-500">
                            {[
                              "名称 / 用户",
                              "服务器 / 协议",
                              "状态",
                              "已用 / 上限",
                              "连接数",
                              "到期",
                              "操作",
                            ].map((label) => (
                              <th
                                key={label}
                                className="whitespace-nowrap px-3 py-2 text-xs font-semibold"
                              >
                                {label}
                              </th>
                            ))}
                          </tr>
                        </thead>
                        <tbody>
                          {nodes.map((node) => (
                            <tr
                              key={node.id}
                              className="border-b border-divider"
                            >
                              <td className="px-3 py-2">
                                <div className="font-medium">{node.name}</div>
                                <div className="text-xs text-default-500">
                                  {node.userName || `用户 ${node.userId}`}
                                </div>
                              </td>
                              <td className="px-3 py-2">
                                <div className="whitespace-nowrap">
                                  {node.serverName ||
                                    servers.find(
                                      (server) =>
                                        server.id === node.landingServerId,
                                    )?.name}
                                </div>
                                <div className="whitespace-nowrap text-xs text-default-500">
                                  {landingProtocolLabel(node.protocol)} ·{" "}
                                  {node.port}
                                </div>
                                {node.protocolOpts && (
                                  <div className="text-xs text-default-500">
                                    {landingProtocolSummary(
                                      node.protocol,
                                      node.protocolOpts,
                                    )}
                                  </div>
                                )}
                                {isMieruUdp(
                                  node.protocol,
                                  node.protocolOpts?.transport,
                                ) && (
                                  <div className="text-xs text-warning-600 dark:text-warning-400">
                                    {MIERU_UDP_HINT}
                                  </div>
                                )}
                                {node.egressLabel && (
                                  <div className="whitespace-nowrap text-xs text-default-500">
                                    出站：{node.egressLabel}
                                  </div>
                                )}
                              </td>
                              <td className="px-3 py-2">{status(node)}</td>
                              <td className="whitespace-nowrap px-3 py-2">
                                {usage(node)}
                              </td>
                              <td className="whitespace-nowrap px-3 py-2">
                                {node.connections ?? 0} /{" "}
                                {node.maxConnections || "不限"}
                                <NodeConnTag node={node} />
                              </td>
                              <td className="px-3 py-2">
                                {landingDate(node.effectiveExpiresAt)}
                              </td>
                              <td className="min-w-64 px-3 py-2">
                                {actions(node)}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </CardBody>
                </Card>
              ) : (
                <div className="grid gap-4 md:grid-cols-2">
                  {nodes.map((node) => (
                    <Card key={node.id}>
                      <CardHeader className="flex items-start justify-between gap-2">
                        <div>
                          <h2 className="text-sm font-bold">{node.name}</h2>
                          <p className="mt-1 text-xs text-default-500">
                            {node.serverName ||
                              servers.find(
                                (server) => server.id === node.landingServerId,
                              )?.name}{" "}
                            · {landingProtocolLabel(node.protocol)}
                          </p>
                          {node.protocolOpts && (
                            <p className="mt-1 text-xs text-default-500">
                              {landingProtocolSummary(
                                node.protocol,
                                node.protocolOpts,
                              )}
                            </p>
                          )}
                          {isMieruUdp(
                            node.protocol,
                            node.protocolOpts?.transport,
                          ) && (
                            <p className="mt-1 text-xs text-warning-600 dark:text-warning-400">
                              {MIERU_UDP_HINT}
                            </p>
                          )}
                        </div>
                        {status(node)}
                      </CardHeader>
                      <CardBody className="space-y-3">
                        <div className="text-sm">
                          <p>已用 / 上限：{usage(node)}</p>
                          {admin && (
                            <p className="mt-2">
                              连接数：{node.connections ?? 0} /{" "}
                              {node.maxConnections || "不限"}
                              <NodeConnTag node={node} />
                            </p>
                          )}
                          {node.egressLabel && (
                            <p className="mt-2">出站：{node.egressLabel}</p>
                          )}
                          <p className="mt-2">
                            到期：{landingDate(node.effectiveExpiresAt)}
                            {node.expiryMode === "follow_user" && (
                              <span className="ml-2 text-default-500">
                                跟随用户
                              </span>
                            )}
                          </p>
                          {node.expiredAt && (
                            <p className="mt-2 text-warning">
                              宽限期结束：
                              {landingDate(node.expiredAt + 7 * 86400000)}
                              ；期内续期可恢复原链接。
                            </p>
                          )}
                        </div>
                        {actions(node)}
                      </CardBody>
                    </Card>
                  ))}
                </div>
              )}
              {!loading && !nodes.length && (
                <Card>
                  <CardBody className="py-12 text-center text-default-500">
                    暂无落地节点，在「{admin ? "落地服务器" : "可用落地"}
                    」中选择服务器创建。
                  </CardBody>
                </Card>
              )}
            </div>
          </Tab>
          {admin && (
            <Tab key="egress" title="出站与路由">
              <Suspense
                fallback={
                  <div className="flex justify-center p-6">
                    <Spinner />
                  </div>
                }
              >
                {tab === "egress" && <LandingEgressTab key={egressReload} />}
              </Suspense>
            </Tab>
          )}
        </Tabs>
        {loading && (
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
            onSaved={() => void load()}
          />
        )}
        {globalSettings && (
          <LandingGlobalSettings onClose={() => setGlobalSettings(false)} />
        )}
        {details && (
          <NodeDetails
            key={details.id}
            node={details}
            server={servers.find((item) => item.id === details.landingServerId)}
            onClose={() => setDetails(null)}
            onOpenRules={(node) => setRulesFor(node)}
            onSaved={() => void load()}
          />
        )}
        {rulesFor && (
          <NodeRulesDialog
            key={rulesFor.id}
            nodeId={rulesFor.id}
            onClose={() => setRulesFor(null)}
            onSaved={() => void load()}
          />
        )}
        {creating && (
          <CreateLandingNodeDialog
            initialServer={creating}
            servers={servers}
            onClose={() => setCreating(null)}
            onCreated={async () => {
              setTab("nodes");
              toast.success("节点已创建");
              await load();
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
            <ModalHeader>节点链接 · {linksFor?.name}</ModalHeader>
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
              {links.map((link, index) => (
                <div
                  key={`${link.kind}-${index}`}
                  className="space-y-2 rounded-lg border border-divider p-3"
                >
                  <h3 className="font-medium">
                    {link.kind === "direct" ? "直连链接" : "经规则转发"} ·{" "}
                    {link.name}
                  </h3>
                  {link.url && (
                    <p className="break-all rounded bg-default-100 p-2 text-xs">
                      {link.url}
                    </p>
                  )}
                  {link.url && (
                    <div className="flex gap-2">
                      <Button
                        color="success"
                        size="sm"
                        startContent={<Copy size={15} />}
                        variant="flat"
                        onPress={() =>
                          void copyToClipboard(link.url).then((ok) =>
                            ok
                              ? toast.success("链接已复制")
                              : toast.error("复制失败"),
                          )
                        }
                      >
                        复制链接
                      </Button>
                      <Button
                        color="secondary"
                        size="sm"
                        startContent={<QrCode size={15} />}
                        variant="flat"
                        onPress={() => setQR(qr === link.url ? "" : link.url)}
                      >
                        二维码
                      </Button>
                    </div>
                  )}
                  {link.url && qr === link.url && (
                    <div className="flex justify-center py-2">
                      <OfflineQRCode value={link.url} />
                    </div>
                  )}
                  {linksFor?.protocol === "anytls" && link.url && (
                    <p className="text-xs text-default-500">
                      链接导入（v2rayN、NekoBox、Shadowrocket
                      等）需允许自签证书；下方 Clash / mihomo
                      配置已固定证书指纹，更安全。
                    </p>
                  )}
                  {linksFor?.protocol === "snell" && !link.url && (
                    <p className="text-xs text-default-500">
                      Snell 没有通用的分享链接，请使用下方 Surge 或 Clash /
                      mihomo 配置。
                    </p>
                  )}
                  <LandingClientConfig
                    label="Clash / mihomo"
                    value={link.clash}
                  />
                  <LandingClientConfig label="Surge" value={link.surge} />
                </div>
              ))}
              {!linksLoading &&
                !linkError &&
                !links.some((link) => link.kind === "forward") && (
                  <p className="text-xs text-default-500">
                    将此节点用于新规则后，每个规则入口会生成一条转发链接。
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
