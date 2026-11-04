import type {
  LandingConnMode,
  LandingConnSumMode,
  LandingServer,
} from "@/api/landing";

// Plan 141 labelled each landing connection number 仅入站 (client
// connections, the same meaning as a rule's 连接数) or 入站+出站 (every TCP
// socket of the node's port). Plan 158 retired the choice: the server total
// is always 入站+出站 (server protection), every node limit counts 入站 (a
// shared server's nodes once 妙妙屋 applies user limits).
export const CONN_MODE_NAME: Record<LandingConnMode, string> = {
  inbound: "入站",
  total: "入站+出站",
};

/**
 * 「(入站)」 / 「(入站+出站)」 after a displayed number; 「(按节点方式)」 for a
 * sum whose nodes count differently (some nodes not upgraded yet).
 */
export const connModeTag = (mode?: LandingConnSumMode | null) =>
  mode === "inbound"
    ? "(入站)"
    : mode === "mixed"
      ? "(按节点方式)"
      : "(入站+出站)";

/** Why a node limit still counts 入站+出站 (agent not upgraded yet). */
export const connModePendingText = () =>
  "节点连接数按入站计算需要升级该节点的 agent，升级前仍按入站+出站计算";

/** Unit of a server's node limits in labels: 入站, or 入站+出站 on a share that does not apply user limits yet. */
export const nodeLimitUnit = (server?: LandingServer) =>
  server?.source === "shared" && !server.connModeSupported
    ? "入站+出站"
    : "入站";

/** Plan 211: the per-node 节点总连接数 and what it counts. */
export const NODE_TOTAL_NAME = "节点总连接数";
export const NODE_TOTAL_UNIT = "入站+出站，含 TIME_WAIT/CLOSE_WAIT";

/** Plan 211: amber from 80 % of a cap, red at the cap. */
export type ConnLevel = "ok" | "warn" | "full";
export const connLevel = (value: number, cap?: number | null): ConnLevel => {
  if (!cap || cap <= 0) return "ok";
  if (value >= cap) return "full";

  return value * 100 >= cap * 80 ? "warn" : "ok";
};

/** Plan 211: why a node's 节点总连接数 does not apply yet. */
export const nodeTotalPendingText = (shared: boolean) =>
  shared
    ? "待妙妙屋升级：这个节点还设了入站上限，节点总连接数要等妙妙屋升级后才生效（入站上限照常生效）"
    : "待 agent 升级：这个节点还设了入站上限，节点总连接数要等这台落地服务器的 agent 升级后才生效（入站上限照常生效）";

/**
 * Plan 211 (was plan 158's server-total hint): what 节点总连接数 counts, the
 * 妙妙屋 share cap of a shared server (read-only, enforced by 妙妙屋) and the
 * whole-machine 整机兜底 — three different limits, whichever is hit first
 * refuses new connections. Warns when the node default is above 整机兜底.
 */
export function MachineCapHint({
  server,
  nodeTotal,
}: {
  server: LandingServer;
  nodeTotal: number;
}) {
  const cap = Number(server.machineMaxTotal ?? 0);
  const owner = server.machineCapOwner === "mmwx" ? "由妙妙屋负责" : "节点设置";
  const shared = server.source === "shared";
  const share = Number(
    server.shareMaxConnections ?? server.sharedCaps?.max_connections ?? 0,
  );

  return (
    <div data-machine-cap-hint className="space-y-2">
      <p className="text-xs text-default-500">
        {NODE_TOTAL_NAME} = 每个节点自己端口的入站+出站 TCP（含
        TIME_WAIT/CLOSE_WAIT），到上限只拒绝新连接，已有连接不断；和节点连接数（入站）、用户的总入站连接数、整机兜底一起生效，先到哪个按哪个。
        {shared
          ? `妙妙屋共享上限 ${share > 0 ? share : "不限"}（这台共享服务器上全部 FLVX 节点合计，入站+出站，由妙妙屋执行）。`
          : ""}
        {cap > 0
          ? `整机兜底 ${cap}（${owner}，整台机器 TCP+UDP 全部端口）。`
          : shared
            ? "整机兜底：未知（妙妙屋还没上报）。"
            : "整机兜底：未知（节点离线、未开启或旧版 agent）。"}
      </p>
      {cap > 0 && nodeTotal > cap && (
        <p
          data-machine-cap-warning
          className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10"
        >
          {NODE_TOTAL_NAME} {nodeTotal} 高于整机兜底 {cap}
          ：整机先满时整台机器的业务端口都会拒绝新连接，节点总连接数到不了。
        </p>
      )}
    </div>
  );
}
