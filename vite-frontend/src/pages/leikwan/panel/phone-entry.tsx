import type {
  LeiKwanEntryCaps,
  LeiKwanEntryLimit,
  LeiKwanPhoneReq,
  LeiKwanRule,
  LeiKwanUsageView,
} from "@/api/leikwan-line";
import type { ReactNode } from "react";
import type {
  AppInfo,
  PhoneForward,
  PhoneLog,
  PhoneState,
} from "@/utils/app-bridge";

import { createContext, useContext, useEffect, useRef, useState } from "react";
import { Copy, Smartphone } from "lucide-react";
import toast from "react-hot-toast";

import { Pill } from "@/pages/landing/pill";
import { CapPills, ClientPill } from "@/pages/leikwan/panel/entry-state";
import { LkDot, LkExpandCard } from "@/pages/leikwan/panel/layout-parts";
import { copyText } from "@/pages/leikwan/panel/rule-actions";
import { KV, LabeledSwitch, fmtBytes } from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { getLeiKwanPhonePlan } from "@/api/leikwan-line";
import {
  appCall,
  appHas,
  appStateCall,
  isAppMode,
  useAppInfo,
  usePhoneState,
} from "@/utils/app-bridge";

// Plan 297 (user 11-23): in the im.gamer App the 利群汇聚 page is the web page
// (fork.162) plus ONE extra entry 「手机本地」 — the phone's own LeiKwan
// client on 127.0.0.1. Everything here renders only inside the App (bridge
// object injected); browsers never reach it.
//   - 入口机 list: 手机本地 first; 「＋」 = its modules (same capability
//     schema as the server entries) + the phone-only parts.
//   - Switching is only 授权 → 换主入口 (option 「手机本地」, own grant only).
//     The choice is stored in the App (setEntry), never on the server: the
//     server 主入口 keeps serving the user's other devices.
//   - With 手机本地 as the entry: the 授权 card says 主入口 手机本地 + a
//     connect panel (manual 连接 by default, three tip blocks), forward rows
//     follow the phone (127.0.0.1, 「手机本地未连接」, its chips), and a
//     subscription made / updated now is a 手机本地 one (entry_mode local).
//   - Admin only this round (normal users: the web page as is).
// Plan 301 (1.0.10-panel): the App runs 利群原版's engine (日志快切 v12 with the
// VPS triggers, 备线 dialled by the phone itself, 预热池) from the phone plan
// this page hands it (setPlan, values of the reference 利群入口). AppInfo
// .features says so → full caps. 首包过滤 is never shown. Every forward row
// follows the chosen entry; the 入口机 row has one status pill.

export const PHONE_KEY = "phone";
export const PHONE_NAME = "手机本地";
export const PHONE_HOST = "127.0.0.1";

/** Plan 301: the App runs the VPS stack (日志快切 + 快切触发 + 备线). */
export const phoneFull = (info: AppInfo | null | undefined) =>
  appHas(info, "logswitch") &&
  appHas(info, "triggers") &&
  appHas(info, "backup");

/** 手机本地 in the plan-295 capability schema (CapPills / ClientPill). */
export function phoneCaps(info: AppInfo | null): LeiKwanEntryCaps {
  const full = phoneFull(info);

  return {
    nodeId: 0,
    name: PHONE_NAME,
    host: PHONE_HOST,
    online: true,
    known: true,
    managed: false,
    client: "手机 App",
    frontBuild: `im.gamer App${info ? ` ${info.app.version}` : ""}`,
    warm: true,
    // Plan 301: never rendered (kept for the schema).
    ssFilter: false,
    logswitch: full,
    triggers: full,
    phone: full,
    missing: full ? [] : ["日志快切", "快切触发", "手机备线"],
  };
}

/**
 * Plan 301: the one status pill of the 手机本地 入口机 row / tile — 「就绪」
 * when connected and every forward is up, 「连接中 x/y」 while forwards come
 * up, else today's not-connected states (未连接 / 连接失败 / 正在断开).
 */
export function phoneEntryPill(s: PhoneState | null): {
  tone: "green" | "amber" | "red" | "slate";
  text: string;
} {
  if (!s) return { tone: "slate", text: "读取中" };
  if (s.connection === "connected" && s.total > 0 && s.ready >= s.total)
    return { tone: "green", text: "就绪" };
  if (
    s.connection === "connected" ||
    s.connection === "partial" ||
    s.connection === "connecting"
  )
    return {
      tone: "amber",
      text: s.total ? `连接中 ${s.ready}/${s.total}` : "连接中",
    };

  return phoneSummary(s);
}

export const phoneConnected = (s: PhoneState | null) =>
  !!s && (s.connection === "connected" || s.connection === "partial");

export const phoneRunning = (s: PhoneState | null) =>
  !!s && (s.running ?? (s.connection !== "idle" && s.connection !== "failed"));

/** Short state of the phone's local link (row, drawer, connect panel). */
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
      return { tone: "red", text: "连接失败", dot: "bad" };
  }

  return { tone: "slate", text: "未连接", dot: "off" };
}

function PhoneDot({ s }: { s: PhoneState | null }) {
  const d = phoneSummary(s).dot;

  if (d === "wait")
    return (
      <span className="h-2 w-2 shrink-0 rounded-full bg-amber-500" role="img" />
    );

  return (
    <LkDot off={d === "bad"} ok={d === "ok"} title={phoneSummary(s).text} />
  );
}

// ---- page context ----

