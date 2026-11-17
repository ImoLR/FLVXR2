import type { ReactNode } from "react";
import type { LeiKwanRule, LeiKwanRuleOptions } from "@/api/leikwan-line";

import { useCallback, useEffect, useState } from "react";
import { AlertTriangle } from "lucide-react";
import toast from "react-hot-toast";

import {
  createLeiKwanRule,
  deleteLeiKwanRule,
  getLeiKwanRuleLink,
  getLeiKwanRuleOptions,
  newLeiKwanRequestId,
  retryLeiKwanBackup,
  updateLeiKwanRule,
} from "@/api/leikwan-line";
import { landingProtocolLabel } from "@/api/landing";
import { shortProtocol } from "@/components/expand-row";
import { Pill } from "@/pages/landing/pill";
import {
  LabeledSwitch,
  QuotaPill,
  fmtBytes,
} from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// Plan 270: the 利群 rule pieces shared by the admin and the user view of the
// 利群汇聚 page (moved here from the old 规则-page section of plan 266/269):
// chips, the 待切换 block, and the rule actions with their dialogs
// (新增 / 换落地 / 切到新落地 / 删除 / 重试备线 / 复制链接).

export async function copyText(text: string, what: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast.success(`已复制${what}`);
  } catch {
    const ta = document.createElement("textarea");

    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand("copy");
    ta.remove();
    toast.success(`已复制${what}`);
  }
}

/** 「CAN → HKG 广港」 → 「CAN → HKG」 (the line name already says 广港). */
export const routeShort = (label?: string) =>
  (label || "")
    .replace(/\s*->\s*/g, " → ")
    .replace(/\s+[\p{Script=Han}·]+$/u, "") ||
  label ||
  "";

/** 「SS2022」, 「VLESS Reality」, 「AnyTLS」… (compact: 「Reality」). */
export const protocolLabel = (p?: string, compact = false) => {
  if (!p) return "";
  const label = shortProtocol(
    landingProtocolLabel(p).replace(/\s*Vision$/, ""),
  );

  return compact ? label.replace(/^VLESS Reality$/, "Reality") : label;
};

export const landingText = (r: LeiKwanRule) =>
  r.landing.kind === "node"
    ? `${r.landing.serverName || "落地"} · ${r.landing.name || ""}`
    : `${r.landing.host}:${r.landing.port}`;

/** Tinted pill buttons of the 利群 page (rules-page palette). */
export const cyanButton =
  "bg-cyan-500/15 text-cyan-700 hover:bg-cyan-500/25 dark:bg-cyan-400/15 dark:text-cyan-300 dark:hover:bg-cyan-400/25";

// Plan 269: billed 利群流量 of the rule (since the owner's reset).
export function FlowPill({ rule }: { rule: LeiKwanRule }) {
  if (rule.inFlow === undefined && rule.outFlow === undefined) return null;

  return (
    <Pill
      title="利群流量：已按专线的计费方式和倍率，从上次重置算起"
      tone="purple"
    >
      ↑{fmtBytes(rule.inFlow)} ↓{fmtBytes(rule.outFlow)}
    </Pill>
  );
}

// Plan 271: 备线流量 of the rule (its 备线 forward, since the owner's
// reset); nothing while 0. A shared 备线 shows its full bytes (tooltip).
export function BackupFlowPill({ rule }: { rule: LeiKwanRule }) {
  const up = rule.backupInFlow || 0;
  const down = rule.backupOutFlow || 0;

  if (up + down <= 0) return null;
  const others = rule.backupSharedCount || 0;
  const names = (rule.backupSharedWith || []).join("、");
  const shared =
    others > 0
      ? `\n这条备线和${names ? `「${names}」` : ""}${
          names && others > (rule.backupSharedWith || []).length
            ? `等 ${others} 条规则`
            : names
              ? ""
              : ` ${others} 条其他规则`
        }共用，显示的是整条备线的流量，没有拆分`
      : "";

  return (
    <Pill
      title={`备线流量：断线切到备线后走的流量（↑${fmtBytes(up)} ↓${fmtBytes(down)}），从上次重置算起，也算进总流量${shared}`}
      tone="green"
    >
      备线 {fmtBytes(up + down)}
      {others > 0 ? " · 共用" : ""}
    </Pill>
  );
}

