import Network from "./network";

// Plan 327: what the 我的节点 tree needs besides the landing / forward lists.

/** An 外部节点 of the tree (no credentials). userName: admin 全部用户 only. */
export interface TreeExternal {
  id: number;
  userId: number;
  userName?: string;
  name: string;
  protocol: string;
  protocolLabel: string;
  host: string;
  port: number;
  summary: string;
}

/** A plan-108 线路机 chain forward and the 外部节点 it was made from (0 = a pasted link not in the list). */
export interface TreeChain {
  forwardId: number;
  userId: number;
  externalId: number;
}

export interface LandingTreeData {
  externals: TreeExternal[];
  chains: TreeChain[];
  all: boolean;
}

/** Users: their own. Admins: their own, or every user's with all. */
export const landingTree = (all = false) =>
  Network.post<LandingTreeData>("/landing/tree", all ? { all: true } : {});
