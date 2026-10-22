import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import { getAllUsers } from "@/api";
import {
  LandingNode,
  LandingProtocol,
  LandingServer,
  MIERU_UDP_HINT,
  createLandingNode,
  isLandingExtraProtocol,
  isMieruUdp,
  landingCreatableProtocols,
  landingProtocolLabel,
  landingProtocolSummary,
  sharedProtocolCap,
} from "@/api/landing";
import { UserApiItem } from "@/api/types";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { isAdmin } from "@/utils/auth";

export function CreateLandingNodeDialog({
  servers,
  initialServer,
  ownerId,
  nodes = [],
  lineMachine = false,
  onClose,
  onCreated,
  onUseNode,
}: {
  servers: LandingServer[];
  initialServer?: LandingServer;
  ownerId?: number;
  nodes?: LandingNode[];
  lineMachine?: boolean;
  onClose: () => void;
  onCreated: (node: LandingNode) => void | Promise<void>;
  onUseNode?: (node: LandingNode) => void;
}) {
  const admin = isAdmin();
  const availableServers = servers.filter(
    (server) =>
      server.status === "active" &&
      server.sharedAvailable !== false &&
      (!server.availableUntil || server.availableUntil > Date.now()) &&
      !server.chainOnly &&
      (!lineMachine || server.protocols.includes("vless_reality_vision")),
  );
  const firstServer = initialServer || availableServers[0];
  const [serverId, setServerId] = useState(firstServer?.id);
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState<LandingProtocol>(
    lineMachine
      ? "vless_reality_vision"
      : (firstServer && landingCreatableProtocols(firstServer)[0]) || "ss2022",
  );
  const [userId, setUserId] = useState(ownerId || 0);
  const [ownerOptions, setOwnerOptions] = useState<UserApiItem[]>([]);
  const [saving, setSaving] = useState(false);
  const [allowDuplicate, setAllowDuplicate] = useState(false);
  const server = availableServers.find((item) => item.id === serverId);
  const existing = onUseNode
    ? nodes.find(
        (node) =>
          node.userId === userId &&
          node.landingServerId === serverId &&
          node.protocol === protocol &&
          node.status === "active" &&
          !node.deletePending &&
          node.serverAvailable !== false &&
          (!node.trafficLimitBytes ||
            node.billedIn + node.billedOut < node.trafficLimitBytes) &&
          (!node.effectiveExpiresAt || node.effectiveExpiresAt > Date.now()),
      )
    : undefined;
  const reuseHint = existing && !allowDuplicate;
  const ownerName = nodes.find((node) => node.userId === userId)?.userName;

  useEffect(() => {
    if (!admin || ownerId !== undefined) return;
    let disposed = false;

    void getAllUsers({ size: 1000 }).then((res) => {
      if (!disposed && res.code === 0) setOwnerOptions(res.data || []);
    });

    return () => {
      disposed = true;
    };
  }, [admin, ownerId]);

  const create = async () => {
    if (!server || !name.trim() || reuseHint) return;
    setSaving(true);
    try {
      const res = await createLandingNode({
        landingServerId: server.id,
        name: name.trim(),
        protocol,
        ...(admin && userId > 0 ? { userId } : {}),
      });

      if (res.code !== 0) {
        toast.error(res.msg);

        return;
      }
      await onCreated(res.data);
      onClose();
    } catch {
      toast.error("创建落地节点失败，请重试");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal isOpen scrollBehavior="inside" onClose={onClose}>
      <ModalContent className="flvx-landing-text">
        <ModalHeader>创建落地节点</ModalHeader>
        <ModalBody className="space-y-4">
          {initialServer ? (
            <p className="text-sm text-default-500">
              {initialServer.name} ·{" "}
              {initialServer.flowType === 2 ? "双向" : "单向"}{" "}
              {initialServer.trafficRatio}×
            </p>
          ) : (
            <Select
              label="落地服务器"
              placeholder="请选择服务器"
              selectedKeys={serverId ? [String(serverId)] : []}
              variant="bordered"
              onSelectionChange={(keys) => {
                const id = Number(Array.from(keys)[0]);
                const selected = availableServers.find(
                  (item) => item.id === id,
                );

                setServerId(id);
                setProtocol(
                  lineMachine
                    ? "vless_reality_vision"
                    : (selected && landingCreatableProtocols(selected)[0]) ||
                        "ss2022",
                );
                setAllowDuplicate(false);
              }}
            >
              {availableServers.map((item) => (
                <SelectItem key={String(item.id)}>
                  {item.name} · {item.flowType === 2 ? "双向" : "单向"}{" "}
                  {item.trafficRatio}×
                </SelectItem>
              ))}
            </Select>
          )}
          {!availableServers.length && (
            <p className="text-sm text-default-500">
              {lineMachine
                ? "暂无可创建 VLESS Reality Vision 节点的服务器"
                : "暂无可创建节点的服务器"}
            </p>
          )}
          <Input
            label="节点名称"
            placeholder="例如：我的日本节点"
            value={name}
            variant="bordered"
            onChange={(event) => setName(event.target.value)}
          />
          {admin &&
            (ownerId !== undefined ? (
              <Input
                readOnly
                description="节点归规则所属用户，按该用户的服务器授权和节点上限检查"
                label="所属用户"
                value={ownerName || `用户 ${userId}`}
                variant="bordered"
              />
            ) : (
              <Select
                description="默认归自己；为其他用户创建时，按该用户的服务器授权和节点上限检查"
                label="所属用户"
                selectedKeys={[String(userId)]}
                variant="bordered"
                onSelectionChange={(keys) =>
                  setUserId(Number(Array.from(keys)[0] || 0))
                }
              >
                {[
                  <SelectItem key="0">自己（管理员）</SelectItem>,
                  ...ownerOptions.map((user) => (
                    <SelectItem key={String(user.id)}>
                      {user.name ? `${user.user}（${user.name}）` : user.user}
                    </SelectItem>
                  )),
                ]}
              </Select>
            ))}
          <Select
            description={
              lineMachine
                ? "线路机隧道固定使用 VLESS Reality Vision"
                : undefined
            }
            isDisabled={lineMachine}
            label="协议模板"
            selectedKeys={[protocol]}
            variant="bordered"
            onSelectionChange={(keys) => {
              setProtocol(String(Array.from(keys)[0]) as LandingProtocol);
              setAllowDuplicate(false);
            }}
          >
            {(lineMachine
              ? ["vless_reality_vision"]
              : server
                ? landingCreatableProtocols(server)
                : []
            ).map((item) => (
              <SelectItem key={item}>{landingProtocolLabel(item)}</SelectItem>
            ))}
          </Select>
          {server && isLandingExtraProtocol(protocol) && (
            <div className="space-y-1 rounded-lg border border-divider p-3 text-xs text-default-600">
              <p>
                {landingProtocolLabel(protocol)} ·{" "}
                {landingProtocolSummary(protocol, {
                  sni:
                    server.anytlsServerName ||
                    (/^[0-9.]+$/.test(server.publicHost) ||
                    server.publicHost.includes(":")
                      ? ""
                      : server.publicHost),
                  version: server.snellVersion,
                  obfs: server.snellObfs,
                  obfsHost: server.snellObfsHost,
                  transport: server.mieruTransport,
                })}
              </p>
              {isMieruUdp(protocol, server.mieruTransport) && (
                <p className="font-medium text-warning-600 dark:text-warning-400">
                  {MIERU_UDP_HINT}
                </p>
              )}
              {!sharedProtocolCap(server, protocol)?.udp && (
                <p>
                  该服务器的 {landingProtocolLabel(protocol)} 暂只支持 TCP。
                </p>
              )}
              <p className="text-default-500">
                {
                  {
                    anytls: "密码与证书",
                    snell: "PSK ",
                    mieru: "用户名与密码",
                  }[protocol]
                }
                自动生成，创建后在「节点链接」查看客户端配置（Clash / mihomo
                {protocol === "snell" ? "、Surge" : ""}）。
              </p>
            </div>
          )}
          {reuseHint && (
            <div className="space-y-3 rounded-lg border border-divider p-3">
              <p className="text-sm">
                你在该服务器已有「{existing.name}」（
                {landingProtocolLabel(existing.protocol)}），可以直接使用
              </p>
              <div className="flex flex-wrap gap-2">
                <Button
                  color="success"
                  size="sm"
                  variant="flat"
                  onPress={() => {
                    onUseNode?.(existing);
                    onClose();
                  }}
                >
                  使用它
                </Button>
                <Button
                  size="sm"
                  variant="bordered"
                  onPress={() => setAllowDuplicate(true)}
                >
                  仍然新建
                </Button>
              </div>
            </div>
          )}
          <p className="text-xs text-default-500">
            端口自动分配
            {server?.source === "shared"
              ? `（${server.portMin}–${server.portMax}）`
              : ""}
            。连接数、流量上限、倍率和有效期由管理员设置。
          </p>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="success"
            isDisabled={!server || !name.trim() || Boolean(reuseHint)}
            isLoading={saving}
            variant="flat"
            onPress={() => void create()}
          >
            创建节点
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