// Plan 269: the rule's 利群 port is blocked on the entry hosts.
export function BlockedPill({ rule }: { rule: LeiKwanRule }) {
  if (!rule.blockReason) return null;

  return (
    <Pill
      title={`已暂停：${rule.blockReason}。链接暂时用不了，恢复后自动解除`}
      tone="red"
    >
      已暂停 · {rule.blockReason}
    </Pill>
  );
}

export const blockedNote = (r: LeiKwanRule) =>
  `链接暂时用不了（入口端口已屏蔽、备线一起暂停）：${r.blockReason}；恢复后自动解除`;

export const pendingText = (r: LeiKwanRule) => {
  const p = r.pendingLanding;

  if (!p) return "";

  return `待切换 → ${p.name || `落地 #${p.landingNodeId}`}（${p.serverName || p.host || "—"}:${p.port ?? "—"}）`;
};

/** Status dot: red = 已暂停, slate = 停用, green = OK. */
export function RuleDot({ rule }: { rule: LeiKwanRule }) {
  const cls = rule.blockReason
    ? "bg-red-500"
    : rule.status !== 1
      ? "bg-slate-400"
      : "bg-green-500";

  return (
    <span
      aria-label={rule.blockReason ? `已暂停：${rule.blockReason}` : "运行中"}
      className={`mt-[7px] h-2 w-2 shrink-0 rounded-full ${cls}`}
      role="img"
    />
  );
}

/** Admin: 备线 #id (+ 与 X 共用) or the ⚠ 「备线未建好」 chip. */
export function AdminBackupChip({ rule }: { rule: LeiKwanRule }) {
  if (rule.backupError)
    return (
      <Pill
        icon={<AlertTriangle className="text-red-600" size={12} />}
        title={rule.backupError}
        tone="amber"
      >
        备线未建好
      </Pill>
    );
  const b = rule.backup;

  if (!rule.backupOn || !b) return null;
  const shared = (b.sharedWith || []).filter((id) => id !== rule.forwardId);

  return (
    <Pill
      title={`${b.name || ""} · ${b.tunnelName || ""} · 入口 ${b.entry || "—"} → ${b.remoteAddr || "—"}\n${b.mode === "linked" ? "导入时链接的现有规则" : "面板自动建的"} · 已用 ${fmtBytes((b.inFlow || 0) + (b.outFlow || 0))}`}
      tone={b.missing ? "amber" : "green"}
    >
      备线 #{b.forwardId}
      {shared.length > 0 ? ` · 与 ${shared.join("、")} 共用` : ""}
    </Pill>
  );
}

/** User: 备线 开 (no forward ids), or the neutral 「备线还没建好」. */
export function UserBackupChip({ rule }: { rule: LeiKwanRule }) {
  if (rule.backupError)
    return (
      <Pill title={rule.backupError} tone="amber">
        备线准备中
      </Pill>
    );
  if (!rule.backupOn) return null;

  return (
    <Pill title="断线时自动走备线，地址不变" tone="green">
      备线 开
    </Pill>
  );
}

/** 「待切换 → …」 + 切到新落地 (plan 269). */
export function PendingBlock({
  rule,
  compact = false,
  inField = false,
  onSwap,
}: {
  rule: LeiKwanRule;
  /** wrapping text instead of a pill */
  compact?: boolean;
  /** inside an H5 「待切换」 field: drop the label from the text */
  inField?: boolean;
  onSwap: (r: LeiKwanRule) => void;
}) {
  if (!rule.pendingLanding) return null;

  return (
    <div
      className="mt-1 flex flex-wrap items-center gap-1.5"
      data-leikwan-pending={rule.id}
    >
      {compact ? (
        <span className="w-full text-xs text-amber-700 dark:text-amber-300">
          {inField
            ? pendingText(rule).replace(/^待切换 /, "")
            : pendingText(rule)}
        </span>
      ) : (
        <Pill
          wrap
          title="已准备好的面板落地节点；点「切到新落地」才会换"
          tone="amber"
        >
          {pendingText(rule)}
        </Pill>
      )}
      <Button
        className={inField ? "" : "h-7 rounded-lg px-2.5 text-xs"}
        color="warning"
        size="sm"
        variant="flat"
        onPress={() => onSwap(rule)}
      >
        切到新落地
      </Button>
    </div>
  );
}

