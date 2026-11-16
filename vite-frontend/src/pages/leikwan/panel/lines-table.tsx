import type { LeiKwanLine, LeiKwanLineList } from "@/api/leikwan-line";

import {
  ExpandActions,
  ExpandField,
  ExpandRow,
  useExpandOne,
} from "@/components/expand-row";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Pill } from "@/pages/landing/pill";
import { LineChip, PipeChip } from "@/pages/leikwan/panel/shared";
import { BackupRebuildButton } from "@/pages/leikwan/panel/backup-tunnel";

// Plan 266: the tunnel group 「利群汇聚」 — one row per 利群专线 (route, entry
// 按授权, the rules on it, 备线隧道, 利群加速 summary) + the pipe-mode tunnel
// of plan 094 shown separately (not in the group). Desktop table, H5 rows.

// Plan 269: 利群流量 billing of the line (单向 / 双向 · 倍率).
export function FlowModePill({ line }: { line: LeiKwanLine }) {
  if (line.flowMode === undefined && line.trafficRatio === undefined)
    return null;

  return (
    <Pill
      title="单向：(上传+下载)×倍率；双向：(上传+下载)×倍率×2"
      tone="purple"
    >
      {line.flowMode === 2 ? "双向" : "单向"} ×{line.trafficRatio || 1}
    </Pill>
  );
}

export function AccelPills({ line }: { line: LeiKwanLine }) {
  const a = line.accel;

  return (
    <div className="flex flex-wrap gap-1">
      <Pill tone={a.warm.main > 0 ? "green" : "slate"}>
        预热 {a.warm.main}/{a.warm.backup}
      </Pill>
      <Pill tone={a.ssFilter === "enforce" ? "green" : "slate"}>首包过滤</Pill>
      <Pill tone={a.logswitch ? "green" : "slate"}>
        日志快切{a.logswitch && a.returnCheck ? " · 复查" : ""}
      </Pill>
    </div>
  );
}

const ruleIds = (line: LeiKwanLine) =>
  line.forwardIds.length ? line.forwardIds.join(" · ") : "—";

export function LeiKwanLinesTable({
  data,
  h5,
  onEdit,
  onChanged,
}: {
  data: LeiKwanLineList;
  h5: boolean;
  onEdit: (line: LeiKwanLine) => void;
  // Plan 268: reload after 「按新备线重建」.
  onChanged?: () => void;
}) {
  const changed = () => onChanged?.();
  const expand = useExpandOne(data.lines.map((l) => l.id));

  if (h5) {
    return (
      <div className="divide-y divide-divider rounded-xl border border-divider">
        {data.lines.map((line) => (
          <ExpandRow
            key={line.id}
            chip={<LineChip />}
            name={line.name}
            open={expand.open === line.id}
            onToggle={() => expand.toggle(line.id)}
          >
            <ExpandField label="路线">{line.routeLabel}</ExpandField>
            <ExpandField label="入口">按授权</ExpandField>
            <ExpandField label="规则">{ruleIds(line)}</ExpandField>
            <ExpandField label="备线">
              {line.backupTunnelName || "不建"}
            </ExpandField>
            {(line.flowMode !== undefined ||
              line.trafficRatio !== undefined) && (
              <ExpandField label="计费">
                <FlowModePill line={line} />
              </ExpandField>
            )}
            <ExpandField label="加速">
              <AccelPills line={line} />
            </ExpandField>
            <ExpandActions>
              <BackupRebuildButton line={line} onDone={changed} />
              <Button
                color="primary"
                size="sm"
                variant="flat"
                onPress={() => onEdit(line)}
              >
                编辑
              </Button>
            </ExpandActions>
          </ExpandRow>
        ))}
        {data.pipes.map((p) => (
          <div
            key={p.forwardId}
            className="flex min-h-[52px] items-center gap-2 px-3 py-2"
          >
            <span className="min-w-0 flex-1 truncate text-sm font-medium">
              {p.tunnelName || `利群 #${p.forwardId}`}
            </span>
            <PipeChip />
          </div>
        ))}
      </div>
    );
  }

  return (
    <div className="overflow-x-auto rounded-xl border border-divider">
      <table className="w-full text-sm">
        <thead className="bg-default-100/60 text-xs text-default-500">
          <tr>
            <th className="px-3 py-2 text-left font-medium">隧道</th>
            <th className="px-3 py-2 text-left font-medium">路线</th>
            <th className="px-3 py-2 text-left font-medium">入口</th>
            <th className="px-3 py-2 text-left font-medium">
              规则（利群转发）
            </th>
            <th className="px-3 py-2 text-left font-medium">备线隧道</th>
            <th className="px-3 py-2 text-left font-medium">利群加速</th>
            <th className="px-3 py-2 text-right font-medium">操作</th>
          </tr>
        </thead>
        <tbody>
          {data.lines.map((line) => (
            <tr key={line.id} className="border-t border-divider">
              <td className="px-3 py-2.5">
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="font-medium">{line.name}</span>
                  <LineChip />
                  {line.status !== 1 && <Pill tone="slate">已停用</Pill>}
                  <FlowModePill line={line} />
                </div>
              </td>
              <td className="whitespace-nowrap px-3 py-2.5">
                {line.routeLabel}
              </td>
              <td className="px-3 py-2.5">
                <Pill tone="purple">按授权</Pill>
              </td>
              <td className="px-3 py-2.5 tabular-nums">{ruleIds(line)}</td>
              <td className="max-w-[220px] px-3 py-2.5 text-xs text-default-600">
                <span className="line-clamp-2">
                  {line.backupTunnelName || "不建"}
                </span>
                {(line.backupStats?.stale || 0) > 0 && (
                  <div className="mt-1">
                    <BackupRebuildButton line={line} onDone={changed} />
                  </div>
                )}
              </td>
              <td className="px-3 py-2.5">
                <AccelPills line={line} />
              </td>
              <td className="px-3 py-2.5 text-right">
                <Button
                  color="primary"
                  size="sm"
                  variant="flat"
                  onPress={() => onEdit(line)}
                >
                  编辑
                </Button>
              </td>
            </tr>
          ))}
          {data.lines.length === 0 && (
            <tr>
              <td
                className="px-3 py-6 text-center text-default-500"
                colSpan={7}
              >
                还没有利群专线：用「导入为规则」把现有转发登记进来，或「新建利群专线」
              </td>
            </tr>
          )}
          {data.pipes.map((p) => (
            <tr
              key={p.forwardId}
              className="border-t border-dashed border-divider bg-default-50/50"
            >
              <td className="px-3 py-2.5">
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="font-medium">
                    {p.tunnelName || `利群 #${p.forwardId}`}
                  </span>
                  <PipeChip />
                </div>
              </td>
              <td className="whitespace-nowrap px-3 py-2.5">{p.routeLabel}</td>
              <td className="px-3 py-2.5 text-xs text-default-500">
                隧道自己的入口
              </td>
              <td className="px-3 py-2.5 tabular-nums">{p.forwardId}</td>
              <td className="px-3 py-2.5 text-xs text-default-500" colSpan={3}>
                不在利群汇聚组里，照旧单独一条（在隧道列表里管理）
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
