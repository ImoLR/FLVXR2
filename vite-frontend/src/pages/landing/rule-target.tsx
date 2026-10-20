import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import {
  LandingNode,
  LandingServer,
  landingNodes,
  landingProtocolLabel,
  landingServers,
} from "@/api/landing";
import { ChainTargetInput } from "@/pages/landing/chain-target";
import { CreateLandingNodeDialog } from "@/pages/landing/create-node";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Select,
  SelectItem,
  SelectSection,
} from "@/shadcn-bridge/heroui/select";

export interface LandingTargetValue {
  targetMode?: "manual" | "node" | "chain";
  landingNodeId?: number;
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
  const [loading, setLoading] = useState(false);
  const [creating, setCreating] = useState(false);
  const lineMachine = Boolean(line?.lineMachine);
  const mode = value.targetMode || "manual";

  useEffect(() => {
    if (mode !== "node") return;
    let disposed = false;

    setLoading(true);
    setNodes([]);
    setServers([]);
    void Promise.all([landingNodes(), landingServers(userId)]).then(
      ([nodeRes, serverRes]) => {
        if (disposed) return;
        if (nodeRes.code === 0)
          setNodes(
            (nodeRes.data || []).filter((node) => node.userId === userId),
          );
        else toast.error(nodeRes.msg);
        if (serverRes.code === 0) setServers(serverRes.data || []);
        else toast.error(serverRes.msg);
        setLoading(false);
      },
    );

    return () => {
      disposed = true;
    };
  }, [mode, userId]);

  const disabledReason = (node: LandingNode) => {
    const server = servers.find((item) => item.id === node.landingServerId);

    if (
      node.serverAvailable === false ||
      !server ||
      server.status !== "active" ||
      server.sharedAvailable === false ||
      (server.availableUntil && server.availableUntil <= Date.now())
    )
      return "服务器不可用";
    if (
      node.status !== "active" ||
      node.deletePending ||
      (node.trafficLimitBytes > 0 &&
        node.billedIn + node.billedOut >= node.trafficLimitBytes) ||
      (node.effectiveExpiresAt && node.effectiveExpiresAt <= Date.now())
    )
      return "已停用";
    if (lineMachine && node.protocol !== "vless_reality_vision")
      return "线路机需要 VLESS Reality Vision";

    return "";
  };
  const groups = Array.from(new Set(nodes.map((node) => node.landingServerId)));
  const selectNode = (node: LandingNode) =>
    onChange({ ...value, targetMode: "node", landingNodeId: node.id });

  return (
    <div
      className={`min-w-0 w-full max-w-full space-y-3 break-words [overflow-wrap:anywhere]${lineMachine ? " flvx-landing-text" : ""}`}
    >
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
            chainLink: "",
          })
        }
      >
        {[
          ...(lineMachine
            ? []
            : [<SelectItem key="manual">手动填写</SelectItem>]),
          <SelectItem key="node">我的落地节点</SelectItem>,
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
        <>
          <Select
            classNames={{
              base: "min-w-0 max-w-full",
              trigger: "max-w-full [&>span]:whitespace-normal",
              listbox: "static min-w-0 max-w-full !max-h-52",
            }}
            description="只能选择规则所属用户自己的节点"
            disabledKeys={nodes
              .filter(disabledReason)
              .map((node) => String(node.id))}
            isDisabled={loading}
            label="我的落地节点"
            listboxVariant="grouped"
            placeholder={loading ? "正在加载…" : "请选择落地节点"}
            selectedKeys={
              value.landingNodeId ? [String(value.landingNodeId)] : []
            }
            variant="bordered"
            onSelectionChange={(keys) =>
              onChange({ ...value, landingNodeId: Number(Array.from(keys)[0]) })
            }
          >
            {groups.map((id) => {
              const server = servers.find((item) => item.id === id);
              const serverNodes = nodes.filter(
                (node) => node.landingServerId === id,
              );
              const first = serverNodes[0];
              const flowType = server?.flowType ?? first.effectiveFlowType;
              const ratio =
                server?.trafficRatio ?? first.effectiveTrafficRatio ?? 1;

              return (
                <SelectSection
                  key={id}
                  title={`${server?.name || first.serverName || "落地服务器"} · ${flowType === 2 ? "双向" : "单向"} ${ratio}×`}
                >
                  {serverNodes.map((node) => (
                    <SelectItem
                      key={String(node.id)}
                      description={disabledReason(node)}
                    >
                      {node.name} · {landingProtocolLabel(node.protocol)} ·{" "}
                      {node.port}
                    </SelectItem>
                  ))}
                </SelectSection>
              );
            })}
          </Select>
          {!loading && !nodes.some((node) => !disabledReason(node)) && (
            <p className="text-sm text-default-500">
              暂无可用的落地节点，可先新建一个。
            </p>
          )}
          <Button
            color="success"
            isDisabled={loading || userId <= 0}
            size="sm"
            variant="flat"
            onPress={() => setCreating(true)}
          >
            ＋ 新建落地节点
          </Button>
          <p className="text-xs text-default-500">
            删除规则不会删除落地节点；一个节点可同时直连和被多条规则使用。
            本人落地不重复计入用户总流量，按隧道与落地较贵的费率计费。此规则只使用一个落地目标。
          </p>
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
      {creating && (
        <CreateLandingNodeDialog
          key={`${userId}-${lineMachine}`}
          lineMachine={lineMachine}
          nodes={nodes}
          ownerId={userId}
          servers={servers}
          onClose={() => setCreating(false)}
          onCreated={async (node) => {
            const res = await landingNodes();

            if (res.code === 0) {
              setNodes(
                (res.data || []).filter((item) => item.userId === userId),
              );
            } else {
              setNodes((previous) => [...previous, node]);
              toast.error(res.msg);
            }
            selectNode(node);
            toast.success("已创建落地节点，也会显示在「我的节点」");
          }}
          onUseNode={selectNode}
        />
      )}
    </div>
  );
}