export interface LeiKwanRuleActions {
  link: (r: LeiKwanRule) => Promise<void>;
  retry: (r: LeiKwanRule) => Promise<void>;
  openCreate: (lineId?: number) => void;
  openLanding: (r: LeiKwanRule) => void;
  openDelete: (r: LeiKwanRule) => void;
  openSwap: (r: LeiKwanRule) => void;
  /** The dialogs; render once in the page. */
  modals: ReactNode;
}

export function useLeiKwanRuleActions({
  admin,
  reload,
}: {
  admin: boolean;
  reload: () => void;
}): LeiKwanRuleActions {
  const [dialog, setDialog] = useState<{
    mode: "create" | "landing";
    rule?: LeiKwanRule;
    lineId?: number;
  } | null>(null);
  const [del, setDel] = useState<LeiKwanRule | null>(null);
  const [keep, setKeep] = useState(false);
  const [busy, setBusy] = useState(false);
  // Plan 269: 换落地 to the prepared 「待切换」 panel landing node.
  const [swap, setSwap] = useState<LeiKwanRule | null>(null);
  const [swapping, setSwapping] = useState(false);

  const link = useCallback(async (r: LeiKwanRule) => {
    const res = await getLeiKwanRuleLink(r.id);

    if (res.code !== 0) toast.error(res.msg || "复制失败");
    else if (res.data.url) await copyText(res.data.url, "链接");
    else {
      await copyText(res.data.address, "入口地址");
      if (res.data.note) toast(res.data.note);
    }
  }, []);
  const retry = useCallback(
    async (r: LeiKwanRule) => {
      const res = await retryLeiKwanBackup(r.id);

      if (res.code !== 0) toast.error(res.msg || "重试失败");
      else toast.success("备线已建好");
      reload();
    },
    [reload],
  );
  const remove = async () => {
    if (!del) return;
    setBusy(true);
    const res = await deleteLeiKwanRule(del.id, keep);

    setBusy(false);
    if (res.code !== 0) {
      toast.error(res.msg || "删除失败");

      return;
    }
    toast.success(keep ? "已解除登记" : "已删除（利群转发和备线一起删了）");
    if (res.data?.backupNote) toast(res.data.backupNote);
    setDel(null);
    reload();
  };
  const doSwap = async () => {
    const p = swap?.pendingLanding;

    if (!swap || !p) return;
    setSwapping(true);
    const res = await updateLeiKwanRule({
      id: swap.id,
      changeTarget: true,
      landingNodeId: p.landingNodeId,
    });

    setSwapping(false);
    if (res.code !== 0) {
      toast.error(res.msg || "换落地失败");

      return;
    }
    toast.success(`「${swap.name}」已换落地：点「复制链接」拿新链接更新客户端`);
    (res.data?.warnings || []).forEach((w) => toast.error(w));
    setSwap(null);
    reload();
  };

  const modals = (
    <>
      <LeiKwanRuleDialog
        admin={admin}
        lineId={dialog?.lineId}
        mode={dialog?.mode || "create"}
        open={!!dialog}
        rule={dialog?.rule}
        onClose={() => setDialog(null)}
        onSaved={reload}
      />
      <Modal
        classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-md rounded-2xl" }}
        isOpen={!!del}
        placement="center"
        size="md"
        onClose={() => setDel(null)}
      >
        <ModalContent>
          <ModalHeader>删除利群规则</ModalHeader>
          <ModalBody className="space-y-2 text-sm">
            <p>
              删除「{del?.name}」：利群那边的转发一起删掉，释放 1
              个利群名额；面板自动建的备线也一起删。
            </p>
            {admin && del?.source === "import" && (
              <LabeledSwitch isSelected={keep} onValueChange={setKeep}>
                只解除登记（利群转发和链接的备线都保留）
              </LabeledSwitch>
            )}
          </ModalBody>
          <ModalFooter>
            <Button color="default" variant="flat" onPress={() => setDel(null)}>
              取消
            </Button>
            <Button
              color="danger"
              isLoading={busy}
              variant="flat"
              onPress={() => void remove()}
            >
              {keep ? "解除登记" : "删除"}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      <Modal
        classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-md rounded-2xl" }}
        isOpen={!!swap}
        placement="center"
        size="md"
        onClose={() => setSwap(null)}
      >
        <ModalContent>
          <ModalHeader>换落地 · {swap?.name}</ModalHeader>
          <ModalBody className="space-y-3 text-sm">
            {swap && swap.pendingLanding && (
              <div className="space-y-1.5 rounded-xl bg-default-100/60 p-3 dark:bg-default-50/40">
                <div className="flex items-start gap-2">
                  <span className="w-12 shrink-0 text-default-500">现在</span>
                  <span className="min-w-0 break-all">{landingText(swap)}</span>
                </div>
                <div className="flex items-start gap-2">
                  <span className="w-12 shrink-0 text-default-500">换成</span>
                  <span className="min-w-0 break-all">
                    <b>
                      {swap.pendingLanding.serverName || "落地"} ·{" "}
                      {swap.pendingLanding.name ||
                        `#${swap.pendingLanding.landingNodeId}`}
                    </b>
                    <span className="text-xs text-default-500">
                      {" "}
                      {swap.pendingLanding.host || "—"}:
                      {swap.pendingLanding.port ?? "—"}
                      {swap.pendingLanding.protocol
                        ? ` · ${protocolLabel(swap.pendingLanding.protocol)}`
                        : ""}
                    </span>
                  </span>
                </div>
              </div>
            )}
            <p className="rounded-lg bg-amber-500/15 px-3 py-2 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
              换落地后旧链接（旧密钥）立刻失效；备线和首包过滤密钥一起换；换完点「复制链接」拿新链接更新客户端。
            </p>
            <p className="text-xs text-default-500">
              入口地址不变；利群那边的目标和备线目标一起改到新落地。
            </p>
          </ModalBody>
          <ModalFooter>
            <Button color="danger" variant="flat" onPress={() => setSwap(null)}>
              取消
            </Button>
            <Button
              color="warning"
              isLoading={swapping}
              variant="flat"
              onPress={() => void doSwap()}
            >
              换落地
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  );

  return {
    link,
    retry,
    openCreate: (lineId?: number) => setDialog({ mode: "create", lineId }),
    openLanding: (r: LeiKwanRule) => setDialog({ mode: "landing", rule: r }),
    openDelete: (r: LeiKwanRule) => {
      setKeep(false);
      setDel(r);
    },
    openSwap: setSwap,
    modals,
  };
}

