import { useEffect, useState } from "react";
import toast from "react-hot-toast";
import { CircleCheck, CircleX, Lock, Radar } from "lucide-react";

import { Pill, type LandingTone } from "./pill";

import {
  type LandingNode,
  landingDate,
  unblockLandingScan,
} from "@/api/landing";
import {
  type EgressBuiltin,
  type ScanEvent,
  type ScanGuardAdmin,
  type ScanGuardState,
  listScanEvents,
  saveScanGuard,
} from "@/api/landing-egress";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Plan 202 「禁止端口扫描」: the 内置 row (admin: switch + thresholds +
// per-server state + event list; users: locked, read-only), its collapsed H5
// line, and the note on a node it disabled (owner: 联系管理员; admin: 解除禁用).

type Guard = NonNullable<EgressBuiltin["scanGuard"]>;

const stateTone: Record<ScanGuardState, LandingTone> = {
  on: "green",
  off: "slate",
  shared: "slate",
  agent: "amber",
  core: "amber",
  error: "red",
  pending: "slate",
};

// Short server state for the admin chips (full label in the tooltip).
const stateShort: Record<ScanGuardState, string> = {
  on: "生效中",
  off: "全局已关闭",
  shared: "共享服务器暂不支持",
  agent: "需要升级节点",
  core: "需重启落地 Core",
  error: "检测异常",
  pending: "等待同步",
};

function ScanStateIcon({ on, label }: { on: boolean; label: string }) {
  const Icon = on ? CircleCheck : CircleX;

  return (
    <span
      aria-label={label}
      className={`inline-flex shrink-0 ${on ? "text-success" : "text-danger"}`}
      role="img"
      title={label}
    >
      <Icon aria-hidden size={16} strokeWidth={2.25} />
    </span>
  );
}

const minutes = (s: number) =>
  s % 60 === 0 && s >= 120 ? `${s / 60} 分钟` : `${s} 秒`;

/** Chips of the three patterns, e.g. 「横向：同端口 ≥100 IP / 60 秒」. */
export function scanGuardChips(g: Guard, admin: boolean): string[] {
  const web = (g.webPorts || [80, 443]).join("/");
  const chips = [
    `横向：同端口 ≥${g.horizontal} IP / ${minutes(g.windowS || 60)}`,
    `${web}：≥${g.horizontalWeb} IP`,
    `纵向：同 IP ≥${g.vertical} 端口 / ${minutes(g.windowS || 60)}`,
    `总量：≥${g.total} IP / ${minutes(g.totalWindowS || 300)}`,
    "仅 TCP",
  ];

  if (admin) chips.push("仅自有落地");

  return chips;
}

/** In force on the viewed node (node view) or globally (admin view). */
function guardState(
  g: Guard,
  nodeView: boolean,
): { on: boolean; label: string } {
  if (nodeView && g.state)
    return { on: g.state === "on", label: g.label || "" };

  return g.on
    ? { on: true, label: "生效中" }
    : { on: false, label: "未生效：全局已关闭" };
}

export function ScanGuardSection({
  guard,
  admin,
  onSaved,
}: {
  guard: Guard;
  // admin = the global 内置 card (switch, thresholds, servers, events).
  admin?: boolean;
  onSaved?: (g: ScanGuardAdmin) => void;
}) {
  const editable = Boolean(admin && onSaved);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [eventsOpen, setEventsOpen] = useState(false);
  const [draft, setDraft] = useState({
    horizontal: String(guard.horizontal),
    horizontalWeb: String(guard.horizontalWeb),
    vertical: String(guard.vertical),
    total: String(guard.total),
  });
  const nodeView = !admin;
  const state = guardState(guard, nodeView);

  useEffect(() => {
    if (!editing)
      setDraft({
        horizontal: String(guard.horizontal),
        horizontalWeb: String(guard.horizontalWeb),
        vertical: String(guard.vertical),
        total: String(guard.total),
      });
  }, [guard, editing]);

  const save = async (on: boolean, values = draft) => {
    setSaving(true);
    const res = await saveScanGuard({
      on,
      horizontal: Number(values.horizontal),
      horizontalWeb: Number(values.horizontalWeb),
      vertical: Number(values.vertical),
      total: Number(values.total),
    });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success("禁止端口扫描已保存，正在同步到自有落地服务器");
    setEditing(false);
    onSaved?.(res.data);
  };

  const native = (guard.servers || []).filter((s) => s.source === "native");
  const shared = (guard.servers || []).filter((s) => s.source === "shared");
  const hint = admin
    ? "落地节点被用来批量探测别人服务器的端口时，落地 Core 立即禁用这个节点（端口、密钥和链接都保留）并通知管理员，只有管理员能「解除禁用」。正常上网、看视频、测速达不到这些数字。"
    : `如果有人用这个落地节点批量扫描端口（例如同一端口 1 分钟内连接 ${guard.horizontal} 个以上不同 IP），节点会被立即禁用，需要联系管理员解除。正常上网、看视频、下载不受影响。`;

  return (
    <>
      <div
        data-scan-guard-header
        className="flex items-start gap-2 bg-default-100/70 px-3 py-1.5"
      >
        <Radar className="mt-0.5 shrink-0 text-default-500" size={14} />
        <span className="shrink-0 whitespace-nowrap text-xs font-medium text-default-700">
          行为检测
        </span>
        <span className="min-w-0 text-xs text-default-500">
          {admin
            ? "按连接行为判断，超过阈值立即禁用节点；只对自有落地生效"
            : "1 条，节点规则不能放行"}
        </span>
      </div>
      <div
        data-scan-guard-row
        className={`flex items-start gap-3 p-3 ${state.on ? "bg-default-50" : "bg-default-50/40"}`}
      >
        {admin ? (
          <span className="mt-1 w-[14px] shrink-0" />
        ) : (
          <Lock
            aria-label="锁定"
            className="mt-1 shrink-0 text-default-500"
            size={14}
          />
        )}
        <div
          className={`min-w-0 flex-1 space-y-1 ${state.on ? "" : "opacity-80"}`}
        >
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">禁止端口扫描</span>
            <Pill tone="red">→ 禁用节点</Pill>
          </div>
          <div className="flex flex-wrap gap-1">
            {scanGuardChips(guard, Boolean(admin)).map((chip) => (
              <Pill key={chip} tone="slate">
                {chip}
              </Pill>
            ))}
          </div>
          <p className="text-xs text-default-500">{hint}</p>
          {!state.on && !editable && (
            <p className="text-xs text-danger">{state.label}</p>
          )}
          {editable && editing && (
            <div data-scan-guard-editor className="space-y-2 pt-1">
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <Input
                  label="横向：同一端口 IP 数"
                  min="20"
                  type="number"
                  value={draft.horizontal}
                  variant="bordered"
                  onChange={(e) =>
                    setDraft({ ...draft, horizontal: e.target.value })
                  }
                />
                <Input
                  label="80/443 端口 IP 数"
                  min="20"
                  type="number"
                  value={draft.horizontalWeb}
                  variant="bordered"
                  onChange={(e) =>
                    setDraft({ ...draft, horizontalWeb: e.target.value })
                  }
                />
                <Input
                  label="纵向：同一 IP 端口数"
                  min="10"
                  type="number"
                  value={draft.vertical}
                  variant="bordered"
                  onChange={(e) =>
                    setDraft({ ...draft, vertical: e.target.value })
                  }
                />
                <Input
                  label="总量：5 分钟 IP 数"
                  min="100"
                  type="number"
                  value={draft.total}
                  variant="bordered"
                  onChange={(e) =>
                    setDraft({ ...draft, total: e.target.value })
                  }
                />
              </div>
              <p className="text-xs text-default-500">
                横向和纵向按 60 秒统计，总量按 5 分钟统计；只数
                TCP，UDP（游戏、QUIC）不计。保存后自动同步。
              </p>
              <div className="flex justify-end gap-2">
                <Button
                  color="danger"
                  size="sm"
                  variant="flat"
                  onPress={() => setEditing(false)}
                >
                  取消
                </Button>
                <Button
                  color="success"
                  isLoading={saving}
                  size="sm"
                  onPress={() => void save(guard.on)}
                >
                  保存阈值
                </Button>
              </div>
            </div>
          )}
          {admin && (
            <div className="space-y-1 pt-1">
              <div className="flex flex-wrap items-center gap-1">
                {native.map((s) => (
                  <Pill
                    key={s.id}
                    title={s.label}
                    tone={stateTone[s.state] || "slate"}
                  >
                    {s.name}：{stateShort[s.state] || s.label}
                  </Pill>
                ))}
                {shared.length > 0 && (
                  <Pill
                    title={shared.map((s) => s.name).join("、")}
                    tone="slate"
                  >
                    妙妙屋共享 {shared.length} 台：暂不支持
                  </Pill>
                )}
              </div>
              <div className="flex flex-wrap items-center gap-2 text-xs text-default-500">
                <span>
                  当前因端口扫描禁用 {guard.blockedNodes ?? 0} 个节点 · 记录{" "}
                  {guard.events ?? 0} 条（保留 30 天）
                </span>
                {editable && !editing && (
                  <Button
                    className="h-6 min-w-0 px-2 text-xs"
                    color="primary"
                    size="sm"
                    variant="flat"
                    onPress={() => setEditing(true)}
                  >
                    修改阈值
                  </Button>
                )}
                <Button
                  className="h-6 min-w-0 px-2 text-xs"
                  color="secondary"
                  size="sm"
                  variant="flat"
                  onPress={() => setEventsOpen(true)}
                >
                  查看记录
                </Button>
              </div>
            </div>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2 self-center">
          {editable && (
            <Switch
              aria-label="禁止端口扫描全局开关"
              isDisabled={saving}
              isSelected={guard.on}
              onValueChange={(v) => void save(v)}
            />
          )}
          <ScanStateIcon label={state.label} on={state.on} />
        </div>
      </div>
      {admin && (
        <ScanEventsDialog
          open={eventsOpen}
          onClose={() => setEventsOpen(false)}
        />
      )}
    </>
  );
}

/** Plan 202: the collapsed H5 line of the 内置 layer. */
export function ScanGuardCompactRow({ guard }: { guard: Guard }) {
  const state = guardState(guard, true);

  return (
    <div
      className={`grid grid-cols-[14px_minmax(0,1fr)_auto_auto] items-center gap-2 px-3 py-2 ${state.on ? "" : "opacity-70"}`}
      data-builtin-compact="scan"
    >
      <Lock aria-label="锁定" className="text-default-500" size={14} />
      <span className="truncate text-sm font-medium" title="禁止端口扫描">
        禁止端口扫描
      </span>
      <Pill tone="red">→ 禁用节点</Pill>
      <ScanStateIcon label={state.label} on={state.on} />
    </div>
  );
}

const typeTone: Record<string, LandingTone> = {
  horizontal: "purple",
  vertical: "teal",
  total: "pink",
};

function EventStatus({ e }: { e: ScanEvent }) {
  if (!e.blocked) return <Pill tone="slate">仅记录（节点已禁用）</Pill>;
  if (e.nodeBlocked && !e.unblockedAt) return <Pill tone="red">禁用中</Pill>;
  if (e.unblockedAt)
    return (
      <Pill
        title={`${e.unblockedBy} · ${landingDate(e.unblockedAt)}`}
        tone="green"
      >
        已解除 · {e.unblockedBy || "管理员"}
      </Pill>
    );

  return <Pill tone="slate">节点已删除或已解除</Pill>;
}

/** Admin list of 禁止端口扫描 events (newest 200, kept 30 days). */
export function ScanEventsDialog({
  open,
  onClose,
  nodeId = 0,
}: {
  open: boolean;
  onClose: () => void;
  nodeId?: number;
}) {
  const [events, setEvents] = useState<ScanEvent[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!open) return;
    let alive = true;

    setLoading(true);
    void listScanEvents(nodeId).then((res) => {
      if (!alive) return;
      setLoading(false);
      if (res.code !== 0) {
        toast.error(res.msg || "读取记录失败");

        return;
      }
      setEvents(res.data.events || []);
      setTotal(res.data.total || 0);
    });

    return () => {
      alive = false;
    };
  }, [open, nodeId]);

  return (
    <Modal isOpen={open} scrollBehavior="inside" size="2xl" onClose={onClose}>
      <ModalContent className="flvx-landing">
        <ModalHeader>禁止端口扫描 · 检测记录</ModalHeader>
        <ModalBody data-scan-events className="space-y-2">
          <p className="text-xs text-default-500">
            共 {total} 条，显示最新 {events.length} 条；保留 30 天、最多 5000
            条。一个节点被禁用后，Core 不再为它计数，直到管理员解除禁用。
          </p>
          {loading ? (
            <div className="flex justify-center py-6">
              <Spinner size="sm" />
            </div>
          ) : events.length === 0 ? (
            <p className="rounded-lg bg-default-100 p-4 text-center text-sm text-default-500">
              还没有检测记录
            </p>
          ) : (
            <div className="divide-y divide-divider rounded-lg border border-divider">
              {events.map((e) => (
                <div
                  key={e.id}
                  data-scan-event
                  className="grid gap-1 p-3 text-sm sm:grid-cols-[150px_minmax(0,1fr)_auto] sm:items-center sm:gap-3"
                >
                  <span className="text-xs tabular-nums text-default-500">
                    {landingDate(e.hitAt)}
                  </span>
                  <div className="min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-1">
                      <span className="break-all font-medium">
                        {e.nodeName}
                      </span>
                      <Pill tone={typeTone[e.type] || "slate"}>
                        {e.typeLabel}
                      </Pill>
                      <span className="text-xs text-default-500">
                        {e.userName || `用户 #${e.userId}`} · {e.serverName}
                      </span>
                    </div>
                    <p className="text-xs text-default-600">{e.reason}</p>
                  </div>
                  <div className="sm:justify-self-end">
                    <EventStatus e={e} />
                  </div>
                </div>
              ))}
            </div>
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            关闭
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}

/**
 * Plan 202: the note on a node disabled by 禁止端口扫描. Owner: the 联系管理员
 * message; admin: reason + time + 「解除禁用」.
 */
export function ScanBlockNote({
  node,
  admin,
  compact = false,
  onUnblocked,
}: {
  node: LandingNode;
  admin: boolean;
  // compact: the admin table cell (time in the tooltip).
  compact?: boolean;
  onUnblocked?: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const block = node.scanBlock;

  if (!block) return null;

  const unblock = async () => {
    setBusy(true);
    const res = await unblockLandingScan(node.id);

    setBusy(false);
    if (res.code !== 0) {
      toast.error(res.msg || "解除禁用失败");

      return;
    }
    toast.success("已解除禁用，正在同步到落地服务器");
    onUnblocked?.();
  };

  return (
    <div
      data-scan-block
      className="w-full space-y-1 rounded-lg bg-danger-50 px-3 py-2 text-xs text-danger-700 dark:bg-danger-500/10 dark:text-danger-400"
      title={admin ? `检测时间 ${landingDate(block.at)}` : undefined}
    >
      {admin ? (
        <>
          <p className="font-medium">
            {compact
              ? `${block.typeLabel}：`
              : `因端口扫描已禁用（${block.typeLabel}）：`}
            {block.reason}
          </p>
          {!compact && (
            <p className="text-default-500">
              检测时间 {landingDate(block.at)}
              ；端口、密钥和链接都保留，解除后立即恢复。
            </p>
          )}
          <Button
            className="h-7"
            color="success"
            isLoading={busy}
            size="sm"
            variant="flat"
            onPress={() => void unblock()}
          >
            解除禁用
          </Button>
        </>
      ) : (
        <p>{block.message}</p>
      )}
    </div>
  );
}
