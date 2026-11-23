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
          : "手机本地的预热池关着（入口机 → 手机本地 里打开）",
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
}: {
  s: PhoneState | null;
  size?: "sm" | "md";
}) {
  const { busy, act } = useAct();

  if (phoneRunning(s))
    return (
      <Button
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
      color="success"
      data-phone-connect=""
      isDisabled={!s?.credentials.set}
      isLoading={busy === "connect"}
      size={size}
      title={
        s?.credentials.set ? undefined : "先填商家账号（只存在这台手机上）"
      }
      variant="flat"
      onPress={() =>
        void act("connect", () => appStateCall("connect", {}, 30000))
      }
    >
      连接
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

function stateLine(s: PhoneState | null) {
  const sum = phoneSummary(s);

  if (s?.connection === "failed")
    return (
      <span className="text-red-700 dark:text-red-300">
        连接失败 · {s.error || "原因看运行日志"}
      </span>
    );
  if (s?.connection === "connecting") return <span>{sum.text}</span>;

  return <span>{sum.text}</span>;
}

/**
 * The 授权 area's connect panel while 手机本地 is this phone's entry:
 * state, 连接 / 断开, 「打开 App 自动连接」, the old App's three tip blocks
 * and the short notes.
 */
export function PhoneConnectPanel({
  rules,
  usage,
  onDetails,
}: {
  rules: LeiKwanRule[];
  usage?: LeiKwanUsageView | null;
  onDetails: () => void;
}) {
  const ctx = usePhoneCtx();
  const s = ctx?.state || null;
  const sum = phoneSummary(s);
  const connected = phoneConnected(s);
  const [tips, setTips] = useState<boolean | null>(null);
  const tipsOpen = tips ?? !connected;
  const dev = usage?.devices;

  if (!ctx?.local) return null;

  return (
    <div
      className="space-y-2.5 rounded-xl border border-violet-500/30 bg-violet-500/[0.05] p-3 dark:bg-violet-400/[0.07]"
      data-phone-connect-panel=""
      id="phone-connect-panel"
    >
      <div className="flex flex-wrap items-center gap-1.5">
        <Smartphone
          className="text-violet-600 dark:text-violet-300"
          size={15}
        />
        <span className="text-sm font-semibold">手机本地</span>
        <Pill tone={sum.tone}>{sum.text}</Pill>
        {s?.stage && s.connection === "connecting" && (
          <Pill tone="amber">{s.stage}</Pill>
        )}
        <span className="flex-1" />
        <button
          className="text-xs font-medium text-primary"
          type="button"
          onClick={onDetails}
        >
          详情
        </button>
      </div>
      <div className="text-xs text-default-600" data-phone-state-line="">
        {stateLine(s)}
      </div>
      {s && !s.credentials.set && (
        <div className="rounded-lg bg-amber-500/10 px-3 py-2 text-xs leading-5 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
          先填商家的客户端 ID 和 Agent
          Token：只存在这台手机上（系统加密），不会发给面板。
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <ConnectButtons s={s} />
        {s && !s.credentials.set && (
          <Button
            color="warning"
            size="sm"
            variant="flat"
            onPress={() => void run(appCall("editCredentials", {}, 600000))}
          >
            填写商家账号
          </Button>
        )}
        <AutoConnectSwitch s={s} />
      </div>
      <p className="text-[11px] leading-4 text-default-500">{autoNote(s)}</p>
      <div className="flex items-center gap-2">
        <span className="text-xs font-semibold text-default-600">
          用之前看一下
        </span>
        <button
          className="text-xs text-primary"
          data-phone-tips-toggle=""
          type="button"
          onClick={() => setTips(!tipsOpen)}
        >
          {tipsOpen ? "收起" : "展开"}
        </button>
        {!tipsOpen && !s?.battery.unrestricted && (
          <Pill tone="red">电池没设成无限制</Pill>
        )}
      </div>
      {tipsOpen && (
        <div className="space-y-2" data-phone-tips="">
          <BatteryTip s={s} />
          <NodeTip rules={rules} s={s} />
          <BypassTip />
        </div>
      )}
      <ul className="list-disc space-y-0.5 pl-4 text-[11px] leading-4 text-default-500">
        <li>
          一台手机 = 商家的一台设备
          {dev ? `（现在在线 ${dev.count} / ${dev.limit}）` : ""}
          ：名额满了会连不上，先在商家那边下线一台。
        </li>
        <li>
          这台手机上的其他利群 App（重仓利群、利群原版、官方
          App）要先关掉：它们用同样的端口。
        </li>
        <li>只在这台手机上生效：电脑和别的设备照常走服务器主入口。</li>
      </ul>
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
 * 手机本地 modules (desktop drawer body, H5 ＋ row body) — laid out like a
 * server entry's details (支持功能 from the same capability schema) plus the
 * phone-only parts.
 */
export function PhoneDetails({
  usage,
  rules,
  compact = false,
  onSetEntry,
}: {
  /** admin: the panel's vendor usage view (在线设备 x/y) */
  usage?: LeiKwanUsageView | null;
  rules: LeiKwanRule[];
  compact?: boolean;
  /** 「设为入口…」 (opens 换主入口 with 手机本地 picked) */
  onSetEntry?: () => void;
}) {
  const ctx = usePhoneCtx();
  const { busy, act } = useAct();
  const [log, setLog] = useState<PhoneLog | null>(null);
  const kvw = compact ? "w-[5.25rem]" : "w-28";
  const s = ctx?.state || null;
  const info = ctx?.info || null;
  const sum = phoneSummary(s);
  const running = phoneRunning(s);
  const dev = usage?.devices;
  const full = !!dev && dev.limit > 0 && dev.count >= dev.limit;
  const caps = phoneCaps(info);
  const ruleOf = (fid: number, port: number) =>
    rules.find((r) =>
      r.forwardId ? r.forwardId === fid : r.listenPort === port,
    );

  return (
    <div className="space-y-2" data-leikwan-phone-details="">
      <div className="flex flex-wrap items-center gap-1.5 text-xs text-default-500">
        <span className="font-mono">{PHONE_HOST}</span>
        <Pill tone={sum.tone}>{sum.text}</Pill>
        {ctx?.local ? (
          <Pill tone="teal">这台手机的入口</Pill>
        ) : (
          <Pill tone="slate">没选它当入口</Pill>
        )}
        {s?.startedAt && running && <span>{fmtTime(s.startedAt)} 起 台湾</span>}
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
      {!ctx?.local && (
        <p className="rounded-lg bg-slate-500/10 px-3 py-2 text-xs text-default-600 dark:bg-slate-400/10">
          要用手机本地：授权 → 换主入口 → 选「手机本地」（只在这台手机上生效）。
        </p>
      )}
      <div className="divide-y divide-divider/60">
        <KV label="利群客户端" labelClassName={kvw}>
          <Pill tone={sum.tone}>
            {s?.connection === "failed" ? "连接失败" : sum.text}
          </Pill>
          {(ctx?.local || running) && <ConnectButtons s={s} />}
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
            <LabeledSwitch
              isSelected={!!s.warm.on}
              onValueChange={(on) =>
                void act("warm", () => appStateCall("setWarm", { on }))
              }
            >
              {s.warm.on ? "开" : "关"}
            </LabeledSwitch>
          ) : (
            <span className="text-xs text-default-500">这台手机不支持</span>
          )}
        </KV>
        <KV label="自动连接" labelClassName={kvw}>
          <AutoConnectSwitch s={s} />
          <span className="w-full text-xs text-default-500">{autoNote(s)}</span>
        </KV>
        <KV label="电池" labelClassName={kvw}>
          {s?.battery.unrestricted ? (
            <Pill tone="green">无限制</Pill>
          ) : (
            <>
              <Pill tone="red">没设成无限制</Pill>
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
                  名额满了：这台手机是单独的一台设备，连接可能被商家拒绝（先在商家那边下线一台）
                </span>
              )}
            </>
          ) : (
            <span className="text-xs text-default-500">面板还没读到</span>
          )}
        </KV>
        <KV label="商家账号" labelClassName={kvw}>
          <Button
            color="warning"
            isLoading={busy === "cred"}
            size="sm"
            variant="flat"
            onPress={() =>
              void act("cred", () => appCall("editCredentials", {}, 600000))
            }
          >
            {s?.credentials.set
              ? `已填${s.credentials.clientIdHint ? ` ·…${s.credentials.clientIdHint}` : ""} · 修改`
              : "填写商家账号"}
          </Button>
          <span className="w-full text-xs text-default-500">
            客户端 ID + Agent Token 只存在这台手机上，不发给面板
          </span>
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

      <div className="flex flex-wrap items-center gap-2 pt-1 text-xs text-default-500">
        {info && <span>{info.device.name}</span>}
        {onSetEntry && !ctx?.local && (
          <Button color="primary" size="sm" variant="flat" onPress={onSetEntry}>
            设为入口…
          </Button>
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
  onSetEntry,
  ...rest
}: {
  open: boolean;
  onClose: () => void;
} & Parameters<typeof PhoneDetails>[0]) {
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
          <PhoneDetails {...rest} />
        </ModalBody>
        <ModalFooter>
          {onSetEntry && !ctx?.local && (
            <Button color="primary" variant="flat" onPress={onSetEntry}>
              设为入口…
            </Button>
          )}
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
