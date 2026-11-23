import type {
  LeiKwanHostFileDiff,
  LeiKwanHostList,
  LeiKwanHostManage,
  LeiKwanHostManageReply,
  LeiKwanHostOverride,
} from "@/api/leikwan-line";
import type { AttentionLevel } from "@/pages/landing/admin-attention";
import type { ReactNode } from "react";

import { useState } from "react";
import { RefreshCw } from "lucide-react";
import toast from "react-hot-toast";

import {
  forceLeiKwanHost,
  learnLeiKwanHost,
  setLeiKwanHostManage,
  setLeiKwanHostTraffic,
  syncLeiKwanHost,
} from "@/api/leikwan-line";
import { AttentionIcon } from "@/pages/landing/admin-attention";
import { Pill } from "@/pages/landing/pill";
import { KV, LabeledSwitch, OnOff } from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";

// Plan 269: the 「面板接管」 block under each 利群入口 host card. The entry agent
// reports the live host state and the managed files; the panel learns the
// host's current values (采用主机现值, semantic diff 0), then 接管 lets the
// agent write the panel's content. Drift / write errors are admin actions:
// they get the plan 218 ⚠ (card name + the 「利群入口」 tab).

export type LeiKwanHostItem = LeiKwanHostList["hosts"][number];

const NIGHT_WINDOW = "台湾 02:00–06:00（避开 03:45–04:20）";
// Diff files whose write restarts 日志快切 or reloads nft (a few seconds).
const RESTART_LABEL = "写入时会重启日志快切 / 重新加载 nft（几秒）";
const STALE_MS = 15 * 60 * 1000;

export const twTime = (ms?: number) =>
  ms
    ? `${new Date(ms).toLocaleString("zh-CN", {
        timeZone: "Asia/Taipei",
        hour12: false,
      })} 台湾`
    : "—";

/** ⚠ level of one host: red when offline or something failed, amber
 * otherwise. Plan 295: the backend's attentionLevel decides (one helper). */
export function hostAttentionLevel(
  m?: LeiKwanHostManage | null,
): AttentionLevel | null {
  const reasons = m?.attention || [];

  if (!reasons.length) return null;
  if (m?.attentionLevel) return m.attentionLevel;

  return reasons.some((r) => r.includes("失败") || r === "节点离线")
    ? "error"
    : "warn";
}

/** Plan 295: 「节点离线」 gets its since-when (台湾). */
const reasonText = (r: string, m?: LeiKwanHostManage | null) =>
  r === "节点离线" && m?.offlineSince
    ? `节点离线（自 ${twTime(m.offlineSince)}）`
    : r;

/** ⚠ level of the whole 「利群入口」 tab (null = nothing to do). */
export function hostsAttentionLevel(
  hosts?: LeiKwanHostList | null,
): AttentionLevel | null {
  if (!hosts || !(hosts.attention || 0)) return null;
  const levels = hosts.hosts.map((h) => hostAttentionLevel(h.manage));

  return levels.includes("error") ? "error" : "warn";
}

const attentionText = (reasons: string[], m?: LeiKwanHostManage | null) =>
  `需要处理：\n${reasons.map((r) => `• ${reasonText(r, m)}`).join("\n")}`;

export function HostNameMarker({ manage }: { manage?: LeiKwanHostManage }) {
  const level = hostAttentionLevel(manage);

  if (!level) return null;
  const text = attentionText(manage?.attention || [], manage);

  return (
    <span
      aria-label={text}
      className="inline-flex shrink-0"
      data-attention-name={level}
      role="img"
      title={text}
    >
      <AttentionIcon level={level} />
    </span>
  );
}

const nativeUdpText = (v?: string) =>
  v === "all" ? "全部" : v === "some" ? "部分" : v === "none" ? "无" : v || "—";

const lineClass = (l: string) =>
  l.startsWith("- ")
    ? "bg-red-500/10 text-red-700 dark:bg-red-400/10 dark:text-red-300"
    : l.startsWith("+ ")
      ? "bg-green-500/10 text-green-700 dark:bg-green-400/10 dark:text-green-300"
      : l.startsWith("~ ")
        ? "bg-amber-500/10 text-amber-700 dark:bg-amber-400/10 dark:text-amber-300"
        : "text-default-500";

