import Network from "./network";

// Plan 187 公开探针: admin settings (设置 → 公开探针页).
export interface ProbeAdminNode {
  id: number;
  name: string;
  probeName: string;
  hidden: boolean;
  region: string;
  online: boolean;
}

export interface ProbeAdminSettings {
  enabled: boolean;
  title: string;
  domain: string;
  latency: boolean;
  nodes: ProbeAdminNode[];
  defaultTitle: string;
}

export interface ProbeAdminSave {
  title: string;
  domain: string;
  latency: boolean;
  nodes: { id: number; probeName: string; hidden: boolean }[];
}

export const getProbeSettings = () =>
  Network.post<ProbeAdminSettings>("/config/probe", {});

export const saveProbeSettings = (body: ProbeAdminSave) =>
  Network.post<null>("/config/probe/save", body);
