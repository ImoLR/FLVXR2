import type { PhonePlan } from "@/utils/app-bridge";

import Network from "@/api/network";
import { getToken, isAdmin } from "@/utils/auth";

// Plan 266 利群专线入面板 (step 1).

export interface LeiKwanWarm {
  main: number;
  backup: number;
  standby: number;
  paceMs: number;
  flush: boolean;
  burst: string;
}

export interface LeiKwanAccel {
  warm: LeiKwanWarm;
  ssFilter: "enforce" | "off";
  logswitch: boolean;
  returnCheck: boolean;
}

export interface LeiKwanRuleAccelOverride {
  warmMain?: number;
  ssFilter?: string;
  logswitch?: boolean;
  returnCheck?: boolean;
  note?: string;
}

export interface LeiKwanGlobal {
  holdS: number;
  leaseSettleS: number;
  grace: string;
  returnCheckS: number;
  ioError: string;
  coalesce: string;
  coalesceByNode: Record<string, string>;
  // Plan 269: restarts / nft changes on the entry hosts only 02:00–06:00 台湾
  // (default off: they run right away).
  nightOnly?: boolean;
  // Plan 272: 「只开主入口」 — a 利群 rule only connects on its owner's 主入口
  // (old entry kept until a subscription refresh or 24 h; default off).
  entryLock?: boolean;
}

export interface LeiKwanHostValue {
  nodeId: number;
  nodeName: string;
  warmMain?: number | null;
  warmBackup?: number | null;
  ssFilter: string;
  logswitch: boolean;
  backup: string;
  returnCheck: string;
}

export interface LeiKwanEntryNode {
  id: number;
  name: string;
  host: string;
  online: boolean;
  leikwanStatus: string;
  leikwanForwards: number;
  /** Plan 295: when the node went 离线 (ms; absent = online). */
  offlineSince?: number;
}

/** Plan 295: what one 利群入口 can run (its learned profile files). */
export interface LeiKwanEntryCaps {
  nodeId: number;
  name: string;
  host?: string;
  online: boolean;
  offlineSince?: number;
  /** profile learned (false = the panel does not know, claims nothing) */
  known: boolean;
  managed: boolean;
  front?: string;
  frontBuild?: string;
  /** 「稳定版」 / 「测试版」 / 「未知」 */
  client: string;
  warm: boolean;
  ssFilter: boolean;
  logswitch: boolean;
  triggers: boolean;
  phone: boolean;
  /** 「预热池」「首包过滤」「日志快切」「快切触发」「手机备线」 the host lacks */
  missing: string[];
}

/** Plan 295: something a rule wants that its owner's 主入口 cannot run. */
export interface LeiKwanEntryLimit {
  key: "warm" | "ssfilter" | "logswitch" | "triggers" | "phone" | string;
  label: string;
  why: string;
}

/** Plan 295: one feature of a forward that changes on the new 主入口. */
export interface LeiKwanSwitchChange {
  key: string;
  label: string;
  from: string;
  to: string;
  lost?: boolean;
  gain?: boolean;
  why?: string;
}

export interface LeiKwanGrantPreview {
  userId: number;
  from?: LeiKwanEntryCaps;
  to: LeiKwanEntryCaps;
  rules: {
    id: number;
    name: string;
    forwardId: number;
    lineName: string;
    changes: LeiKwanSwitchChange[];
    kept: string[];
  }[];
  lostRules: number;
  gainRules: number;
  notes: string[];
  same: boolean;
  /** Plan 297: the preview is for 手机本地 (im.gamer App, phone-only). */
  local?: boolean;
}

/** Plan 297: 手机本地 as the App reports it (preview / rule list). */
export interface LeiKwanPhoneReq {
  /** the App's 预热池 switch */
  warm: boolean;
  /** the App's version */
  app?: string;
  /** Plan 301: AppInfo.features (1.0.10-panel runs 日志快切 / 备线 itself) */
  features?: string[];
}

export interface LeiKwanTrigger {
  key: string;
  name: string;
  source: string;
  recover?: string;
  note?: string;
}

export interface LeiKwanLine {
  id: number;
  name: string;
  routeId: number;
  routeLabel: string;
  transport: string;
  backupTunnelId: number;
  backupTunnelName: string;
  accel: LeiKwanAccel;
  inx: number;
  status: number;
  ruleCount: number;
  forwardIds: number[];
  hostValues: {
    forwardId: number;
    ruleName: string;
    values: LeiKwanHostValue[];
  }[];
  // Plan 268: auto / linked backups and how many 「按新备线重建」 would change.
  backupStats?: { auto: number; linked: number; stale: number };
  // Plan 269: 利群流量 billing, same meaning as tunnels (1 单向 / 2 双向).
  flowMode?: number;
  trafficRatio?: number;
}

