// Plan 294 / 297: the im.gamer Android App bridge (contract:
// docs/panel-app-bridge.md, the same file lives in the Android repo).
//
// App vs browser is decided by the injected bridge object, never by the
// User-Agent: the App injects `window.panelApp` (androidx.webkit
// addWebMessageListener, allowed origin = exactly the saved panel origin) into
// the panel's main frame only. Browsers (desktop or phone) never have it, so
// every App-only piece (手机本地, the App nav) stays hidden there.
//
// Plan 297: the page is the web page (fork.162) plus one entry 「手机本地」.
// Which entry THIS phone uses is the App's own choice (getEntry / setEntry,
// "local" | "server"); it is never written to the server grant, so the
// server 主入口 keeps serving the user's other devices.
//
// Wire format: JSON strings both ways, protocol version 1.
//   page → App   {"v":1,"id":7,"method":"connect","params":{}}
//   App → page   {"v":1,"id":7,"ok":true,"result":{…}} | {"v":1,"id":7,"ok":false,"error":{"code":"…","message":"…"}}
//   App → page   {"v":1,"event":"state","data":PhoneState}   (push, no id)

import { useEffect, useState } from "react";

export const APP_BRIDGE_VERSION = 1;

/** One LeiKwan forward as the phone's local client runs it. */
export interface PhoneForward {
  forwardId: number;
  /** vendor name / route of the forward, when the core knows it */
  name?: string;
  route?: string;
  /** local listen port on 127.0.0.1 (= the vendor port, no offset) */
  localPort: number;
  /** ready = listening and has a lease; connecting; failed (see reason); off = not started on the phone */
  state: "ready" | "connecting" | "failed" | "off";
  /** Chinese, user-facing (connect failure, port in use …) */
  reason?: string;
  /** 预热池 for this forward: true on, false off / auto-off, null = not supported */
  warm?: boolean | null;
  /** native UDP on this forward */
  udp?: boolean;
}

export interface PhoneState {
  /** idle = 未连接; partial = some forwards ready, some not */
  connection:
    | "idle"
    | "connecting"
    | "connected"
    | "partial"
    | "failed"
    | "disconnecting";
  /** the local link's service is running (断开 available; also while it retries) */
  running?: boolean;
  /** Chinese stage text while connecting (读取账号 / 连接商家 / …) */
  stage?: string;
  ready: number;
  total: number;
  /** last failure, Chinese (shown on the red tile and in the drawer) */
  error?: string;
  /** short machine code of the failure (diag code) */
  errorCode?: string;
  startedAt?: number;
  updatedAt: number;
  forwards: PhoneForward[];
  /**
   * Plan 297: this phone's entry — "local" = 手机本地 (picked in 换主入口,
   * stored in the App only), "server" = the grant's 主入口 (default).
   */
  entry: PhoneEntry;
  /** 「打开 App 自动连接」 (default off since plan 297; per phone) */
  autoConnect: boolean;
  /** why auto-connect did not run, Chinese (e.g. 电池没设成无限制) */
  autoConnectBlocked?: string;
  warm: { on: boolean; supported: boolean };
  /** hint = the phone brand's own extra switch (Chinese), when known */
  battery: { unrestricted: boolean; hint?: string };
  /** 客户端 ID / Agent Token entered on the phone (never sent to the panel) */
  credentials: { set: boolean; clientIdHint?: string };
  /** run log size (bytes, ≤ 2 MiB) */
  logBytes: number;
}

export type PhoneEntry = "local" | "server";

export interface AppInfo {
  app: { name: string; version: string; versionCode: number; flavor: string };
  core: { version: string; agentVersion?: string };
  device: { name: string };
  bridge: number;
  /** panel origin the App is locked to */
  origin: string;
  /** reserved calls the App knows but does not implement yet (v0: speedTest) */
  reserved?: string[];
}

export interface PhoneLog {
  text: string;
  bytes: number;
  truncated: boolean;
}

export type AppMethod =
  | "getInfo"
  | "getState"
  | "connect"
  | "disconnect"
  | "setAutoConnect"
  | "setWarm"
  | "getLog"
  | "copyLog"
  | "shareLog"
  | "openBatterySettings"
  | "editCredentials"
  | "changePanel"
  // Plan 297: this phone's entry (手机本地 / the server 主入口).
  | "getEntry"
  | "setEntry"
  // Reserved for 本机测速 (not in v0: the App answers not_implemented).
  | "speedTest";

interface InjectedBridge {
  postMessage: (msg: string) => void;
  addEventListener?: (
    type: "message",
    fn: (e: { data: unknown }) => void,
  ) => void;
  onmessage?: ((e: { data: unknown }) => void) | null;
}

declare global {
  interface Window {
    panelApp?: InjectedBridge;
  }
}

const injected = (): InjectedBridge | null => {
  const b = typeof window !== "undefined" ? window.panelApp : undefined;

  return b && typeof b.postMessage === "function" ? b : null;
};