/** 复制链接 (cyan) · 换落地 (blue) · [重试备线] · 删除 (red). */
export function RuleButtons({
  rule,
  actions,
  admin,
  fill = false,
  compact = false,
}: {
  rule: LeiKwanRule;
  actions: LeiKwanRuleActions;
  admin: boolean;
  /** H5: equal-width buttons filling the row */
  fill?: boolean;
  /** desktop rule rows: small 12 px pills (design btn-sm) */
  compact?: boolean;
}) {
  const f = fill
    ? "min-w-0 flex-1"
    : compact
      ? "h-7 min-w-0 rounded-lg px-2.5 text-xs"
      : "";

  return (
    <>
      <Button
        className={`${cyanButton} ${f}`}
        size="sm"
        variant="flat"
        onPress={() => void actions.link(rule)}
      >
        复制链接
      </Button>
      <Button
        className={f}
        color="primary"
        size="sm"
        variant="flat"
        onPress={() => actions.openLanding(rule)}
      >
        换落地
      </Button>
      {admin && rule.backupError && (
        <Button
          className={f}
          color="warning"
          size="sm"
          variant="flat"
          onPress={() => void actions.retry(rule)}
        >
          重试备线
        </Button>
      )}
      <Button
        className={f}
        color="danger"
        size="sm"
        variant="flat"
        onPress={() => actions.openDelete(rule)}
      >
        删除
      </Button>
    </>
  );
}