// Plan 268: any tunnel can be a line's 备线隧道; path + 线路分组 + the
// 前置 → backup-entry latency the panel already measured.
export interface LeiKwanBackupTunnel {
  id: number;
  name: string;
  type: number;
  entryNames: string;
  entryNodeIds?: number[];
  hops?: string[];
  exits?: string[];
  groups?: { id: number; name: string; color: string; order: number }[];
  frontLatency?: {
    frontId: number;
    entryId: number;
    entryName: string;
    ms: number;
    ok: boolean;
    same?: boolean;
    far?: boolean;
  }[];
}

export interface LeiKwanFront {
  id: number;
  name: string;
  grants: number;
}

export interface LeiKwanQuota {
  limit: number;
  active: number;
  error?: string;
}

export interface LeiKwanLineList {
  setting: {
    accountId: number;
    entryGroupId: number;
    entryGroupName: string;
    groupName: string;
    global: LeiKwanGlobal;
  };
  entryNodes: LeiKwanEntryNode[];
  lines: LeiKwanLine[];
  pipes: {
    // Plan 270: id + etag (delete an unbound pipe), rules on its tunnel.
    id?: number;
    etag?: string;
    forwardId: number;
    listenPort: number;
    routeLabel: string;
    tunnelId?: number | null;
    tunnelName?: string;
    tunnelRules?: number;
  }[];
  quota: LeiKwanQuota;
  routes: { id: number; label: string; available: boolean }[];
  backupTunnels: LeiKwanBackupTunnel[];
  fronts?: LeiKwanFront[];
  frontFarMs?: number;
  triggers: LeiKwanTrigger[];
  accountReady: boolean;
}

export interface LeiKwanGrant {
  id: number;
  userId: number;
  /** Plan 297 (admin list): the user is an admin (not held by 只开主入口) */
  ownerAdmin?: boolean;
  userName?: string;
  mainEntryNodeId: number;
  mainEntryName?: string;
  mainEntryHost?: string;
  mainEntryOutsideGroup?: boolean;
  // Plan 295: the 主入口's 在线 / 离线.
  mainEntryOnline?: boolean;
  mainEntryOfflineSince?: number;
  backupLine: boolean;
  status: number;
  ruleCount: number;
  // Plan 271: the user's 备线流量 since their reset (a shared 备线 once).
  backupFlow?: number;
  // Plan 272: the previous 主入口 kept open after a change (until the user
  // refreshes a subscription, at most until `until`).
  grace?: LeiKwanGrantGrace | null;
}

export interface LeiKwanGrantGrace {
  prevEntryNodeId: number;
  prevEntryName: string;
  changedAt: number;
  until: number;
  waitingSub: boolean;
}

export interface LeiKwanRuleBackup {
  forwardId: number;
  mode: "auto" | "linked";
  name?: string;
  tunnelName?: string;
  remoteAddr?: string;
  entry?: string;
  inFlow?: number;
  outFlow?: number;
  status?: number;
  missing?: boolean;
  sharedWith?: number[];
}

/** Plan 310: a rule's 全程 latency tag value. */
export interface LeiKwanRuleLatency {
  ms: number;
  src: "udp" | "tcp";
  /** seconds since the client measured it (as of the list call) */
  ageS: number;
}