/** True only inside the im.gamer App (bridge object injected). */
export const isAppMode = (): boolean => injected() !== null;

type Pending = {
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
  timer: number;
};

let seq = 0;
let wired = false;
const pending = new Map<number, Pending>();
const stateListeners = new Set<(s: PhoneState) => void>();
let lastState: PhoneState | null = null;

function onMessage(e: { data: unknown }) {
  let msg: {
    v?: number;
    id?: number;
    ok?: boolean;
    result?: unknown;
    error?: { code?: string; message?: string };
    event?: string;
    data?: unknown;
  };

  try {
    msg = typeof e.data === "string" ? JSON.parse(e.data) : (e.data as never);
  } catch {
    return;
  }
  if (!msg || msg.v !== APP_BRIDGE_VERSION) return;
  if (msg.event === "state" && msg.data) {
    lastState = msg.data as PhoneState;
    stateListeners.forEach((fn) => fn(lastState!));

    return;
  }
  if (typeof msg.id !== "number") return;
  const p = pending.get(msg.id);

  if (!p) return;
  pending.delete(msg.id);
  window.clearTimeout(p.timer);
  if (msg.ok) p.resolve(msg.result);
  else {
    const err = new Error(msg.error?.message || "App 没有完成这个操作");

    (err as Error & { code?: string }).code = msg.error?.code;
    p.reject(err);
  }
}

function wire(b: InjectedBridge) {
  if (wired) return;
  wired = true;
  if (b.addEventListener) b.addEventListener("message", onMessage);
  else b.onmessage = onMessage;
}

/** Calls the App; rejects outside the App or after `timeoutMs`. */
export function appCall<T = unknown>(
  method: AppMethod,
  params: Record<string, unknown> = {},
  timeoutMs = 15000,
): Promise<T> {
  const b = injected();

  if (!b) return Promise.reject(new Error("不在 App 里"));
  wire(b);
  const id = ++seq;

  return new Promise<T>((resolve, reject) => {
    const timer = window.setTimeout(() => {
      pending.delete(id);
      reject(new Error("App 没有回应"));
    }, timeoutMs);

    pending.set(id, {
      resolve: resolve as (v: unknown) => void,
      reject,
      timer,
    });
    b.postMessage(
      JSON.stringify({ v: APP_BRIDGE_VERSION, id, method, params }),
    );
  });
}

function publish(v: unknown) {
  const st = v as PhoneState | null;

  if (st && typeof st === "object" && Array.isArray(st.forwards)) {
    lastState = st;
    stateListeners.forEach((fn) => fn(st));
  }
}

/**
 * Plan 297: a call whose answer is the new PhoneState (connect, disconnect,
 * setAutoConnect, setWarm, setEntry) — every listener sees it at once (the
 * push event follows anyway).
 */
export async function appStateCall(
  method: AppMethod,
  params: Record<string, unknown> = {},
  timeoutMs = 15000,
): Promise<PhoneState> {
  const v = await appCall<PhoneState>(method, params, timeoutMs);

  publish(v);

  return v;
}

/**
 * Plan 297: pick this phone's entry. "local" connects at once only when the
 * App's 「打开 App 自动连接」 is on (and the battery is 无限制); "server"
 * disconnects 手机本地 (it is no longer the entry).
 */
export const setPhoneEntry = (entry: PhoneEntry) =>
  appStateCall("setEntry", { entry }, 30000);

/** Push updates of the phone's local link (also seeds from getState). */
export function onPhoneState(fn: (s: PhoneState) => void): () => void {
  const b = injected();

  if (!b) return () => {};
  wire(b);
  stateListeners.add(fn);

  return () => {
    stateListeners.delete(fn);
  };
}

/** The phone's local link state; null in a browser or before the first answer. */
export function usePhoneState(): PhoneState | null {
  const [s, setS] = useState<PhoneState | null>(() =>
    isAppMode() ? lastState : null,
  );

  useEffect(() => {
    if (!isAppMode()) return;
    const off = onPhoneState(setS);

    void appCall<PhoneState>("getState")
      .then((v) => {
        lastState = v;
        setS(v);
      })
      .catch(() => {});

    return off;
  }, []);

  return s;
}

/** App + core versions (null in a browser). */
export function useAppInfo(): AppInfo | null {
  const [info, setInfo] = useState<AppInfo | null>(null);

  useEffect(() => {
    if (!isAppMode()) return;
    void appCall<AppInfo>("getInfo")
      .then(setInfo)
      .catch(() => {});
  }, []);

  return info;
}

/** Plan 294: App mode shows only 利群汇聚 (v0); the start page there. */
export const APP_HOME = "/leikwan";

/** Paths the App's WebView may show (everything else goes to APP_HOME). */
export const appPathAllowed = (path: string) =>
  path === APP_HOME ||
  path.startsWith(APP_HOME + "/") ||
  path === "/" ||
  path === "/login" ||
  path === "/profile";
