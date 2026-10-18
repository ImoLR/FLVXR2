import { useState } from "react";
import toast from "react-hot-toast";

import {
  LandingNode,
  LandingServer,
  landingDate,
  landingGB,
  updateLandingNode,
} from "@/api/landing";
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
import { dateInput, dateValue } from "./server-settings";

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
  const [form, setForm] = useState(node);
  const [saving, setSaving] = useState(false);
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
          <Button variant="light" onPress={onClose}>
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
