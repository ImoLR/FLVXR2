import Network from "./network";

// 线路机 /「我自己的节点」(plan 108). The pasted link never comes back from the
// server: only the protocol, address and port summary.

export interface ChainParseResult {
  protocol: string;
  protocolLabel: string;
  host: string;
  port: number;
  summary: string;
  name: string;
  udp: boolean;
  resolved: string;
}

export interface ChainStatus {
  chainId: number;
  state: "creating" | "active" | "failed";
  op: string;
  progress: string;
  opStartedAt: number;
  lastOpAt: number;
  error: string;
  forwardId: number;
  summary: string;
}

export interface ForwardChainInfo {
  state: "creating" | "active" | "failed";
  op: string;
  opStartedAt: number;
  lastOpAt: number;
  error: string;
  upstreamProtocol: string;
  upstreamHost: string;
  upstreamPort: number;
  summary: string;
}

export interface ChainLink {
  name: string;
  url: string;
  entryNodeId: number;
  entryNodeName: string;
}

export const parseChainLink = (link: string) =>
  Network.post<ChainParseResult>("/forward/chain/parse", { link });

export const getChainStatus = (id: number) =>
  Network.post<ChainStatus>("/forward/chain/status", { id });

export const getChainLinks = (forwardId: number) =>
  Network.post<ChainLink[]>("/forward/chain/links", { forwardId });

// Chain creates/edits probe the user's node from every exit before
// answering, so they get a longer timeout than plain rule saves.
export const createChainForward = (data: Record<string, unknown>) =>
  Network.post<ChainStatus>("/forward/create", data, { timeout: 45000 });

export const updateChainForward = (data: Record<string, unknown>) =>
  Network.post<ChainStatus>("/forward/update", data, { timeout: 45000 });

export const newSubmitKey = (): string => {
  const bytes = new Uint8Array(16);

  crypto.getRandomValues(bytes);

  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
};

// waitChainOperation polls until the operation that started at startedAt has
// finished; it resolves with the final status (error === "" on success).
export const waitChainOperation = async (
  chainId: number,
  startedAt: number,
  onProgress: (status: ChainStatus) => void,
  isCancelled: () => boolean,
): Promise<ChainStatus | null> => {
  for (let attempt = 0; attempt < 400; attempt++) {
    if (isCancelled()) return null;
    const res = await getChainStatus(chainId);

    if (res.code !== 0 || !res.data) {
      return {
        chainId,
        state: "failed",
        op: "",
        progress: "",
        opStartedAt: 0,
        lastOpAt: startedAt,
        error: res.msg || "「我自己的节点」状态读取失败",
        forwardId: 0,
        summary: "",
      };
    }
    const status = res.data;

    // The operation is over once the chain no longer reports it as running.
    if (status.opStartedAt !== startedAt) {
      return status;
    }
    onProgress(status);
    await new Promise((resolve) =>
      setTimeout(resolve, attempt < 10 ? 1200 : 2500),
    );
  }

  return null;
};
