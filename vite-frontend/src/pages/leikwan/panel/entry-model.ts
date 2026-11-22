import type { LeiKwanGrant, LeiKwanRule } from "@/api/leikwan-line";
import type { LeiKwanHostItem } from "@/pages/leikwan/panel/host-manage";
import type { PhoneForward, PhoneState } from "@/utils/app-bridge";

import { useMemo, useState } from "react";

// Plan 294: one 入口机 = one way into the 利群 forwards — a front machine of the
// 「利群入口」 group (火山广州 / 百度广州 / 阿里广州 / 火山上海) or, in the im.gamer
// App only, 手机本地 (the phone's own LeiKwan client on 127.0.0.1). Selecting an
// entry re-renders the 专线 · 转发 tree for it: same forwards, per-entry chips.
//
// One schema for every entry and forward (EntryRuleView):
//   chip "on"   = supported on this entry and switched on  → coloured
//   chip "off"  = supported, switched off                    → grey
//   chip null   = not supported on this entry                → hidden
//   open false  = the forward does not run on this entry     → row greyed 「此入口未开」
// Front machines: the panel's existing host status (host/list snapshot + the
// live agent report, rule.hostValues, 只开主入口). 手机本地: the App bridge.

export type EntryKey = "phone" | number;
export type ChipState = "on" | "off" | null;

export interface EntryOption {
  key: EntryKey;
  phone: boolean;
  name: string;
  /** IP the clients dial (127.0.0.1 for 手机本地) */
  host: string;
  front?: LeiKwanHostItem;
}

export interface EntryRuleView {
  open: boolean;
  /** why 「此入口未开」 (title of the grey chip) */
  closedReason?: string;
  /** host:port on this entry */
  address: string;
  warm: { state: ChipState; label: string; title?: string };
  logswitch: ChipState;
  /** the 备线 switch (日志快切 moves to the FLVX 备线) */
  backup: ChipState;
  udp: { state: ChipState; label: string };
  /** 手机本地: this forward on the phone */
  phone?: PhoneForward;
}

const joinHost = (host: string, port: number) =>
  host.includes(":") && !host.startsWith("[")
    ? `[${host}]:${port}`
    : `${host}:${port}`;

export const phoneConnected = (s: PhoneState | null) =>
  !!s && (s.connection === "connected" || s.connection === "partial");

/** The phone's forward for a rule (admins match the LeiKwan forward id, users the vendor port). */
export function phoneForward(
  s: PhoneState | null,
  r: LeiKwanRule,
): PhoneForward | undefined {
  if (!s) return undefined;

  return s.forwards.find((f) =>
    r.forwardId ? f.forwardId === r.forwardId : f.localPort === r.listenPort,
  );
}

function phoneView(s: PhoneState | null, r: LeiKwanRule): EntryRuleView {
  const f = phoneForward(s, r);
  const known = !!s && s.forwards.length > 0;
  const warmOn = !!s?.warm.on;
  let warm: EntryRuleView["warm"] = { state: null, label: "" };

  if (s?.warm.supported && (!f || f.warm !== null)) {
    warm =
      warmOn && f?.warm !== false
        ? { state: "on", label: "预热" }
        : {
            state: "off",
            label: warmOn ? "预热 已自动关" : "不预热",
            title: warmOn
              ? "这条转发的落地会提前断开空闲连接，手机上的预热自动关了"
              : "手机本地的预热池关着（详情里打开）",
          };
  }

  return {
    open: !known || !!f,
    closedReason:
      known && !f ? "手机本地的利群客户端里没有这条转发" : undefined,
    address: joinHost("127.0.0.1", f?.localPort || r.listenPort),
    warm,
    // 手机本地 has no 日志快切 / 备线 / 首包合并 (the hosts' own designs).
    logswitch: null,
    backup: null,
    udp: f?.udp ? { state: "on", label: "UDP" } : { state: null, label: "" },
    phone: f,
  };
}