export interface LeiKwanRule {
  id: number;
  name: string;
  lineId: number;
  lineName: string;
  routeLabel: string;
  listenPort: number;
  transport: string;
  status: number;
  createdTime: number;
  backupOn: boolean;
  backupError?: string;
  // Plan 276 (F02): the 备线 exists but still points at the old landing (a
  // 换落地 could not move it); 「重试备线」 moves it.
  backupStale?: boolean;
  // Plan 268: the owner's account is past its 到期.
  userExpired?: boolean;
  landing: {
    // Plan 272: "external" = one of the owner's 外部节点 (name = its name,
    // protocol = a label such as "VLESS Reality").
    kind: "node" | "manual" | "external";
    landingNodeId?: number;
    externalNodeId?: number;
    host: string;
    port: number;
    name?: string;
    serverName?: string;
    protocol?: string;
  };
  // Plan 272: full = a complete node link exists (node / external);
  // address = manual landing (only the 入口地址).
  linkKind?: "full" | "address";
  // Plan 272 (admin only): 「所有利群入口都可用」 exemption from 只开主入口.
  allEntries?: boolean;
  entry?: {
    nodeId: number;
    nodeName: string;
    host: string;
    address: string;
    // Plan 295: the owner's 主入口 在线 / 离线.
    online?: boolean;
    offlineSince?: number;
  };
  // Plan 295 (admin only): what the owner's 主入口 cannot run for this rule.
  entryLimits?: LeiKwanEntryLimit[];
  // Plan 297 (admin, im.gamer App): the same for 手机本地 (own rules only,
  // when the list was asked with `phone`).
  phoneLimits?: LeiKwanEntryLimit[];
  // Plan 297 (admin view): the owner is an admin — open on every entry
  // (not held by 「只开主入口」).
  ownerAdmin?: boolean;
  // Plan 301 (admin): 「只开主入口」 keeps it closed on the shown entry.
  entryClosed?: boolean;
  // Plan 310 (admin + owner): 全程 of the shown entry (入口机 → 利群专线出口,
  // the LeiKwan client's own measurement, from the entry's latest report) —
  // or latencyNote = why there is none (入口机离线 / agent 需要升级 / …).
  latency?: LeiKwanRuleLatency;
  latencyNote?: string;
  // admin only
  userId?: number;
  userName?: string;
  forwardId?: number;
  source?: string;
  accel?: {
    warmMain: number;
    warmBackup: number;
    ssFilter: string;
    logswitch: boolean;
    returnCheck: boolean;
  };
  accelOverride?: LeiKwanRuleAccelOverride;
  backup?: LeiKwanRuleBackup;
  landingHint?: string;
  hostValues?: LeiKwanHostValue[];
  // Plan 269: billed 利群流量 since the owner's reset, the block reason
  // ('' = open) and the prepared panel landing node 「待切换」.
  inFlow?: number;
  outFlow?: number;
  blockReason?: string;
  pendingLanding?: LeiKwanPendingLanding;
  // Plan 271: billed bytes of the rule's 备线 since the owner's reset; a 备线
  // shared with other rules shows its full bytes (count + names; users get
  // only their own rules' names).
  backupInFlow?: number;
  backupOutFlow?: number;
  backupSharedCount?: number;
  backupSharedWith?: string[];
  // Plan 273: 利群计费流量 = the vendor's own bytes of this forward in the
  // current cycle (separate from inFlow/outFlow above). Users: own rules only.
  vendorFlow?: LeiKwanVendorFlow;
}

/** Plan 273: one rule's 利群计费流量 (vendor report, every 5 min). */
export interface LeiKwanVendorFlow {
  total: number;
  in: number;
  out: number;
  cycleStart?: number | null;
  cycleEnd?: number | null;
  updatedAt: number;
  /** old data or the last vendor read failed */
  stale: boolean;
  // admin only
  extra?: number;
  share?: number;
  state?: string;
  error?: string;
}

/** Plan 273: when a vendor report was read (ms) and whether it is fresh. */
export interface LeiKwanUsageStatus {
  fetchedAt: number;
  checkedAt: number;
  nextAt: number;
  stale: boolean;
  /** never read yet */
  pending?: boolean;
  error?: string;
  errorCount?: number;
}

export interface LeiKwanUsageForward {
  forwardId: number;
  state: string;
  listenPort: number;
  routeLabel: string;
  totalBytes: number;
  bytesIn: number;
  bytesOut: number;
  extraBilledBytes: number;
  sharePercent: number;
  ruleId?: number;
  ruleName?: string;
  userName?: string;
}

export interface LeiKwanDevice {
  deviceId: string;
  clientId: number;
  name: string;
  platform: string;
  osVersion: string;
  clientBuild: string;
  ip: string;
  online: boolean;
  /** online / offline / blocked / banned / kicked */
  state: string;
  countsTowardLimit: boolean;
  blockedUntil?: number | null;
  firstSeenAt?: number | null;
  lastSeenAt?: number | null;
  /** our node with this IP (e.g. 火山上海) */
  nodeId?: number;
  nodeName?: string;
  leikwanEntry?: boolean;
}

/** Plan 273: POST /leikwan/usage (admin) — 套餐流量 + 利群在线设备. */
export interface LeiKwanUsageView {
  accountReady: boolean;
  note?: string;
  accountId?: number;
  accountName?: string;
  serviceId?: number;
  pollEverySec?: number;
  service?: {
    name: string;
    planName: string;
    status: string;
    expiresAt: number | null;
    expiresAtRaw: string;
    forwardLimit: number;
    activeForwardCount: number;
  };
  serviceStatus?: LeiKwanUsageStatus;
  usage?: {
    cycleStart: number | null;
    cycleEnd: number | null;
    countedSince: number | null;
    trafficResetAt: number | null;
    billingMode: string;
    status: string;
    usedBytes: number;
    includedBytes: number;
    remainingBytes: number | null;
    usedPercent: number | null;
    billableBytes: number;
    lifetimeUsedBytes?: number | null;
    attributedBytes: number;
    unattributedBytes: number;
    forwards: LeiKwanUsageForward[];
    otherBytes: number;
    otherForwards: number[];
    daily: { date: string; totalBytes: number }[];
  };
  usageStatus?: LeiKwanUsageStatus;
  previous?: {
    cycleStart: number | null;
    cycleEnd: number | null;
    usedBytes: number;
    includedBytes: number;
    usedPercent: number | null;
  };
  previousNone?: boolean;
  devices?: {
    limit: number;
    count: number;
    slots: number;
    limitSource: string;
    windowSec: number;
    items: LeiKwanDevice[];
  };
  devicesStatus?: LeiKwanUsageStatus;
}

