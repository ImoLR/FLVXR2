import type { DOMAttributes, ReactNode } from "react";
import type { Forward } from "@/pages/forward";

import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
import { Switch } from "@/shadcn-bridge/heroui/switch";
import { formatInAddress, formatRemoteAddress } from "@/pages/forward/address";

interface MobileForwardCardProps {
  forward: Forward;
  listeners?: DOMAttributes<HTMLButtonElement>;
  compactMode: boolean;
  viewMode: "grouped" | "direct";
  isAdmin: boolean;
  selectedIds: ReadonlySet<number>;
  togglingIds: ReadonlySet<number>;
  statusDisplay: { color: string; text: string };
  strategyDisplay: { text: string };
  getStatusDisplay: (status: number) => { color: string; text: string };
  normalizeForwardTunnelName: (name?: string) => string;
  formatTunnelTrafficRatio: (value?: number) => string;
  getForwardDisplayFlow: (forward: Forward) => number;
  formatFlow: (value: number) => string;
  formatSpeed: (value: number) => string;
  copyToClipboard: (text: string, label?: string) => void;
  showAddressModal: (value: string, port: number | null, title: string) => void;
  toggleSelect: (id: number) => void;
  handleServiceToggle: (forward: Forward) => void;
  handleEdit: (forward: Forward) => void;
  handleCopy: (forward: Forward) => void;
  handleDiagnose: (forward: Forward) => void;
  handleDelete: (forward: Forward) => void;
  handleViewTrafficResetLogs: (forward: Forward) => void;
  children: ReactNode;
}

