// Plan 187 公开探针: data layer. One snapshot every 2 s from the panel, the same
// bytes for every viewer. First paint comes from a plain GET fired before the
// page chunk even loads (see main.tsx), in parallel with the live socket.
import { useEffect, useState } from "react";

export interface ProbeLatency {
  to: string;
  ms: number;
  ok: boolean;
}

export interface ProbeNode {
  k: string;
  name: string;
  region: string;
  online: boolean;
  os: string;
  arch: string;
  seen: number;
  cpu: number;
  load1: number;
  mem: number;
  disk: number;
  net_in: number;
  net_out: number;
  traffic_in: number;
  traffic_out: number;
  uptime: number;
  tcp: number;
  latency?: ProbeLatency[];
  /** Plan 189: indexes into ProbeSnapshot.groups (absent = in no group). */
  g?: number[];
  /**
   * Plan 194: whole days (UTC+8) until the next due date, 0 = today,
   * negative = expired; absent = no expiry set or 显示到期天数 off.
   */
  days?: number;
  /** Plan 194: renewal cycle token (absent = not set). */
  cycle?: ProbeCycle;
}

export type ProbeCycle = "month" | "quarter" | "halfyear" | "year";

export interface ProbeGroup {
  name: string;
}

export type ProbeGroupMode = "region" | "group";

export interface ProbeSnapshot {
  title: string;
  ts: number;
  latency_on: boolean;
  nodes: ProbeNode[];
  /** Plan 189: only groups with a shown machine; absent when none. */
  groups?: ProbeGroup[];
  group_mode?: ProbeGroupMode;
}

export type ProbeStatus = "loading" | "ok" | "off" | "error";

export interface ProbeState {
  snap: ProbeSnapshot | null;
  status: ProbeStatus;
  /** false while frames come from the fallback GET poll. */
  live: boolean;
}

const BASE = (
  (import.meta.env.VITE_API_BASE as string | undefined) || ""
).replace(/\/$/, "");

export const probeUrl = (path: string) => `${BASE}/api/v1/probe/${path}`;

const GZIP = typeof DecompressionStream === "function";

function wsUrl(): string {
  const origin = BASE || window.location.origin;

  return `${origin.replace(/^http/, "ws")}/api/v1/probe/ws${GZIP ? "?gzip=1" : ""}`;
}

function frameText(data: string | Blob): Promise<string> {
  if (typeof data === "string") return Promise.resolve(data);

  return new Response(
    data.stream().pipeThrough(new DecompressionStream("gzip")),
  ).text();
}

/**
 * Header sparkline: total ↓↑ of online nodes, one point per snapshot, 3 min.
 * Module level, so it survives the page unmounting (detail open, navigation).
 */
export const speedHistory: { t: number; rx: number; tx: number }[] = [];
const SPEED_KEEP = 90;

function sample(snap: ProbeSnapshot) {
  const last = speedHistory[speedHistory.length - 1];

  if (last && last.t === snap.ts) return;
  let rx = 0;
  let tx = 0;

  for (const n of snap.nodes) {
    if (!n.online) continue;
    rx += n.net_in;
    tx += n.net_out;
  }
  speedHistory.push({ t: snap.ts, rx, tx });
  if (speedHistory.length > SPEED_KEEP) speedHistory.shift();
}

function sameLatency(a?: ProbeLatency[], b?: ProbeLatency[]) {
  if (a === b) return true;
  if (!a || !b || a.length !== b.length)
    return (a?.length ?? 0) === 0 && (b?.length ?? 0) === 0;
  for (let i = 0; i < a.length; i++) {
    if (a[i].to !== b[i].to || a[i].ms !== b[i].ms || a[i].ok !== b[i].ok)
      return false;
  }

  return true;
}

function sameNums(a?: number[], b?: number[]) {
  if (a === b) return true;
  if ((a?.length ?? 0) !== (b?.length ?? 0)) return false;
  for (let i = 0; i < (a?.length ?? 0); i++) if (a![i] !== b![i]) return false;

  return true;
}

const groupSig = (s: ProbeSnapshot) =>
  `${s.group_mode ?? ""}|${(s.groups ?? []).map((g) => g.name).join("\u0000")}`;

