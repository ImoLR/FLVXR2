import type { LeiKwanRule, LeiKwanUsageView } from "@/api/leikwan-line";
import type { ReactNode } from "react";
import type {
  EntryOption,
  EntryRuleView,
} from "@/pages/leikwan/panel/entry-model";
import type { AppInfo, PhoneLog, PhoneState } from "@/utils/app-bridge";

import { useState } from "react";
import { Check, ChevronRight, Smartphone } from "lucide-react";
import toast from "react-hot-toast";

import { Pill } from "@/pages/landing/pill";
import { hostRunning, hostStatusText } from "@/pages/leikwan/panel/host-drawer";
import { HostNameMarker } from "@/pages/leikwan/panel/host-manage";
import { LkDot } from "@/pages/leikwan/panel/layout-parts";
import { KV, LabeledSwitch, fmtBytes } from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { appCall } from "@/utils/app-bridge";

// Plan 294: the 入口机 pieces of the im.gamer App design — tile tap = select
// (the 专线 · 转发 tree follows the selected entry), a small 「详情」 opens the
// drawer; 手机本地 (App only) is one more tile, first in the list; per-entry
// chips on the forward rows.

/** Short state of the phone's local link (tile + drawer). */
export function phoneSummary(s: PhoneState | null): {
  tone: "green" | "amber" | "red" | "slate";
  text: string;
  dot: "ok" | "wait" | "bad" | "off";
} {
  if (!s) return { tone: "slate", text: "读取中", dot: "off" };
  switch (s.connection) {
    case "connected":
      return { tone: "green", text: `已连接 ${s.ready}/${s.total}`, dot: "ok" };
    case "partial":
      return {
        tone: "amber",
        text: `已连接 ${s.ready}/${s.total}`,
        dot: "wait",
      };
    case "connecting":
      return {
        tone: "amber",
        text: s.total ? `连接中 ${s.ready}/${s.total}` : s.stage || "连接中",
        dot: "wait",
      };
    case "disconnecting":
      return { tone: "slate", text: "正在断开", dot: "wait" };
    case "failed":
      return { tone: "red", text: "连接失败 · 点看原因", dot: "bad" };
  }

  return { tone: "slate", text: "未连接", dot: "off" };
}

function StateDot({ dot }: { dot: "ok" | "wait" | "bad" | "off" }) {
  const cls =
    dot === "ok"
      ? "bg-green-500"
      : dot === "wait"
        ? "bg-amber-500"
        : dot === "bad"
          ? "bg-red-500"
          : "bg-slate-400";

  return <span className={`h-2 w-2 shrink-0 rounded-full ${cls}`} />;
}

const tileBase =
  "relative flex min-w-0 cursor-pointer flex-col gap-1.5 rounded-xl border p-2.5 text-left transition-colors";
const tileIdle =
  "border-divider bg-default-50/60 hover:border-teal-500/40 hover:bg-teal-500/5 dark:bg-default-50/30";
const tileSelected =
  "border-teal-500 bg-teal-500/[0.07] ring-2 ring-teal-500/25 dark:border-teal-400 dark:bg-teal-400/10";
const tileFailed =
  "border-red-500/50 bg-red-500/[0.06] hover:border-red-500/70 dark:bg-red-400/10";

function DetailsButton({
  onPress,
  label = "详情",
}: {
  onPress: () => void;
  label?: string;
}) {
  return (
    <button
      className="inline-flex h-6 shrink-0 items-center gap-0.5 rounded-full bg-blue-500/15 px-2 text-[11px] font-semibold text-blue-700 hover:bg-blue-500/25 dark:bg-blue-400/20 dark:text-blue-300"
      data-entry-details=""
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        onPress();
      }}
    >
      {label}
      <ChevronRight size={12} />
    </button>
  );
}

function SelectedMark() {
  return (
    <span
      aria-label="当前入口"
      className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-teal-600 text-white dark:bg-teal-400 dark:text-teal-950"
      role="img"
    >
      <Check size={11} strokeWidth={3} />
    </span>
  );
}

