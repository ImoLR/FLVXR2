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

/**
 * Plan 158: the whole-machine protection cap next to the server total — a
 * different limit (whole machine, TCP+UDP, every port and state) — with a
 * warning when the server total is higher, plus the notice for a total still
 * stored as 仅入站 (plan 141).
 */
export function MachineCapHint({
  server,
  total,
  legacyInbound,
}: {
  server: LandingServer;
  total: number;
  legacyInbound: boolean;
}) {
  const cap = Number(server.machineMaxTotal ?? 0);
  const owner = server.machineCapOwner === "mmwx" ? "由妙妙屋负责" : "节点设置";
  const shared = server.source === "shared";

  return (
    <div data-machine-cap-hint className="space-y-2">
      <p className="text-xs text-default-500">
        {shared
          ? `服务器总连接数 ${total > 0 ? total : "不限"}（入站+出站，由妙妙屋管理）。`
          : "服务器总连接数 = 本服务器全部用户落地端口的入站+出站 TCP，用来保护服务器。"}
        {cap > 0
          ? `整机保护上限 ${cap}（${owner}，整台机器 TCP+UDP 全部端口，与服务器总连接数不是同一个数）。`
          : shared
            ? "整机保护上限：未知（妙妙屋还没上报）。"
            : "整机保护上限：未知（节点离线、未开启或旧版 agent）。"}
      </p>
      {cap > 0 && total > cap && (
        <p
          data-machine-cap-warning
          className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10"
        >
          服务器总连接数 {total} 高于整机保护上限 {cap}
          ：整机先满时整台机器的业务端口都会拒绝新连接，服务器总连接数到不了。
        </p>
      )}
      {legacyInbound && (
        <p
          data-legacy-inbound-total
          className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10"
        >
          此服务器的总连接数之前设为「仅入站」，目前仍按仅入站生效；保存服务器设置后改为按入站+出站计算。
        </p>
      )}
    </div>
  );
}
