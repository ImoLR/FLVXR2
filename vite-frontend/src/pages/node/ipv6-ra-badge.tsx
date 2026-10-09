import { TriangleAlert } from "lucide-react";

import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

interface IPv6RAReport {
  ipv6RaStatus?: string;
  ipv6RaDetail?: string;
  ipv6RaCheckedAt?: number;
}

const states = {
  ok: { label: "IPv6 RA 已兼容", color: "success", className: "text-success" },
  warn: { label: "IPv6 RA 待恢复", color: "warning", className: "text-warning" },
  error: { label: "IPv6 需处理", color: "danger", className: "text-danger" },
} as const;

export function NodeIPv6RABadge({ node }: { node: IPv6RAReport }) {
  const status = node.ipv6RaStatus;

  if (status !== "ok" && status !== "warn" && status !== "error") return null;

  const state = states[status];
  const checked = node.ipv6RaCheckedAt
    ? new Date(node.ipv6RaCheckedAt).toLocaleString("zh-CN", { hour12: false })
    : "暂无记录";
  const detail = node.ipv6RaDetail || state.label;

  return (
    <Dropdown>
      <DropdownTrigger>
        <Button
          aria-label={state.label}
          className={`h-6 min-w-0 shrink-0 gap-1 px-1 text-xs ${state.className}`}
          color={state.color}
          size="sm"
          title={`${detail}\n检查记录：${checked}`}
          variant="light"
          onClick={(event) => event.stopPropagation()}
        >
          <TriangleAlert aria-hidden="true" className="h-4 w-4 shrink-0" />
          <span className="hidden sm:inline">{state.label}</span>
        </Button>
      </DropdownTrigger>
      <DropdownMenu aria-label="IPv6 RA 详情">
        <DropdownMenuLabel className="max-w-[min(20rem,calc(100vw-2rem))] whitespace-normal text-xs font-normal">
          <p className="break-words text-foreground">{detail}</p>
          <p className="mt-2 text-default-500">检查记录：{checked}</p>
        </DropdownMenuLabel>
      </DropdownMenu>
    </Dropdown>
  );
}