export interface LeiKwanPendingLanding {
  landingNodeId: number;
  name?: string;
  protocol?: string;
  port?: number;
  serverName?: string;
  host?: string;
}

export interface LeiKwanRuleList {
  rules: LeiKwanRule[];
  admin: boolean;
  // Plan 270: false = no active grant (users get no rules / lines).
  granted?: boolean;
  grant?: LeiKwanGrant;
  // Admin: every line; granted users: the active ones (「可用专线」).
  lines?: {
    id: number;
    name: string;
    routeLabel: string;
    transport?: string;
    status?: number;
  }[];
  // Plan 272: global 「只开主入口」 on/off and whether the 订阅 feature is on.
  entryLock?: boolean;
  subEnabled?: boolean;
  // Plan 280: the user's 利群转发数 (users only; limit 0 / ≥ 99999 = 不限).
  ruleLimit?: LeiKwanRuleLimit;
}

export interface LeiKwanRuleLimit {
  limit: number;
  used: number;
  unlimited: boolean;
  full?: boolean;
  error?: string;
}

export interface LeiKwanRuleOptions {
  admin: boolean;
  ownerId: number;
  grant?: LeiKwanGrant;
  grantError?: string;
  // Plan 268/280: grantError comes from the user's 到期 / 利群转发数.
  userBlocked?: boolean;
  ruleLimit?: LeiKwanRuleLimit;
  lines: { id: number; name: string; routeLabel: string; backup: boolean }[];
  landings: {
    id: number;
    name: string;
    protocol: string;
    serverName: string;
    host: string;
    port: number;
  }[];
  quota: LeiKwanQuota;
  // Plan 272: the owner's 外部节点 (unsupported = greyed with the reason).
  externals?: LeiKwanExternalOption[];
}

export interface LeiKwanExternalOption {
  id: number;
  name: string;
  protocol: string;
  host: string;
  port: number;
  supported: boolean;
  reason?: string;
}

/**
 * Plan 272 「生成订阅」: the owner's 「利群汇聚」 subscription (an ordinary sub,
 * created on first use or reused). leikwanNodes = 利群 rules in it now,
 * manual = 手动落地 rules that cannot be in a sub, total = all its nodes.
 */
export interface LeiKwanSub {
  id: number;
  token: string;
  name: string;
  format: string;
  ruleMode: string;
  leikwanNodes: number;
  total: number;
  rules: number;
  manual: number;
  lastPullAt: number;
  note: string;
  /** Plan 297: main = 跟主入口, local = 手机本地 (127.0.0.1) */
  entryMode?: "main" | "local";
  /** Plan 297: the owner's subscriptions with 利群 nodes */
  subs?: {
    id: number;
    name: string;
    entryMode: "main" | "local";
    auto: string;
  }[];
  /** Plan 297: the owner's 主入口 name */
  mainEntry?: string;
}

// ---- Plan 272: 利群规则诊断 ----

// Plan 301: device (本机 → 入口), vendor (专线入口 → 专线出口, 估算), landing
// (专线出口 → 落地, 实测); hop2 = 专线 → 落地 (估算) when the landing is not
// measured. "other" (other 利群入口 hosts) is gone.
export type LeiKwanDiagSection =
  | "device"
  | "hop1"
  | "vendor"
  | "landing"
  | "hop2"
  | "exit"
  | "backup"
  | "other";

export interface LeiKwanDiagItem {
  section: LeiKwanDiagSection;
  title: string;
  sub?: string;
  /** "tcp" | "udp" | "tcp+udp" | "" — non-empty = the entry in use */
  inUse?: string;
  success: boolean;
  estimate?: boolean;
  skipped?: boolean;
  averageTime: number;
  /** Plan 306: upper end of a range (shared landing, several plausible exits) */
  averageMaxMs?: number;
  /** Plan 306: shared landing — the row shows a range, not one exit */
  ambiguous?: boolean;
  /** Plan 306 r2: 「端内 19 ms」 tag on the 利群专线入口 → 专线出口 row (every user) */
  endIn?: LeiKwanDiagEndIn;
  packetLoss: number;
  message?: string;
  diagnosing?: boolean;
}

