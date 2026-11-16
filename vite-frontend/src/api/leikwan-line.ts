import Network from "@/api/network";

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
    forwardId: number;
    listenPort: number;
    routeLabel: string;
    tunnelId?: number | null;
    tunnelName?: string;
  }[];
  quota: LeiKwanQuota;
  routes: { id: number; label: string; available: boolean }[];
  backupTunnels: {
    id: number;
    name: string;
    type: number;
    entryNames: string;
  }[];
  triggers: LeiKwanTrigger[];
  accountReady: boolean;
}

export interface LeiKwanGrant {
  id: number;
  userId: number;
  userName?: string;
  mainEntryNodeId: number;
  mainEntryName?: string;
  mainEntryHost?: string;
  mainEntryOutsideGroup?: boolean;
  backupLine: boolean;
  num: number;
  flowGb: number;
  expTime: number;
  status: number;
  ruleCount: number;
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
  landing: {
    kind: "node" | "manual";
    landingNodeId?: number;
    host: string;
    port: number;
    name?: string;
    serverName?: string;
    protocol?: string;
  };
  entry?: { nodeId: number; nodeName: string; host: string; address: string };
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
}

export interface LeiKwanRuleList {
  rules: LeiKwanRule[];
  admin: boolean;
  grant?: LeiKwanGrant;
  lines?: { id: number; name: string; routeLabel: string }[];
}

export interface LeiKwanRuleOptions {
  admin: boolean;
  ownerId: number;
  grant?: LeiKwanGrant;
  grantError?: string;
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
  }[];
  global: LeiKwanGlobal;
  triggers: LeiKwanTrigger[];
}

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
  }>("grant/list");
export const saveLeiKwanGrant = (body: {
  userId: number;
  mainEntryNodeId: number;
  backupLine: boolean;
  num: number;
  flowGb: number;
  expTime: number;
  status?: number;
}) =>
  lk<{ grant: LeiKwanGrant; movedRules: number; warnings: string[] }>(
    "grant/save",
    body,
  );
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
export const retryLeiKwanBackup = (id: number) =>
  lk("rule/backup-retry", { id });

export const getLeiKwanRules = (userId = 0) =>
  lkr<LeiKwanRuleList>("list", { userId });
export const getLeiKwanRuleOptions = (userId = 0) =>
  lkr<LeiKwanRuleOptions>("options", { userId });
export const createLeiKwanRule = (body: {
  lineId: number;
  name: string;
  landingNodeId?: number;
  targetHost?: string;
  targetPort?: number;
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
  accel?: LeiKwanRuleAccelOverride;
}) => lkr<{ rule: LeiKwanRule; warnings?: string[] }>("update", body);
export const deleteLeiKwanRule = (id: number, keepLeiKwan = false) =>
  lkr<{ backupNote?: string }>("delete", { id, confirmed: true, keepLeiKwan });
export const getLeiKwanRuleLink = (id: number) =>
  lkr<{ address: string; url?: string; note?: string }>("link", { id });

export const newLeiKwanRequestId = () =>
  `r${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`;

/** 日志快切 trigger source → tinted pill tone. */
export const triggerSourceTone = (source: string) =>
  source === "前端信号" ? "teal" : source === "状态文件" ? "amber" : "purple";