/** One 入口机 tile (desktop 2-column grid): tap = select, 「详情」 = drawer. */
export function EntryTile({
  option,
  selected,
  phone,
  onSelect,
  onDetails,
  noDetails = false,
}: {
  option: EntryOption;
  selected: boolean;
  phone: PhoneState | null;
  onSelect: () => void;
  onDetails: () => void;
  /** user view: the own 主入口 has no host details */
  noDetails?: boolean;
}) {
  const h = option.front;
  const ps = option.phone ? phoneSummary(phone) : null;
  const failed = ps?.dot === "bad";

  return (
    <div
      aria-pressed={selected}
      className={`${tileBase} ${selected ? tileSelected : failed ? tileFailed : tileIdle}`}
      data-entry-selected={selected ? "" : undefined}
      data-leikwan-host-tile={option.phone ? "phone" : option.key}
      role="button"
      tabIndex={0}
      onClick={() => {
        onSelect();
        // 「连接失败 · 点看原因」: the tap also shows the reason.
        if (failed) onDetails();
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect();
        }
      }}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        {ps ? <StateDot dot={ps.dot} /> : h && <LkDot ok={hostRunning(h)} />}
        <span
          className={`min-w-0 flex-1 text-[13px] font-semibold ${option.phone ? "whitespace-nowrap" : "break-words"}`}
        >
          {option.phone && (
            <Smartphone
              aria-hidden="true"
              className="-mt-0.5 mr-1 inline text-violet-600 dark:text-violet-300"
              size={13}
            />
          )}
          {option.name}
          {h && <HostNameMarker manage={h.manage} />}
        </span>
        {selected && <SelectedMark />}
      </span>
      <span className="flex flex-wrap gap-1">
        {ps ? (
          <>
            <Pill tone={ps.tone}>{ps.text}</Pill>
            {phone?.autoConnectBlocked && phone.connection === "idle" ? (
              <Pill title={phone.autoConnectBlocked} tone="amber">
                没自动连接
              </Pill>
            ) : (
              <Pill title="127.0.0.1（本机的利群客户端）" tone="violet">
                本机
              </Pill>
            )}
          </>
        ) : h ? (
          <>
            <Pill tone={hostRunning(h) ? "green" : "slate"}>
              {hostStatusText(h)}
            </Pill>
            <Pill tone={h.mainEntryUsers > 0 ? "teal" : "slate"}>
              主入口 {h.mainEntryUsers} 人
            </Pill>
          </>
        ) : (
          <>
            <Pill tone="teal">主入口</Pill>
            {option.host && (
              <span className="font-mono text-[11px] text-default-500">
                {option.host}
              </span>
            )}
          </>
        )}
      </span>
      {!noDetails && (
        <span className="flex justify-end">
          <DetailsButton onPress={onDetails} />
        </span>
      )}
    </div>
  );
}

/** H5 「入口：xx」 chip on the 转发 tab → jumps to the 入口机 tab. */
export function EntryJumpChip({
  option,
  phone,
  onPress,
}: {
  option?: EntryOption;
  phone: PhoneState | null;
  onPress: () => void;
}) {
  if (!option) return null;
  const ps = option.phone ? phoneSummary(phone) : null;

  return (
    <button
      className="inline-flex h-7 max-w-full items-center gap-1 rounded-full bg-teal-500/15 px-2.5 text-xs font-semibold text-teal-800 dark:bg-teal-400/20 dark:text-teal-200"
      data-entry-jump=""
      type="button"
      onClick={onPress}
    >
      {ps && <StateDot dot={ps.dot} />}
      <span className="truncate">入口：{option.name}</span>
      <ChevronRight className="shrink-0" size={13} />
    </button>
  );
}