/** Plan 306: ready-made 端内 tag (r2: always amber, shown to everyone). */
export interface LeiKwanDiagEndIn {
  label: string;
  tone: "amber" | "slate";
  title: string;
}

export interface LeiKwanDiagPath {
  entryName: string;
  entryAddress: string;
  lineName: string;
  routeLabel: string;
  landingName: string;
  landingSub: string;
  landingKind: "node" | "manual" | "external";
  /** Plan 301: 手机本地 is the entry (the path starts at the phone) */
  local?: boolean;
  /** Plan 301: 本机 → 入口 was measured by the App */
  device?: boolean;
  /** Plan 301: 专线出口 → 落地 can be measured (native landing) */
  landingMeasurable?: boolean;
}

export interface LeiKwanDiagSummary {
  /** Plan 301: 本机 → 入口 (App) */
  deviceMs?: number;
  hop1Ms?: number;
  /** Plan 301: 专线入口 → 专线出口 = 全程 − measured hops (估算) */
  vendorMs?: number;
  /** Plan 301: ≈ 全程 (to the line's exit) + 专线出口 → 落地 */
  endToEndMs?: number;
  /** Plan 301: 专线出口 → 落地 measured on the landing (plan 306: its ping to the exit IP) */
  landingMs?: number;
  landingMeasured?: boolean;
  /** Plan 306: upper ends of the ranges on a shared landing */
  landingMaxMs?: number;
  vendorMaxMs?: number;
  endToEndMaxMs?: number;
  landingAmbiguous?: boolean;
  /** Plan 306 r2: 端内 = the whole 利群专线入口 → 专线出口 (= vendorMs) */
  endInMs?: number;
  endInMaxMs?: number;
  endIn?: LeiKwanDiagEndIn;
  /** Plan 306 r2: 专线出口 → 落地 is the landing's ping (else the conn rtt) */
  landingPing?: boolean;
  local?: boolean;
  hop2Ms?: number;
  hop2Estimate: boolean;
  totalMs?: number;
  totalSource: "udp" | "tcp" | "";
  totalAgeS?: number;
  totalLoss?: number;
}

export interface LeiKwanDiagProgress {
  total: number;
  completed: number;
  success: number;
  failed: number;
}

export interface LeiKwanDiagStart {
  total: number;
  timestamp: number;
  admin: boolean;
  ruleName: string;
  path: LeiKwanDiagPath;
  items: LeiKwanDiagItem[];
}

export interface LeiKwanDiagDone {
  progress?: LeiKwanDiagProgress;
  timedOut: boolean;
  summary?: LeiKwanDiagSummary;
  /** the final, authoritative rows (sorted by section) */
  items?: LeiKwanDiagItem[];
}

/** Non-stream fallback of the 利群 rule diagnosis. */
export interface LeiKwanDiagResult {
  ruleName: string;
  admin: boolean;
  path: LeiKwanDiagPath;
  items: LeiKwanDiagItem[];
  summary?: LeiKwanDiagSummary;
  timedOut: boolean;
}

export interface LeiKwanHostSnapshot {
  host: string;
  capturedAt: string;
  agentVersion: string;
  coreVersion: string;
  updateLock: boolean;
  nativeUdp: boolean;
  selectionMode: string;
  forwardsReady: string;
  frontChannel: string;
  frontBuild: string;
  fastFailover: string;
  logswitch: string;
  logswitchRecordOnly?: string[] | null;
  holdS: number;
  leaseSettleS: number;
  grace: string;
  returnCheckS: number;
  coalesce: string;
  warm?: Record<string, number> | null;
  ssFilter?: Record<string, string> | null;
  triggers?: Record<string, string> | null;
  lines?: { forwardId: number; listenPort: number; backup: string }[] | null;
  forwards?:
    | {
        forwardId: number;
        listenPort: number;
        target: string;
        transport: string;
        state: string;
      }[]
    | null;
  phoneBackup?: { port: number; target: string; note: string }[] | null;
  deviceQuota: string;
  extra?: string[] | null;
}

export interface LeiKwanHostList {
  entryGroupId: number;
  entryGroupName: string;
  hosts: {
    node: LeiKwanEntryNode;
    mainEntryUsers: number;
    snapshot?: LeiKwanHostSnapshot | null;
    source?: string;
    capturedAt?: number;
    // Plan 269: 面板接管 of this host.
    manage?: LeiKwanHostManage;
    // Plan 295: what this host can run.
    caps?: LeiKwanEntryCaps;
  }[];
  global: LeiKwanGlobal;
  triggers: LeiKwanTrigger[];
  // Plan 269: hosts that need the admin (⚠ on the 「利群入口」 tab).
  attention?: number;
}