const SCALARS: (keyof ProbeNode)[] = [
  "name",
  "region",
  "online",
  "os",
  "arch",
  "seen",
  "cpu",
  "load1",
  "mem",
  "disk",
  "net_in",
  "net_out",
  "traffic_in",
  "traffic_out",
  "uptime",
  "tcp",
];

/** Keep the previous object of every node whose visible data did not change,
 *  so memoized cards skip the render. Returns prev itself if nothing changed. */
export function mergeSnapshot(
  prev: ProbeSnapshot | null,
  next: ProbeSnapshot,
): ProbeSnapshot {
  if (!prev) return next;
  const old = new Map(prev.nodes.map((n) => [n.k, n]));
  let changed =
    prev.nodes.length !== next.nodes.length ||
    prev.title !== next.title ||
    prev.latency_on !== next.latency_on ||
    groupSig(prev) !== groupSig(next);
  const nodes = next.nodes.map((n, i) => {
    const o = old.get(n.k);

    if (
      o &&
      SCALARS.every((f) => o[f] === n[f]) &&
      sameLatency(o.latency, n.latency) &&
      sameNums(o.g, n.g)
    ) {
      if (prev.nodes[i] !== o) changed = true;

      return o;
    }
    changed = true;

    return n;
  });

  if (!changed) return prev;

  return { ...next, nodes };
}

declare global {
  interface Window {
    __flvxProbeFirst?: Promise<Response>;
  }
}

const WATCHDOG_MS = 10_000;
const POLL_MS = 5_000;
const OFF_POLL_MS = 30_000;