/** Per-file semantic diff (host now vs panel). */
const HIDDEN_FILES = ["ssfilter", "sskeys"];

export function HostDiffFiles({
  diffs,
  onlyChanged = false,
  nightOnly = false,
}: {
  diffs: LeiKwanHostFileDiff[];
  onlyChanged?: boolean;
  /** global 利群 setting: restarts / nft only in the night window */
  nightOnly?: boolean;
}) {
  // Plan 301: the first-packet filter files (ssfilter / sskeys) stay managed
  // but are never listed (their drift / errors still raise the ⚠).
  const shown = diffs.filter((d) => !HIDDEN_FILES.includes(d.file));
  const list = onlyChanged ? shown.filter((d) => !d.same) : shown;

  if (!list.length)
    return <p className="text-xs text-default-400">面板没有管这台的文件</p>;

  return (
    <div className="space-y-2">
      {list.map((d) => (
        <div
          key={d.file}
          className="rounded-lg border border-divider p-2"
          data-leikwan-diff-file={d.file}
        >
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-sm font-medium">{d.name || d.file}</span>
            {d.same ? (
              <Pill tone="green">和主机一致</Pill>
            ) : (
              <Pill tone="amber">有差异</Pill>
            )}
            {!d.managed && <Pill tone="slate">面板不管</Pill>}
            {d.restart && (
              <Pill
                title={
                  nightOnly
                    ? `要重启日志快切或重新加载 nft：agent 只在${NIGHT_WINDOW}执行`
                    : "这个文件写入时日志快切会重启或 nft 重新加载，影响几秒"
                }
                tone="amber"
              >
                {nightOnly ? "需夜间窗口" : RESTART_LABEL}
              </Pill>
            )}
          </div>
          {d.path && (
            <div className="mt-0.5 break-all font-mono text-[11px] text-default-400">
              {d.path}
            </div>
          )}
          {!d.same && (d.lines || []).length > 0 && (
            <div className="mt-1.5 max-h-56 overflow-auto rounded bg-default-100/70 p-1.5 font-mono text-[11px] leading-5 dark:bg-default-50/40">
              {(d.lines || []).map((l, i) => (
                <div
                  key={`${i}-${l}`}
                  className={`whitespace-pre-wrap break-all rounded px-1 ${lineClass(l)}`}
                >
                  {l}
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

const overrideText = (o: LeiKwanHostOverride) => {
  const parts: string[] = [];

  if (o.warmMain !== undefined || o.warmBackup !== undefined)
    parts.push(`预热 ${o.warmMain ?? "—"}/${o.warmBackup ?? "—"}`);
  // Plan 301: 首包过滤 is never shown (the value stays on the host).
  if (o.logswitch !== undefined)
    parts.push(`日志快切 ${o.logswitch ? "开" : "关"}`);
  if (o.backup) parts.push(`备线 ${o.backup}`);
  if (o.returnCheck !== undefined)
    parts.push(`切回复查 ${o.returnCheck ? "开" : "关"}`);

  return parts.join(" · ") || "—";
};

function DiffLegend() {
  return (
    <p className="flex flex-wrap items-center gap-1.5 text-xs text-default-500">
      <span className={`rounded px-1 font-mono ${lineClass("- ")}`}>-</span>
      主机现在的值
      <span className={`rounded px-1 font-mono ${lineClass("+ ")}`}>+</span>
      面板要写的值
      <span className={`rounded px-1 font-mono ${lineClass("~ ")}`}>~</span>
      密钥（只比指纹）
    </p>
  );
}

type Dialog =
  | "learn"
  | "diff"
  | "manageOn"
  | "manageOff"
  | "force"
  | "trafficOn"
  | "trafficOff"
  | null;

export function HostManageBlock({
  host,
  nightOnly = false,
  onView,
}: {
  host: LeiKwanHostItem;
  /** global 利群 setting nightOnly (default off) */
  nightOnly?: boolean;
  onView: (nodeId: number, view: LeiKwanHostManage) => void;
}) {
  const [dialog, setDialog] = useState<Dialog>(null);
  const [busy, setBusy] = useState("");
  const m: LeiKwanHostManage = host.manage || {
    manage: false,
    traffic: false,
    learned: false,
    reported: false,
  };
  const id = host.node.id;
  const name = host.node.name;
  const st = m.state;
  const agent = m.agent;
  const diffs = m.diff || [];
  const diffCount = m.diffCount ?? diffs.filter((d) => !d.same).length;
  const drift = agent?.drift || [];
  const attention = m.attention || [];
  const level = hostAttentionLevel(m);
  const stale = !!m.reportedAt && Date.now() - m.reportedAt > STALE_MS;
  const forwards = st?.forwards || [];
  const running = forwards.filter((f) => f.running);
  const writtenAt = Math.max(0, ...Object.values(agent?.written || {}));
  const overrides = Object.entries(m.overrides || {});
  const blocked = m.trafficState?.blocked || [];

  const run = async (
    key: string,
    call: () => Promise<{
      code: number;
      msg: string;
      data: LeiKwanHostManageReply;
    }>,
    ok: (d: LeiKwanHostManageReply) => string,
  ) => {
    setBusy(key);
    const res = await call();

    setBusy("");
    if (res.code !== 0 || !res.data) {
      toast.error(res.msg || "操作失败");

      return;
    }
    const { warning } = res.data;
    const view: LeiKwanHostManageReply = { ...res.data };

    delete view.warning;
    delete view.learnedDiff;
    onView(id, view);
    setDialog(null);
    if (warning)
      toast(warning, {
        duration: 6000,
        icon: <AttentionIcon level="warn" />,
      });
    else toast.success(ok(res.data));
  };

  return (
    <div
      className="mt-3 space-y-2 rounded-lg bg-default-100/60 p-2.5 dark:bg-default-50/40"
      data-leikwan-manage={id}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="flex items-center gap-1.5 text-sm font-semibold">
          面板接管
          {level && <AttentionIcon level={level} />}
        </span>
        {m.reported ? (
          <span className="text-xs text-default-400">
            agent 上报 {twTime(m.reportedAt)}
          </span>
        ) : (
          <Pill tone="amber">agent 还没上报（需要新版 agent）</Pill>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        <OnOff label="接管" on={m.manage} />
        <Pill
          title={m.learnedAt ? `采用于 ${twTime(m.learnedAt)}` : undefined}
          tone={m.learned ? "teal" : "slate"}
        >
          {m.learned ? "已采用主机现值" : "未采用主机现值"}
        </Pill>
        {m.learned && m.diff && (
          <Pill tone={diffCount === 0 ? "green" : "amber"}>
            差异 {diffCount} 项
          </Pill>
        )}
        {m.applying && (
          <Pill title="面板的值已下发，等 agent 写入后上报" tone="amber">
            等 agent 写入
          </Pill>
        )}
        <OnOff label="流量统计" on={m.traffic} />
        {m.traffic && m.trafficState?.pending && (
          <Pill
            title={
              nightOnly
                ? `计数表只在${NIGHT_WINDOW}创建`
                : "计数表还没建好，agent 下一轮检查时建"
            }
            tone="amber"
          >
            {nightOnly ? "等夜间窗口" : "等待执行"}
          </Pill>
        )}
        {m.traffic && m.trafficState?.counting && (
          <Pill tone="green">计数中</Pill>
        )}
        {blocked.length > 0 && (
          <Pill title={`已屏蔽端口：${blocked.join("、")}`} tone="red">
            已屏蔽 {blocked.length} 个端口
          </Pill>
        )}
        {stale && (
          <Pill title="采用主机现值要 15 分钟内的上报" tone="amber">
            上报超过 15 分钟
          </Pill>
        )}
      </div>

      {st && (
        <div className="divide-y divide-divider/60">
          <KV label="agent 实时">
            <Pill tone={st.agentRunning ? "green" : "red"}>
              厂商 agent {st.agentRunning ? "运行中" : "没在跑"}
            </Pill>
            <OnOff label="更新锁" on={st.updateLock} />
            <Pill tone={st.autoUpdateOff ? "green" : "amber"}>
              自动更新 {st.autoUpdateOff ? "关" : "开"}
            </Pill>
            {st.coreVersion && <Pill tone="slate">核心 {st.coreVersion}</Pill>}
            <Pill
              tone={
                st.nativeUdp === "all"
                  ? "green"
                  : st.nativeUdp === "some"
                    ? "amber"
                    : "slate"
              }
            >
              原生 UDP {nativeUdpText(st.nativeUdp)}
            </Pill>
          </KV>
          {forwards.length > 0 && (
            <KV label="利群转发">
              <Pill
                title={forwards
                  .map(
                    (f) =>
                      `#${f.forwardId} :${f.listenPort} ${f.running ? "在跑" : "没在跑"}${f.state ? ` · ${f.state}` : ""}`,
                  )
                  .join("\n")}
                tone={running.length === forwards.length ? "green" : "amber"}
              >
                {running.length} / {forwards.length} 在跑
              </Pill>
              {forwards
                .filter((f) => !f.running)
                .map((f) => (
                  <Pill key={f.forwardId} tone="red">
                    #{f.forwardId} :{f.listenPort} 没在跑
                  </Pill>
                ))}
            </KV>
          )}
          {(writtenAt > 0 || agent?.backup) && (
            <KV label="上次写入">
              <span className="text-xs text-default-500">
                {writtenAt > 0 ? twTime(writtenAt) : "—"}
                {agent?.backup ? ` · 备份 ${agent.backup}` : ""}
              </span>
            </KV>
          )}
        </div>
      )}

      {attention.length > 0 && (
        <div
          className="rounded-lg bg-red-500/10 px-2.5 py-2 text-xs text-red-700 dark:bg-red-400/15 dark:text-red-300"
          data-leikwan-attention={id}
        >
          <p className="flex items-center gap-1.5 font-semibold">
            <AttentionIcon level={level || "warn"} />
            需要处理 {attention.length} 项
          </p>
          <ul className="mt-1 list-disc space-y-0.5 pl-5">
            {attention.map((a) => (
              <li key={a}>{reasonText(a, m)}</li>
            ))}
          </ul>
          {drift.length > 0 && (
            <p className="mt-1 text-default-600 dark:text-default-500">
              漂移 =
              有人在主机上手改了接管的文件；面板不会自动覆盖：看过差异后「按面板覆盖」，或「采用主机现值」把主机的值记进面板。
            </p>
          )}
        </div>
      )}
      {(m.pending || []).length > 0 && (
        <div className="rounded-lg bg-amber-500/15 px-2.5 py-2 text-xs text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
          <span className="font-semibold">
            {nightOnly ? "等 02:00–06:00 窗口：" : "等待执行："}
          </span>
          {(m.pending || []).join("、")}
          <span className="text-default-500">
            {" "}
            {nightOnly
              ? `（要重启日志快切或重新加载 nft，agent 只在${NIGHT_WINDOW}执行）`
              : "（agent 下一轮检查时执行）"}
          </span>
        </div>
      )}
      {m.trafficState?.error && (
        <p className="text-xs text-red-700 dark:text-red-300">
          流量统计：{m.trafficState.error}
        </p>
      )}

      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 pt-1">
        <LabeledSwitch
          isDisabled={(!m.learned && !m.manage) || busy === "manage"}
          isSelected={m.manage}
          onValueChange={(v) => setDialog(v ? "manageOn" : "manageOff")}
        >
          面板接管
        </LabeledSwitch>
        <LabeledSwitch
          isDisabled={busy === "traffic"}
          isSelected={m.traffic}
          onValueChange={(v) => setDialog(v ? "trafficOn" : "trafficOff")}
        >
          流量统计
        </LabeledSwitch>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          color="secondary"
          isDisabled={!m.reported}
          isLoading={busy === "learn"}
          size="sm"
          variant="flat"
          onPress={() => setDialog("learn")}
        >
          采用主机现值
        </Button>
        <Button
          color="primary"
          isDisabled={!m.learned || !m.diff}
          size="sm"
          variant="flat"
          onPress={() => setDialog("diff")}
        >
          查看差异
        </Button>
        {m.manage && drift.length > 0 && (
          <Button
            color="warning"
            isLoading={busy === "force"}
            size="sm"
            variant="flat"
            onPress={() => setDialog("force")}
          >
            按面板覆盖
          </Button>
        )}
        <Button
          color="success"
          isLoading={busy === "sync"}
          size="sm"
          startContent={<RefreshCw size={14} />}
          variant="flat"
          onPress={() =>
            void run(
              "sync",
              () => syncLeiKwanHost(id),
              () => `已同步 ${name}`,
            )
          }
        >
          立即同步
        </Button>
      </div>
      {!m.learned && m.reported && (
        <p className="text-xs text-default-500">
          先「采用主机现值」：面板记下这台现在的值（语义差异
          0），再打开「面板接管」。
        </p>
      )}

      {/* 采用主机现值 */}
      <Confirm
        busy={busy === "learn"}
        confirm="采用主机现值"
        confirmColor="secondary"
        open={dialog === "learn"}
        title={`采用主机现值 · ${name}`}
        onClose={() => setDialog(null)}
        onConfirm={() =>
          void run(
            "learn",
            () => learnLeiKwanHost(id),
            (d) =>
              `已采用 ${name} 的现值 · 语义差异 ${
                (d.learnedDiff || d.diff || []).filter((x) => !x.same).length
              } 项`,
          )
        }
      >
        <p>把这台主机现在的值记进面板，接管后不会改主机。</p>
        <p className="text-xs text-default-500">
          只记和专线默认不同的项（这台自己的预热、日志快切线路、首包合并等）；主机上的文件这一步一个都不动。
        </p>
      </Confirm>

      {/* 查看差异 */}
      <Modal
        classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-2xl rounded-2xl" }}
        isOpen={dialog === "diff"}
        placement="center"
        scrollBehavior="inside"
        size="2xl"
        onClose={() => setDialog(null)}
      >
        <ModalContent>
          <ModalHeader className="flex flex-wrap items-center gap-2">
            差异 · {name}
            <Pill tone={diffCount === 0 ? "green" : "amber"}>
              {diffCount === 0 ? "语义差异 0" : `差异 ${diffCount} 项`}
            </Pill>
          </ModalHeader>
          <ModalBody className="space-y-3 text-sm">
            <p className="text-xs text-default-500">
              面板要写的值和主机现在的值逐个文件比（按语义，不比空格和顺序）。
              {m.manage
                ? "已接管：有差异的文件 agent 会写成面板的值。"
                : "还没接管：主机不会被改。"}
            </p>
            {diffCount > 0 && <DiffLegend />}
            <HostDiffFiles diffs={diffs} nightOnly={nightOnly} />
            {overrides.length > 0 && (
              <div className="space-y-1">
                <div className="text-xs font-medium">
                  这台自己的值（和专线默认不同，采用主机现值时记下）
                </div>
                <ul className="space-y-0.5 text-xs text-default-600">
                  {overrides.map(([fid, o]) => (
                    <li key={fid}>
                      <span className="font-mono">#{fid}</span>{" "}
                      {overrideText(o)}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setDialog(null)}
            >
              关闭
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* 接管 on: show the diff first */}
      <Confirm
        wide
        busy={busy === "manage"}
        confirm={diffCount > 0 ? `接管并写入 ${diffCount} 项` : "确认接管"}
        confirmColor={diffCount > 0 ? "warning" : "success"}
        open={dialog === "manageOn"}
        title={`面板接管 · ${name}`}
        onClose={() => setDialog(null)}
        onConfirm={() =>
          void run(
            "manage",
            () => setLeiKwanHostManage(id, true),
            () => `已接管 ${name}`,
          )
        }
      >
        {diffCount > 0 ? (
          <>
            <p className="rounded-lg bg-amber-500/15 px-3 py-2 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
              接管后会把下面 {diffCount} 个文件写成面板的值（先备份原文件）。
              {diffs.some((d) => !d.same && d.restart) &&
                (nightOnly
                  ? `标「需夜间窗口」的要重启日志快切或重新加载 nft，agent 只在${NIGHT_WINDOW}执行。`
                  : `标「${RESTART_LABEL}」的文件，写入时会有几秒影响。`)}
            </p>
            <DiffLegend />
            <HostDiffFiles onlyChanged diffs={diffs} nightOnly={nightOnly} />
          </>
        ) : (
          <p className="rounded-lg bg-green-500/15 px-3 py-2 text-green-700 dark:bg-green-400/15 dark:text-green-300">
            语义差异 0，接管后不会写任何文件。
          </p>
        )}
        <p className="text-xs text-default-500">
          接管后：在面板改专线 /
          转发的加速设置会自动写到这台主机；有人手改主机上的文件会标 ⚠
          漂移，面板不会自动覆盖。
        </p>
      </Confirm>

      {/* 接管 off */}
      <Confirm
        busy={busy === "manage"}
        confirm="关掉接管"
        confirmColor="warning"
        open={dialog === "manageOff"}
        title={`关掉接管 · ${name}`}
        onClose={() => setDialog(null)}
        onConfirm={() =>
          void run(
            "manage",
            () => setLeiKwanHostManage(id, false),
            () => `已关掉 ${name} 的接管`,
          )
        }
      >
        <p>
          关掉后 agent
          只读这台主机、照常上报，不再写任何文件；主机保持现在的样子。
        </p>
      </Confirm>

      {/* 按面板覆盖 */}
      <Confirm
        wide
        busy={busy === "force"}
        confirm="按面板覆盖"
        confirmColor="warning"
        open={dialog === "force"}
        title={`按面板覆盖 · ${name}`}
        onClose={() => setDialog(null)}
        onConfirm={() =>
          void run(
            "force",
            () => forceLeiKwanHost(id),
            () => `已下发：${name} 被手改的文件改回面板的值`,
          )
        }
      >
        <p>
          被手改（漂移）的{" "}
          <b>
            {drift
              .map((f) => diffs.find((d) => d.file === f)?.name || f)
              .join("、")}
          </b>{" "}
          改回面板的值，只写这一次；原文件先备份。
        </p>
        <p className="text-xs text-default-500">
          {nightOnly
            ? `要重启日志快切或重新加载 nft 的文件只在${NIGHT_WINDOW}执行。`
            : "要重启日志快切或重新加载 nft 的文件，写入时会有几秒影响。"}
          不想覆盖就点「采用主机现值」，把主机的值记进面板。
        </p>
        <HostDiffFiles
          onlyChanged
          diffs={diffs.filter((d) => drift.includes(String(d.file)))}
          nightOnly={nightOnly}
        />
      </Confirm>

      {/* 流量统计 */}
      <Confirm
        busy={busy === "traffic"}
        confirm="开启流量统计"
        confirmColor="success"
        open={dialog === "trafficOn"}
        title={`利群流量统计 · ${name}`}
        onClose={() => setDialog(null)}
        onConfirm={() =>
          void run(
            "traffic",
            () => setLeiKwanHostTraffic(id, true),
            () => `已开启 ${name} 的利群流量统计`,
          )
        }
      >
        <ul className="list-disc space-y-1 pl-5">
          <li>只统计利群转发的端口（这台入口机上的利群端口）。</li>
          <li>
            统计到的流量按专线的计费方式和倍率，计进转发所属用户的总流量（和隧道一样）。
          </li>
          <li>
            {nightOnly
              ? `计数表只在${NIGHT_WINDOW}才建；建好前显示「等夜间窗口」，这段时间不计数。`
              : "计数表马上建好（agent 收到同步就建），建好后开始计数。"}
          </li>
        </ul>
      </Confirm>
      <Confirm
        busy={busy === "traffic"}
        confirm="关闭流量统计"
        confirmColor="warning"
        open={dialog === "trafficOff"}
        title={`关闭利群流量统计 · ${name}`}
        onClose={() => setDialog(null)}
        onConfirm={() =>
          void run(
            "traffic",
            () => setLeiKwanHostTraffic(id, false),
            () => `已关闭 ${name} 的利群流量统计`,
          )
        }
      >
        <p>关闭后这台不再计利群流量，已经计进用户的流量保留。</p>
      </Confirm>
    </div>
  );
}

function Confirm({
  open,
  title,
  children,
  confirm,
  confirmColor,
  busy,
  wide = false,
  onClose,
  onConfirm,
}: {
  open: boolean;
  title: string;
  children: ReactNode;
  confirm: string;
  confirmColor: "success" | "warning" | "secondary";
  busy: boolean;
  wide?: boolean;
  onClose: () => void;
  onConfirm: () => void;
}) {
  return (
    <Modal
      classNames={{
        base: `!w-[calc(100%-24px)] ${wide ? "sm:max-w-2xl" : "sm:max-w-md"} rounded-2xl`,
      }}
      isOpen={open}
      placement="center"
      scrollBehavior="inside"
      size={wide ? "2xl" : "md"}
      onClose={onClose}
    >
      <ModalContent>
        <ModalHeader>{title}</ModalHeader>
        <ModalBody className="space-y-3 text-sm">{children}</ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color={confirmColor}
            isLoading={busy}
            variant="flat"
            onPress={onConfirm}
          >
            {confirm}
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
