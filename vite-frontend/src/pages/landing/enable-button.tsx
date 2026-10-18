import { useState } from "react";
import { useNavigate } from "react-router-dom";
import toast from "react-hot-toast";

import { enableLandingServer } from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";

export function EnableLandingButton({
  node,
}: {
  node: {
    id: number;
    landingSupported?: boolean;
    landingServerId?: number;
    isRemote?: number;
    connectionStatus?: string;
  };
}) {
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);

  if (node.isRemote === 1) return null;
  const protectedHost = node.id === 36;
  const enabled = Boolean(node.landingServerId);

  return (
    <Button
      className="h-auto min-h-8 min-w-0 max-w-full whitespace-normal px-2 py-1 text-xs leading-tight"
      color="secondary"
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
        toast.success("已启用落地，请设置端口段和可见用户组");
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
    </Button>
  );
}