/** Live probe data: WebSocket (2 s pushes) + GET fallback poll. */
export function useProbe(): ProbeState {
  const [state, setState] = useState<ProbeState>({
    snap: null,
    status: "loading",
    live: false,
  });

  useEffect(() => {
    let socket: WebSocket | null = null;
    let poll: ReturnType<typeof setInterval> | null = null;
    let pollEvery = 0;
    let retry: ReturnType<typeof setTimeout> | null = null;
    let silent: ReturnType<typeof setTimeout> | null = null;
    let epoch = 0;
    let gap = false;
    let attempts = 0;
    let etag = "";
    let off = false;
    let current: ProbeSnapshot | null = null;
    let live = false;

    const publish = (status: ProbeStatus) => {
      setState((s) =>
        s.snap === current && s.status === status && s.live === live
          ? s
          : { snap: current, status, live },
      );
    };
    const receive = (snap: ProbeSnapshot, fromSocket: boolean) => {
      if (!snap || !Array.isArray(snap.nodes)) return;
      if (gap) speedHistory.length = 0;
      gap = false;
      off = false;
      sample(snap);
      current = mergeSnapshot(current, snap);
      live = fromSocket || live;
      publish("ok");
    };
    const stopPoll = () => {
      if (poll) clearInterval(poll);
      poll = null;
      pollEvery = 0;
    };
    const startPoll = (every: number) => {
      if (poll && pollEvery === every) return;
      stopPoll();
      pollEvery = every;
      poll = setInterval(() => void fetchOnce(), every);
    };
    const switchedOff = () => {
      off = true;
      live = false;
      closeSocket();
      if (retry) clearTimeout(retry);
      retry = null;
      startPoll(OFF_POLL_MS);
      publish("off");
    };
    const fetchOnce = async (first?: Promise<Response>) => {
      const started = epoch;

      try {
        const res = await (first ??
          fetch(probeUrl("snapshot"), {
            headers: etag ? { "If-None-Match": etag } : undefined,
            cache: "no-cache",
          }));

        if (started !== epoch) return;
        if (res.status === 404) {
          switchedOff();

          return;
        }
        if (res.status === 304) return;
        if (!res.ok) throw new Error(String(res.status));
        etag = res.headers.get("ETag") || "";
        const body = await res.json();

        if (started !== epoch || body?.code !== 0) return;
        const wasOff = off;

        receive(body.data, false);
        if (wasOff) {
          stopPoll();
          connect();
        }
      } catch {
        if (started === epoch && !current) publish("error");
      }
    };
    const closeSocket = () => {
      if (silent) clearTimeout(silent);
      silent = null;
      if (socket) {
        socket.onclose = null;
        socket.onmessage = null;
        socket.onerror = null;
        socket.close();
        socket = null;
      }
    };
    const watch = () => {
      if (silent) clearTimeout(silent);
      // No frame for 10 s: some proxies freeze idle sockets without closing
      // them. Reconnect quietly; the slow GET poll covers the gap.
      silent = setTimeout(() => {
        live = false;
        startPoll(POLL_MS);
        closeSocket();
        scheduleReconnect();
      }, WATCHDOG_MS);
    };
    const scheduleReconnect = () => {
      if (retry || off) return;
      const delay = Math.min(60_000, 2_000 * 2 ** Math.min(attempts, 5));

      attempts++;
      retry = setTimeout(() => {
        retry = null;
        connect();
      }, delay);
    };
    const connect = () => {
      if (off || document.hidden) return;
      closeSocket();
      let ws: WebSocket;

      try {
        ws = new WebSocket(wsUrl());
      } catch {
        startPoll(POLL_MS);

        return;
      }
      ws.binaryType = "blob";
      socket = ws;
      let decoded = Promise.resolve();

      ws.onmessage = (event) => {
        decoded = decoded
          .then(async () => {
            const text = await frameText(event.data);

            if (socket !== ws) return;
            const body = JSON.parse(text);

            if (body?.code !== 0) return;
            attempts = 0;
            watch();
            stopPoll();
            receive(body.data, true);
          })
          .catch(() => undefined);
      };
      ws.onerror = () => ws.close();
      ws.onclose = (event) => {
        if (socket !== ws) return;
        socket = null;
        if (silent) clearTimeout(silent);
        silent = null;
        live = false;
        if (event.code === 4404) {
          switchedOff();

          return;
        }
        // Refused (busy / limits) or dropped: GET every 5 s, retry later.
        startPoll(POLL_MS);
        publish(current ? "ok" : "loading");
        scheduleReconnect();
      };
      watch();
    };
    const pause = () => {
      epoch++;
      closeSocket();
      stopPoll();
      if (retry) clearTimeout(retry);
      retry = null;
    };
    const resume = (first?: Promise<Response>) => {
      pause();
      gap = current !== null;
      attempts = 0;
      void fetchOnce(first);
      connect();
    };
    const onVisibility = () => (document.hidden ? pause() : resume());

    document.addEventListener("visibilitychange", onVisibility);
    const first = window.__flvxProbeFirst;

    window.__flvxProbeFirst = undefined;
    resume(first);

    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      pause();
    };
  }, []);

  return state;
}

/** Probe-only domain check (cached per host). True → no login link. */
export async function isProbeOnlyHost(): Promise<boolean> {
  const key = `flvx_probe_host:${window.location.hostname}`;

  try {
    const res = await fetch(probeUrl("host"), { cache: "no-store" });
    const yes = res.ok && (await res.json())?.data?.probeOnly === true;

    localStorage.setItem(key, yes ? "1" : "0");

    return yes;
  } catch {
    return localStorage.getItem(key) === "1";
  }
}

export interface ProbeSeries {
  k: string;
  range: string;
  step: number;
  points: [number, number, number, number, number, number][];
  latency?: { to: string; points: [number, number][] }[];
}

const seriesCache = new Map<string, { at: number; data: ProbeSeries }>();

export async function fetchSeries(
  k: string,
  range: string,
  points: number,
): Promise<ProbeSeries | null> {
  const key = `${k}|${range}|${points}`;
  const hit = seriesCache.get(key);

  if (hit && Date.now() - hit.at < 30_000) return hit.data;
  const res = await fetch(
    probeUrl(
      `series?k=${encodeURIComponent(k)}&range=${range}&points=${points}`,
    ),
    { cache: "no-cache" },
  );

  if (!res.ok) return null;
  const body = await res.json();

  if (body?.code !== 0) return null;
  if (seriesCache.size > 64) seriesCache.clear();
  seriesCache.set(key, { at: Date.now(), data: body.data });

  return body.data;
}
