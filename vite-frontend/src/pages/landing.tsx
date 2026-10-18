import { IntegrationSettings } from "./landing/integration-settings";
import { SharedCaps } from "./landing/shared-caps";

import { useCallback, useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Copy, Plus, QrCode, RefreshCw, Settings2 } from "lucide-react";
import toast from "react-hot-toast";

import {
  LandingLink,
  LandingNode,
  LandingProtocol,
  LandingServer,
  createLandingNode,
  deleteLandingNode,
  landingDate,
  landingGB,
  landingLinks,
  landingNodes,
  landingProtocolLabel,
  landingServers,
  landingStatus,
} from "@/api/landing";
import { AnimatedPage } from "@/components/animated-page";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import { Tab, Tabs } from "@/shadcn-bridge/heroui/tabs";
import { isAdmin } from "@/utils/auth";
import { NodeDetails } from "@/pages/landing/node-details";
import { ServerSettings } from "@/pages/landing/server-settings";
import { OfflineQRCode } from "@/pages/node/node-deploy-modal";

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
  const [creating, setCreating] = useState<{
    server: LandingServer;
    name: string;
    protocol: LandingProtocol;
  } | null>(null);
  const [deleting, setDeleting] = useState<LandingNode | null>(null);
  const [linksFor, setLinksFor] = useState<LandingNode | null>(null);
  const [links, setLinks] = useState<LandingLink[]>([]);
  const [linkError, setLinkError] = useState("");
  const [linksLoading, setLinksLoading] = useState(false);
  const [qr, setQR] = useState("");
  const [saving, setSaving] = useState(false);

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
  const actions = (node: LandingNode) => (
    <div className="flex flex-wrap gap-2">
      <Button size="sm" variant="flat" onPress={() => setLinksFor(node)}>
        链接 / 二维码
      </Button>
      {admin && !node.deletePending && (
        <Button size="sm" variant="flat" onPress={() => setDetails(node)}>
          节点详情
        </Button>
      )}
      <Button
        isDisabled={node.status !== "active"}
        size="sm"
        variant="flat"
        onPress={() =>
          navigate(
            `/forward?landingNodeId=${node.id}&landingUserId=${node.userId}`,
          )
        }
      >
        用于新规则
      </Button>
      <Button
        color="danger"
        size="sm"
        variant="light"
        onPress={() => setDeleting(node)}
      >
        删除
      </Button>
    </div>
  );
  const status = (node: LandingNode) => (
    <div className="flex flex-wrap gap-1">
      <Chip
        color={
          node.status === "active"
            ? "success"
            : node.status === "disabled"
              ? "default"
              : "warning"
        }
        size="sm"
        variant="flat"
      >
        {landingStatus[node.status] || node.status}
      </Chip>
      {node.deletePending && (
        <Chip color="warning" size="sm" variant="flat">
          删除中
        </Chip>
      )}
      {node.syncError && (
        <span className="w-full text-xs text-danger">{node.syncError}</span>
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
    <AnimatedPage>
      <div className="space-y-6">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h1 className="text-2xl font-bold">落地</h1>
            <p className="mt-1 text-sm text-default-500">
              {admin
                ? "管理自有与共享落地服务器、节点和独立限制"
                : "选择获授权的服务器，创建自己的 SS2022 或 VLESS 节点"}
            </p>
          </div>
          <Button
            isIconOnly
            aria-label="刷新落地"
            isLoading={loading}
            variant="flat"
            onPress={load}
          >
            <RefreshCw size={18} />
          </Button>
        </div>
        {error && (
          <p className="rounded-lg bg-danger-50 p-3 text-sm text-danger">
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
            <div className="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              {servers.map((server) => (
                <Card key={server.id}>
                  <CardHeader className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <h2 className="truncate font-semibold">{server.name}</h2>
                      <p className="mt-1 text-xs text-default-500">
                        {server.source === "shared"
                          ? "妙妙屋共享"
                          : "FLVX 自有落地"}
                      </p>
                    </div>
                    <Chip
                      color={
                        server.online && server.status === "active"
                          ? "success"
                          : "default"
                      }
                      size="sm"
                      variant="flat"
                    >
                      {server.status !== "active"
                        ? "已停用"
                        : server.online
                          ? "在线"
                          : "离线"}
                    </Chip>
                  </CardHeader>
                  <CardBody className="space-y-4">
                    {server.source === "shared" && (
                      <>
                        <SharedCaps server={server} />
                        <p className="text-xs text-default-500">
                          最近成功同步：
                          {server.sharedLastSyncAt
                            ? landingDate(server.sharedLastSyncAt)
                            : "尚未同步"}
                        </p>
                        {server.sharedSyncError && (
                          <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning">
                            同步异常 · {server.sharedSyncError}
                          </p>
                        )}
                      </>
                    )}
                    <div className="flex flex-wrap gap-2">
                      <Chip color="primary" size="sm" variant="flat">
                        {server.flowType === 2 ? "双向" : "单向"} ·{" "}
                        {server.trafficRatio}×
                      </Chip>
                      {(server.protocols || []).map((protocol) => (
                        <Chip key={protocol} size="sm" variant="flat">
                          {landingProtocolLabel(protocol)}
                        </Chip>
                      ))}
                    </div>
                    <dl className="grid grid-cols-2 gap-3 text-sm">
                      <div>
                        <dt className="text-default-500">计费流量</dt>
                        <dd className="mt-1">
                          {landingGB(server.usedBytes)} GB
                        </dd>
                      </div>
                      <div>
                        <dt className="text-default-500">连接数 / 上限</dt>
                        <dd className="mt-1">
                          {server.connections ?? 0} /{" "}
                          {server.maxConnections || "不限"}
                        </dd>
                      </div>
                      <div>
                        <dt className="text-default-500">节点数</dt>
                        <dd className="mt-1">
                          {server.nodeCount ?? 0} · 每人最多{" "}
                          {server.maxNodesPerUser}
                        </dd>
                      </div>
                      <div>
                        <dt className="text-default-500">可用至</dt>
                        <dd className="mt-1">
                          {landingDate(server.availableUntil)}
                        </dd>
                      </div>
                    </dl>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        color="primary"
                        isDisabled={
                          server.status !== "active" ||
                          server.sharedAvailable === false
                        }
                        size="sm"
                        startContent={<Plus size={16} />}
                        onPress={() =>
                          setCreating({
                            server,
                            name: "",
                            protocol: server.protocols[0] || "ss2022",
                          })
                        }
                      >
                        创建节点
                      </Button>
                      {admin && (
                        <Button
                          size="sm"
                          startContent={<Settings2 size={16} />}
                          variant="flat"
                          onPress={() => setSettings(server)}
                        >
                          设置
                        </Button>
                      )}
                    </div>
                  </CardBody>
                </Card>
              ))}
            </div>
            {!loading && !servers.length && (
              <Card className="mt-5">
                <CardBody className="py-12 text-center text-default-500">
                  {admin
                    ? "暂无落地服务器，请在「节点」页选择「启用为落地」。节点程序需要支持落地功能。"
                    : "暂无获授权的落地服务器，请联系管理员分配用户组。"}
                </CardBody>
              </Card>
            )}
          </Tab>
          <Tab key="nodes" title={admin ? "全部节点" : "我的节点"}>
            <div className="mt-5">
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
                              <th key={label} className="whitespace-nowrap p-3">
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
                              <td className="p-3">
                                <div className="font-medium">{node.name}</div>
                                <div className="text-xs text-default-500">
                                  {node.userName || `用户 ${node.userId}`}
                                </div>
                              </td>
                              <td className="p-3">
                                <div>
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
                              </td>
                              <td className="p-3">{status(node)}</td>
                              <td className="whitespace-nowrap p-3">
                                {usage(node)}
                              </td>
                              <td className="p-3">
                                {node.connections ?? 0} /{" "}
                                {node.maxConnections || "不限"}
                              </td>
                              <td className="p-3">
                                {landingDate(node.effectiveExpiresAt)}
                              </td>
                              <td className="min-w-64 p-3">{actions(node)}</td>
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
                          <h2 className="font-semibold">{node.name}</h2>
                          <p className="mt-1 text-xs text-default-500">
                            {node.serverName ||
                              servers.find(
                                (server) => server.id === node.landingServerId,
                              )?.name}{" "}
                            · {landingProtocolLabel(node.protocol)}
                          </p>
                        </div>
                        {status(node)}
                      </CardHeader>
                      <CardBody className="space-y-4">
                        <div className="text-sm">
                          <p>已用 / 上限：{usage(node)}</p>
                          <p className="mt-2">
                            连接数：{node.connections ?? 0} /{" "}
                            {node.maxConnections || "不限"}
                          </p>
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
        </Tabs>
        {loading && (
          <div className="flex justify-center p-4">
            <Spinner />
          </div>
        )}
        {settings && (
          <ServerSettings
            key={settings.id}
            server={settings}
            onClose={() => setSettings(null)}
            onSaved={() => void load()}
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
        <Modal
          isOpen={Boolean(creating)}
          scrollBehavior="inside"
          onClose={() => setCreating(null)}
        >
          <ModalContent>
            <ModalHeader>创建落地节点</ModalHeader>
            <ModalBody className="space-y-4">
              {creating && (
                <>
                  <p className="text-sm text-default-500">
                    {creating.server.name} ·{" "}
                    {creating.server.flowType === 2 ? "双向" : "单向"}{" "}
                    {creating.server.trafficRatio}×
                  </p>
                  <Input
                    label="节点名称"
                    placeholder="例如：我的日本节点"
                    value={creating.name}
                    variant="bordered"
                    onChange={(event) =>
                      setCreating({ ...creating, name: event.target.value })
                    }
                  />
                  <Select
                    label="协议模板"
                    selectedKeys={[creating.protocol]}
                    variant="bordered"
                    onSelectionChange={(keys) =>
                      setCreating({
                        ...creating,
                        protocol: String(
                          Array.from(keys)[0],
                        ) as LandingProtocol,
                      })
                    }
                  >
                    {creating.server.protocols.map((protocol) => (
                      <SelectItem key={protocol}>
                        {landingProtocolLabel(protocol)}
                      </SelectItem>
                    ))}
                  </Select>
                  <p className="text-xs text-default-500">
                    端口自动分配
                    {creating.server.source === "shared"
                      ? `（${creating.server.portMin}–${creating.server.portMax}）`
                      : ""}
                    。连接数、流量上限、倍率和有效期由管理员设置。
                  </p>
                </>
              )}
            </ModalBody>
            <ModalFooter>
              <Button variant="light" onPress={() => setCreating(null)}>
                取消
              </Button>
              <Button
                color="primary"
                isDisabled={!creating?.name.trim()}
                isLoading={saving}
                onPress={async () => {
                  if (!creating) return;
                  setSaving(true);
                  const res = await createLandingNode({
                    landingServerId: creating.server.id,
                    name: creating.name,
                    protocol: creating.protocol,
                  });
                  setSaving(false);
                  if (res.code !== 0) {
                    toast.error(res.msg);
                    return;
                  }
                  setCreating(null);
                  setTab("nodes");
                  toast.success("节点已创建");
                  void load();
                }}
              >
                创建节点
              </Button>
            </ModalFooter>
          </ModalContent>
        </Modal>
        <Modal
          isOpen={Boolean(linksFor)}
          scrollBehavior="inside"
          size="lg"
          onClose={() => setLinksFor(null)}
        >
          <ModalContent>
            <ModalHeader>节点链接 · {linksFor?.name}</ModalHeader>
            <ModalBody className="space-y-4">
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
                  <p className="break-all rounded bg-default-100 p-2 text-xs">
                    {link.url}
                  </p>
                  <div className="flex gap-2">
                    <Button
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
                      size="sm"
                      startContent={<QrCode size={15} />}
                      variant="flat"
                      onPress={() => setQR(qr === link.url ? "" : link.url)}
                    >
                      二维码
                    </Button>
                  </div>
                  {qr === link.url && (
                    <div className="flex justify-center py-2">
                      <OfflineQRCode value={link.url} />
                    </div>
                  )}
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
              <Button onPress={() => setLinksFor(null)}>关闭</Button>
            </ModalFooter>
          </ModalContent>
        </Modal>
        <Modal isOpen={Boolean(deleting)} onClose={() => setDeleting(null)}>
          <ModalContent>
            <ModalHeader>删除落地节点</ModalHeader>
            <ModalBody>
              <p>
                删除「{deleting?.name}」将停用入站并释放端口，原链接无法再使用。
              </p>
            </ModalBody>
            <ModalFooter>
              <Button variant="light" onPress={() => setDeleting(null)}>
                取消
              </Button>
              <Button
                color="danger"
                isLoading={saving}
                onPress={async () => {
                  if (!deleting) return;
                  setSaving(true);
                  const res = await deleteLandingNode(deleting.id);
                  setSaving(false);
                  if (res.code !== 0) {
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
