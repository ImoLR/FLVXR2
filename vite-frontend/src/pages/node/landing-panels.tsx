import {
  Suspense,
  lazy,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import { useNavigate } from "react-router-dom";
import { Settings2 } from "lucide-react";

import {
  LandingNode,
  LandingServer,
  landingNodes,
  landingServers,
} from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import {
  AdminAttention,
  AttentionIcon,
  AttentionLevel,
  serverAdminAttention,
} from "@/pages/landing/admin-attention";
import { LandingGlobalSettings } from "@/pages/landing/global-settings";
import { IntegrationSettings } from "@/pages/landing/integration-settings";

// Plan 327: the 服务器 page (was 节点) now carries what left the 落地 page —
// 出站与路由, 妙妙屋接入设置, 落地全局设置 and the landing attention notices.
// A small view switch above the toolbar (a segmented control, so it never
// looks like the plan 324 group tabs below it); 落地全局设置 is a header
// button. Notices jump to 我的节点 (?focus=server:id / node:id).

export type ServerPagePanel = "servers" | "egress" | "integration";

// Loaded on demand (big editors), like it was on the 落地 page.
const LandingEgressTab = lazy(() => import("@/pages/landing/egress"));

const PANELS: { key: ServerPagePanel; label: string }[] = [
  { key: "servers", label: "服务器" },
  { key: "egress", label: "出站与路由" },
  { key: "integration", label: "妙妙屋接入" },
];

/** Landing servers + nodes for the notices and the ⚠ on 「落地设置」. */
export function useServerPageLanding() {
  const [servers, setServers] = useState<LandingServer[]>([]);
  const [nodes, setNodes] = useState<LandingNode[]>([]);
  const reload = useCallback(async () => {
    const [serverRes, nodeRes] = await Promise.all([
      landingServers(),
      landingNodes(),
    ]);

    if (serverRes.code === 0) setServers(serverRes.data || []);
    if (nodeRes.code === 0) setNodes(nodeRes.data || []);
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);
  const attention = useMemo(() => {
    const map = new Map<number, AdminAttention>();

    for (const server of servers) {
      const a = serverAdminAttention(server);

      if (a) map.set(server.id, a);
    }

    return map;
  }, [servers]);

  return { servers, nodes, attention, reload };
}

export function ServerPageBar({
  panel,
  onPanel,
}: {
  panel: ServerPagePanel;
  onPanel: (panel: ServerPagePanel) => void;
}) {
  const [globalOpen, setGlobalOpen] = useState(false);

  return (
    <div
      className="mb-3 flex flex-wrap items-center justify-between gap-2"
      data-server-page-bar=""
    >
      <div
        aria-label="服务器页"
        className="inline-flex h-9 items-center rounded-lg bg-default-100 p-0.5 dark:bg-default-50/20"
        role="tablist"
      >
        {PANELS.map((item) => (
          <button
            key={item.key}
            aria-selected={panel === item.key}
            className={`h-8 rounded-md px-3 text-sm transition-colors max-sm:px-2.5 ${panel === item.key ? "bg-content1 font-semibold text-foreground shadow-sm" : "font-medium text-default-500 hover:text-foreground"}`}
            data-server-panel={item.key}
            role="tab"
            type="button"
            onClick={() => onPanel(item.key)}
          >
            {item.label}
          </button>
        ))}
      </div>
      <Button
        color="warning"
        data-landing-global=""
        size="sm"
        startContent={<Settings2 size={16} />}
        variant="flat"
        onPress={() => setGlobalOpen(true)}
      >
        落地全局设置
      </Button>
      {globalOpen && (
        <LandingGlobalSettings onClose={() => setGlobalOpen(false)} />
      )}
    </div>
  );
}

/** Plan 202 / 218 notices (moved from the 落地 page); 查看 → 我的节点. */
export function ServerPageNotices({
  servers,
  nodes,
  attention,
}: {
  servers: LandingServer[];
  nodes: LandingNode[];
  attention: Map<number, AdminAttention>;
}) {
  const navigate = useNavigate();
  const blocked = nodes.filter((node) => node.scanBlock);
  const attentionServers = servers.filter((server) => attention.has(server.id));
  const level: AttentionLevel = attentionServers.some(
    (server) => attention.get(server.id)?.level === "error",
  )
    ? "error"
    : "warn";

  if (!blocked.length && !attentionServers.length) return null;

  return (
    <div className="mb-3 space-y-2">
      {blocked.length > 0 && (
        <div
          data-scan-notice
          className="flex flex-wrap items-center gap-2 rounded-lg bg-danger-50 px-3 py-2 text-sm text-danger-700 dark:bg-danger-500/10 dark:text-danger-400"
        >
          <span className="min-w-0 flex-1">
            {blocked.length}{" "}
            个落地节点因端口扫描已被禁用，用户看到的是「联系管理员」提示
          </span>
          <Button
            className="h-7"
            color="danger"
            size="sm"
            variant="flat"
            onPress={() => navigate(`/landing?focus=node:${blocked[0].id}`)}
          >
            查看节点
          </Button>
        </div>
      )}
      {attentionServers.length > 0 && (
        <div
          data-attention-notice
          className={`flex items-center gap-2 rounded-lg px-3 py-2 text-sm ${
            level === "error"
              ? "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300"
              : "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300"
          }`}
        >
          <AttentionIcon level={level} />
          <span className="min-w-0 flex-1">
            {attentionServers.length} 台服务器需要处理：
            {attentionServers.map((server) => server.name).join("、")}
          </span>
          <Button
            className="h-7 shrink-0"
            color="warning"
            size="sm"
            variant="flat"
            onPress={() =>
              navigate(`/landing?focus=server:${attentionServers[0].id}`)
            }
          >
            查看
          </Button>
        </div>
      )}
    </div>
  );
}

/** 出站与路由 / 妙妙屋接入 views (the 服务器 view is the page itself). */
export function ServerPagePanelView({
  panel,
  reloadKey,
  onSynced,
}: {
  panel: ServerPagePanel;
  reloadKey: number;
  onSynced: () => void;
}) {
  if (panel === "egress")
    return (
      <div className="flvx-landing" data-server-panel-view="egress">
        <Suspense
          fallback={
            <div className="flex justify-center p-6">
              <Spinner />
            </div>
          }
        >
          <LandingEgressTab key={reloadKey} />
        </Suspense>
      </div>
    );
  if (panel === "integration")
    return (
      <div className="flvx-landing" data-server-panel-view="integration">
        <IntegrationSettings onSynced={onSynced} />
      </div>
    );

  return null;
}
