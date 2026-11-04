import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import { NODE_TOTAL_NAME, nodeTotalPendingText } from "./conn-mode";

import { getAllUsers } from "@/api";
import {
  LandingNode,
  LandingServer,
  landingDate,
  landingGB,
  landingProtocolLabel,
  transferLandingNode,
} from "@/api/landing";
import { UserApiItem } from "@/api/types";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { getToken } from "@/utils/auth";
import { getUserIdFromToken } from "@/utils/jwt";

// Plan 211 (user 11-05): 节点详情 is information only (admins). The limits a
// node runs with come from the server's 新节点默认值 (「同时应用到现有 N 个节点」);
// only the owner transfer (「转移」) stays here, unchanged.
export function NodeDetails({
  node,
  server,
  onClose,
  onSaved,
}: {
  node: LandingNode;
  server?: LandingServer;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [users, setUsers] = useState<UserApiItem[]>([]);
  const [owner, setOwner] = useState(String(node.userId));
  const [transferring, setTransferring] = useState(false);
  const token = getToken();
  const selfId = token ? getUserIdFromToken(token) : null;

  useEffect(() => {
    void getAllUsers({ size: 1000 }).then((res) => {
      if (res.code === 0) setUsers(res.data || []);
    });
  }, []);
  // Admin accounts are not in the user list: the current owner and the
  // calling admin get their own entries.
  const ownerOptions = [
    ...(users.some((user) => user.id === node.userId)
      ? []
      : [
          {
            key: String(node.userId),
            label: `${node.userName || `用户 ${node.userId}`}（当前）`,
          },
        ]),
    ...(selfId && selfId !== node.userId
      ? [{ key: String(selfId), label: "自己（管理员）" }]
      : []),
    ...users.map((user) => ({
      key: String(user.id),
      label: `${user.name ? `${user.user}（${user.name}）` : user.user}${user.id === node.userId ? "（当前）" : ""}`,
    })),
  ];
  const transfer = async () => {
    const target = Number(owner);

    if (!target || target === node.userId) return;
    setTransferring(true);
    try {
      const res = await transferLandingNode(node.id, target);

      if (res.code !== 0) {
        toast.error(res.msg);

        return;
      }
      toast.success(
        `已转移给 ${ownerOptions.find((item) => item.key === owner)?.label || `用户 ${target}`}`,
      );
      onSaved();
      onClose();
    } finally {
      setTransferring(false);
    }
  };
  const effectiveFlow =
    node.effectiveFlowType ?? node.flowType ?? server?.flowType ?? 2;
  const effectiveRatio =
    node.effectiveTrafficRatio ??
    node.trafficRatio ??
    server?.trafficRatio ??
    1;
  const used = node.billedIn + node.billedOut;
  const total = node.maxTotalConnections ?? 0;
  const expiry = node.effectiveExpiresAt
    ? `${landingDate(node.effectiveExpiresAt)}${
        node.expiryMode === "fixed" &&
        node.expiresAt === node.effectiveExpiresAt
          ? "（节点独立有效期）"
          : server?.availableUntil === node.effectiveExpiresAt
            ? "（服务器可用期限）"
            : "（跟随用户）"
      }`
    : "未设置期限";
  const rows: [string, string][] = [
    ["所属用户", node.userName || `用户 ${node.userId}`],
    [
      "服务器",
      server?.name || node.serverName || `服务器 ${node.landingServerId}`,
    ],
    ["协议 / 端口", `${landingProtocolLabel(node.protocol)} · ${node.port}`],
    ["倍率", `${effectiveRatio}×`],
    ["流量计算", effectiveFlow === 2 ? "双向" : "单向"],
    [
      "本期流量 / 上限",
      `${landingGB(used)} GB / ${
        node.trafficLimitBytes
          ? `${landingGB(node.trafficLimitBytes)} GB`
          : "不限"
      }`,
    ],
    ["有效期", expiry],
    ["在线 IP", node.maxOnlineIps ? `最多 ${node.maxOnlineIps} 个` : "不限"],
    [
      "连接",
      `${NODE_TOTAL_NAME} ${total > 0 ? total : "不限"}${
        node.maxConnections > 0 ? ` · 入站上限 ${node.maxConnections}` : ""
      }`,
    ],
  ];

  return (
    <Modal isOpen scrollBehavior="inside" size="lg" onClose={onClose}>
      <ModalContent className="flvx-landing">
        <ModalHeader>节点详情 · {node.name}</ModalHeader>
        <ModalBody className="space-y-5">
          <dl
            data-node-details-info
            className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm"
          >
            {rows.map(([label, value]) => (
              <div key={label} className="contents">
                <dt className="whitespace-nowrap text-default-500">{label}</dt>
                <dd className="min-w-0 break-words font-medium text-foreground">
                  {value}
                </dd>
              </div>
            ))}
          </dl>
          {node.nodeTotalPending && (
            <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10">
              {nodeTotalPendingText(server?.source === "shared")}
            </p>
          )}
          <p className="text-xs text-default-500">
            {NODE_TOTAL_NAME} = 这个节点端口的入站+出站 TCP（含
            TIME_WAIT/CLOSE_WAIT），到上限只拒绝新连接。限制来自服务器的「新节点默认值」，可在落地服务器设置里「同时应用到现有节点」。
          </p>
          <div className="space-y-2">
            <div className="flex items-end gap-2">
              <Select
                className="min-w-0 flex-1"
                label="所属用户"
                selectedKeys={[owner]}
                variant="bordered"
                onSelectionChange={(keys) =>
                  setOwner(String(Array.from(keys)[0] || node.userId))
                }
              >
                {ownerOptions.map((item) => (
                  <SelectItem key={item.key}>{item.label}</SelectItem>
                ))}
              </Select>
              <Button
                color="warning"
                isDisabled={owner === String(node.userId)}
                isLoading={transferring}
                variant="flat"
                onPress={() => void transfer()}
              >
                转移
              </Button>
            </div>
            <p className="text-xs text-default-500">
              转移后端口、密码、链接和各项限制不变；有效期为「跟随用户」时改为跟随新用户。转移前的流量仍计在原用户，之后的流量计入新用户。按新用户的服务器授权和节点上限检查；原用户的规则仍在使用此节点时不能转移。
            </p>
          </div>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            关闭
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
