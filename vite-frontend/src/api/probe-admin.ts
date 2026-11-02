import Network from "./network";

// Plan 187 公开探针: admin settings (设置 → 公开探针页).
export interface ProbeAdminNode {
  /** FLVX node id; a 救命版-only machine (plan 195) has the negative 救命版 id. */
  id: number;
  name: string;
  probeName: string;
  hidden: boolean;
  region: string;
  online: boolean;
  /** Plan 195: "flvx" = 转发面板 node, "old" = machine only in the 救命版. */
  source?: "flvx" | "old";
  /** FLVX node that also runs in the 救命版: its name there. */
  oldMatch?: string;
  /** 救命版 rows: GeoIP region and the admin's override ("" = automatic). */
  regionAuto?: string;
  regionOverride?: string;
}

// Plan 189 custom groups. New groups carry a temporary id ≤ 0; the panel
// assigns the real one on save.
export interface ProbeAdminGroup {
  id: number;
  name: string;
  nodes: number[];
}

export type ProbeGroupMode = "region" | "group";

export interface ProbeAdminSettings {
  enabled: boolean;
  title: string;
  domain: string;
  latency: boolean;
  nodes: ProbeAdminNode[];
  defaultTitle: string;
  groups: ProbeAdminGroup[];
  groupMode: ProbeGroupMode;
  /** Plan 194: 显示到期天数 (default on). */
  expiry: boolean;
  /** Plan 195: 救命版 source — "" not configured, "ok", "error". */
  oldPanel?: "" | "ok" | "error";
}

export interface ProbeAdminSave {
  title: string;
  domain: string;
  latency: boolean;
  nodes: {
    id: number;
    probeName: string;
    hidden: boolean;
    /** 救命版 rows only: region override ("" = automatic). */
    region?: string;
  }[];
  groups: ProbeAdminGroup[];
  groupMode: ProbeGroupMode;
  /** Plan 194: 显示到期天数 (default on). */
  expiry: boolean;
}

export const getProbeSettings = () =>
  Network.post<ProbeAdminSettings>("/config/probe", {});

export const saveProbeSettings = (body: ProbeAdminSave) =>
  Network.post<null>("/config/probe/save", body);
