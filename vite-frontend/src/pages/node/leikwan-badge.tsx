import type { LeiKwanNodeDetection } from "@/api/types";

import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

export function NodeLeiKwanBadge({ node }: { node: LeiKwanNodeDetection }) {
  if (node.leikwanStatus !== "running" && node.leikwanStatus !== "present")
    return null;
  const running = node.leikwanStatus === "running";
  const label = running ? "利群客户端运行中" : "利群客户端已安装，未检测到运行";
  const forwards = node.leikwanForwards || [];
  const detail =
    forwards
      .map(
        (forward) =>
          `#${forward.forwardId} · :${forward.listenPort} · ${forward.running ? "监听中" : "未监听"}`,
      )
      .join("\n") || "尚未检测到转发监听端口";
  const checked = node.leikwanCheckedAt
    ? new Date(node.leikwanCheckedAt).toLocaleString("zh-CN", { hour12: false })
    : "暂无记录";

  return (
    <Dropdown>
      <DropdownTrigger>
        <Button
          aria-label={label}
          className={`h-6 min-w-0 shrink-0 px-1 text-xs ${running ? "text-success" : "text-warning"}`}
          color={running ? "success" : "warning"}
          size="sm"
          title={`${label}\n${detail}\n检查记录：${checked}`}
          variant="light"
          onClick={(event) => event.stopPropagation()}
        >
          利群
        </Button>
      </DropdownTrigger>
      <DropdownMenu aria-label="利群客户端详情">
        <DropdownMenuLabel className="max-w-[min(20rem,calc(100vw-2rem))] whitespace-normal text-xs font-normal">
          <p className="text-foreground">{label}</p>
          <p className="mt-2 whitespace-pre-line text-foreground">{detail}</p>
          <p className="mt-2 text-default-500">检查记录：{checked}</p>
          <p className="mt-1 text-default-500">状态变化时更新记录</p>
        </DropdownMenuLabel>
      </DropdownMenu>
    </Dropdown>
  );
}