function LeiKwanRuleDialog({
  open,
  mode,
  rule,
  lineId: presetLine,
  admin,
  onClose,
  onSaved,
}: {
  open: boolean;
  mode: "create" | "landing";
  rule?: LeiKwanRule;
  /** 「在这条专线建规则」: preselect a line */
  lineId?: number;
  admin: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [opts, setOpts] = useState<LeiKwanRuleOptions | null>(null);
  const [name, setName] = useState("");
  const [lineId, setLineId] = useState("");
  const [landing, setLanding] = useState("");
  const [host, setHost] = useState("");
  const [port, setPort] = useState("");
  const [requestId, setRequestId] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setOpts(null);
    setName(rule?.name || "");
    setLineId(
      rule ? String(rule.lineId) : presetLine ? String(presetLine) : "",
    );
    setLanding(
      rule?.landing.kind === "node"
        ? String(rule.landing.landingNodeId)
        : rule
          ? "manual"
          : "",
    );
    setHost(rule?.landing.kind === "manual" ? rule.landing.host : "");
    setPort(rule?.landing.kind === "manual" ? String(rule.landing.port) : "");
    setRequestId(newLeiKwanRequestId());
    void getLeiKwanRuleOptions(rule?.userId || 0).then((res) => {
      if (res.code === 0) {
        setOpts(res.data);
        if (!rule && res.data.lines.length)
          setLineId((v) => v || String(res.data.lines[0].id));
      }
    });
  }, [open, rule, presetLine]);

  const full =
    !!opts && opts.quota.limit > 0 && opts.quota.active >= opts.quota.limit;
  const save = async () => {
    const manual = landing === "manual";
    const target = manual
      ? { targetHost: host.trim(), targetPort: parseInt(port, 10) || 0 }
      : { landingNodeId: parseInt(landing, 10) || 0 };

    if (
      mode === "create" &&
      (!name.trim() || !lineId || (!manual && !target.landingNodeId))
    ) {
      toast.error("请填写规则名称、选择专线和落地");

      return;
    }
    setSaving(true);
    const res =
      mode === "create"
        ? await createLeiKwanRule({
            lineId: parseInt(lineId, 10),
            name: name.trim(),
            requestId,
            ...target,
          })
        : await updateLeiKwanRule({
            id: rule!.id,
            name: name.trim(),
            changeTarget: true,
            ...target,
          });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success(mode === "create" ? "利群规则已建好" : "已换落地");
    (res.data?.warnings || []).forEach((w) => toast.error(w));
    onSaved();
    onClose();
  };

  return (
    <Modal
      classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-lg rounded-2xl" }}
      isOpen={open}
      placement="center"
      scrollBehavior="inside"
      size="lg"
      onClose={onClose}
    >
      <ModalContent>
        <ModalHeader>
          {mode === "create" ? "新增利群规则" : `换落地 · ${rule?.name}`}
        </ModalHeader>
        <ModalBody className="space-y-4">
          {opts?.grantError && mode === "create" && (
            <p className="rounded-lg bg-amber-500/15 px-3 py-2 text-sm text-amber-700 dark:text-amber-300">
              {opts.grantError}
            </p>
          )}
          <Input
            label="规则名称"
            placeholder="我的广港"
            value={name}
            variant="bordered"
            onChange={(e) => setName(e.target.value)}
          />
          {mode === "create" && (
            <Select
              label="选择专线"
              placeholder="选择利群专线"
              selectedKeys={lineId ? [lineId] : []}
              variant="bordered"
              onSelectionChange={(keys) =>
                setLineId(String(Array.from(keys)[0] || ""))
              }
            >
              {(opts?.lines || []).map((l) => (
                <SelectItem
                  key={String(l.id)}
                  description={`利群专线 · ${l.routeLabel}`}
                >
                  {l.name}
                </SelectItem>
              ))}
            </Select>
          )}
          <Select
            label="落地"
            placeholder="选择我的落地节点"
            selectedKeys={landing ? [landing] : []}
            variant="bordered"
            onSelectionChange={(keys) =>
              setLanding(String(Array.from(keys)[0] || ""))
            }
          >
            {[
              ...(opts?.landings || []).map((n) => (
                <SelectItem
                  key={String(n.id)}
                  description={`${protocolLabel(n.protocol)} · 自己的密钥`}
                >
                  {`${n.serverName} · ${n.name}`}
                </SelectItem>
              )),
              ...(admin
                ? [
                    <SelectItem key="manual" description="仅管理员">
                      手动填写 地址:端口
                    </SelectItem>,
                  ]
                : []),
            ]}
          </Select>
          {landing === "manual" && admin && (
            <div className="grid grid-cols-3 gap-2">
              <Input
                className="col-span-2"
                label="落地地址"
                size="sm"
                value={host}
                variant="bordered"
                onChange={(e) => setHost(e.target.value)}
              />
              <Input
                inputMode="numeric"
                label="端口"
                size="sm"
                value={port}
                variant="bordered"
                onChange={(e) => setPort(e.target.value.replace(/\D/g, ""))}
              />
            </div>
          )}
          {opts && opts.landings.length === 0 && (
            <p className="-mt-2 text-xs text-default-500">
              还没有自己的落地节点：先到「落地」页新建一个 SS2022
              节点（自己的密钥，别的规则用不了）。
            </p>
          )}
          {mode === "create" && (
            <div className="space-y-1.5 rounded-xl bg-default-100/60 p-3 text-sm dark:bg-default-50/40">
              <div className="flex items-start gap-2">
                <span className="w-16 shrink-0 text-default-500">入口</span>
                {opts?.grant ? (
                  <span>
                    主 <b>{opts.grant.mainEntryName}</b>
                    <span className="text-xs text-default-500">
                      （授权时指定）
                    </span>
                  </span>
                ) : (
                  <span className="text-default-400">—</span>
                )}
              </div>
              <div className="flex items-start gap-2">
                <span className="w-16 shrink-0 text-default-500">入口端口</span>
                <span>由利群分配</span>
              </div>
              <div className="flex items-start gap-2">
                <span className="w-16 shrink-0 text-default-500">备线</span>
                <span>
                  {opts?.grant?.backupLine
                    ? "自动建一条（同一落地，断线时自动切）"
                    : "授权没开备线"}
                </span>
              </div>
              {admin && (
                <div className="flex flex-wrap items-center gap-2 pt-1">
                  <Pill tone={full ? "amber" : "purple"}>
                    将占用 1 个利群名额
                  </Pill>
                  <QuotaPill quota={opts?.quota} />
                </div>
              )}
              {full && (
                <p className="text-xs text-amber-700 dark:text-amber-300">
                  {admin
                    ? "名额已满，利群名额提升前建不了新规则。"
                    : "利群名额暂时满了，请联系管理员。"}
                </p>
              )}
            </div>
          )}
          {mode === "landing" && (
            <p className="text-xs text-default-500">
              换落地会改利群那边的目标；面板自动建的备线跟着换到新落地。入口地址不变。
            </p>
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="success"
            isDisabled={mode === "create" && (full || !!opts?.grantError)}
            isLoading={saving}
            variant="flat"
            onPress={() => void save()}
          >
            {mode === "create" ? "建规则" : "保存"}
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
