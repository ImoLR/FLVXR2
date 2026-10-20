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
import { ChainTargetInput } from "@/pages/landing/chain-target";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

export interface LandingTargetValue {
  targetMode?: "manual" | "node" | "server" | "chain";
  landingNodeId?: number;
  landingServerId?: number;
  landingProtocol?: LandingProtocol;
  chainLink?: string;
}

// 线路机 tunnels (plan 108) only allow VLESS Reality Vision landings or the
// user's own node through the tunnel exit; manual targets are hidden.
export interface LineTunnelInfo {
  lineMachine?: boolean;
  chainAvailable?: boolean;
  chainUnavailableReason?: string;
}

export function RuleLandingTarget({
  value,
  userId,
  onChange,
  line,
  chainSummary,
  chainError,
}: {
  value: LandingTargetValue;
  userId: number;
  onChange: (value: LandingTargetValue) => void;
  line?: LineTunnelInfo;
  chainSummary?: string;
  chainError?: string;
}) {
  const [nodes, setNodes] = useState<LandingNode[]>([]);
  const [servers, setServers] = useState<LandingServer[]>([]);
  const lineMachine = Boolean(line?.lineMachine);
  const mode = value.targetMode || "manual";
  const realityOnly = (protocols: LandingProtocol[]) =>
    lineMachine
      ? protocols.filter((protocol) => protocol === "vless_reality_vision")
      : protocols;

  useEffect(() => {
    if (mode === "manual" || mode === "chain") return;
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
              (server) =>
                server.status === "active" && server.sharedAvailable !== false,
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
  const visibleNodes = lineMachine
    ? nodes.filter((node) => node.protocol === "vless_reality_vision")
    : nodes;
  const visibleServers = lineMachine
    ? servers.filter((item) =>
        (item.protocols || []).includes("vless_reality_vision"),
      )
    : servers;
  const defaultProtocol: LandingProtocol = lineMachine
    ? "vless_reality_vision"
    : "ss2022";

  return (
    <div className={lineMachine ? "flvx-landing-text space-y-3" : "space-y-3"}>
      <Select
        description={
          lineMachine
            ? "线路机隧道只能转发到 VLESS Reality Vision 落地，或经隧道出口连接你自己的节点"
            : undefined
        }
        disabledKeys={lineMachine && !line?.chainAvailable ? ["chain"] : []}
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
            landingProtocol: defaultProtocol,
            chainLink: "",
          })
        }
      >
        {[
          ...(lineMachine
            ? []
            : [<SelectItem key="manual">手动填写</SelectItem>]),
          <SelectItem key="node">我的落地节点</SelectItem>,
          <SelectItem key="server">落地服务器（自动建节点）</SelectItem>,
          ...(lineMachine
            ? [<SelectItem key="chain">我自己的节点</SelectItem>]
            : []),
        ]}
      </Select>
      {lineMachine && !line?.chainAvailable && line?.chainUnavailableReason && (
        <p className="text-xs text-default-500">
          「我自己的节点」暂不可用：{line.chainUnavailableReason}
        </p>
      )}
      {mode === "node" && (
        <Select
          description={
            lineMachine
              ? "只列出 VLESS Reality Vision 节点"
              : "只能选择规则所属用户自己的节点"
          }
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
          {visibleNodes.map((node) => (
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
                  realityOnly(
                    servers.find((item) => item.id === id)?.protocols || [],
                  )[0] || defaultProtocol,
              });
            }}
          >
            {visibleServers.map((item) => (
              <SelectItem key={String(item.id)}>
                {item.name} · {item.flowType === 2 ? "双向" : "单向"}{" "}
                {item.trafficRatio}×
              </SelectItem>
            ))}
          </Select>
          <Select
            description={
              lineMachine
                ? "线路机隧道固定使用 VLESS Reality Vision"
                : undefined
            }
            isDisabled={lineMachine}
            label="协议模板"
            selectedKeys={[
              lineMachine
                ? "vless_reality_vision"
                : value.landingProtocol || "ss2022",
            ]}
            variant="bordered"
            onSelectionChange={(keys) =>
              onChange({
                ...value,
                landingProtocol: String(Array.from(keys)[0]) as LandingProtocol,
              })
            }
          >
            {realityOnly(
              server?.protocols || ["ss2022", "vless_reality_vision"],
            ).map((protocol) => (
              <SelectItem key={protocol}>
                {landingProtocolLabel(protocol)}
              </SelectItem>
            ))}
          </Select>
        </>
      )}
      {mode === "chain" && (
        <ChainTargetInput
          currentSummary={chainSummary}
          error={chainError}
          value={value.chainLink || ""}
          onChange={(chainLink) => onChange({ ...value, chainLink })}
        />
      )}
      {mode !== "manual" && mode !== "chain" && (
        <p className="text-xs text-default-500">
          本人落地不重复计入用户总流量，按隧道与落地较贵的费率计费。此规则只使用一个落地目标。
        </p>
      )}
    </div>
  );
}