function frontView(
  h: LeiKwanHostItem,
  r: LeiKwanRule,
  entryLock: boolean,
): EntryRuleView {
  const id = h.node.id;
  const fid = r.forwardId;
  const own = r.entry?.nodeId === id;
  const address =
    own && r.entry ? r.entry.address : joinHost(h.node.host, r.listenPort);
  const hv = r.hostValues?.find((v) => v.nodeId === id);
  const snap = h.snapshot;
  const live = h.manage?.state?.forwards || null;
  const liveF = fid ? live?.find((f) => f.forwardId === fid) : undefined;
  const snapF = fid
    ? snap?.forwards?.find((f) => f.forwardId === fid)
    : undefined;
  let open = true;
  let closedReason: string | undefined;

  if (h.node.leikwanStatus && h.node.leikwanStatus !== "running") {
    open = false;
    closedReason = `${h.node.name} 的利群客户端没在运行`;
  } else if (fid && live && live.length > 0) {
    if (!liveF) {
      open = false;
      closedReason = `${h.node.name} 的利群客户端里没有这条转发`;
    } else if (!liveF.running) {
      open = false;
      closedReason = `${h.node.name} 上这条转发没在跑`;
    }
  } else if (fid && snap?.forwards && snap.forwards.length > 0) {
    if (!snapF || snapF.state === "stopped") {
      open = false;
      closedReason = `${h.node.name} 上没有在跑这条转发（主机快照）`;
    }
  }
  if (open && entryLock && !r.allEntries && r.entry && !own) {
    open = false;
    closedReason = `只开主入口：这条转发只在 ${r.entry.nodeName} 能连`;
  }

  // 预热: the host's own warm file (via the snapshot), not the panel default.
  const hostWarm = !!snap?.warm && Object.keys(snap.warm).length > 0;
  const warmMain = hv?.warmMain;
  let warm: EntryRuleView["warm"] = { state: null, label: "" };

  if (typeof warmMain === "number" && warmMain > 0)
    warm = {
      state: "on",
      label: `预热 ${warmMain}`,
      title: `${h.node.name} 上这条转发预热 ${warmMain} 条${hv?.warmBackup ? `，备线 ${hv.warmBackup}` : ""}`,
    };
  else if (hostWarm || typeof warmMain === "number")
    warm = {
      state: "off",
      label: "不预热",
      title: `${h.node.name} 上这条转发不预热`,
    };

  const hasLogswitch = !!(snap?.logswitch || h.manage?.state?.logswitch);
  const udp = liveF?.udp || "";

  return {
    open,
    closedReason,
    address,
    warm,
    logswitch: hasLogswitch ? (hv?.logswitch ? "on" : "off") : null,
    backup: hasLogswitch ? (hv?.backup ? "on" : "off") : null,
    udp: !udp
      ? { state: null, label: "" }
      : udp === "native_udp"
        ? { state: "on", label: "原生 UDP" }
        : { state: "off", label: "UDP 非原生" },
  };
}

export function entryRuleView(
  entry: EntryOption | undefined,
  r: LeiKwanRule,
  ctx: { phone: PhoneState | null; entryLock: boolean },
): EntryRuleView | null {
  if (!entry) return null;
  if (entry.phone) return phoneView(ctx.phone, r);
  if (entry.front) return frontView(entry.front, r, ctx.entryLock);

  // A user's own 主入口 (no host internals for users): what the list says.
  return {
    open: true,
    address: r.entry?.address || joinHost(entry.host, r.listenPort),
    warm: { state: null, label: "" },
    logswitch: null,
    backup: null,
    udp: { state: null, label: "" },
  };
}

/**
 * The page's entry list and selection. Default: App → 手机本地 once it is
 * connected (the 主入口 before that); browser → the grant's 主入口. A tap
 * picks an entry for as long as the page is open (never stored).
 */
export function useLeiKwanEntries({
  app,
  phone,
  hosts,
  mainEntryId,
  userEntry,
}: {
  app: boolean;
  phone: PhoneState | null;
  /** admin: the 「利群入口」 hosts */
  hosts?: LeiKwanHostItem[];
  /** the grant's 主入口 node id */
  mainEntryId?: number;
  /** user view: the own 主入口 (no host list) */
  userEntry?: { nodeId: number; name: string; host: string };
}) {
  const [picked, setPicked] = useState<EntryKey | null>(null);
  const options = useMemo<EntryOption[]>(() => {
    const out: EntryOption[] = [];

    if (app)
      out.push({
        key: "phone",
        phone: true,
        name: "手机本地",
        host: "127.0.0.1",
      });
    (hosts || []).forEach((h) =>
      out.push({
        key: h.node.id,
        phone: false,
        name: h.node.name,
        host: h.node.host,
        front: h,
      }),
    );
    if (!hosts && userEntry)
      out.push({
        key: userEntry.nodeId,
        phone: false,
        name: userEntry.name,
        host: userEntry.host,
      });

    return out;
  }, [app, hosts, userEntry]);

  const main =
    options.find((o) => o.key === mainEntryId) ||
    options.find(
      (o) => !o.phone && o.front?.node.leikwanStatus === "running",
    ) ||
    options.find((o) => !o.phone);
  const auto: EntryKey | undefined =
    app && (phoneConnected(phone) || !main) ? "phone" : main?.key;
  const key =
    picked !== null && options.some((o) => o.key === picked) ? picked : auto;
  const selected = options.find((o) => o.key === key);

  return {
    options,
    selected,
    /** true while the default is in force (nothing tapped yet) */
    auto: picked === null,
    select: (k: EntryKey) => setPicked(k),
  };
}

export type LeiKwanEntries = ReturnType<typeof useLeiKwanEntries>;

/** The admin's own 主入口 (their grant), else the entry most grants use. */
export function adminMainEntry(
  grants: LeiKwanGrant[],
  me: string | null,
): number | undefined {
  const own = grants.find((g) => me && g.userName === me);

  if (own?.mainEntryNodeId) return own.mainEntryNodeId;
  const count = new Map<number, number>();

  grants.forEach((g) =>
    count.set(g.mainEntryNodeId, (count.get(g.mainEntryNodeId) || 0) + 1),
  );
  let best: number | undefined;
  let n = 0;

  count.forEach((c, id) => {
    if (c > n) {
      n = c;
      best = id;
    }
  });

  return best;
}