// ---- Plan 269: 面板接管 (agent-reported host state + managed files) ----

/** File keys of the managed host files. */
export type LeiKwanHostFile =
  | "warm"
  | "ssfilter"
  | "sskeys"
  | "triggers"
  | "logswitch"
  | "coalesce"
  | "phone";

export interface LeiKwanHostLiveForward {
  forwardId: number;
  listenPort: number;
  target?: string;
  transport?: string;
  state?: string;
  policy?: string;
  udp?: string;
  running: boolean;
}

/** The live host state from the entry agent's report. */
export interface LeiKwanHostLiveState {
  host?: string;
  agentVersion?: string;
  coreVersion?: string;
  agentRunning: boolean;
  updateLock: boolean;
  autoUpdateOff: boolean;
  selectionMode?: string;
  nativeUdp?: "all" | "some" | "none" | string;
  frontChannel?: string;
  frontBuild?: string;
  fastFailover?: string;
  logswitch?: string;
  coalesce?: string;
  phonePorts?: number[] | null;
  forwards?: LeiKwanHostLiveForward[] | null;
}

/** What the entry agent says about the managed files. */
export interface LeiKwanHostAgentManage {
  manage: boolean;
  rev: number;
  force: number;
  applied?: Record<string, number> | null;
  drift?: string[] | null;
  pending?: string[] | null;
  errors?: Record<string, string> | null;
  written?: Record<string, number> | null;
  backup?: string;
}

export interface LeiKwanHostFileDiff {
  file: LeiKwanHostFile | string;
  name: string;
  path?: string;
  managed: boolean;
  same: boolean;
  /** "- x=1" = host now, "+ x=2" = panel; other lines are notes. */
  lines?: string[] | null;
  /** Applying needs a logswitch restart / nft change (night window only). */
  restart?: boolean;
}

export interface LeiKwanHostOverride {
  warmMain?: number;
  warmBackup?: number;
  ssFilter?: string;
  logswitch?: boolean;
  returnCheck?: boolean;
  backup?: string;
}

export interface LeiKwanHostManage {
  manage: boolean;
  traffic: boolean;
  learned: boolean;
  reported: boolean;
  rev?: number;
  force?: number;
  learnedAt?: number;
  reportedAt?: number;
  syncedAt?: number;
  syncError?: string;
  state?: LeiKwanHostLiveState;
  agent?: LeiKwanHostAgentManage;
  trafficState?: {
    counting: boolean;
    pending: boolean;
    blocked?: number[] | null;
    error?: string;
  };
  diff?: LeiKwanHostFileDiff[] | null;
  diffCount?: number;
  overrides?: Record<string, LeiKwanHostOverride> | null;
  profile?: unknown;
  applying?: boolean;
  /** Chinese reasons: 节点离线 / 漂移 / 写入失败 / 同步失败 / unit 改了还没重启. */
  attention?: string[] | null;
  /** Plan 295: the backend's ⚠ level (error = offline or a failure). */
  attentionLevel?: "error" | "warn";
  /** Plan 295: the node is offline (since offlineSince, ms). */
  offline?: boolean;
  offlineSince?: number;
  /** Chinese file names waiting for the 02:00–06:00 window. */
  pending?: string[] | null;
}

/** host/* replies: the manage view (+ a warning when the push failed). */
export type LeiKwanHostManageReply = LeiKwanHostManage & {
  warning?: string;
  learnedDiff?: LeiKwanHostFileDiff[] | null;
};

export interface LeiKwanImportItem {
  forwardId: number;
  listenPort: number;
  routeId: number;
  routeLabel: string;
  target: string;
  action: "import" | "skip" | "registered";
  reason?: string;
  lineName?: string;
  ruleName?: string;
  backupForwardId?: number;
  backupName?: string;
  backupSharedWith?: number[];
  backupAddr?: string;
  accel: LeiKwanRuleAccelOverride;
  landingHint?: string;
  hostValues?: LeiKwanHostValue[];
}

export interface LeiKwanImportPlan {
  items: LeiKwanImportItem[];
  lines: {
    exists: boolean;
    id?: number;
    name: string;
    routeId: number;
    routeLabel: string;
    accel: LeiKwanAccel;
    backupTunnelId: number;
    backupTunnelName: string;
    forwardIds: number[];
  }[];
  entryGroup: {
    id: number;
    name: string;
    nodes: { id: number; name: string; host: string; inGroup: boolean }[];
  };
  adminGrant: {
    exists: boolean;
    userId?: number;
    userName?: string;
    mainEntryNodeId: number;
    mainEntryName?: string;
    backupLine?: boolean;
  };
  global: LeiKwanGlobal;
  quota: LeiKwanQuota;
  referenceHost: string;
  notes: string[];
}