/** The per-entry chips of a forward row (coloured / grey / hidden). */
export function EntryChips({
  view,
  backupChip,
}: {
  view: EntryRuleView | null;
  /** the existing 备线 chip, shown where the entry switches to it */
  backupChip?: ReactNode;
}) {
  if (!view) return null;
  const p = view.phone;

  return (
    <>
      {!view.open && (
        <Pill title={view.closedReason} tone="slate">
          此入口未开
        </Pill>
      )}
      {p && view.open && (
        <Pill
          title={p.reason}
          tone={
            p.state === "ready"
              ? "green"
              : p.state === "failed"
                ? "red"
                : p.state === "connecting"
                  ? "amber"
                  : "slate"
          }
        >
          {p.state === "ready"
            ? "本机就绪"
            : p.state === "failed"
              ? "本机失败"
              : p.state === "connecting"
                ? "本机连接中"
                : "本机未连接"}
        </Pill>
      )}
      {view.open && view.warm.state && (
        <Pill
          title={view.warm.title}
          tone={view.warm.state === "on" ? "amber" : "slate"}
        >
          {view.warm.label}
        </Pill>
      )}
      {view.open && view.logswitch && (
        <Pill tone={view.logswitch === "on" ? "green" : "slate"}>
          快切 {view.logswitch === "on" ? "开" : "关"}
        </Pill>
      )}
      {view.open && view.backup === "on" && backupChip}
      {view.open && view.backup === "off" && (
        <Pill title="这台入口机不会自动切到备线" tone="slate">
          备线 不切
        </Pill>
      )}
      {view.open && view.udp.state && (
        <Pill tone={view.udp.state === "on" ? "green" : "slate"}>
          {view.udp.label}
        </Pill>
      )}
    </>
  );
}

const fmtTime = (ms?: number) =>
  ms
    ? new Date(ms).toLocaleString("zh-CN", {
        timeZone: "Asia/Taipei",
        hour12: false,
      })
    : "—";

async function run<T>(p: Promise<T>, done?: string): Promise<T | null> {
  try {
    const v = await p;

    if (done) toast.success(done);

    return v;
  } catch (e) {
    toast.error((e as Error).message || "App 没有完成这个操作");

    return null;
  }
}