export default function MobileForwardCard({
  forward,
  listeners,
  compactMode,
  viewMode,
  isAdmin,
  selectedIds,
  togglingIds,
  statusDisplay,
  strategyDisplay,
  getStatusDisplay,
  normalizeForwardTunnelName,
  formatTunnelTrafficRatio,
  getForwardDisplayFlow,
  formatFlow,
  formatSpeed,
  copyToClipboard,
  showAddressModal,
  toggleSelect,
  handleServiceToggle,
  handleEdit,
  handleCopy,
  handleDiagnose,
  handleDelete,
  handleViewTrafficResetLogs,
  children,
}: MobileForwardCardProps) {
  const isCardMode = compactMode && viewMode === "direct";
  const mobileStatus = isCardMode
    ? statusDisplay
    : getStatusDisplay(forward.serviceRunning ? 1 : 0);
  const entryAddress = formatInAddress(forward.inIp, forward.inPort) || `默认IP:${forward.inPort}`;
  const tunnelName = normalizeForwardTunnelName(forward.tunnelName);

  return (
    <Card
      key={forward.id}
      className={`min-w-0 h-full border-divider shadow-sm ${selectedIds.has(forward.id) ? "bg-primary-50/70 dark:bg-primary-900/40" : ""}`}
    >
      <CardHeader className="p-3 pb-2 gap-2">
        <div className="flex items-center gap-2 min-w-0 w-full">
          <Checkbox
            aria-label={`选择规则 ${forward.name}`}
            className="shrink-0"
            isSelected={selectedIds.has(forward.id)}
            onValueChange={() => toggleSelect(forward.id)}
          />
          <button
            className="min-w-0 flex-1 truncate text-left text-sm font-bold text-foreground hover:text-primary"
            title={forward.name}
            type="button"
            onClick={() => copyToClipboard(forward.name, "规则名称")}
          >
            {forward.name}
          </button>
          <span
            className={`shrink-0 rounded px-1.5 py-0.5 text-xs font-medium ${mobileStatus.color === "success" ? "bg-success-500/10 text-success-600 dark:text-success-400" : mobileStatus.color === "warning" ? "bg-warning-500/10 text-warning-600 dark:text-warning-400" : mobileStatus.color === "danger" ? "bg-danger-500/10 text-danger-600 dark:text-danger-400" : "bg-default-100 text-default-500"}`}
          >
            {mobileStatus.text}
          </span>
          <Switch
            aria-label={`${forward.serviceRunning ? "暂停" : "启用"}规则 ${forward.name}`}
            isDisabled={togglingIds.has(forward.id) || (isCardMode && forward.status !== 1 && forward.status !== 0)}
            isSelected={forward.serviceRunning}
            size="sm"
            onValueChange={() => handleServiceToggle(forward)}
          />
        </div>
        <div className="flex items-center gap-2 min-w-0 w-full">
          <button
            className="min-w-0 flex-1 text-left text-xs text-default-600 break-all"
            title={tunnelName}
            type="button"
            onClick={() => copyToClipboard(tunnelName, "隧道名称")}
          >
            隧道 · {tunnelName}
            <span className="ml-1 text-primary font-semibold">^{formatTunnelTrafficRatio(forward.tunnelTrafficRatio)}</span>
          </button>
          <span className="shrink-0 rounded bg-default-100 px-1.5 py-0.5 text-xs text-default-500">
            {strategyDisplay.text}
          </span>
          <Button
            isIconOnly
            aria-label="拖拽排序"
            className="h-7 w-7 min-w-7 shrink-0 cursor-grab text-default-400 active:cursor-grabbing"
            size="sm"
            style={{ touchAction: "none" }}
            title="拖拽排序"
            variant="light"
            {...listeners}
          >
            <svg aria-hidden="true" className="h-4 w-4" fill="currentColor" viewBox="0 0 20 20">
              <path d="M7 2a2 2 0 1 1 .001 4.001A2 2 0 0 1 7 2zm0 6a2 2 0 1 1 .001 4.001A2 2 0 0 1 7 8zm0 6a2 2 0 1 1 .001 4.001A2 2 0 0 1 7 14zm6-8a2 2 0 1 1-.001-4.001A2 2 0 0 1 13 6zm0 2a2 2 0 1 1 .001 4.001A2 2 0 0 1 13 8zm0 6a2 2 0 1 1 .001 4.001A2 2 0 0 1 13 14z" />
            </svg>
          </Button>
        </div>
        {(forward.mode === "nftables" || (isAdmin && !isCardMode)) && (
          <div className="flex flex-wrap gap-1.5 text-xs text-default-500 break-all">
            {isAdmin && !isCardMode && <span>用户 · {forward.userRemark?.trim() || forward.userName || "-"}</span>}
            {forward.mode === "nftables" && <span className="rounded bg-primary-500/10 px-1.5 text-primary">nft</span>}
          </div>
        )}
        {children}
      </CardHeader>
      <CardBody className="min-w-0 px-3 pb-3 pt-0 space-y-3">
        <div className="space-y-2 rounded-lg bg-default-100/60 p-2.5 divide-y divide-divider">
          {[
            { label: "入口地址", value: forward.inIp || entryAddress, display: entryAddress, port: forward.inIp ? forward.inPort : null },
            { label: "落地地址", value: forward.remoteAddr, display: formatRemoteAddress(forward.remoteAddr) || "-", port: null },
          ].map((address) => (
            <div key={address.label} className="space-y-1 min-w-0 not-first:pt-2">
              <div className="text-[11px] text-default-500">{address.label} · 端口</div>
              <button
                className="block w-full min-w-0 text-left font-mono text-xs leading-relaxed text-foreground break-all hover:text-primary"
                title={address.value}
                type="button"
                onClick={() => showAddressModal(address.value, address.port, address.label)}
              >
                {address.display}
              </button>
            </div>
          ))}
        </div>
        <div className="flex flex-wrap gap-1.5 text-[11px]">
          {[
            ["上行流量", `↑ ${formatFlow(forward.inFlow || 0)}`, "bg-primary-500/10 text-primary"],
            ["下行流量", `↓ ${formatFlow(forward.outFlow || 0)}`, "bg-secondary-500/10 text-secondary"],
            ["用量", `用量 ${formatFlow(isCardMode ? (forward.inFlow || 0) + (forward.outFlow || 0) : getForwardDisplayFlow(forward))}${isCardMode ? "" : " ▾"}`, "bg-default-100 text-default-600"],
            ["上行带宽", `↑ ${formatSpeed(forward.inSpeed || 0)}`, "bg-primary-500/10 text-primary"],
            ["下行带宽", `↓ ${formatSpeed(forward.outSpeed || 0)}`, "bg-secondary-500/10 text-secondary"],
            ["当前连接数", `${forward.currentConnections ?? 0} 连接`, "bg-success-500/10 text-success-600 dark:text-success-400"],
          ].map(([label, value, color]) => label === "用量" && !isCardMode ? (
            <button key={label} className={`rounded px-2 py-1 hover:text-primary ${color}`} title="查看流量归零日志" type="button" onClick={() => handleViewTrafficResetLogs(forward)}>{value}</button>
          ) : (
            <span key={label} className={`rounded px-2 py-1 ${color}`} title={label}>{value}</span>
          ))}
        </div>
        <div className="grid grid-cols-4 gap-1.5 border-t border-divider pt-3">
          {([
            ["编辑", "primary", handleEdit],
            ["复制", "warning", handleCopy],
            ["诊断", "secondary", handleDiagnose],
            ["删除", "danger", handleDelete],
          ] as const).map(([label, color, action]) => (
            <Button key={label} className="min-w-0 px-1" color={color} size="sm" variant="flat" onPress={() => action(forward)}>{label}</Button>
          ))}
        </div>
      </CardBody>
    </Card>
  );
}