const lk = <T = unknown>(path: string, body: unknown = {}) =>
  Network.post<T>("/leikwan/" + path, body);
const lkr = <T = unknown>(path: string, body: unknown = {}) =>
  Network.post<T>("/leikwan-rule/" + path, body);

export const getLeiKwanLines = () => lk<LeiKwanLineList>("line/list");
// Plan 273: 套餐流量 + 利群在线设备 (the panel's last snapshot, no vendor call).
export const getLeiKwanUsage = () => lk<LeiKwanUsageView>("usage");
export const saveLeiKwanLine = (body: {
  id?: number;
  name: string;
  routeId: number;
  routeLabel?: string;
  transport: string;
  backupTunnelId: number;
  accel: LeiKwanAccel;
  inx?: number;
  status?: number;
  flowMode?: number;
  trafficRatio?: number;
}) => lk("line/save", body);
export const deleteLeiKwanLine = (id: number) => lk("line/delete", { id });
export const saveLeiKwanSetting = (body: {
  entryGroupId?: number;
  global?: LeiKwanGlobal;
}) => lk("setting/save", body);
export const getLeiKwanGrants = () =>
  lk<{
    grants: LeiKwanGrant[];
    entryNodes: LeiKwanEntryNode[];
    entryGroupId: number;
    entryLock?: boolean;
  }>("grant/list");
export const saveLeiKwanGrant = (body: {
  userId: number;
  mainEntryNodeId: number;
  backupLine: boolean;
  status?: number;
}) =>
  lk<{ grant: LeiKwanGrant; movedRules: number; warnings: string[] }>(
    "grant/save",
    body,
  );
// Plan 295: read-only 换主入口 preview (what each forward keeps / loses).
export const previewLeiKwanGrant = (body: {
  userId: number;
  mainEntryNodeId: number;
  /** Plan 297: preview 手机本地 instead (mainEntryNodeId ignored) */
  local?: boolean;
  phone?: LeiKwanPhoneReq;
}) => lk<LeiKwanGrantPreview>("grant/preview", body);
export const deleteLeiKwanGrant = (userId: number) =>
  lk("grant/delete", { userId });
export const previewLeiKwanImport = () =>
  lk<LeiKwanImportPlan>("import/preview");
export const applyLeiKwanImport = (forwardIds: number[]) =>
  lk<{
    groupCreated: boolean;
    nodesAdded: string[];
    linesCreated: string[];
    rulesImported: number[];
    grantCreated: boolean;
    skipped: string[];
  }>("import/apply", { confirmed: true, forwardIds });
export const getLeiKwanHosts = () => lk<LeiKwanHostList>("host/list");
// Plan 269: 面板接管 actions; each returns the host's new manage view.
export const learnLeiKwanHost = (nodeId: number) =>
  lk<LeiKwanHostManageReply>("host/learn", { nodeId });
export const setLeiKwanHostManage = (nodeId: number, manage: boolean) =>
  lk<LeiKwanHostManageReply>("host/manage", { nodeId, manage });
export const forceLeiKwanHost = (nodeId: number) =>
  lk<LeiKwanHostManageReply>("host/force", { nodeId });
export const setLeiKwanHostTraffic = (nodeId: number, traffic: boolean) =>
  lk<LeiKwanHostManageReply>("host/traffic", { nodeId, traffic });
export const syncLeiKwanHost = (nodeId: number) =>
  lk<LeiKwanHostManageReply>("host/sync", { nodeId });
export const rebuildLeiKwanLineBackups = (lineId: number) =>
  lk<{
    result: {
      rebuilt: number;
      created: number;
      removed: number;
      unchanged: number;
      linked: number;
    };
    warnings: string[];
  }>("line/backup-rebuild", { lineId });
export const retryLeiKwanBackup = (id: number) =>
  lk("rule/backup-retry", { id });
// Plan 272: 「所有利群入口都可用」 on one rule (admin).
export const setLeiKwanRuleAllEntries = (id: number, on: boolean) =>
  lk("rule/all-entries", { id, on });

// Plan 301 (admin, im.gamer App): the values 手机本地 runs with (setPlan).
export const getLeiKwanPhonePlan = () => lk<PhonePlan>("phone/plan", {});

// Plan 297: `phone` (im.gamer App only) also asks for phoneLimits.
export const getLeiKwanRules = (userId = 0, phone?: LeiKwanPhoneReq) =>
  lkr<LeiKwanRuleList>("list", phone ? { userId, phone } : { userId });
export const getLeiKwanRuleOptions = (userId = 0) =>
  lkr<LeiKwanRuleOptions>("options", { userId });
