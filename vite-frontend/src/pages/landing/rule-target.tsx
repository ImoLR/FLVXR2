import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import {
  LandingNode,
  LandingProtocol,
  LandingServer,
  landingNodes,
  landingProtocolLabel,
  landingServers,
} from "@/api/landing";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

export interface LandingTargetValue {
  targetMode?: "manual" | "node" | "server";
  landingNodeId?: number;
  landingServerId?: number;
  landingProtocol?: LandingProtocol;
}
export function RuleLandingTarget({
  value,
  userId,
  onChange,
}: {
  value: LandingTargetValue;
  userId: number;
  onChange: (value: LandingTargetValue) => void;
}) {
  const [nodes, setNodes] = useState<LandingNode[]>([]);
  const [servers, setServers] = useState<LandingServer[]>([]);
  const mode = value.targetMode || "manual";
  useEffect(() => {
    if (mode === "manual") return;
    let disposed = false;
    void Promise.all([landingNodes(), landingServers()]).then(
      ([nodeRes, serverRes]) => {
        if (disposed) return;
        if (nodeRes.code === 0)
          setNodes(
            (nodeRes.data || []).filter(
              (node) => node.userId === userId && node.status === "active",
            ),
          );
        else toast.error(nodeRes.msg);
        if (serverRes.code === 0)
          setServers(
            (serverRes.data || []).filter(
              (server) => server.status === "active",
            ),
          );
        else toast.error(serverRes.msg);
      },
    );
    return () => {
      disposed = true;
    };
  }, [mode, userId]);
  const server = servers.find((item) => item.id === value.landingServerId);

  return (
    <div className="space-y-3">
      <Select
        label="目标类型"
        selectedKeys={[mode]}
        variant="bordered"
        onSelectionChange={(keys) =>
          onChange({
            targetMode: String(
              Array.from(keys)[0],
            ) as LandingTargetValue["targetMode"],
            landingNodeId: undefined,
            landingServerId: undefined,
            landingProtocol: "ss2022",
          })
        }
      >
        <SelectItem key="manual">手动填写</SelectItem>
        <SelectItem key="node">我的落地节点</SelectItem>
        <SelectItem key="server">落地服务器（自动建节点）</SelectItem>
      </Select>
      {mode === "node" && (
        <Select
          description="只能选择规则所属用户自己的节点"
          label="我的落地节点"
          placeholder="请选择落地节点"
          selectedKeys={
            value.landingNodeId ? [String(value.landingNodeId)] : []
          }
          variant="bordered"
          onSelectionChange={(keys) =>
            onChange({ ...value, landingNodeId: Number(Array.from(keys)[0]) })
          }
        >
          {nodes.map((node) => (
            <SelectItem key={String(node.id)}>
              {node.name} · {landingProtocolLabel(node.protocol)} ·{" "}
              {node.serverName || ""}
            </SelectItem>
          ))}
        </Select>
      )}
      {mode === "server" && (
        <>
          <Select
            description="保存规则时为该用户自动创建一个节点，端口由系统分配"
            label="落地服务器"
            placeholder="请选择服务器"
            selectedKeys={
              value.landingServerId ? [String(value.landingServerId)] : []
            }
            variant="bordered"
            onSelectionChange={(keys) => {
              const id = Number(Array.from(keys)[0]);
              onChange({
                ...value,
                landingServerId: id,
                landingProtocol:
                  servers.find((item) => item.id === id)?.protocols[0] ||
                  "ss2022",
              });
            }}
          >
            {servers.map((item) => (
              <SelectItem key={String(item.id)}>
                {item.name} · {item.flowType === 2 ? "双向" : "单向"}{" "}
                {item.trafficRatio}×
              </SelectItem>
            ))}
          </Select>
          <Select
            label="协议模板"
            selectedKeys={[value.landingProtocol || "ss2022"]}
            variant="bordered"
            onSelectionChange={(keys) =>
              onChange({
                ...value,
                landingProtocol: String(Array.from(keys)[0]) as LandingProtocol,
              })
            }
          >
            {(server?.protocols || ["ss2022", "vless_reality_vision"]).map(
              (protocol) => (
                <SelectItem key={protocol}>
                  {landingProtocolLabel(protocol)}
                </SelectItem>
              ),
            )}
          </Select>
        </>
      )}
      {mode !== "manual" && (
        <p className="text-xs text-default-500">
          本人落地不重复计入用户总流量，按隧道与落地较贵的费率计费。此规则只使用一个落地目标。
        </p>
      )}
    </div>
  );
}