export interface PhoneCtx {
  state: PhoneState | null;
  info: AppInfo | null;
  /** 手机本地 is this phone's entry (App choice) */
  local: boolean;
  /** admin: the own grant's user (whose rules follow the phone); users: self */
  ownerId?: number;
  /** what the backend needs (grant/preview, rule/list) */
  req: LeiKwanPhoneReq;
  openDrawer: () => void;
  /** jump to the connect panel (H5: the 授权 tab) */
  openConnect?: () => void;
}

const PhoneContext = createContext<PhoneCtx | null>(null);

export const PhoneProvider = PhoneContext.Provider;

/** The App's 手机本地 for this page (null in a browser). */
export function usePhoneCtx() {
  return useContext(PhoneContext);
}

/**
 * The page's phone state. In a browser everything is null / false and no
 * bridge call is made.
 */
export function usePhoneEntry() {
  const app = isAppMode();
  const state = usePhoneState();
  const info = useAppInfo();
  const req: LeiKwanPhoneReq = {
    warm: !!state?.warm.on,
    app: info?.app.version,
    features: info?.features,
  };

  return {
    app,
    state,
    info,
    local: app && state?.entry === "local",
    req,
  };
}

/** One rule as 手机本地 runs it (rows follow the phone while it is the entry). */
export interface PhoneRuleView {
  address: string;
  forward?: PhoneForward;
  /** grey 「手机本地未连接」 / red 失败 / amber 连接中 / null = ready */
  status: {
    tone: "slate" | "red" | "amber";
    text: string;
    title?: string;
  } | null;
  limits: LeiKwanEntryLimit[];
  warm: { on: boolean; title: string };
}

export const phoneForwardOf = (s: PhoneState | null, r: LeiKwanRule) =>
  s?.forwards.find((f) =>
    r.forwardId ? f.forwardId === r.forwardId : f.localPort === r.listenPort,
  );

/** null = render the web (fork.162) row as is. */
export function usePhoneRule(r: LeiKwanRule): PhoneRuleView | null {
  const ctx = usePhoneCtx();

  // Plan 301: every row follows the chosen entry (not only own forwards).
  if (!ctx || !ctx.local) return null;
  const s = ctx.state;
  const f = phoneForwardOf(s, r);
  let status: PhoneRuleView["status"] = null;

  if (!phoneRunning(s))
    status = {
      tone: "slate",
      text: "手机本地未连接",
      title: "在 授权 里点「连接」",
    };
  else if (!f)
    status = {
      tone: "slate",
      text: "手机本地没有这条",
      title: "这台手机的利群客户端里没有这条转发（商家账号不同或还没同步）",
    };
  else if (f.state === "failed")
    status = { tone: "red", text: "手机本地失败", title: f.reason };
  else if (f.state === "connecting")
    status = { tone: "amber", text: "手机本地连接中" };
  else if (f.state === "off")
    status = { tone: "slate", text: "手机本地未连接" };
  const warmOn = !!s?.warm.on && f?.warm !== false;

  return {
    address: `${PHONE_HOST}:${f?.localPort || r.listenPort}`,
    forward: f,
    status,
    limits: r.phoneLimits || [],
    warm: {
      on: warmOn,
      title: warmOn
        ? "手机本地的预热池开着"
        : s?.warm.on
          ? "这条转发的落地会提前断开空闲连接，手机上的预热自动关了"
          : "手机本地的预热池关着（授权 → 我的入口 → 快捷设置 里打开）",
    },
  };
}

/** The 预热 chip of a row on 手机本地 (replaces the host's 预热 N). */
export function PhoneWarmChip({ view }: { view: PhoneRuleView }) {
  return view.warm.on ? (
    <Pill title={view.warm.title} tone="amber">
      预热
    </Pill>
  ) : (
    <Pill title={view.warm.title} tone="slate">
      不预热
    </Pill>
  );
}

/** H5 「说明」 line: what 手机本地 cannot run. */
export const phoneLimitsNote = (view: PhoneRuleView) =>
  `主入口 手机本地 不支持 ${view.limits.map((l) => l.label).join("、")}`;

/** Status pill of a row on 手机本地 (null = ready). */
export function PhoneRowStatus({ view }: { view: PhoneRuleView }) {
  const ctx = usePhoneCtx();

  if (!view.status) return null;
  const pill = (
    <Pill wrap title={view.status.title} tone={view.status.tone}>
      {view.status.text}
    </Pill>
  );

  if (view.status.text !== "手机本地未连接" || !ctx?.openConnect) return pill;

  return (
    <button
      className="inline-flex max-w-full rounded-full"
      data-phone-row-connect=""
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        ctx.openConnect?.();
      }}
    >
      {pill}
    </button>
  );
}

/** The 入口地址 cell of a row on 手机本地 (desktop). */
export function PhoneEntryCopy({ view }: { view: PhoneRuleView }) {
  return (
    <div
      className="flex min-w-0 flex-col items-start gap-1"
      data-phone-entry-cell=""
    >
      <button
        className="inline-flex max-w-full items-center gap-1.5 rounded-lg bg-violet-500/10 px-2 py-1 text-foreground hover:bg-violet-500/15 dark:bg-violet-400/10"
        title="手机本地 · 点一下复制（要在开着利群客户端的设备上用）"
        type="button"
        onClick={() => void copyText(view.address, "手机本地地址")}
      >
        <span className="whitespace-nowrap font-mono text-xs">
          {view.address}
        </span>
        <Copy className="shrink-0 text-default-400" size={12} />
      </button>
      <PhoneRowStatus view={view} />
    </div>
  );
}

