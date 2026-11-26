import type {
  LeiKwanEntryCaps,
  LeiKwanEntryLimit,
  LeiKwanPhoneReq,
  LeiKwanRule,
} from "@/api/leikwan-line";
import type { ReactNode } from "react";
import type { AppInfo, PhoneForward, PhoneState } from "@/utils/app-bridge";

import { createContext, useContext, useEffect, useRef, useState } from "react";
import { Copy, Smartphone } from "lucide-react";
import toast from "react-hot-toast";

import { Pill } from "@/pages/landing/pill";
import { CapPills, ClientPill } from "@/pages/leikwan/panel/entry-state";
import { LkDot, LkExpandCard } from "@/pages/leikwan/panel/layout-parts";
import { copyText } from "@/pages/leikwan/panel/rule-actions";
import { KV } from "@/pages/leikwan/panel/shared";
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
          : "手机本地的预热池关着（授权 → 我的入口 → ⚙ 里打开）",
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

/** Runs an App call; a failure becomes a toast (null). */
export async function run<T>(p: Promise<T>, done?: string): Promise<T | null> {
  try {
    const v = await p;

    if (done) toast.success(done);

    return v;
  } catch (e) {
    toast.error((e as Error).message || "App 没有完成这个操作");

    return null;
  }
}

export function useAct() {
  const [busy, setBusy] = useState("");
  const act = async (key: string, p: () => Promise<unknown>, done?: string) => {
    setBusy(key);
    await run(p(), done);
    setBusy("");
  };

  return { busy, act };
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
  // Plan 315 r3: labels never wrap (「利群客户端」 at 360 px / 125 % text).
  const kvw = compact ? "w-[5.75rem] whitespace-nowrap" : "w-28";
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
              <Pill
                title={`App ${info.app.version}（versionCode ${info.app.versionCode}）`}
                tone="slate"
              >
                App {info.app.version}
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
