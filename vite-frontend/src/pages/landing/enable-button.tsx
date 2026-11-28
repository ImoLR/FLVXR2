import { createContext, useContext, useState } from "react";
import { useNavigate } from "react-router-dom";
import toast from "react-hot-toast";

import {
  AdminAttention,
  AttentionCorner,
  attentionTitle,
} from "./admin-attention";

import { enableLandingServer } from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";

/**
 * Plan 327: the 服务器 page provides each landing server's admin-attention
 * state, so 「落地设置」 carries the ⚠ that the 落地 server card had (memory
 * admin-attention-marker) — no prop threading through the card / list views.
 */
export const LandingAttentionContext = createContext<
  Map<number, AdminAttention>
>(new Map());

export function EnableLandingButton({
  node,
}: {
  node: {
    id: number;
    landingSupported?: boolean;
    landingServerId?: number;
    landingChainOnly?: boolean;
    isRemote?: number;
    connectionStatus?: string;
  };
}) {
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);
  const attentionMap = useContext(LandingAttentionContext);

  if (node.isRemote === 1) return null;
  const protectedHost = node.id === 36;
  // Plan 274: a chain-only exit (「我自己的节点」) is not a public landing yet —
  // 「启用为落地」 converts it (the backend keeps its chain rules).
  const enabled = Boolean(node.landingServerId) && !node.landingChainOnly;
  // Only on 「落地设置」 (a chain-only exit's button converts it instead).
  const attention =
    enabled && node.landingServerId
      ? (attentionMap.get(node.landingServerId) ?? null)
      : null;

  return (
    <Button
      className={`h-auto min-h-8 min-w-0 max-w-full whitespace-normal px-2 py-1 text-xs leading-tight${attention ? " relative overflow-visible" : ""}`}
      color="secondary"
      data-attention-settings={attention?.level}
      isDisabled={
        protectedHost ||
        (!enabled &&
          (!node.landingSupported || node.connectionStatus !== "online"))
      }
      isLoading={loading}
      size="sm"
      title={
        protectedHost
          ? "此节点禁止启用落地"
          : attention
            ? attentionTitle(attention)
            : !node.landingSupported
              ? "节点程序需升级"
              : "管理 FLVX 自有落地"
      }
      variant="flat"
      onPress={async () => {
        if (enabled) {
          navigate(`/landing?server=${node.landingServerId}`);

          return;
        }
        setLoading(true);
        const response = await enableLandingServer(node.id);

        setLoading(false);
        if (response.code !== 0) {
          toast.error(response.msg || "启用失败");

          return;
        }
        // Plan 216: 30000-39999 busy → the panel picked another free block.
        const rangeNote = (response.data as { rangeNote?: string }).rangeNote;

        if (rangeNote) {
          toast.success(
            `已启用落地，${rangeNote}；该端口段已从节点转发端口段中排除，请设置可见用户组`,
            { duration: 8000 },
          );
        } else {
          toast.success(
            `已启用落地，端口 ${response.data.portMin}-${response.data.portMax} 已从节点转发端口段中排除；请设置端口段和可见用户组`,
          );
        }
        navigate(`/landing?server=${response.data.id}`);
      }}
    >
      {protectedHost
        ? "禁止启用落地"
        : enabled
          ? "落地设置"
          : node.landingSupported
            ? "启用为落地"
            : "节点程序需升级"}
      {attention && <AttentionCorner level={attention.level} />}
    </Button>
  );
}
