import type { NodeDDNSView } from "@/api/ddns";

import { STATUS_META } from "./ddns-section";

import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// Node card / list badge (plan 142): 「DDNS 已同步 / 待同步 / 失败 / 需升级节点」
// with the per-record state in a dropdown. Shown only when DDNS is on.

const BADGE: Record<
  string,
  { label: string; color: "success" | "warning" | "danger" }
> = {
  synced: { label: "DDNS 已同步", color: "success" },
  pending: { label: "DDNS 待同步", color: "warning" },
  failed: { label: "DDNS 失败", color: "danger" },
  upgrade: { label: "DDNS 需升级节点", color: "warning" },
};

const fmtTime = (ms: number) =>
  ms ? new Date(ms).toLocaleString("zh-CN", { hour12: false }) : "—";

export function NodeDDNSBadge({ ddns }: { ddns?: NodeDDNSView | null }) {
  if (!ddns?.enabled || !ddns.status) return null;
  const badge = BADGE[ddns.status] || BADGE.pending;

  return (
    <Dropdown>
      <DropdownTrigger>
        <Button
          aria-label={badge.label}
          className="h-6 min-w-0 shrink-0 rounded-full px-2 text-[11px]"
          color={badge.color}
          data-testid="ddns-badge"
          size="sm"
          title={ddns.records
            .map((r) => `${r.domain} → ${r.currentIp || "—"}`)
            .join("\n")}
          variant="flat"
          onClick={(event) => event.stopPropagation()}
        >
          {badge.label}
        </Button>
      </DropdownTrigger>
      <DropdownMenu aria-label="DDNS 详情">
        <DropdownMenuLabel className="max-w-[min(22rem,calc(100vw-2rem))] space-y-2 whitespace-normal text-xs font-normal">
          <p className="text-default-500">
            {ddns.providerName || "DNS 服务商"} · TTL 2 分钟
            {!ddns.online
              ? " · 节点离线"
              : !ddns.addrReport
                ? " · 节点版本不上报网卡地址"
                : ""}
          </p>
          {ddns.records.map((r) => {
            const meta = STATUS_META[r.status] || STATUS_META.pending;

            return (
              <div key={r.id} className="space-y-0.5">
                <p className="break-all text-foreground">
                  <span className="font-medium">{r.domain}</span>
                  <span className="text-default-500">
                    {" "}
                    {r.type} · {r.role === "main" ? "主地址" : "附加入口"}
                  </span>
                </p>
                <p className="break-all">
                  <span
                    className={
                      meta.color === "success"
                        ? "text-success"
                        : meta.color === "danger"
                          ? "text-danger"
                          : "text-warning"
                    }
                  >
                    {meta.label}
                  </span>
                  <span className="text-default-500">
                    {" "}
                    · {r.currentIp || "—"}
                  </span>
                </p>
                <p className="text-default-500">
                  上次同步：{fmtTime(r.lastSyncAt)}
                </p>
                {r.lastError ? (
                  <p className="break-all text-danger">{r.lastError}</p>
                ) : null}
              </div>
            );
          })}
        </DropdownMenuLabel>
      </DropdownMenu>
    </Dropdown>
  );
}