/** The 入口 field of an H5 row on 手机本地. */
export function PhoneEntryField({ view }: { view: PhoneRuleView }) {
  return (
    <>
      <button
        className="font-mono text-xs"
        type="button"
        onClick={() => void copyText(view.address, "手机本地地址")}
      >
        {view.address}
      </button>
      <Pill tone="violet">手机本地</Pill>
      <PhoneRowStatus view={view} />
    </>
  );
}

// ---- 授权 card ----

/** 「主入口 手机本地」 + the server 主入口 the other devices keep. */
export function PhoneMainChip({
  serverName,
  onPress,
  prefix = "主入口 ",
}: {
  serverName?: string;
  onPress?: () => void;
  prefix?: string;
}) {
  const pill = (
    <Pill
      icon={<Smartphone size={11} />}
      title={`这台手机用手机本地；别的设备照常用 ${serverName || "服务器主入口"}${onPress ? " · 点一下换主入口" : ""}`}
      tone="violet"
    >
      {prefix}
      {PHONE_NAME}
    </Pill>
  );

  return (
    <>
      {onPress ? (
        <button
          className="inline-flex max-w-full rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
          data-phone-main-chip=""
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onPress();
          }}
        >
          {pill}
        </button>
      ) : (
        pill
      )}
      {serverName && (
        <Pill title="面板里的主入口没变：电脑和别的设备照常走它" tone="slate">
          其他设备 {serverName}
        </Pill>
      )}
    </>
  );
}

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

function useAct() {
  const [busy, setBusy] = useState("");
  const act = async (key: string, p: () => Promise<unknown>, done?: string) => {
    setBusy(key);
    await run(p(), done);
    setBusy("");
  };

  return { busy, act };
}

function ConnectButtons({
  s,
  size = "sm",
  full = false,
}: {
  s: PhoneState | null;
  size?: "sm" | "md" | "lg";
  /** Plan 315: the big full-width button of 我的入口 */
  full?: boolean;
}) {
  const { busy, act } = useAct();
  const cls = full ? "h-11 w-full text-[15px] font-semibold" : undefined;

  if (phoneRunning(s))
    return (
      <Button
        className={cls}
        color="danger"
        data-phone-disconnect=""
        isLoading={busy === "disconnect"}
        size={size}
        variant="flat"
        onPress={() => void act("disconnect", () => appStateCall("disconnect"))}
      >
        断开
      </Button>
    );

  return (
    <Button
      className={cls}
      color="success"
      data-phone-connect=""
      isDisabled={!s?.credentials.set}
      isLoading={busy === "connect"}
      size={size}
      title={
        s?.credentials.set ? undefined : "先填商家账号（只存在这台手机上）"
      }
      variant={full ? "solid" : "flat"}
      onPress={() =>
        void act("connect", () => appStateCall("connect", {}, 30000))
      }
    >
      {full && !s?.credentials.set ? "连接（先填商家账号）" : "连接"}
    </Button>
  );
}

function AutoConnectSwitch({ s }: { s: PhoneState | null }) {
  const { act } = useAct();

  return (
    <LabeledSwitch
      isSelected={!!s?.autoConnect}
      onValueChange={(on) =>
        void act("auto", () => appStateCall("setAutoConnect", { on }))
      }
    >
      打开 App 自动连接
    </LabeledSwitch>
  );
}

const autoNote = (s: PhoneState | null) =>
  s?.autoConnect
    ? s.battery.unrestricted
      ? "开着：打开 App、或在换主入口里选手机本地时，会马上连接。"
      : "开着，但电池没设成「无限制」：自动连接不会跑，先把电池设好（手动「连接」照样可以）。"
    : "关着：选了手机本地以后，要自己点「连接」。";

const tipBox =
  "space-y-1.5 rounded-xl border px-3 py-2.5 text-xs leading-5 text-default-600";

/** Old App card 1: 电池设为「无限制」 (live state). */
function BatteryTip({ s }: { s: PhoneState | null }) {
  const { act } = useAct();
  const ok = !!s?.battery.unrestricted;

  return (
    <div
      className={`${tipBox} ${ok ? "border-divider" : "border-red-500/40 bg-red-500/[0.06] dark:bg-red-400/10"}`}
      data-phone-tip="battery"
    >
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-[13px] font-semibold text-foreground">
          电池设为「无限制」
        </span>
        {ok ? (
          <Pill tone="green">✓ 已设好</Pill>
        ) : (
          <Pill tone="red">还没设</Pill>
        )}
        {!ok && (
          <Button
            className="ml-auto"
            color="warning"
            size="sm"
            variant="flat"
            onPress={() =>
              void act("battery", () => appCall("openBatterySettings"))
            }
          >
            去设置
          </Button>
        )}
      </div>
      {ok ? (
        <p>锁屏后本地端口也一直开着。</p>
      ) : (
        <>
          <p>
            im.gamer 要一直在后台开着本地端口。电池没设成「无限制」时，
            <b>锁屏后系统会冻结或关掉它</b>，代理客户端就会断线。
          </p>
          <p>
            点「去设置」，在弹出的窗口里选「允许」（有的手机叫「无限制」）。
            {s?.battery.hint ? ` ${s.battery.hint}` : ""}
          </p>
          <p className="text-red-700 dark:text-red-300">
            没设好也可以手动点「连接」；「打开 App
            自动连接」要先设好这个才会自动连。
          </p>
        </>
      )}
    </div>
  );
}

