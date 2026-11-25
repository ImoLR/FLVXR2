import type { LineKind, LineMonitorLeiKwanLine } from "@/api/line-monitor";

import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// Plan 310 r3: the 线路监测 line kind 「利群专线端内」 — the vendor's own
// segment 专线入口 → 专线出口 of one 专线 (端内 = 全程 − 入口段), read once a
// minute from the 利群入口 reports. Passive: nothing is probed.

export interface EndInEntry {
  id: number;
  name: string;
  online: boolean;
}

const AUTO = "0";

/** TCP 连接 / 利群专线端内 (shown when the panel has 利群专线). */
export function LineKindToggle({
  kind,
  onChange,
}: {
  kind: LineKind;
  onChange: (k: LineKind) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-1.5" data-testid="lm-kind">
      <span className="text-sm font-medium">类型</span>
      <Button
        color={kind !== "lk_endin" ? "primary" : "default"}
        size="sm"
        variant={kind !== "lk_endin" ? "solid" : "flat"}
        onPress={() => onChange("")}
      >
        TCP 连接
      </Button>
      <Button
        color={kind === "lk_endin" ? "primary" : "default"}
        data-testid="lm-kind-endin"
        size="sm"
        variant={kind === "lk_endin" ? "solid" : "flat"}
        onPress={() => onChange("lk_endin")}
      >
        利群专线端内
      </Button>
    </div>
  );
}

export function EndInLineFields({
  lines,
  entries,
  leikwanLineId,
  sourceNodeId,
  label,
  onChange,
}: {
  lines: LineMonitorLeiKwanLine[];
  entries: EndInEntry[];
  leikwanLineId: number;
  sourceNodeId: number;
  label: string;
  onChange: (p: {
    leikwanLineId?: number;
    sourceNodeId?: number;
    label?: string;
  }) => void;
}) {
  return (
    <div className="space-y-2" data-testid="lm-endin-fields">
      <div className="grid gap-2 sm:grid-cols-2">
        <Select
          label="利群专线"
          placeholder="选择专线"
          selectedKeys={leikwanLineId ? [String(leikwanLineId)] : []}
          onSelectionChange={(keys) =>
            onChange({ leikwanLineId: Number(Array.from(keys)[0] || 0) })
          }
        >
          {lines.map((l) => (
            <SelectItem key={String(l.id)} textValue={l.name}>
              {l.name}
              {l.status === 1 ? "" : "（已停用）"}
            </SelectItem>
          ))}
        </Select>
        <Select
          label="入口机"
          selectedKeys={[sourceNodeId > 0 ? String(sourceNodeId) : AUTO]}
          onSelectionChange={(keys) =>
            onChange({ sourceNodeId: Number(Array.from(keys)[0] || 0) })
          }
        >
          {[
            <SelectItem key={AUTO} textValue="自动">
              自动（在线、有新读数的利群入口）
            </SelectItem>,
            ...entries.map((e) => (
              <SelectItem key={String(e.id)} textValue={e.name}>
                {e.name}
                {e.online ? "" : "（离线）"}
              </SelectItem>
            )),
          ]}
        </Select>
      </div>
      <Input
        label="备注（可选）"
        placeholder="图例里显示，例如：广港"
        value={label}
        onChange={(e) => onChange({ label: e.target.value })}
      />
      <p className="text-xs text-default-500">
        端内 = 全程（利群客户端选线时用 UDP 测的，入口机 → 专线出口）−
        入口段（入口机到它在用的入口，已有连接的内核延迟）。每分钟从利群入口的上报读一次，不发任何探测；入口机要升级到带「入口段」的
        agent。没有读数的分钟显示「无数据」，不算断流；自动模式记录每个点来自哪台入口机。
      </p>
    </div>
  );
}
