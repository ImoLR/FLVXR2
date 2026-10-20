import type { MachineView } from "@/api/types";

import { Input } from "@/shadcn-bridge/heroui/input";
import { Switch } from "@/shadcn-bridge/heroui/switch";
import {
  Dropdown,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// 整机连接数 + 整机连接保护 (plan 118). The number is every TCP + UDP socket of the
// machine in every state, the same number the 妙妙屋 server card shows.

export const MACHINE_DEFAULTS = {
  enabled: true,
  maxTotal: 1000,
  twBuckets: 500,
  closeWaitSeconds: 10,
};
export const MACHINE_RELEASE_PERCENT = 95;
const WARN_PERCENT = 80;

type RawMetric = Record<string, unknown>;

const num = (v: unknown): number => {
  const n = Number(v);

  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
};
const optNum = (v: unknown): number | null => {
  const n = num(v);

  return n > 0 ? n : null;
};

/** The agent's snake_case machine_conns / machine_protect (admin WS metric). */
export const machineFromMetric = (
  metric: RawMetric | null | undefined,
  receivedAt = Date.now(),
): MachineView | null => {
  if (!metric) return null;
  const c = metric.machine_conns as RawMetric | undefined;
  const p = metric.machine_protect as RawMetric | undefined;

  if (!c && !p) return null;
  const view: MachineView = { updatedAt: receivedAt };

  if (c && typeof c === "object") {
    const tcp = num(c.tcp);
    const udp = num(c.udp);

    view.conns = {
      total: tcp + udp,
      tcp,
      udp,
      established: num(c.established),
      timeWait: num(c.time_wait),
      closeWait: num(c.close_wait),
      otherTcp: num(c.other_tcp),
    };
  }
  if (
    p &&
    typeof p === "object" &&
    (p.owner === "flvx" || p.owner === "mmwx")
  ) {
    view.protect = {
      owner: p.owner,
      enabled: p.enabled === true,
      maxTotal: optNum(p.max_total),
      twBuckets: optNum(p.tw_buckets),
      closeWaitSeconds: optNum(p.close_wait_seconds),
      blocking: p.blocking === true,
      droppedTotal: num(p.dropped_total),
      ports: num(p.ports) || undefined,
      error: typeof p.error === "string" ? p.error : undefined,
      mmwxStatus:
        p.mmwx_status === "ok" ||
        p.mmwx_status === "stale" ||
        p.mmwx_status === "missing"
          ? p.mmwx_status
          : undefined,
    };
  }

  return view;
};

/** Newer of two samples (live WS vs. API snapshot). */
export const newerMachine = (
  a: MachineView | null | undefined,
  b: MachineView | null | undefined,
): MachineView | null => {
  if (!a) return b ?? null;
  if (!b) return a;

  return (a.updatedAt || 0) >= (b.updatedAt || 0) ? a : b;
};

const capOf = (m: MachineView | null | undefined): number | null => {
  const p = m?.protect;

  if (!p || !p.enabled || !p.maxTotal) return null;

  return p.maxTotal;
};

type Tone = "neutral" | "warning" | "danger";

const toneOf = (m: MachineView | null | undefined): Tone => {
  if (!m?.conns) return "neutral";
  if (m.protect?.blocking) return "danger";
  const cap = capOf(m);

  if (cap && m.conns.total * 100 >= cap * WARN_PERCENT) return "warning";

  return "neutral";
};

const toneClass: Record<Tone, string> = {
  neutral:
    "border border-default-300 bg-default-100/70 text-default-600 dark:border-default-200/40 dark:bg-default-100/20 dark:text-default-400",
  warning:
    "border border-warning-300 bg-warning-100/80 text-warning-700 dark:border-warning-500/40 dark:bg-warning-500/15 dark:text-warning-400",
  danger:
    "border border-danger-300 bg-danger-100/80 text-danger-600 dark:border-danger-500/40 dark:bg-danger-500/15 dark:text-danger-400",
};

const ownerText = (m: MachineView): string => {
  const p = m.protect;

  if (!p) return "保护：未知";
  if (p.owner === "mmwx") {
    if (p.mmwxStatus === "missing")
      return "由妙妙屋负责（妙妙屋助手版本过旧，暂未启用保护）";
    if (p.mmwxStatus === "stale") return "由妙妙屋负责（妙妙屋助手状态未更新）";

    return p.enabled ? "由妙妙屋负责" : "由妙妙屋负责（保护已关闭）";
  }

  return p.enabled ? "由 FLVX 负责" : "FLVX 保护已关闭";
};

const timeText = (ms: number) =>
  ms ? new Date(ms).toLocaleTimeString("zh-CN", { hour12: false }) : "暂无记录";

export const machineSummary = (m: MachineView | null | undefined): string => {
  if (!m?.conns) return "整机连接：暂无数据（节点离线或节点程序版本过旧）";
  const cap = capOf(m);
  const c = m.conns;
  const lines = [
    `整机连接 ${c.total}${cap ? ` / 上限 ${cap}` : ""}（TCP + UDP，含所有状态）`,
    `ESTABLISHED ${c.established} · TIME_WAIT ${c.timeWait} · CLOSE_WAIT ${c.closeWait}`,
    `其它 TCP ${c.otherTcp} · UDP ${c.udp}`,
  ];

  if (m.protect) {
    lines.push(`已丢弃新连接 ${m.protect.droppedTotal} · ${ownerText(m)}`);
  }

  return lines.join("\n");
};

function BreakdownRow({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex items-center justify-between gap-6">
      <span className="text-default-500">{label}</span>
      <span className="font-mono tabular-nums text-foreground">{value}</span>
    </div>
  );
}

export function MachineBreakdown({
  machine,
  admin = false,
}: {
  machine: MachineView | null | undefined;
  admin?: boolean;
}) {
  if (!machine?.conns) {
    return (
      <div className="space-y-1 text-xs">
        <p className="text-foreground">整机连接：暂无数据</p>
        <p className="text-default-500">
          节点离线，或节点程序版本过旧（升级后显示整机连接数）。
        </p>
      </div>
    );
  }
  const c = machine.conns;
  const p = machine.protect;
  const cap = capOf(machine);

  return (
    <div className="space-y-2 text-xs">
      <div>
        <p className="text-sm font-semibold text-foreground">
          整机连接 {c.total}
          {cap ? <span className="text-default-500"> / 上限 {cap}</span> : null}
        </p>
        <p className="text-default-500">
          TCP + UDP，含所有状态（与妙妙屋服务器卡片一致）
        </p>
      </div>
      <div className="space-y-1 rounded-lg border border-divider bg-default-50/70 p-2 dark:bg-default-100/10">
        <BreakdownRow label="ESTABLISHED" value={c.established} />
        <BreakdownRow label="TIME_WAIT" value={c.timeWait} />
        <BreakdownRow label="CLOSE_WAIT" value={c.closeWait} />
        <BreakdownRow label="其它 TCP" value={c.otherTcp} />
        <BreakdownRow label="UDP" value={c.udp} />
      </div>
      {p ? (
        <div className="space-y-1">
          <BreakdownRow label="已丢弃新连接" value={p.droppedTotal} />
          <p className="text-default-600">{ownerText(machine)}</p>
          {p.blocking ? (
            <p className="text-danger-600 dark:text-danger-400">
              保护中：业务端口暂停接受新连接，已有连接不受影响
              {cap
                ? `；降到 ${Math.floor((cap * MACHINE_RELEASE_PERCENT) / 100)} 以下自动恢复`
                : ""}
            </p>
          ) : null}
          {admin && p.enabled && (p.twBuckets || p.closeWaitSeconds) ? (
            <p className="text-default-500">
              {p.twBuckets ? `TIME_WAIT 上限 ${p.twBuckets}` : "TIME_WAIT 不限"}
              {" · "}
              {p.closeWaitSeconds
                ? `CLOSE_WAIT 超时 ${p.closeWaitSeconds} 秒`
                : "CLOSE_WAIT 不限"}
            </p>
          ) : null}
          {admin && p.error ? (
            <p className="break-words text-warning-700 dark:text-warning-400">
              {p.error}
            </p>
          ) : null}
        </div>
      ) : null}
      <p className="text-default-400">更新于 {timeText(machine.updatedAt)}</p>
    </div>
  );
}

/** 「整机连接 823/1000」 chip with a click breakdown (hover shows the summary). */
export function MachineConnChip({
  machine,
  admin = false,
  showLabel = true,
  label = "整机连接",
  className = "",
}: {
  machine: MachineView | null | undefined;
  admin?: boolean;
  showLabel?: boolean;
  label?: string;
  className?: string;
}) {
  const tone = toneOf(machine);
  const cap = capOf(machine);
  const value = machine?.conns
    ? `${machine.conns.total}${cap ? `/${cap}` : ""}`
    : "-";

  return (
    <Dropdown>
      <DropdownTrigger>
        <button
          aria-label="整机连接详情"
          className={`inline-flex h-6 shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 text-xs font-medium transition-colors hover:brightness-95 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary ${toneClass[tone]} ${className}`}
          title={machineSummary(machine)}
          type="button"
          onClick={(event) => event.stopPropagation()}
        >
          {showLabel ? <span>{label}</span> : null}
          <span className="font-mono tabular-nums">{value}</span>
          {machine?.protect?.blocking ? (
            <span className="font-semibold">保护中</span>
          ) : null}
        </button>
      </DropdownTrigger>
      <DropdownMenu aria-label="整机连接详情">
        <DropdownMenuLabel className="w-72 max-w-[min(18rem,calc(100vw-2rem))] whitespace-normal font-normal">
          <MachineBreakdown admin={admin} machine={machine} />
        </DropdownMenuLabel>
      </DropdownMenu>
    </Dropdown>
  );
}

export interface MachineProtectForm {
  machineProtectEnabled: boolean;
  machineMaxTotal: string;
  machineTwBuckets: string;
  machineCloseWaitSeconds: string;
}

export const machineFormDefaults = (): MachineProtectForm => ({
  machineProtectEnabled: MACHINE_DEFAULTS.enabled,
  machineMaxTotal: String(MACHINE_DEFAULTS.maxTotal),
  machineTwBuckets: String(MACHINE_DEFAULTS.twBuckets),
  machineCloseWaitSeconds: String(MACHINE_DEFAULTS.closeWaitSeconds),
});

export const machineFormFromNode = (node: {
  machineProtectEnabled?: boolean;
  machineMaxTotal?: number;
  machineTwBuckets?: number;
  machineCloseWaitSeconds?: number;
}): MachineProtectForm => ({
  machineProtectEnabled: node.machineProtectEnabled ?? MACHINE_DEFAULTS.enabled,
  machineMaxTotal: String(node.machineMaxTotal ?? MACHINE_DEFAULTS.maxTotal),
  machineTwBuckets: String(node.machineTwBuckets ?? MACHINE_DEFAULTS.twBuckets),
  machineCloseWaitSeconds: String(
    node.machineCloseWaitSeconds ?? MACHINE_DEFAULTS.closeWaitSeconds,
  ),
});

const ranges = {
  machineMaxTotal: { label: "整机连接上限", min: 100, max: 1000000 },
  machineTwBuckets: { label: "TIME_WAIT 上限", min: 100, max: 2000000 },
  machineCloseWaitSeconds: { label: "CLOSE_WAIT 超时", min: 1, max: 3600 },
} as const;

/** Same ranges as the panel and agent; 0 = 该项不启用. */
export const validateMachineForm = (
  form: MachineProtectForm,
): Partial<Record<keyof typeof ranges, string>> => {
  const errors: Partial<Record<keyof typeof ranges, string>> = {};

  for (const key of Object.keys(ranges) as (keyof typeof ranges)[]) {
    const raw = form[key].trim();
    const r = ranges[key];

    if (!/^\d+$/.test(raw)) {
      errors[key] = `${r.label}须为整数（0 表示不启用）`;
      continue;
    }
    const v = Number(raw);

    if (v !== 0 && (v < r.min || v > r.max)) {
      errors[key] = `${r.label}须为 0 或 ${r.min}–${r.max}`;
    }
  }

  return errors;
};

export const machinePayload = (form: MachineProtectForm) => ({
  machineProtectEnabled: form.machineProtectEnabled,
  machineMaxTotal: Number(form.machineMaxTotal.trim() || 0),
  machineTwBuckets: Number(form.machineTwBuckets.trim() || 0),
  machineCloseWaitSeconds: Number(form.machineCloseWaitSeconds.trim() || 0),
});

/** 节点编辑「连接保护」. On a machine the 妙妙屋助手 owns, it is read-only. */
export function MachineProtectSection({
  form,
  errors,
  machine,
  online,
  capable,
  isEdit,
  onChange,
}: {
  form: MachineProtectForm;
  errors: Partial<Record<string, string>>;
  machine: MachineView | null | undefined;
  online: boolean;
  capable: boolean;
  isEdit: boolean;
  onChange: (patch: Partial<MachineProtectForm>) => void;
}) {
  const mmwx = machine?.protect?.owner === "mmwx" ? machine.protect : null;

  if (mmwx) {
    const known = mmwx.mmwxStatus !== "missing";

    return (
      <div className="space-y-2 rounded-xl border border-divider bg-default-50/60 p-3 dark:bg-default-100/10">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-semibold text-foreground">
            连接保护
          </span>
          <span className="inline-flex items-center rounded-full border border-secondary-300 bg-secondary-100/80 px-2 py-0.5 text-xs font-medium text-secondary-700 dark:border-secondary-500/40 dark:bg-secondary-500/15 dark:text-secondary-300">
            由妙妙屋负责
          </span>
          <MachineConnChip admin machine={machine} />
        </div>
        {known ? (
          <div className="grid grid-cols-1 gap-1 text-xs text-default-600 sm:grid-cols-3">
            <span>
              整机连接上限：
              {mmwx.enabled && mmwx.maxTotal ? mmwx.maxTotal : "不限"}
            </span>
            <span>
              TIME_WAIT 上限：
              {mmwx.enabled && mmwx.twBuckets ? mmwx.twBuckets : "不限"}
            </span>
            <span>
              CLOSE_WAIT 超时：
              {mmwx.enabled && mmwx.closeWaitSeconds
                ? `${mmwx.closeWaitSeconds} 秒`
                : "不限"}
            </span>
          </div>
        ) : (
          <p className="text-xs text-warning-700 dark:text-warning-400">
            妙妙屋助手版本过旧，暂未启用保护
          </p>
        )}
        <p className="text-xs text-default-500">
          这台机器运行着妙妙屋助手，整机连接保护由妙妙屋统一负责（含 FLVX
          的业务端口），请在妙妙屋中修改。
          {mmwx.mmwxStatus === "stale" ? " 妙妙屋助手状态暂未更新。" : ""}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-3 rounded-xl border border-divider p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 text-sm">
          <Switch
            aria-label="连接保护"
            isSelected={form.machineProtectEnabled}
            onValueChange={(machineProtectEnabled) =>
              onChange({ machineProtectEnabled })
            }
          />
          <span className="font-semibold text-foreground">连接保护</span>
        </div>
        {isEdit ? <MachineConnChip admin machine={machine} /> : null}
      </div>
      <p className="text-xs text-default-500">
        整机连接数 = 整台机器的 TCP + UDP（含 TIME_WAIT
        等所有状态）。达到上限时只拒绝业务端口的新连接，已有连接不受影响；降到上限的{" "}
        {MACHINE_RELEASE_PERCENT}% 以下自动恢复。0 表示该项不启用。
      </p>
      {form.machineProtectEnabled ? (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
          <Input
            errorMessage={errors.machineMaxTotal}
            isInvalid={!!errors.machineMaxTotal}
            label="整机连接上限"
            placeholder="1000"
            type="number"
            value={form.machineMaxTotal}
            variant="bordered"
            onChange={(e) => onChange({ machineMaxTotal: e.target.value })}
          />
          <Input
            errorMessage={errors.machineTwBuckets}
            isInvalid={!!errors.machineTwBuckets}
            label="TIME_WAIT 上限"
            placeholder="500"
            type="number"
            value={form.machineTwBuckets}
            variant="bordered"
            onChange={(e) => onChange({ machineTwBuckets: e.target.value })}
          />
          <Input
            description="仅作用于本节点的落地"
            errorMessage={errors.machineCloseWaitSeconds}
            isInvalid={!!errors.machineCloseWaitSeconds}
            label="CLOSE_WAIT 超时（秒）"
            placeholder="10"
            type="number"
            value={form.machineCloseWaitSeconds}
            variant="bordered"
            onChange={(e) =>
              onChange({ machineCloseWaitSeconds: e.target.value })
            }
          />
        </div>
      ) : null}
      {isEdit && online && !capable ? (
        <p className="text-xs text-warning-700 dark:text-warning-400">
          节点程序版本过旧，升级节点后生效。
        </p>
      ) : null}
      {isEdit && !online ? (
        <p className="text-xs text-default-500">节点离线，上线后自动下发。</p>
      ) : null}
    </div>
  );
}