/** Old App card 2: 节点地址改成 127.0.0.1 (with the per-专线 local ports). */
function NodeTip({ s, rules }: { s: PhoneState | null; rules: LeiKwanRule[] }) {
  const ports = (s?.forwards.length ? s.forwards : [])
    .map((f) => ({
      port: f.localPort,
      name:
        rules.find((r) =>
          r.forwardId
            ? r.forwardId === f.forwardId
            : r.listenPort === f.localPort,
        )?.name ||
        f.name ||
        `利群 #${f.forwardId}`,
    }))
    .concat(
      s?.forwards.length
        ? []
        : rules.map((r) => ({ port: r.listenPort, name: r.name })),
    );

  return (
    <div className={`${tipBox} border-divider`} data-phone-tip="node">
      <div className="text-[13px] font-semibold text-foreground">
        节点地址改成 127.0.0.1
      </div>
      <p>
        在代理客户端（NekoBox、FlClash、v2rayNG 等）的节点里，<b>只改两项</b>
        ：服务器 / 地址填 <span className="font-mono">127.0.0.1</span>
        ，端口填这条专线的本机端口。
      </p>
      <div className="flex flex-wrap gap-1">
        {ports.map((p) => (
          <Pill key={`${p.port}-${p.name}`} title={p.name} tone="violet">
            {p.name.replace(/^(\d+)\s.*/, "$1")} → 127.0.0.1:{p.port}
          </Pill>
        ))}
      </div>
      <p>
        <b>其它照落地填</b>
        ：密码、加密方式、协议、传输这些都和落地一样，不用改。更省事：用「复制链接」，或在手机本地下「生成订阅」，地址已经是
        127.0.0.1。
      </p>
    </div>
  );
}

const BYPASS_STEPS: [string, string][] = [
  ["NekoBox", "设置 → 路由设置 → 分应用代理 → 绕过"],
  ["FlClash", "工具 → 访问控制 → 黑名单模式"],
  ["v2rayNG", "分应用设置 → 启用分应用 + 绕行模式"],
  ["Clash Meta", "设置 → 网络 → 访问控制模式 → 不允许已选择的应用"],
];

/** Old App card 3: 分应用代理里绕过 im.gamer. */
function BypassTip() {
  return (
    <div className={`${tipBox} border-divider`} data-phone-tip="bypass">
      <div className="text-[13px] font-semibold text-foreground">
        分应用代理里绕过 im.gamer
      </div>
      <p>
        im.gamer 要放在代理客户端的 <b>VPN 外面</b>
        ，不然它自己连线路的流量会绕回代理。用客户端设置里的
        <b>「分应用代理 / 访问控制」</b>把 im.gamer <b>排除</b>
        （绕过、黑名单或不允许已选应用）。
      </p>
      <ul className="space-y-0.5">
        {BYPASS_STEPS.map(([app, how]) => (
          <li key={app} className="flex gap-1.5">
            <span className="w-[4.5rem] shrink-0 font-semibold text-foreground">
              {app}
            </span>
            <span>{how}</span>
          </li>
        ))}
      </ul>
      <p>
        <b>不要用路由或规则</b>
        里的「按应用直连 / 绕过」代替：那样它的流量还是先进
        VPN，规则没匹配上或客户端重启时就会绕回代理。
      </p>
    </div>
  );
}

// ---- 授权 → 我的入口: setup + connect (plan 315) ----

// Plan 315 (user 11-26): everything about connecting 手机本地 lives in 授权 →
// 我的入口 (tips, checks, 快捷设置, 连接 and its stages); 入口机 → 手机本地
// only shows information. Flags kept in the WebView's localStorage (this
// phone only): the first good connection, and the manual 分应用代理 tick.
const ONCE_KEY = "flvx_phone_ok_once";
const BYPASS_KEY = "flvx_phone_bypass_ok";

const lsOn = (k: string) => {
  try {
    return localStorage.getItem(k) === "1";
  } catch {
    return false;
  }
};

const lsSet = (k: string, on: boolean) => {
  try {
    if (on) localStorage.setItem(k, "1");
    else localStorage.removeItem(k);
  } catch {
    /* storage off: the flag lives for this page only */
  }
};

/** This phone has connected successfully at least once (WebView storage). */
function useConnectedOnce(s: PhoneState | null) {
  const good = phoneConnected(s) && (s?.ready || 0) > 0;
  const [once, setOnce] = useState(() => lsOn(ONCE_KEY));

  useEffect(() => {
    if (good && !once) {
      lsSet(ONCE_KEY, true);
      setOnce(true);
    }
  }, [good, once]);

  return once || good;
}

