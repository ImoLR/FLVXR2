import type { LeiKwanRule, LeiKwanUsageView } from "@/api/leikwan-line";
import type { PhoneForward, PhoneLog, PhoneState } from "@/utils/app-bridge";
import type { ReactNode } from "react";

import { useEffect, useRef, useState } from "react";
import { AlertTriangle } from "lucide-react";

import { Pill } from "@/pages/landing/pill";
import { PhoneFirstRun, PhoneSheet } from "@/pages/leikwan/panel/phone-cards";
import {
  phoneConnected,
  phoneRunning,
  run,
  useAct,
  usePhoneCtx,
} from "@/pages/leikwan/panel/phone-entry";
import { dropLeiKwan } from "@/pages/leikwan/panel/rule-actions";
import { LabeledSwitch, fmtBytes } from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";
import { appCall, appStateCall } from "@/utils/app-bridge";

// Plan 315 r3 (user 11-26: 「信息量太大了，不够简洁，没有层次感」): 授权 → 我的入口
// with 手机本地 = three layers under the dropdown —
//   1. the old App's first-run cards (phone-cards.tsx), gone once read;
//   2. one connect row: 商家账号 (tap = the App's account dialog) + a big
//      连接 / 断开 (no account → the account first; battery not 「无限制」 → a
//      small quick-fix sheet: 去设置 / 仍然连接);
//   3. status only while connecting (the old App's steps), failed (one red
//      line) or connected (one row per forward, one pill); nothing when idle.
//      The vendor device limit: one amber line, only when it is full.
// 打开 App 自动连接 / 预热池 / 运行日志 / 换面板地址 sit behind the gear of the
// 我的入口 card (PhoneGearSheet).
// Plan 315 r4 (user 11-26): 商家账号 became the 2nd first-run card (and a row
// of the gear sheet); layer 2 is a small connect bar — status dot + word on
// the left, a mint flat 连接 (red flat 断开) on the right — that glides up as
// the cards leave. 连接 with no account and / or the battery not 「无限制」 →
// one quick-fix sheet naming what is missing (填商家账号; 去设置 + 仍然连接
// only when the account is there).

/** The old App's connect steps (BridgeService stages, panel flavor words). */
const STEPS = ["读取账号", "连接商家", "等待商家授权", "启动入口"];

const elapsedText = (ms: number) => {
  const v = Math.max(0, Math.floor(ms / 1000));

  return v < 60
    ? `${v} 秒`
    : `${Math.floor(v / 60)} 分 ${String(v % 60).padStart(2, "0")} 秒`;
};

const fmtClock = (ms?: number) =>
  ms
    ? new Date(ms).toLocaleTimeString("zh-CN", {
        timeZone: "Asia/Taipei",
        hour12: false,
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";

/** Layer 3, connecting: the four steps, the current one highlighted. */
function PhoneSteps({ s }: { s: PhoneState }) {
  const idx = (() => {
    const i = STEPS.indexOf(s.stage || "");

    return i >= 0 ? i : s.total > 0 ? 3 : 0;
  })();
  const [since, setSince] = useState(() => Date.now());
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    setSince(Date.now());
  }, [idx]);
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000);

    return () => window.clearInterval(t);
  }, []);

  return (
    <div
      className="space-y-2.5 rounded-2xl bg-amber-500/[0.08] px-3.5 py-3 dark:bg-amber-400/10"
      data-phone-state="connecting"
    >
      <ol className="space-y-2">
        {STEPS.map((name, i) => {
          const st = i < idx ? "done" : i === idx ? "now" : "wait";

          return (
            <li
              key={name}
              aria-label={`${name}：${st === "done" ? "已完成" : st === "now" ? "进行中" : "等待"}`}
              className="flex min-h-5 items-center gap-2.5 text-[13px]"
              data-phone-step={st}
            >
              <span className="flex h-5 w-5 shrink-0 items-center justify-center">
                {st === "done" ? (
                  <span className="flex h-5 w-5 items-center justify-center rounded-full bg-green-500/15 text-[11px] font-bold text-green-700 dark:bg-green-400/20 dark:text-green-300">
                    ✓
                  </span>
                ) : st === "now" ? (
                  <span className="h-4 w-4 animate-spin rounded-full border-2 border-amber-600 border-t-transparent dark:border-amber-300 dark:border-t-transparent" />
                ) : (
                  <span className="h-1.5 w-1.5 rounded-full bg-slate-400/60" />
                )}
              </span>
              <span
                className={`min-w-0 flex-1 ${
                  st === "now"
                    ? "font-semibold text-foreground"
                    : st === "wait"
                      ? "text-default-400"
                      : "text-default-600"
                }`}
              >
                {name}
              </span>
              {st === "now" && (
                <span className="shrink-0 text-xs tabular-nums text-default-500">
                  {elapsedText(now - since)}
                </span>
              )}
            </li>
          );
        })}
      </ol>
      {s.total > 0 && (
        <div className="pl-[30px] text-xs text-default-500">
          {s.ready}/{s.total} 已就绪
        </div>
      )}
    </div>
  );
}