/** The 手机本地 details (desktop drawer body, H5 ＋ row body). */
export function PhoneDetails({
  phone,
  info,
  usage,
  rules,
  compact = false,
}: {
  phone: PhoneState | null;
  info: AppInfo | null;
  /** admin: the panel's vendor usage view (在线设备 x/y) */
  usage?: LeiKwanUsageView | null;
  rules: LeiKwanRule[];
  compact?: boolean;
}) {
  const [busy, setBusy] = useState("");
  const [log, setLog] = useState<PhoneLog | null>(null);
  const kvw = compact ? "w-[5.25rem]" : "w-28";
  const s = phone;
  const sum = phoneSummary(s);
  const running =
    s?.running ?? (!!s && s.connection !== "idle" && s.connection !== "failed");
  const dev = usage?.devices;
  const full = !!dev && dev.limit > 0 && dev.count >= dev.limit;
  const ruleOf = (fid: number, port: number) =>
    rules.find((r) =>
      r.forwardId ? r.forwardId === fid : r.listenPort === port,
    );
  const act = async (key: string, p: () => Promise<unknown>, done?: string) => {
    setBusy(key);
    await run(p(), done);
    setBusy("");
  };

  return (
    <div className="space-y-3" data-leikwan-phone-details="">
      <div className="flex flex-wrap items-center gap-1.5">
        <Pill tone={sum.tone}>
          {s?.connection === "failed" ? "连接失败" : sum.text}
        </Pill>
        {s?.stage && s.connection === "connecting" && (
          <Pill tone="amber">{s.stage}</Pill>
        )}
        {s?.startedAt && running && (
          <span className="text-xs text-default-500">
            {fmtTime(s.startedAt)} 起 · 台湾
          </span>
        )}
      </div>
      {s?.error && (
        <div
          className="rounded-lg bg-red-500/10 px-3 py-2 text-xs leading-5 text-red-700 dark:bg-red-400/15 dark:text-red-300"
          data-phone-error=""
        >
          {s.error}
          {s.errorCode && (
            <span className="ml-1 font-mono text-[11px] opacity-70">
              ({s.errorCode})
            </span>
          )}
        </div>
      )}
      {s && !s.credentials.set && (
        <div className="rounded-lg bg-amber-500/10 px-3 py-2 text-xs leading-5 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
          先填商家的客户端 ID 和 Agent
          Token：只存在这台手机上（系统加密），不会发给面板。
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        {running ? (
          <Button
            color="danger"
            isLoading={busy === "disconnect"}
            size="sm"
            variant="flat"
            onPress={() => void act("disconnect", () => appCall("disconnect"))}
          >
            断开
          </Button>
        ) : (
          <Button
            color="success"
            isDisabled={!s?.credentials.set}
            isLoading={busy === "connect"}
            size="sm"
            variant="flat"
            onPress={() =>
              void act("connect", () => appCall("connect", {}, 30000))
            }
          >
            连接
          </Button>
        )}
        <Button
          color="warning"
          size="sm"
          variant="flat"
          onPress={() =>
            void act("cred", () => appCall("editCredentials", {}, 600000))
          }
        >
          {s?.credentials.set
            ? `商家账号${s.credentials.clientIdHint ? ` ·…${s.credentials.clientIdHint}` : ""}`
            : "填写商家账号"}
        </Button>
      </div>

      <div className="divide-y divide-divider/60">
        <KV label="自动连接" labelClassName={kvw}>
          <LabeledSwitch
            isSelected={!!s?.autoConnect}
            onValueChange={(on) =>
              void act("auto", () => appCall("setAutoConnect", { on }))
            }
          >
            打开 App 自动连接
          </LabeledSwitch>
          {s?.autoConnectBlocked && (
            <span className="text-xs text-amber-700 dark:text-amber-300">
              {s.autoConnectBlocked}
            </span>
          )}
        </KV>
        <KV label="预热池" labelClassName={kvw}>
          {s?.warm.supported ? (
            <LabeledSwitch
              isSelected={!!s.warm.on}
              onValueChange={(on) =>
                void act("warm", () => appCall("setWarm", { on }))
              }
            >
              {s.warm.on ? "开" : "关"}
            </LabeledSwitch>
          ) : (
            <span className="text-xs text-default-500">这台手机不支持</span>
          )}
        </KV>
        <KV label="电池" labelClassName={kvw}>
          {s?.battery.unrestricted ? (
            <Pill tone="green">无限制</Pill>
          ) : (
            <>
              <Pill tone="amber">没设成无限制</Pill>
              <Button
                color="warning"
                size="sm"
                variant="flat"
                onPress={() =>
                  void act("battery", () => appCall("openBatterySettings"))
                }
              >
                去设置
              </Button>
              <span className="w-full text-xs text-default-500">
                不设的话锁屏后系统会关掉本地端口，也不会自动连接
              </span>
            </>
          )}
        </KV>
        <KV label="商家在线设备" labelClassName={kvw}>
          {dev ? (
            <>
              <Pill tone={full ? "amber" : "purple"}>
                {dev.count} / {dev.limit}
              </Pill>
              {full && (
                <span className="text-xs text-amber-700 dark:text-amber-300">
                  名额满了：这台手机是单独的一台设备，连接可能被商家拒绝（先在商家网站下线一台）
                </span>
              )}
            </>
          ) : (
            <span className="text-xs text-default-500">面板还没读到</span>
          )}
        </KV>
      </div>

      <div className="space-y-1.5">
        <div className="text-xs font-semibold text-default-600">专线状态</div>
        {(s?.forwards || []).map((f) => {
          const r = ruleOf(f.forwardId, f.localPort);

          return (
            <div
              key={f.forwardId}
              className="flex flex-wrap items-center gap-1.5 rounded-lg border border-divider px-2.5 py-1.5"
              data-phone-forward={f.forwardId}
            >
              <span className="min-w-0 flex-1 break-words text-[13px] font-medium">
                {r
                  ? `${r.lineName} · ${r.name}`
                  : f.name || `利群 #${f.forwardId}`}
              </span>
              <span className="font-mono text-xs text-default-600">
                127.0.0.1:{f.localPort}
              </span>
              <Pill
                tone={
                  f.state === "ready"
                    ? "green"
                    : f.state === "failed"
                      ? "red"
                      : f.state === "connecting"
                        ? "amber"
                        : "slate"
                }
              >
                {f.state === "ready"
                  ? "就绪"
                  : f.state === "failed"
                    ? "失败"
                    : f.state === "connecting"
                      ? "连接中"
                      : "未连接"}
              </Pill>
              {f.reason && f.state !== "ready" && (
                <span className="w-full text-xs text-default-500">
                  {f.reason}
                </span>
              )}
            </div>
          );
        })}
        {s && s.forwards.length === 0 && (
          <p className="text-xs text-default-500">
            连接一次后，这里列出商家账号的每条转发和本机端口。
          </p>
        )}
      </div>

      <div className="space-y-1.5">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs font-semibold text-default-600">
            运行日志
          </span>
          <span className="text-xs text-default-500">
            {fmtBytes(s?.logBytes || 0)} · 最多 2 MB
          </span>
        </div>
        <div className="flex flex-wrap gap-1.5">
          <Button
            color="primary"
            size="sm"
            variant="flat"
            onPress={async () => {
              if (log) {
                setLog(null);

                return;
              }
              setLog(
                await run(appCall<PhoneLog>("getLog", { maxChars: 20000 })),
              );
            }}
          >
            {log ? "收起" : "查看"}
          </Button>
          <Button
            color="secondary"
            size="sm"
            variant="flat"
            onPress={() =>
              void act("copy", () => appCall("copyLog"), "运行日志已复制")
            }
          >
            复制
          </Button>
          <Button
            color="secondary"
            size="sm"
            variant="flat"
            onPress={() => void act("share", () => appCall("shareLog"))}
          >
            分享
          </Button>
        </div>
        {log && (
          <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-default-100 p-2 font-mono text-[11px] leading-4 text-default-700 dark:bg-default-100/50">
            {log.truncated ? "…（只显示最后一段）\n" : ""}
            {log.text}
          </pre>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-1.5 text-xs text-default-500">
        {info ? (
          <>
            <Pill tone="slate">
              App {info.app.version} ({info.app.versionCode})
            </Pill>
            <Pill tone="slate">核心 {info.core.version}</Pill>
            {info.core.agentVersion && (
              <Pill title="向商家上报的客户端版本" tone="slate">
                上报 {info.core.agentVersion}
              </Pill>
            )}
            <span>{info.device.name}</span>
          </>
        ) : (
          <span>版本读取中</span>
        )}
        <button
          className="ml-auto text-xs text-default-500 underline-offset-2 hover:underline"
          type="button"
          onClick={() => void run(appCall("changePanel"))}
        >
          换面板地址
        </button>
      </div>
    </div>
  );
}

export function PhoneDrawer({
  open,
  onClose,
  ...rest
}: {
  open: boolean;
  onClose: () => void;
} & Parameters<typeof PhoneDetails>[0]) {
  const sum = phoneSummary(rest.phone);

  return (
    <Modal
      classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-2xl rounded-2xl" }}
      isOpen={open}
      placement="center"
      scrollBehavior="inside"
      size="2xl"
      onClose={onClose}
    >
      <ModalContent data-leikwan-phone-drawer="">
        <ModalHeader>
          <span className="flex items-center gap-2">
            <StateDot dot={sum.dot} />
            <Smartphone
              className="text-violet-600 dark:text-violet-300"
              size={16}
            />
            手机本地
            <Pill tone="violet">127.0.0.1</Pill>
          </span>
        </ModalHeader>
        <ModalBody>
          <PhoneDetails {...rest} />
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