/** One check row: ✓ / ⚠ (/ · unknown), what, its state, the action. */
function CheckRow({
  ok,
  warn = !ok,
  label,
  value,
  action,
  note,
  check,
}: {
  ok: boolean;
  warn?: boolean;
  label: string;
  value?: ReactNode;
  action?: ReactNode;
  note?: ReactNode;
  check: string;
}) {
  return (
    <div
      className="flex flex-wrap items-center gap-x-2 gap-y-1 py-2"
      data-phone-check={check}
      data-phone-check-ok={ok ? "" : undefined}
    >
      <span
        aria-label={ok ? "已好" : warn ? "要处理" : "未知"}
        className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[11px] font-bold ${
          ok
            ? "bg-green-500/15 text-green-700 dark:bg-green-400/20 dark:text-green-300"
            : warn
              ? "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300"
              : "bg-slate-500/15 text-slate-600 dark:bg-slate-400/20 dark:text-slate-300"
        }`}
        role="img"
      >
        {ok ? "✓" : warn ? "⚠" : "·"}
      </span>
      <span className="whitespace-nowrap text-[13px] font-medium">{label}</span>
      {value && <span className="text-xs text-default-500">{value}</span>}
      <span className="flex-1" />
      {action}
      {note && (
        <p className="w-full pl-7 text-xs leading-5 text-default-500">{note}</p>
      )}
    </div>
  );
}

/** The old App's connect steps (BridgeService stages, panel flavor labels). */
const STEPS = ["读取账号", "连接商家", "等待商家授权", "启动入口"];

const elapsedText = (ms: number) => {
  const v = Math.max(0, Math.floor(ms / 1000));

  return v < 60
    ? `已等待 ${v} 秒`
    : `已等待 ${Math.floor(v / 60)} 分 ${String(v % 60).padStart(2, "0")} 秒`;
};

/** 「3/6 已就绪 · 其余 3 条仍在连接中，请稍候…」 (old App wording). */
function readyLine(s: PhoneState) {
  const failed = s.forwards.filter((f) => f.state === "failed").length;
  const pending = Math.max(0, s.total - s.ready - failed);
  const head = `${s.ready}/${s.total} 已就绪`;

  if (!s.total || s.ready >= s.total) return { line: head, line2: "" };

  return {
    line:
      pending > 0
        ? `${head} · ${s.ready === 0 && failed === 0 ? "都还" : `其余 ${pending} 条`}仍在连接中，请稍候…`
        : `${head} · 其余 ${failed} 条没连上，看下面的专线状态`,
    line2:
      pending > 0 && failed > 0
        ? `另有 ${failed} 条没连上，看下面的专线状态`
        : "",
  };
}

const boxTone = {
  amber:
    "bg-amber-500/10 text-amber-800 dark:bg-amber-400/10 dark:text-amber-200",
  green:
    "bg-green-500/10 text-green-800 dark:bg-green-400/10 dark:text-green-200",
  red: "bg-red-500/10 text-red-800 dark:bg-red-400/10 dark:text-red-200",
  slate:
    "bg-slate-500/10 text-slate-700 dark:bg-slate-400/10 dark:text-slate-200",
} as const;

/**
 * The state block under 连接 (like the old App's connect box): 正在连接 + the
 * four steps with the waiting time, 已连接 x/y, or the failure reason.
 */
function ConnectState({ s }: { s: PhoneState | null }) {
  const connecting = s?.connection === "connecting";
  const stageIdx = (() => {
    if (!s) return 0;
    const i = STEPS.indexOf(s.stage || "");

    return i >= 0 ? i : s.total > 0 ? 3 : 0;
  })();
  const [since, setSince] = useState(() => Date.now());
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    setSince(Date.now());
  }, [stageIdx, connecting]);
  useEffect(() => {
    if (!connecting) return;
    const t = window.setInterval(() => setNow(Date.now()), 1000);

    return () => window.clearInterval(t);
  }, [connecting]);

  if (!s) return null;
  if (s.connection === "idle")
    return (
      <p className="text-xs text-default-500" data-phone-state="idle">
        {s.autoConnectBlocked
          ? `未连接 · ${s.autoConnectBlocked}`
          : "未连接 · 点上面的「连接」开始"}
      </p>
    );
  if (s.connection === "disconnecting")
    return (
      <div
        className={`rounded-xl px-3.5 py-2.5 text-[13px] font-semibold ${boxTone.slate}`}
        data-phone-state="disconnecting"
      >
        正在断开…
      </div>
    );
  if (s.connection === "failed")
    return (
      <div
        className={`space-y-1 rounded-xl px-3.5 py-2.5 ${boxTone.red}`}
        data-phone-state="failed"
      >
        <div className="text-[13px] font-semibold">连接失败</div>
        <p className="select-text text-xs leading-5">
          {s.error || "原因看运行日志（下面「更多」）"}
          {s.errorCode && (
            <span className="ml-1 font-mono text-[11px] opacity-70">
              ({s.errorCode})
            </span>
          )}
        </p>
      </div>
    );
  const lines = readyLine(s);

  if (connecting)
    return (
      <div
        className={`space-y-1.5 rounded-xl px-3.5 py-2.5 ${boxTone.amber}`}
        data-phone-state="connecting"
      >
        <div className="text-[13px] font-semibold">正在连接</div>
        <p className="text-xs">正在读取账户并申请线路…</p>
        <ol className="space-y-1 pt-1">
          {STEPS.map((name, i) => {
            const state =
              i < stageIdx ? "done" : i === stageIdx ? "now" : "wait";

            return (
              <li
                key={name}
                aria-label={`${name}：${state === "done" ? "已完成" : state === "now" ? "进行中" : "等待"}`}
                className="flex min-h-5 items-center gap-2 text-[12.5px]"
                data-phone-step={state}
              >
                <span className="flex h-4 w-4 shrink-0 items-center justify-center">
                  {state === "done" ? (
                    <span className="text-[12px] font-bold text-green-700 dark:text-green-300">
                      ✓
                    </span>
                  ) : state === "now" ? (
                    <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-current border-t-transparent" />
                  ) : (
                    <span className="h-1.5 w-1.5 rounded-full bg-slate-400/70" />
                  )}
                </span>
                <span
                  className={`min-w-0 flex-1 ${state === "now" ? "font-semibold text-foreground" : state === "wait" ? "opacity-60" : ""}`}
                >
                  {name}
                </span>
                {state === "now" && (
                  <span className="shrink-0 text-xs text-default-500">
                    {elapsedText(now - since)}
                  </span>
                )}
              </li>
            );
          })}
        </ol>
        {s.total > 0 && (
          <p className="pl-6 text-[12.5px] font-medium">{lines.line}</p>
        )}
      </div>
    );
  const all = s.connection === "connected" && s.ready >= s.total;

  return (
    <div
      className={`space-y-1 rounded-xl px-3.5 py-2.5 ${all ? boxTone.green : boxTone.amber}`}
      data-phone-state={all ? "connected" : "partial"}
    >
      <div className="flex flex-wrap items-baseline gap-x-2 text-[13px] font-semibold">
        {all ? "已连接" : "部分已连接"}
        {s.startedAt && (
          <span className="text-xs font-normal text-default-500">
            {fmtTime(s.startedAt)} 起 台湾
          </span>
        )}
      </div>
      <p className="text-[12.5px] font-medium">{lines.line}</p>
      {lines.line2 && (
        <p className="text-xs text-amber-700 dark:text-amber-300">
          {lines.line2}
        </p>
      )}
      {s.error && (
        <p className="text-xs text-red-700 dark:text-red-300">{s.error}</p>
      )}
    </div>
  );
}

/** 专线状态: one line per forward the phone runs (moved from 入口机). */
function PhoneForwardList({
  s,
  rules,
}: {
  s: PhoneState | null;
  rules: LeiKwanRule[];
}) {
  const ruleOf = (fid: number, port: number) =>
    rules.find((r) =>
      r.forwardId ? r.forwardId === fid : r.listenPort === port,
    );

  return (
    <div className="space-y-1.5" data-phone-forwards="">
      <div className="text-xs font-semibold text-default-600">专线状态</div>
      {(s?.forwards || []).map((f) => {
        const r = ruleOf(f.forwardId, f.localPort);

        return (
          <div
            key={f.forwardId}
            className="space-y-0.5 rounded-lg border border-divider px-2.5 py-1.5"
            data-phone-forward={f.forwardId}
          >
            <div className="flex items-center gap-1.5">
              <span className="min-w-0 flex-1 text-[13px] font-medium [word-break:keep-all]">
                {r ? r.name : f.name || `利群 #${f.forwardId}`}
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
              {f.protect === "logswitch" && f.state === "ready" && (
                <Pill
                  title={
                    f.switched
                      ? "日志快切：新连接正走备线"
                      : "日志快切待命：出故障时新连接改走备线"
                  }
                  tone={f.switched ? "amber" : "green"}
                >
                  {f.switched ? "正走备线" : "快切"}
                </Pill>
              )}
            </div>
            <div className="font-mono text-xs text-default-500">
              127.0.0.1:{f.localPort}
              {r?.lineName && (
                <span className="ml-1.5 font-sans">{r.lineName}</span>
              )}
            </div>
            {f.reason && f.state !== "ready" && (
              <div className="text-xs text-default-500">{f.reason}</div>
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
  );
}

/** 更多: 运行日志 (查看 / 复制 / 分享) + 换面板地址. */
function PhoneMore({ s }: { s: PhoneState | null }) {
  const ctx = usePhoneCtx();
  const { act } = useAct();
  const [open, setOpen] = useState(false);
  const [log, setLog] = useState<PhoneLog | null>(null);

  return (
    <div className="border-t border-divider/70 pt-2" data-phone-more="">
      <button
        aria-expanded={open}
        className="text-xs font-medium text-primary"
        data-phone-more-toggle=""
        type="button"
        onClick={() => setOpen(!open)}
      >
        更多 {open ? "▴" : "▾"}
      </button>
      {open && (
        <div className="mt-2 space-y-2">
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
          <div className="flex flex-wrap items-center gap-2 text-xs text-default-500">
            {ctx?.info && <span>{ctx.info.device.name}</span>}
            <button
              className="ml-auto text-xs text-default-500 underline-offset-2 hover:underline"
              type="button"
              onClick={() => void run(appCall("changePanel"))}
            >
              换面板地址
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * Plan 315: the setup + connect panel under 授权 → 我的入口 while this phone's
 * entry is 手机本地 — first-time tips, the checks (电池 / 商家账号 / 分应用代理 /
 * 在线设备) with their actions, 快捷设置, 连接 / 断开 + the stage block, 专线状态,
 * 更多 (运行日志 / 换面板地址) and the short notes.
 */
export function PhoneSetupPanel({
  rules,
  usage,
}: {
  rules: LeiKwanRule[];
  usage?: LeiKwanUsageView | null;
}) {
  const ctx = usePhoneCtx();
  const s = ctx?.state || null;
  const { busy, act } = useAct();
  const once = useConnectedOnce(s);
  const [tips, setTips] = useState<boolean | null>(null);
  const tipsOpen = tips ?? !once;
  const [bypass, setBypass] = useState(() => lsOn(BYPASS_KEY));
  const dev = usage?.devices;
  const full = !!dev && dev.limit > 0 && dev.count >= dev.limit;
  const batteryOk = !!s?.battery.unrestricted;
  const credOk = !!s?.credentials.set;
  const failing = !batteryOk || !credOk || full;

  if (!ctx?.local) return null;
  const setBypassOk = (on: boolean) => {
    lsSet(BYPASS_KEY, on);
    setBypass(on);
  };

  return (
    <div
      className="space-y-3"
      data-phone-connect-panel=""
      data-phone-first={once ? undefined : ""}
      id="phone-connect-panel"
    >
      {tipsOpen ? (
        <div className="space-y-2" data-phone-tips="">
          <div className="flex items-center gap-2">
            <span className="text-xs font-semibold text-default-600">
              用之前看一下
            </span>
            {once && (
              <button
                className="text-xs text-primary"
                data-phone-tips-toggle=""
                type="button"
                onClick={() => setTips(false)}
              >
                收起
              </button>
            )}
          </div>
          <BatteryTip s={s} />
          <NodeTip rules={rules} s={s} />
          <BypassTip />
        </div>
      ) : (
        <button
          className="flex w-full items-center gap-2 text-left"
          data-phone-tips-toggle=""
          type="button"
          onClick={() => setTips(true)}
        >
          <span className="text-xs font-semibold text-default-600">
            用之前看一下
          </span>
          <span className="text-xs text-primary">展开</span>
          {failing && (
            <Pill tone="red">
              {!batteryOk
                ? "电池没设成无限制"
                : !credOk
                  ? "还没填商家账号"
                  : "商家设备名额满了"}
            </Pill>
          )}
        </button>
      )}

      <div
        className="divide-y divide-divider/60 rounded-xl border border-divider px-3"
        data-phone-checks=""
      >
        <CheckRow
          action={
            !batteryOk && (
              <Button
                color="warning"
                isLoading={busy === "battery"}
                size="sm"
                variant="flat"
                onPress={() =>
                  void act("battery", () => appCall("openBatterySettings"))
                }
              >
                去设置
              </Button>
            )
          }
          check="battery"
          label="电池「无限制」"
          note={
            !batteryOk &&
            `不设的话锁屏后系统会关掉本地端口，自动连接也不会跑。${s?.battery.hint ? ` ${s.battery.hint}` : ""}`
          }
          ok={batteryOk}
          value={batteryOk ? "已设好" : "还没设"}
        />
        <CheckRow
          action={
            <Button
              color="warning"
              isLoading={busy === "cred"}
              size="sm"
              variant="flat"
              onPress={() =>
                void act("cred", () => appCall("editCredentials", {}, 600000))
              }
            >
              {credOk ? "修改" : "填写"}
            </Button>
          }
          check="credentials"
          label="商家账号"
          note={
            !credOk &&
            "客户端 ID + Agent Token：只存在这台手机上（系统加密），不会发给面板。"
          }
          ok={credOk}
          value={
            credOk
              ? `已填${s?.credentials.clientIdHint ? ` ·…${s.credentials.clientIdHint}` : ""}`
              : "还没填"
          }
        />
        <CheckRow
          action={
            bypass ? (
              <button
                className="text-xs text-default-500 underline-offset-2 hover:underline"
                type="button"
                onClick={() => setBypassOk(false)}
              >
                撤销
              </button>
            ) : (
              <Button
                color="success"
                size="sm"
                variant="flat"
                onPress={() => setBypassOk(true)}
              >
                已设好
              </Button>
            )
          }
          check="bypass"
          label="分应用代理"
          note={
            !bypass && (
              <>
                面板查不到这一项：在代理客户端里把 im.gamer
                设成「绕过」，设好后点「已设好」。
                <button
                  className="ml-1 text-primary"
                  type="button"
                  onClick={() => setTips(true)}
                >
                  怎么设
                </button>
              </>
            )
          }
          ok={bypass}
          value={bypass ? "已设好（自己勾的）" : "绕过 im.gamer"}
          warn={false}
        />
        <CheckRow
          check="devices"
          label="商家在线设备"
          note={
            full &&
            "名额满了：这台手机是单独的一台设备，连接可能被商家拒绝（先在商家那边下线一台）。"
          }
          ok={!!dev && !full}
          value={dev ? `${dev.count} / ${dev.limit}` : "面板还没读到"}
          warn={full}
        />
      </div>

      <div
        className="space-y-1.5 rounded-xl border border-divider px-3 py-2.5"
        data-phone-quick=""
      >
        <div className="text-xs font-semibold text-default-600">快捷设置</div>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
          <AutoConnectSwitch s={s} />
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
        <p className="text-[11px] leading-4 text-default-500">{autoNote(s)}</p>
      </div>

      <div className="space-y-2" data-phone-connect-block="">
        <ConnectButtons full s={s} size="lg" />
        <ConnectState s={s} />
      </div>

      <PhoneForwardList rules={rules} s={s} />

      <ul className="list-disc space-y-0.5 pl-4 text-[11px] leading-4 text-default-500">
        <li>
          一台手机 = 商家的一台设备
          {dev ? `（现在在线 ${dev.count} / ${dev.limit}）` : ""}
          ：名额满了会连不上。
        </li>
        <li>这台手机上的其他利群 App 要先关掉：它们用同样的端口。</li>
        <li>只在这台手机上生效：电脑和别的设备照常走服务器主入口。</li>
      </ul>

      <PhoneMore s={s} />
    </div>
  );
}

// ---- 入口机 list ----

/** Desktop 入口机 tile of 手机本地 (same look as the host tiles). */
export function PhoneHostTile({ onOpen }: { onOpen: () => void }) {
  const ctx = usePhoneCtx();
  const s = ctx?.state || null;
  // Plan 301: one status pill only.
  const pill = phoneEntryPill(s);

  return (
    <button
      className="flex min-w-0 flex-col gap-1.5 rounded-xl border border-divider bg-default-50/60 p-2.5 text-left transition-colors hover:border-teal-500/40 hover:bg-teal-500/5 dark:bg-default-50/30"
      data-leikwan-host-tile="phone"
      type="button"
      onClick={onOpen}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <PhoneDot s={s} />
        <span className="min-w-0 whitespace-nowrap text-[13px] font-semibold">
          {PHONE_NAME}
        </span>
        <Smartphone
          className="shrink-0 text-violet-600 dark:text-violet-300"
          size={13}
        />
      </span>
      <span className="flex flex-wrap gap-1">
        <Pill tone={pill.tone}>{pill.text}</Pill>
      </span>
    </button>
  );
}

/** H5 入口机 row of 手机本地 (＋ = its details). */
export function PhoneHostRow({
  open,
  onToggle,
  children,
}: {
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  const ctx = usePhoneCtx();
  const s = ctx?.state || null;
  // Plan 301: one status pill only (就绪 / 连接中 x/y / not connected).
  const pill = phoneEntryPill(s);

  return (
    <LkExpandCard
      chip={
        <span className="inline-flex shrink-0 items-center gap-1">
          <Pill tone={pill.tone}>{pill.text}</Pill>
        </span>
      }
      data-leikwan-host-row="phone"
      dot={<PhoneDot s={s} />}
      name={
        <span className="inline-flex items-center gap-1 whitespace-nowrap">
          {PHONE_NAME}
          <Smartphone
            className="text-violet-600 dark:text-violet-300"
            size={13}
          />
        </span>
      }
      open={open}
      onToggle={onToggle}
    >
      {children}
    </LkExpandCard>
  );
}

const fmtTime = (ms?: number) =>
  ms
    ? new Date(ms).toLocaleString("zh-CN", {
        timeZone: "Asia/Taipei",
        hour12: false,
      })
    : "—";

/**
 * Plan 315 (user 11-26): 入口机 → 手机本地 shows information only, like a
 * server entry (address, one status pill, 版本, 支持功能, 日志快切, 预热池,
 * 转发 就绪 x/y). Connecting and every setting live in 授权 → 我的入口.
 */
export function PhoneDetails({ compact = false }: { compact?: boolean }) {
  const ctx = usePhoneCtx();
  const kvw = compact ? "w-[5.25rem]" : "w-28";
  const s = ctx?.state || null;
  const info = ctx?.info || null;
  const pill = phoneEntryPill(s);
  const running = phoneRunning(s);
  const caps = phoneCaps(info);

  return (
    <div className="space-y-2" data-leikwan-phone-details="">
      <div className="flex flex-wrap items-center gap-1.5 text-xs text-default-500">
        <span className="font-mono">{PHONE_HOST}</span>
        {s?.startedAt && running && <span>{fmtTime(s.startedAt)} 起 台湾</span>}
      </div>
      <div className="divide-y divide-divider/60">
        <KV label="利群客户端" labelClassName={kvw}>
          <Pill tone={pill.tone}>{pill.text}</Pill>
          <Pill tone="slate">手机 App</Pill>
          {info && (
            <span className="text-xs text-default-500">{info.device.name}</span>
          )}
        </KV>
        <KV label="版本" labelClassName={kvw}>
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
            </>
          ) : (
            <span className="text-xs text-default-500">读取中</span>
          )}
        </KV>
        <KV label="支持功能" labelClassName={kvw}>
          <ClientPill caps={caps} />
          <CapPills phone caps={caps} />
        </KV>
        {phoneFull(info) && (
          <KV label="日志快切" labelClassName={kvw}>
            {s?.plan ? (
              <>
                <Pill
                  title="触发条件、预热池大小、备线都按面板给这个入口的设置"
                  tone="green"
                >
                  跟 {s.plan.ref || "入口机"} 一样
                </Pill>
                <Pill tone="slate">{s.plan.forwards} 条转发</Pill>
              </>
            ) : (
              <Pill title="打开利群汇聚页面时面板会把设置发给 App" tone="amber">
                还没拿到面板的设置（现在只有预热池）
              </Pill>
            )}
          </KV>
        )}
        <KV label="预热池" labelClassName={kvw}>
          {s?.warm.supported ? (
            <Pill tone={s.warm.on ? "green" : "slate"}>
              {s.warm.on ? "开" : "关"}
            </Pill>
          ) : (
            <span className="text-xs text-default-500">这台手机不支持</span>
          )}
        </KV>
        <KV label="转发" labelClassName={kvw}>
          <span className="text-xs">
            {running && s?.total ? `就绪 ${s.ready} / ${s.total}` : "—"}
          </span>
        </KV>
      </div>
      {ctx?.openConnect && (
        <button
          className="text-xs text-default-500 underline-offset-2 hover:underline"
          data-phone-goto-grants=""
          type="button"
          onClick={() => ctx.openConnect?.()}
        >
          连接和设置在 授权 → 我的入口 ›
        </button>
      )}
    </div>
  );
}

export function PhoneDrawer({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const ctx = usePhoneCtx();
  const s = ctx?.state || null;

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
            <PhoneDot s={s} />
            {PHONE_NAME}
            <Smartphone
              className="text-violet-600 dark:text-violet-300"
              size={15}
            />
            {ctx?.local && <Pill tone="teal">当前入口</Pill>}
          </span>
        </ModalHeader>
        <ModalBody>
          <PhoneDetails />
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
 * Plan 301 (admin, App 1.0.10-panel): hand the App the phone plan — the
 * values 手机本地's 日志快切 / 备线 / 预热池 run with — when 利群汇聚 opens and
 * after each reload (`rev` changes). One panel read per load, no polling; the
 * App ignores an unchanged plan.
 */
export function usePhonePlanSync(enabled: boolean, rev: unknown) {
  const last = useRef("");

  useEffect(() => {
    if (!enabled) return;
    let stop = false;

    void getLeiKwanPhonePlan().then(async (res) => {
      if (stop || res.code !== 0 || !res.data) return;
      const key = JSON.stringify({ ...res.data, updatedAt: 0 });

      if (key === last.current) return;
      try {
        await appCall(
          "setPlan",
          res.data as unknown as Record<string, unknown>,
        );
        last.current = key;
      } catch {
        /* an older App: it keeps its 预热池 only */
      }
    });

    return () => {
      stop = true;
    };
  }, [enabled, rev]);
}