/** One forward's one pill: 失败 / 正走备线 / 快切 / 就绪 (/ 连接中 / 未连接). */
function forwardPill(f: PhoneForward): {
  tone: "green" | "amber" | "red" | "slate";
  text: string;
  title?: string;
} {
  if (f.state === "failed")
    return { tone: "red", text: "失败", title: f.reason };
  if (f.state === "connecting") return { tone: "amber", text: "连接中" };
  if (f.state === "off") return { tone: "slate", text: "未连接" };
  if (f.switched)
    return {
      tone: "amber",
      text: "正走备线",
      title: "日志快切：新连接正走备线",
    };
  if (f.protect === "logswitch")
    return {
      tone: "green",
      text: "快切",
      title: "就绪 · 日志快切待命：出故障时新连接改走备线",
    };

  return { tone: "green", text: "就绪" };
}

/** Layer 3, connected: one row per forward the phone runs. */
function PhoneForwardRows({
  s,
  rules,
}: {
  s: PhoneState;
  rules: LeiKwanRule[];
}) {
  const ruleOf = (f: PhoneForward) =>
    rules.find((r) =>
      r.forwardId ? r.forwardId === f.forwardId : r.listenPort === f.localPort,
    );
  const all = s.connection === "connected" && s.ready >= s.total;

  return (
    <div
      className="space-y-1.5"
      data-phone-state={all ? "connected" : "partial"}
    >
      <div className="flex items-center gap-1.5 px-0.5 text-xs text-default-500">
        <span
          className={`h-2 w-2 shrink-0 rounded-full ${all ? "bg-green-500" : "bg-amber-500"}`}
        />
        <span className="font-semibold text-foreground">
          {all ? "已连接" : "部分已连接"} {s.ready}/{s.total}
        </span>
        {s.startedAt && <span>· {fmtClock(s.startedAt)} 起</span>}
      </div>
      <div
        className="divide-y divide-divider/60 rounded-2xl bg-content1 px-3 shadow-[0_1px_3px_rgba(15,23,42,0.08)] ring-1 ring-black/[0.04] dark:bg-default-100 dark:ring-white/[0.06]"
        data-phone-forwards=""
      >
        {s.forwards.map((f) => {
          const r = ruleOf(f);
          const p = forwardPill(f);

          return (
            <div
              key={f.forwardId}
              className="py-1.5"
              data-phone-forward={f.forwardId}
            >
              {/* Name on top (whole words), the local address small under
                  it, the one pill on the right. */}
              <div className="flex items-center gap-2">
                <span className="min-w-0 flex-1">
                  <span className="block text-[13px] font-medium leading-5 [word-break:keep-all]">
                    {dropLeiKwan(r?.name || f.name || `利群 #${f.forwardId}`)}
                  </span>
                  <span className="block font-mono text-[11px] leading-4 text-default-500">
                    127.0.0.1:{f.localPort}
                  </span>
                </span>
                <Pill title={p.title} tone={p.tone}>
                  {p.text}
                </Pill>
              </div>
              {f.state === "failed" && f.reason && (
                <div className="pt-0.5 text-xs text-red-700 dark:text-red-300">
                  {f.reason}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

/** Layer 3: nothing when idle. */
function PhoneStatus({ s, rules }: { s: PhoneState; rules: LeiKwanRule[] }) {
  if (s.connection === "connecting") return <PhoneSteps s={s} />;
  if (s.connection === "failed")
    return (
      <p
        className="select-text rounded-2xl bg-red-500/[0.08] px-3.5 py-2.5 text-[13px] leading-5 text-red-700 dark:bg-red-400/10 dark:text-red-300"
        data-phone-state="failed"
      >
        <span className="font-semibold">连接失败：</span>
        {s.error || "原因看运行日志（右上角 ⚙）"}
        {s.errorCode && !(s.error || "").includes(s.errorCode) && (
          <span className="ml-1 font-mono text-[11px] opacity-60">
            {s.errorCode}
          </span>
        )}
      </p>
    );
  if (s.connection === "disconnecting")
    return (
      <p
        className="px-0.5 text-xs text-default-500"
        data-phone-state="disconnecting"
      >
        正在断开…
      </p>
    );
  if (phoneConnected(s) && s.forwards.length > 0)
    return <PhoneForwardRows rules={rules} s={s} />;
  if (phoneConnected(s))
    return (
      <p
        className="px-0.5 text-xs text-default-500"
        data-phone-state="connected"
      >
        已连接 · 商家账号里没有转发
      </p>
    );

  return null;
}

/**
 * Plan 315 r3: everything under the 我的入口 dropdown while this phone's entry
 * is 手机本地 (the three layers).
 */
export function PhoneSetupPanel({
  rules,
  usage,
  onSub,
}: {
  rules: LeiKwanRule[];
  usage?: LeiKwanUsageView | null;
  /** 利群汇聚 「生成订阅」 (from the 127.0.0.1 card) */
  onSub?: () => void;
}) {
  const ctx = usePhoneCtx();
  const s = ctx?.state || null;
  const { busy, act } = useAct();
  const [credBusy, setCredBusy] = useState(false);
  const [fix, setFix] = useState(false);
  const [waitBattery, setWaitBattery] = useState(false);
  const live = useRef(s);

  live.current = s;
  const doConnect = () =>
    act("connect", () => appStateCall("connect", {}, 30000));
  const editCredentials = async () => {
    setCredBusy(true);
    const r = await run(
      appCall<{ saved?: boolean }>("editCredentials", {}, 600000),
    );

    setCredBusy(false);
    // The state event of the save may come just after the reply.
    await new Promise((res) => window.setTimeout(res, 200));

    return !!r?.saved || !!live.current?.credentials.set;
  };

  // Back from the battery settings with 「无限制」 set: go on connecting.
  useEffect(() => {
    if (fix && waitBattery && s?.battery.unrestricted && s.credentials.set) {
      setFix(false);
      setWaitBattery(false);
      void doConnect();
    }
  }, [fix, waitBattery, s?.battery.unrestricted, s?.credentials.set]);

  if (!ctx?.local) return null;
  if (!s)
    return (
      <p className="text-xs text-default-500" data-phone-state="loading">
        读取中…
      </p>
    );
  const running = phoneRunning(s);
  const dev = usage?.devices;
  const full =
    !!dev && dev.limit > 0 && dev.count >= dev.limit && !phoneConnected(s);
  const needCred = !s.credentials.set;
  const needBattery = !s.battery.unrestricted;
  const bar = barState(s);

  // Plan 315 r4: 连接 with something missing → the quick-fix sheet first.
  const connect = () => {
    const cur = live.current;

    if (!cur?.credentials.set || !cur.battery.unrestricted) {
      setWaitBattery(false);
      setFix(true);

      return;
    }
    void doConnect();
  };
  // The sheet's 填商家账号: once saved, connect (battery fine) or the sheet
  // stays with what is still missing.
  const fixCredentials = async () => {
    const saved = await editCredentials();

    if (!saved) return;
    if (live.current?.battery.unrestricted) {
      setFix(false);
      void doConnect();
    }
  };

  return (
    <div data-phone-connect-panel="" id="phone-connect-panel">
      <PhoneFirstRun
        rules={rules}
        s={s}
        onCredentials={() => void editCredentials()}
        onSub={onSub}
      />

      {/* Layer 2 (r4): a small connect bar — state on the left, the button
          on the right (mint, flat; 断开 red flat). */}
      <div
        className="flex min-h-[52px] items-center gap-2.5 rounded-2xl bg-content1 py-2 pl-3.5 pr-2 shadow-[0_1px_3px_rgba(15,23,42,0.08)] ring-1 ring-black/[0.04] dark:bg-default-100 dark:ring-white/[0.06]"
        data-phone-connect-bar={bar.key}
      >
        <span className="relative flex h-2.5 w-2.5 shrink-0">
          {bar.pulse && (
            <span
              className={`absolute inline-flex h-full w-full animate-ping rounded-full opacity-60 ${bar.dot}`}
            />
          )}
          <span
            className={`relative inline-flex h-2.5 w-2.5 rounded-full ${bar.dot}`}
          />
        </span>
        <span className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">
          {bar.text}
        </span>
        {running ? (
          <Button
            className="h-9 shrink-0 rounded-full px-5 text-sm font-semibold"
            color="danger"
            data-phone-disconnect=""
            isLoading={busy === "disconnect"}
            variant="flat"
            onPress={() =>
              void act("disconnect", () => appStateCall("disconnect"))
            }
          >
            断开
          </Button>
        ) : (
          <Button
            className="h-9 shrink-0 rounded-full bg-teal-500/15 px-5 text-sm font-semibold text-teal-700 hover:bg-teal-500/25 dark:bg-teal-400/15 dark:text-teal-300 dark:hover:bg-teal-400/25"
            color="success"
            data-phone-connect=""
            isLoading={busy === "connect" || credBusy}
            variant="flat"
            onPress={connect}
          >
            连接
          </Button>
        )}
      </div>

      {full && (
        <p
          className="mt-2.5 px-0.5 text-xs leading-5 text-amber-700 dark:text-amber-300"
          data-phone-devices-full=""
        >
          商家设备名额已满（{dev!.count}/{dev!.limit}），这台手机可能连不上
        </p>
      )}

      <div className="empty:hidden mt-4" data-phone-status="">
        <PhoneStatus rules={rules} s={s} />
      </div>

      <PhoneSheet
        footer={
          needCred ? (
            <Button
              className="h-11 flex-1 font-semibold"
              color="primary"
              data-phone-fix-cred=""
              isLoading={credBusy}
              onPress={() => void fixCredentials()}
            >
              填商家账号
            </Button>
          ) : (
            <>
              <Button
                className="h-11 flex-1"
                color="default"
                data-phone-fix-anyway=""
                variant="flat"
                onPress={() => {
                  setFix(false);
                  setWaitBattery(false);
                  void doConnect();
                }}
              >
                仍然连接
              </Button>
              <Button
                className="h-11 flex-1 font-semibold"
                color="primary"
                data-phone-fix-go=""
                onPress={() => {
                  setWaitBattery(true);
                  void run(appCall("openBatterySettings"));
                }}
              >
                去设置
              </Button>
            </>
          )
        }
        name={
          needCred ? (needBattery ? "fix-both" : "fix-cred") : "battery-fix"
        }
        open={fix}
        title={needCred && needBattery ? "连接前还差两步" : "连接前还差一步"}
        onClose={() => {
          setFix(false);
          setWaitBattery(false);
        }}
      >
        {needCred && (
          <FixItem title="先填商家账号">没有商家账号连不上利群。</FixItem>
        )}
        {needBattery && (
          <FixItem title="先设置电池「无限制」">
            不设的话，锁屏后系统可能关掉本地端口，代理客户端会断开。
            {s.battery.hint && (
              <span className="mt-1 block text-amber-700 dark:text-amber-300">
                {s.battery.hint}
              </span>
            )}
          </FixItem>
        )}
        <p className="text-xs text-default-500">
          {needCred && needBattery
            ? "先填账号，再去设置电池；都好了自动连接。"
            : needCred
              ? "填好就自动连接。"
              : "设好回来会自动接着连接。"}
        </p>
      </PhoneSheet>
    </div>
  );
}

/** One missing thing in the quick-fix sheet. */
function FixItem({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex gap-2.5 rounded-xl bg-amber-500/[0.08] px-3 py-2.5 dark:bg-amber-400/10">
      <AlertTriangle
        className="mt-0.5 shrink-0 text-amber-600 dark:text-amber-300"
        size={15}
      />
      <div className="min-w-0 text-xs leading-5 text-default-600">
        <div className="text-sm font-semibold text-foreground">{title}</div>
        {children}
      </div>
    </div>
  );
}

/** The connect bar's dot + word (r4). */
function barState(s: PhoneState): {
  key: string;
  text: string;
  dot: string;
  pulse?: boolean;
} {
  switch (s.connection) {
    case "connecting":
      return {
        key: "connecting",
        text: "连接中…",
        dot: "bg-amber-500",
        pulse: true,
      };
    case "connected":
      return s.ready < s.total
        ? { key: "partial", text: "部分已连接", dot: "bg-amber-500" }
        : { key: "connected", text: "已连接", dot: "bg-green-500" };
    case "partial":
      return { key: "partial", text: "部分已连接", dot: "bg-amber-500" };
    case "failed":
      return { key: "failed", text: "连接失败", dot: "bg-red-500" };
    case "disconnecting":
      return {
        key: "disconnecting",
        text: "正在断开…",
        dot: "bg-slate-400",
        pulse: true,
      };
    default:
      return { key: "idle", text: "未连接", dot: "bg-slate-400" };
  }
}

/** The gear of 我的入口 (手机本地): 自动连接, 预热池, 运行日志, 商家账号, 换面板地址. */
export function PhoneGearSheet({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const ctx = usePhoneCtx();
  const s = ctx?.state || null;
  const { act } = useAct();
  const [log, setLog] = useState<PhoneLog | null>(null);
  const autoNote = s?.autoConnect
    ? s.battery.unrestricted
      ? "打开 App、或在上面选手机本地时，马上连接。"
      : "电池没设成「无限制」，自动连接不会跑。"
    : "关着：选了手机本地以后，自己点「连接」。";

  return (
    <PhoneSheet
      footer={
        <Button
          className="h-11 flex-1"
          color="danger"
          variant="flat"
          onPress={onClose}
        >
          关闭
        </Button>
      }
      name="gear"
      open={open}
      title="手机本地设置"
      onClose={onClose}
    >
      <div className="divide-y divide-divider/60">
        <div className="space-y-1 pb-3" data-phone-gear-auto="">
          <LabeledSwitch
            isSelected={!!s?.autoConnect}
            onValueChange={(on) =>
              void act("auto", () => appStateCall("setAutoConnect", { on }))
            }
          >
            打开 App 自动连接
          </LabeledSwitch>
          <p className="pl-11 text-xs leading-5 text-default-500">{autoNote}</p>
        </div>
        <div className="py-3" data-phone-gear-warm="">
          {s?.warm.supported ? (
            <LabeledSwitch
              isSelected={!!s.warm.on}
              onValueChange={(on) =>
                void act("warm", () => appStateCall("setWarm", { on }))
              }
            >
              预热池
            </LabeledSwitch>
          ) : (
            <span className="text-xs text-default-500">
              预热池：这台手机不支持
            </span>
          )}
        </div>
        <div className="space-y-2 py-3" data-phone-gear-log="">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-sm text-foreground">运行日志</span>
            <span className="text-xs text-default-500">
              {fmtBytes(s?.logBytes || 0)}
            </span>
            <span className="flex-1" />
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
            <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-xl bg-default-100 p-2 font-mono text-[11px] leading-4 text-default-700 dark:bg-default-100/50">
              {log.truncated ? "…（只显示最后一段）\n" : ""}
              {log.text}
            </pre>
          )}
        </div>
        {/* Plan 315 r4: the account left the connect row (it is a first-run
            card now); changing it later lives here. */}
        <div className="flex items-center gap-2 py-3" data-phone-gear-cred="">
          <span className="text-sm text-foreground">商家账号</span>
          <span className="min-w-0 truncate text-xs text-default-500">
            {s?.credentials.set
              ? `已填${s.credentials.clientIdHint ? ` ·…${s.credentials.clientIdHint}` : ""}`
              : "未填"}
          </span>
          <span className="flex-1" />
          <Button
            color="primary"
            size="sm"
            variant="flat"
            onPress={() => void run(appCall("editCredentials", {}, 600000))}
          >
            {s?.credentials.set ? "修改" : "填写"}
          </Button>
        </div>
        <div className="flex items-center gap-2 pt-3 text-xs text-default-500">
          {ctx?.info && (
            <span className="truncate">{ctx.info.device.name}</span>
          )}
          <span className="flex-1" />
          <Button
            color="warning"
            data-phone-gear-panel=""
            size="sm"
            variant="flat"
            onPress={() => void run(appCall("changePanel"))}
          >
            换面板地址
          </Button>
        </div>
      </div>
    </PhoneSheet>
  );
}
