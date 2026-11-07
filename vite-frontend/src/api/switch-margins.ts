import Network from "./network";

// Plan 234 网站配置 → 自动切换门槛 (admin only).
export type SwitchMarginKey = "best" | "realityDomain" | "realityIp";

export interface AutoSwitchCounts {
  total: number;
  failover: number;
  /** Window start, or when counting began if later (unix ms). */
  since: number;
  /** Counting began inside the window (fewer than 7 days counted). */
  partial: boolean;
  /** Tunnel / landing server with ≥ busyPerWindow non-failover switches. */
  busyUnit?: number;
  busyCount?: number;
}

export interface SwitchMarginRow {
  key: SwitchMarginKey;
  valueMs: number;
  defaultMs: number;
  counts: AutoSwitchCounts;
  busyName?: string;
}

export interface SwitchMarginsView {
  rows: SwitchMarginRow[];
  maxMs: number;
  confirmRounds: number;
  windowDays: number;
  busyPerWindow: number;
  /** Landing servers taking the 目标 IP margin vs still on 20 ms. */
  ipSupport: {
    native: number;
    nativeOld: number;
    shared: number;
    sharedOld: number;
  };
}

export interface SwitchMarginsSave {
  bestMs: number;
  realityDomainMs: number;
  realityIpMs: number;
}

export const getSwitchMargins = () =>
  Network.post<SwitchMarginsView>("/config/switch-margins", {});

export const saveSwitchMargins = (body: SwitchMarginsSave) =>
  Network.post<SwitchMarginsView>("/config/switch-margins/save", body);
