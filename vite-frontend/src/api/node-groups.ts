import Network from "./network";

// Plan 324: the 节点 page groups = the 探针 groups (one list, edited here or in
// 设置 → 公开探针页) + the 利群入口 node group (admin only, never on the probe).

export interface NodePageGroup {
  /** Probe group id; a new group carries a temporary id ≤ 0 until saved. */
  id: number;
  name: string;
  nodes: number[];
}

export interface NodePageLeiKwanGroup {
  /** node_group id (= 利群汇聚's entry group). */
  id: number;
  name: string;
  nodes: number[];
  /** Index among the probe groups (0 = first). */
  pos: number;
  /** What of 利群汇聚 needs a member — removing it is refused. */
  uses: Record<string, string[]>;
  /** Information only, shown in the remove confirm. */
  notes: Record<string, string[]>;
}

export interface NodePageGroups {
  groups: NodePageGroup[];
  leikwan: NodePageLeiKwanGroup | null;
}

export interface NodePageGroupsSave {
  groups: NodePageGroup[];
  /** Omitted = the 利群入口 group stays as it is. */
  leikwan?: { name: string; nodes: number[]; pos: number };
}

export const getNodePageGroups = () =>
  Network.post<NodePageGroups>("/node/groups");

export const saveNodePageGroups = (data: NodePageGroupsSave) =>
  Network.post<NodePageGroups>("/node/groups/save", data);