export const createLeiKwanRule = (body: {
  lineId: number;
  name: string;
  landingNodeId?: number;
  targetHost?: string;
  targetPort?: number;
  externalNodeId?: number;
  userId?: number;
  requestId: string;
}) => lkr<{ rule: LeiKwanRule; warnings?: string[] }>("create", body);
export const updateLeiKwanRule = (body: {
  id: number;
  name?: string;
  changeTarget?: boolean;
  landingNodeId?: number;
  targetHost?: string;
  targetPort?: number;
  externalNodeId?: number;
  accel?: LeiKwanRuleAccelOverride;
}) => lkr<{ rule: LeiKwanRule; warnings?: string[] }>("update", body);
export const deleteLeiKwanRule = (id: number, keepLeiKwan = false) =>
  lkr<{ backupNote?: string }>("delete", { id, confirmed: true, keepLeiKwan });
// Plan 297: `local` = 手机本地 (127.0.0.1:<vendor port>, im.gamer App).
export const getLeiKwanRuleLink = (id: number, local = false) =>
  lkr<{ address: string; url?: string; note?: string; entryName?: string }>(
    "link",
    local ? { id, local: true } : { id },
  );
// Plan 272 「生成订阅」: the owner's 「利群汇聚」 subscription (admins: userId
// = a granted user; users: themselves).
// Plan 297: entry "local" = the im.gamer App whose entry is 手机本地 (admin,
// own subs); id = show this sub; action create (new 手机本地 sub) | convert
// (sub id follows `entry`: local, or the 主入口 when absent).
export const getLeiKwanRuleSub = (
  userId?: number,
  opt: { entry?: "local"; id?: number; action?: "create" | "convert" } = {},
) => lkr<LeiKwanSub>("sub", userId ? { userId, ...opt } : { ...opt });
// Plan 272: non-stream fallback of 诊断 (the stream lives in diagnosis-stream).
export const diagnoseLeiKwanRule = (
  id: number,
  extra?: { local?: boolean; phone?: unknown },
) =>
  Network.post<LeiKwanDiagResult>(
    "/leikwan-rule/diagnose",
    { id, ...(extra || {}) },
    { timeout: 60000 },
  );

export const newLeiKwanRequestId = () =>
  `r${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`;

/** 日志快切 trigger source → tinted pill tone. */
export const triggerSourceTone = (source: string) =>
  source === "前端信号" ? "teal" : source === "状态文件" ? "amber" : "purple";

// ---- Plan 270: 「利群汇聚」 nav flag (admins always, users with an active grant) ----

const NAV_KEY = "flvx_leikwan_nav";
const NAV_SEEN_KEY = "flvx_leikwan_nav_seen";
const NAV_EVENT = "flvxLeiKwanNav";

export const getLeiKwanAccess = () =>
  lkr<{ admin: boolean; granted: boolean }>("access");

/** Last known flag (instant first paint); false until the first check. */
export function leiKwanNavCached(): boolean {
  try {
    return localStorage.getItem(NAV_KEY) === "1";
  } catch {
    return false;
  }
}

let navCheck: Promise<boolean> | null = null;
let navToken: string | null = null;

/**
 * One access call per page load and login (a new token re-checks); updates
 * the cache and tells the menus.
 */
export function refreshLeiKwanNav(): Promise<boolean> {
  const token = getToken();

  // Admins always see the item (no request).
  if (isAdmin()) return Promise.resolve(true);
  if (!navCheck || token !== navToken) {
    navToken = token;
    navCheck = getLeiKwanAccess()
      .then((res) => {
        const on = res.code === 0 && !!res.data?.granted;

        try {
          localStorage.setItem(NAV_KEY, on ? "1" : "0");
        } catch {
          // ignore
        }
        window.dispatchEvent(new Event(NAV_EVENT));

        return on;
      })
      .catch(() => leiKwanNavCached());
  }

  return navCheck;
}

/** Subscribe to flag changes (returns the unsubscribe). */
export function onLeiKwanNav(fn: () => void): () => void {
  window.addEventListener(NAV_EVENT, fn);
  window.addEventListener(NAV_SEEN_KEY, fn);

  return () => {
    window.removeEventListener(NAV_EVENT, fn);
    window.removeEventListener(NAV_SEEN_KEY, fn);
  };
}

/** Users: 「新」 on the nav item until the page was opened once. */
export function leiKwanNavSeen(): boolean {
  try {
    return localStorage.getItem(NAV_SEEN_KEY) === "1";
  } catch {
    return true;
  }
}

export function markLeiKwanNavSeen() {
  try {
    if (localStorage.getItem(NAV_SEEN_KEY) !== "1") {
      localStorage.setItem(NAV_SEEN_KEY, "1");
      window.dispatchEvent(new Event(NAV_SEEN_KEY));
    }
  } catch {
    // ignore
  }
}
