import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import { getAllUsers } from "@/api";
import {
  LandingNode,
  LandingServer,
  landingDate,
  landingGB,
  transferLandingNode,
  updateLandingNode,
} from "@/api/landing";
import { UserApiItem } from "@/api/types";
import { EgressProfile, egressOverview } from "@/api/landing-egress";
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
import { getToken } from "@/utils/auth";
import { getUserIdFromToken } from "@/utils/jwt";
import { dateInput, dateValue } from "./server-settings";

export function NodeDetails({
  node,
  server,
  onClose,
  onSaved,
  onOpenRules,
}: {
  node: LandingNode;
  server?: LandingServer;
  onClose: () => void;
  onSaved: () => void;
  // Plan 134: opens the node's 路由规则 dialog (rendered by the page).
  onOpenRules?: (node: LandingNode) => void;
}) {
  const [form, setForm] = useState(node);
  const [saving, setSaving] = useState(false);
  const [profiles, setProfiles] = useState<EgressProfile[]>([]);
  const [users, setUsers] = useState<UserApiItem[]>([]);
  const [owner, setOwner] = useState(String(node.userId));
  const [transferring, setTransferring] = useState(false);
  const token = getToken();
  const selfId = token ? getUserIdFromToken(token) : null;

  useEffect(() => {
    void egressOverview().then((res) => {
      if (res.code === 0) setProfiles(res.data.profiles || []);
    });
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
  const serverProfileName = server?.egress?.profileName || "默认（直连）";
  const profileName = (id: number) =>
    id === 0
      ? "默认（直连）"
      : profiles.find((p) => p.id === id)?.name || `方案 ${id}`;
  const set = <K extends keyof LandingNode>(key: K, value: LandingNode[K]) =>
    setForm((prev) => ({ ...prev, [key]: value }));
  const effectiveFlow =
    node.effectiveFlowType ?? node.flowType ?? server?.flowType ?? 2;
  const effectiveRatio =
    node.effectiveTrafficRatio ??
    node.trafficRatio ??
    server?.trafficRatio ??
    1;
  const connectionCaps = [node.maxConnections, server?.maxConnections].filter(
    (value): value is number => Boolean(value),
  );
  const effectiveConnections = connectionCaps.length
    ? Math.min(...connectionCaps)
    : 0;
  const expirySource = !node.effectiveExpiresAt
    ? "未设置期限"
    : node.expiryMode === "fixed" && node.expiresAt === node.effectiveExpiresAt
      ? "节点独立有效期"
      : server?.availableUntil === node.effectiveExpiresAt
        ? "服务器可用期限"
        : "用户到期时间";
  const rows = [
    [
      "流量计算",
      form.flowType == null
        ? "跟随服务器"
        : form.flowType === 2
          ? "双向"
          : "单向",
      effectiveFlow === 2 ? "双向" : "单向",
      node.flowType == null ? "服务器" : "节点覆盖",
    ],
    [
      "倍率",
      form.trafficRatio == null ? "跟随服务器" : `${form.trafficRatio}×`,
      `${effectiveRatio}×`,
      node.trafficRatio == null ? "服务器" : "节点覆盖",
    ],
    [
      "连接数",
      form.maxConnections || "不限",
      effectiveConnections || "不限",
      !effectiveConnections
        ? "节点 / 服务器均不限"
        : effectiveConnections === server?.maxConnections
          ? "服务器总连接池"
          : "节点连接上限",
    ],
    [
      "在线 IP",
      form.maxOnlineIps || "不限",
      node.maxOnlineIps || "不限",
      "节点设置",
    ],
    [
      "流量上限",
      form.trafficLimitBytes
        ? `${landingGB(form.trafficLimitBytes)} GB`
        : "不限",
      node.trafficLimitBytes
        ? `${landingGB(node.trafficLimitBytes)} GB`
        : "不限",
      "节点（另受用户总额及服务器额度约束）",
    ],
    [
      "路由方案",
      form.routeProfileId == null
        ? "跟随服务器"
        : profileName(form.routeProfileId),
      node.effectiveRouteProfileName || serverProfileName,
      node.routeProfileId == null ? "服务器" : "节点覆盖",
    ],
    [
      "有效期",
      form.expiryMode === "follow_user"
        ? "跟随用户"
        : landingDate(form.expiresAt),
      landingDate(node.effectiveExpiresAt),
      expirySource,
    ],
  ];

  return (
    <Modal isOpen scrollBehavior="inside" size="lg" onClose={onClose}>
      <ModalContent className="flvx-landing-text">
        <ModalHeader>节点详情 · {node.name}</ModalHeader>
        <ModalBody className="space-y-5">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-default-500">
                  {["项目", "设置", "实际生效", "来源"].map((title) => (
                    <th key={title} className="p-2">
                      {title}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={String(row[0])} className="border-b border-divider">
                    {row.map((value, index) => (
                      <td
                        key={index}
                        className={
                          index === 0 ? "whitespace-nowrap p-2" : "p-2"
                        }
                      >
                        {value}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="text-xs text-default-500">
            已用 {landingGB(node.billedIn + node.billedOut)} GB；入站{" "}
            {node.inboundTag}；端口 {node.port}。连接数不并入用户规则总连接数。
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
          <Input
            label="名称"
            value={form.name}
            onChange={(event) => set("name", event.target.value)}
          />
          <div className="grid grid-cols-2 gap-3">
            <Input
              label="连接数（0 不限）"
              min="0"
              type="number"
              value={String(form.maxConnections)}
              onChange={(event) =>
                set("maxConnections", Number(event.target.value))
              }
            />
            <Input
              label="在线 IP（0 不限）"
              min="0"
              type="number"
              value={String(form.maxOnlineIps)}
              onChange={(event) =>
                set("maxOnlineIps", Number(event.target.value))
              }
            />
            <Input
              label="流量上限 (GB，0 不限)"
              min="0"
              step="0.01"
              type="number"
              value={String(form.trafficLimitBytes / 1073741824)}
              onChange={(event) =>
                set(
                  "trafficLimitBytes",
                  Math.round(Number(event.target.value) * 1073741824),
                )
              }
            />
            <Input
              description="留空跟随服务器"
              label="倍率覆盖"
              min="0.01"
              step="0.01"
              type="number"
              value={form.trafficRatio == null ? "" : String(form.trafficRatio)}
              onChange={(event) =>
                set(
                  "trafficRatio",
                  event.target.value === "" ? null : Number(event.target.value),
                )
              }
            />
          </div>
          <Select
            label="流量计算覆盖"
            selectedKeys={[
              form.flowType == null ? "inherit" : String(form.flowType),
            ]}
            onSelectionChange={(keys) => {
              const key = Array.from(keys)[0];
              set("flowType", key === "inherit" ? null : Number(key));
            }}
          >
            <SelectItem key="inherit">跟随服务器</SelectItem>
            <SelectItem key="1">单向</SelectItem>
            <SelectItem key="2">双向</SelectItem>
          </Select>
          <Select
            label="有效期模式"
            selectedKeys={[form.expiryMode]}
            onSelectionChange={(keys) =>
              set(
                "expiryMode",
                String(Array.from(keys)[0]) as LandingNode["expiryMode"],
              )
            }
          >
            <SelectItem key="follow_user">跟随用户</SelectItem>
            <SelectItem key="fixed">独立有效期</SelectItem>
          </Select>
          {form.expiryMode === "fixed" && (
            <Input
              description="留空为永久；仍受用户和服务器期限限制"
              label="独立到期日期"
              type="datetime-local"
              value={dateInput(form.expiresAt)}
              onChange={(event) =>
                set("expiresAt", dateValue(event.target.value))
              }
            />
          )}
          <Select
            description={
              server?.egress && !server.egress.supported
                ? server.egress.unsupportedReason
                : "单独为这个节点指定方案；跟随服务器时使用服务器的方案（只替换服务器规则这一层，全局规则和节点规则照常生效）"
            }
            isDisabled={Boolean(server?.egress && !server.egress.supported)}
            label="路由方案覆盖"
            selectedKeys={[
              form.routeProfileId == null
                ? "inherit"
                : String(form.routeProfileId),
            ]}
            onSelectionChange={(keys) => {
              const key = String(Array.from(keys)[0]);

              set("routeProfileId", key === "inherit" ? null : Number(key));
            }}
          >
            {[
              <SelectItem key="inherit">
                跟随服务器（{serverProfileName}）
              </SelectItem>,
              <SelectItem key="0">默认（直连）</SelectItem>,
              ...profiles.map((p) => (
                <SelectItem key={String(p.id)}>{p.name}</SelectItem>
              )),
            ]}
          </Select>
          {onOpenRules && (
            <div className="flex items-center justify-between gap-3 rounded-lg border border-divider p-3">
              <div className="min-w-0">
                <p className="text-sm font-medium">节点规则</p>
                <p className="text-xs text-default-500">
                  {node.routeRuleCount
                    ? `所属用户为这个节点设置了 ${node.routeRuleCount} 条规则`
                    : "所属用户为这个节点设置的规则，管理员也可编辑（不受条数限制）"}
                </p>
              </div>
              <Button
                color="warning"
                size="sm"
                variant="flat"
                onPress={() => onOpenRules(node)}
              >
                路由规则
              </Button>
            </div>
          )}
          <Select
            description="额度和期限限制由系统自动检查"
            label="管理状态"
            selectedKeys={[form.status === "disabled" ? "disabled" : "active"]}
            onSelectionChange={(keys) =>
              set("status", String(Array.from(keys)[0]))
            }
          >
            <SelectItem key="active">启用</SelectItem>
            <SelectItem key="disabled">停用</SelectItem>
          </Select>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="primary"
            isLoading={saving}
            onPress={async () => {
              setSaving(true);
              const res = await updateLandingNode(form);
              setSaving(false);
              if (res.code !== 0) {
                toast.error(res.msg);
                return;
              }
              toast.success("节点设置已保存");
              onSaved();
              onClose();
            }}
          >
            保存覆盖
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
