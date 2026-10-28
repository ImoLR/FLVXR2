import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useMemo,
  useState,
} from "react";
import toast from "react-hot-toast";

import { getNodeList } from "@/api";
import {
  LandingUserSetting,
  PoolServer,
  RelatedServer,
  ServerQuota,
  getLandingUser,
  landingGB,
  landingServers,
  LandingServer,
  saveLandingUser,
} from "@/api/landing";
import { NodeApiItem } from "@/api/types";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import {
  RelatedServerLabel,
  TrafficPoolsEditor,
  serverKey,
  serverRoleText,
} from "@/pages/landing/traffic-pools";

export interface LandingUserSettingsHandle {
  // Saves unsaved 落地 edits (and the optional renewal) with the user's main save.
  persist: () => Promise<void>;
}
const landingSnapshot = (value: LandingUserSetting | null) =>
  value
    ? JSON.stringify({
        maxNodesSource: value.maxNodesSource,
        maxNodes: value.maxNodesSource === "global" ? null : value.maxNodes,
        quotas: value.quotas.map(({ serverKind, serverRefId, limitBytes }) => [
          serverKind,
          serverRefId,
          limitBytes,
        ]),
        serverIds: [...(value.serverIds || [])].sort((a, b) => a - b),
        pools: (value.pools || []).map((pool) => [
          pool.id || 0,
          pool.name,
          pool.limitBytes,
          pool.members.map(serverKey).sort(),
        ]),
        ruleMaxNode: [
          value.ruleMaxNodeSource,
          value.ruleMaxNodeSource === "custom" ? value.ruleMaxNode : null,
        ],
        ruleMaxTotal: [
          value.ruleMaxTotalSource,
          value.ruleMaxTotalSource === "custom" ? value.ruleMaxTotal : null,
        ],
      })
    : "";

// Plan 134: 跟随全局 / 自定义 for one node-rule cap.
function RuleCapField({
  label,
  source,
  value,
  globalValue,
  hint,
  onChange,
}: {
  label: string;
  source?: "global" | "custom" | "admin";
  value?: number;
  globalValue?: number;
  hint: string;
  onChange: (source: "global" | "custom", value: number) => void;
}) {
  if (source !== "global" && source !== "custom") return null;

  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Select
        description={
          source === "global"
            ? "使用「出站与路由 → 全局规则 → 用户规则」的值，修改后立即生效"
            : hint
        }
        label={label}
        selectedKeys={[source]}
        variant="bordered"
        onSelectionChange={(keys) => {
          const next = String(Array.from(keys)[0] || "");

          if (next !== "global" && next !== "custom") return;
          onChange(next, next === "global" ? (globalValue ?? 0) : (value ?? 0));
        }}
      >
        <SelectItem key="global">{`跟随全局（当前 ${globalValue ?? 0}）`}</SelectItem>
        <SelectItem key="custom">自定义</SelectItem>
      </Select>
      {source === "custom" && (
        <Input
          description="0 表示不能添加节点规则；调低不会删除已有规则"
          label="自定义数量"
          min="0"
          type="number"
          value={String(value ?? 0)}
          variant="bordered"
          onChange={(event) => onChange("custom", Number(event.target.value))}
        />
      )}
    </div>
  );
}
export const LandingUserSettings = forwardRef<
  LandingUserSettingsHandle,
  { userId: number; originalExpiry: number; expiry: number }
>(function LandingUserSettings({ userId, originalExpiry, expiry }, ref) {
  const [form, setForm] = useState<LandingUserSetting | null>(null);
  const [nodes, setNodes] = useState<NodeApiItem[]>([]);
  const [shared, setShared] = useState<LandingServer[]>([]);
  const [allServers, setAllServers] = useState<LandingServer[]>([]);
  const [savedSnapshot, setSavedSnapshot] = useState("");
  const [adding, setAdding] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [extend, setExtend] = useState(false);
  const renewalDelta =
    originalExpiry > 0 && expiry > originalExpiry ? expiry - originalExpiry : 0;

  useEffect(() => {
    let disposed = false;
    setForm(null);
    setError("");
    setExtend(false);
    void Promise.all([
      getLandingUser(userId),
      getNodeList(),
      landingServers(),
    ]).then(([settings, nodeRes, serverRes]) => {
      if (disposed) return;
      if (settings.code === 0) {
        const loaded = {
          ...settings.data,
          serverIds: settings.data.serverIds || [],
        };
        setForm(loaded);
        setSavedSnapshot(landingSnapshot(loaded));
      } else setError(settings.msg);
      if (nodeRes.code === 0) setNodes(nodeRes.data || []);
      if (serverRes.code === 0) {
        setShared(
          (serverRes.data || []).filter((server) => server.source === "shared"),
        );
        setAllServers(serverRes.data || []);
      }
    });
    return () => {
      disposed = true;
    };
  }, [userId]);
  useImperativeHandle(
    ref,
    () => ({
      persist: async () => {
        if (!form) return;
        const renewing = extend && renewalDelta > 0;
        const dirty = landingSnapshot(form) !== savedSnapshot;
        if (!renewing && !dirty) return;
        const res = await saveLandingUser(userId, {
          ...form,
          extendFixedByMs: renewing ? renewalDelta : 0,
        });
        if (res.code === 0) {
          setSavedSnapshot(landingSnapshot(form));
          if (renewing) {
            toast.success("独立有效期落地节点已一并延长");
            setExtend(false);
          }
          if (dirty) toast.success("落地设置已一并保存");
        } else
          toast.error(
            renewing
              ? `用户已保存，但落地节点续期失败：${res.msg}`
              : `用户已保存，但落地设置保存失败：${res.msg}`,
          );
      },
    }),
    [extend, renewalDelta, form, userId, savedSnapshot],
  );
  // Plan 157: servers related to this user (tunnel/rule entries + landing
  // servers) for the per-server picker and the pools; 可用落地服务器 ticked but
  // not saved yet count as 落地 too. Older panels send no list: keep the old
  // picker (all FLVX nodes + shared servers).
  const related = useMemo<RelatedServer[] | null>(() => {
    if (!form?.relatedServers) return null;
    const map = new Map<string, RelatedServer>();

    for (const server of form.relatedServers)
      map.set(serverKey(server), { ...server });
    for (const id of form.serverIds || []) {
      const server = allServers.find((item) => item.id === id);

      if (!server || server.chainOnly) continue;
      const shared = server.source === "shared";
      const item: RelatedServer = {
        serverKind: shared ? "landing" : "node",
        serverRefId: shared ? server.id : server.nodeId,
        serverName: shared
          ? server.name
          : nodes.find((node) => node.id === server.nodeId)?.name ||
            server.name,
        entry: false,
        landing: true,
      };
      const existing = map.get(serverKey(item));

      if (existing) existing.landing = true;
      else map.set(serverKey(item), item);
    }

    return Array.from(map.values()).sort((a, b) =>
      a.serverKind !== b.serverKind
        ? a.serverKind === "node"
          ? -1
          : 1
        : a.serverRefId - b.serverRefId,
    );
  }, [form?.relatedServers, form?.serverIds, allServers, nodes]);
  const relatedByKey = useMemo(
    () => new Map((related || []).map((server) => [serverKey(server), server])),
    [related],
  );
  const serverName = (server: PoolServer) =>
    server.serverName ||
    relatedByKey.get(serverKey(server))?.serverName ||
    (server.serverKind === "node" ? nodes : shared).find(
      (item) => item.id === server.serverRefId,
    )?.name ||
    `${server.serverKind === "node" ? "节点" : "落地"} ${server.serverRefId}`;
  const quotaOptions = related
    ? related.map((server) => ({
        key: serverKey(server),
        kind: server.serverKind,
        id: server.serverRefId,
        name: serverName(server),
        server,
      }))
    : [
        ...nodes.map((node) => ({
          key: `node:${node.id}`,
          kind: "node" as const,
          id: node.id,
          name: node.name,
          server: undefined,
        })),
        ...shared.map((server) => ({
          key: `landing:${server.id}`,
          kind: "landing" as const,
          id: server.id,
          name: `${server.name}（共享）`,
          server: undefined,
        })),
      ];
  const setQuota = (index: number, limitBytes: number) =>
    setForm(
      (prev) =>
        prev && {
          ...prev,
          quotas: prev.quotas.map((quota, i) =>
            i === index ? { ...quota, limitBytes, limitOverride: true } : quota,
          ),
        },
    );

  return (
    <section className="mt-5 space-y-3 border-t border-divider pt-5">
      <h3 className="font-semibold">落地</h3>
      <p className="text-xs text-default-500">
        以下设置可点「保存落地设置」单独保存；有改动时点弹窗「保存」也会一并保存。服务器额度合并统计该用户经过此服务器的规则与落地流量。
      </p>
      {error && <p className="text-sm text-danger">{error}</p>}
      {form && (
        <>
          {form.maxNodesSource === "global" ||
          form.maxNodesSource === "custom" ? (
            <div className="grid gap-3 sm:grid-cols-2">
              <Select
                description={
                  form.maxNodesSource === "global"
                    ? "使用落地页「全局设置」的默认值，全局值修改后立即生效"
                    : "该用户单独的数量，不随全局值变化"
                }
                label="可建落地节点总数"
                selectedKeys={[form.maxNodesSource]}
                variant="bordered"
                onSelectionChange={(keys) => {
                  const source = String(Array.from(keys)[0] || "");

                  if (source !== "global" && source !== "custom") return;
                  setForm({
                    ...form,
                    maxNodesSource: source,
                    maxNodes:
                      source === "global"
                        ? (form.globalMaxNodes ?? form.maxNodes)
                        : form.maxNodes,
                  });
                }}
              >
                <SelectItem key="global">
                  {`跟随全局（当前 ${form.globalMaxNodes ?? 5}）`}
                </SelectItem>
                <SelectItem key="custom">自定义</SelectItem>
              </Select>
              {form.maxNodesSource === "custom" && (
                <Input
                  description="0 表示禁止新建落地节点；调低不会删除已有节点"
                  label="自定义数量"
                  min="0"
                  type="number"
                  value={String(form.maxNodes)}
                  variant="bordered"
                  onChange={(event) =>
                    setForm({ ...form, maxNodes: Number(event.target.value) })
                  }
                />
              )}
            </div>
          ) : (
            <Input
              description="默认 5；0 表示禁止新建落地节点"
              label="可建落地节点总数"
              min="0"
              type="number"
              value={String(form.maxNodes)}
              variant="bordered"
              onChange={(event) =>
                setForm({ ...form, maxNodes: Number(event.target.value) })
              }
            />
          )}
          <RuleCapField
            globalValue={form.globalRuleMaxNode}
            hint="该用户每个节点最多可加的节点规则条数"
            label="节点规则：每个节点最多"
            source={form.ruleMaxNodeSource}
            value={form.ruleMaxNode}
            onChange={(source, value) =>
              setForm({
                ...form,
                ruleMaxNodeSource: source,
                ruleMaxNode: value,
              })
            }
          />
          <RuleCapField
            globalValue={form.globalRuleMaxTotal}
            hint="该用户所有节点合计最多可加的节点规则条数"
            label={`节点规则：每个用户总计最多${form.ruleUsed ? `（已用 ${form.ruleUsed} 条）` : ""}`}
            source={form.ruleMaxTotalSource}
            value={form.ruleMaxTotal}
            onChange={(source, value) =>
              setForm({
                ...form,
                ruleMaxTotalSource: source,
                ruleMaxTotal: value,
              })
            }
          />
          <div className="space-y-2">
            <h4 className="text-sm font-medium">可用落地服务器</h4>
            <p className="text-xs text-default-500">
              勾选后该用户可查看并在此服务器创建节点（与服务器「可见用户组」二者满足其一即可）。取消勾选即撤销，已有节点会被停用。
            </p>
            <div className="flex flex-wrap gap-4">
              {allServers.map((server) => (
                <Checkbox
                  key={server.id}
                  isSelected={(form.serverIds || []).includes(server.id)}
                  onValueChange={(checked) =>
                    setForm({
                      ...form,
                      serverIds: checked
                        ? [...(form.serverIds || []), server.id]
                        : (form.serverIds || []).filter(
                            (id) => id !== server.id,
                          ),
                    })
                  }
                >
                  {server.name}
                  {server.source === "shared" ? "（共享）" : ""}
                </Checkbox>
              ))}
            </div>
            {allServers.length === 0 && (
              <p className="text-sm text-default-500">暂无落地服务器。</p>
            )}
          </div>
          <div className="space-y-2">
            <div className="grid grid-cols-[minmax(0,1fr)_100px_80px] gap-2 text-xs text-default-500">
              <span>服务器</span>
              <span>限额 (GB，计费流量)</span>
              <span>已用 (GB)</span>
            </div>
            {form.quotas.map((quota, index) => (
              <div
                key={`${quota.serverKind}-${quota.serverRefId}`}
                className="grid grid-cols-[minmax(0,1fr)_100px_80px] items-center gap-2"
              >
                <div className="min-w-0 text-sm">
                  <p className="flex min-w-0">
                    <RelatedServerLabel
                      name={
                        quota.serverName ||
                        (quota.serverKind === "node" ? nodes : shared).find(
                          (node) => node.id === quota.serverRefId,
                        )?.name ||
                        `${quota.serverKind === "node" ? "节点" : "落地"} ${quota.serverRefId}`
                      }
                      server={relatedByKey.get(serverKey(quota))}
                    />
                  </p>
                  {quota.paused && (
                    <p className="text-xs text-warning">额度已用完</p>
                  )}
                </div>
                <Input
                  aria-label={`${quota.serverName || quota.serverRefId} 限额 GB`}
                  min="0"
                  size="sm"
                  step="0.01"
                  type="number"
                  value={String(quota.limitBytes / 1073741824)}
                  onChange={(event) =>
                    setQuota(
                      index,
                      Math.round(Number(event.target.value) * 1073741824),
                    )
                  }
                />
                <span className="text-sm">
                  {landingGB(quota.usedIn + quota.usedOut)}
                </span>
              </div>
            ))}
          </div>
          <p className="text-xs text-default-500">
            0
            不限；未单独设置的服务器使用「每个用户默认限额」，周期跟随用户流量重置日。
            <br />
            已用 = 计费流量：双向 (上传+下载)×倍率×2，单向
            (上传+下载)×倍率，按各服务器设置。
          </p>
          <div className="flex items-end gap-2">
            <Select
              className="min-w-0 flex-1"
              label="添加服务器限额"
              placeholder={
                related ? "选择该用户的相关服务器" : "选择 FLVX 服务器"
              }
              selectedKeys={adding ? [adding] : []}
              onSelectionChange={(keys) =>
                setAdding(String(Array.from(keys)[0] || ""))
              }
            >
              {quotaOptions
                .filter(
                  (option) =>
                    !form.quotas.some(
                      (quota) =>
                        quota.serverKind === option.kind &&
                        quota.serverRefId === option.id,
                    ),
                )
                .map((option) => (
                  <SelectItem
                    key={option.key}
                    textValue={`${option.name} ${serverRoleText(option.server)}`}
                  >
                    <RelatedServerLabel
                      name={option.name}
                      server={option.server}
                    />
                  </SelectItem>
                ))}
            </Select>
            <Button
              color="success"
              isDisabled={!adding}
              variant="flat"
              onPress={() => {
                const [kind, id] = adding.split(":");
                const node = (kind === "landing" ? shared : nodes).find(
                  (item) => item.id === Number(id),
                );
                if (!node) return;
                const quota: ServerQuota = {
                  serverKind: kind === "landing" ? "landing" : "node",
                  serverRefId: node.id,
                  serverName: serverName({
                    serverKind: kind === "landing" ? "landing" : "node",
                    serverRefId: node.id,
                  }),
                  limitBytes: Number(node.defaultUserQuotaBytes || 0),
                  usedIn: 0,
                  usedOut: 0,
                };
                const serverIds = form.serverIds || [];
                setForm({
                  ...form,
                  quotas: [...form.quotas, quota],
                  // A per-server limit on a shared server also grants it.
                  serverIds:
                    quota.serverKind === "landing" &&
                    !serverIds.includes(node.id)
                      ? [...serverIds, node.id]
                      : serverIds,
                });
                setAdding("");
              }}
            >
              添加
            </Button>
          </div>
          {form.pools && (
            <TrafficPoolsEditor
              candidates={related || []}
              nameOf={serverName}
              pools={form.pools}
              onChange={(pools) => setForm({ ...form, pools })}
            />
          )}
          <Button
            color="primary"
            isLoading={saving}
            size="sm"
            variant="flat"
            onPress={async () => {
              setSaving(true);
              const res = await saveLandingUser(userId, form);
              setSaving(false);
              if (res.code === 0) {
                setSavedSnapshot(landingSnapshot(form));
                toast.success("落地设置已保存");
              } else toast.error(res.msg);
            }}
          >
            保存落地设置
          </Button>
          <p className="text-xs text-default-500">
            跟随用户的落地节点将在用户续期后自动恢复。
          </p>
          {renewalDelta > 0 && (form.fixedNodeCount ?? 0) > 0 && (
            <Checkbox isSelected={extend} onValueChange={setExtend}>
              保存用户时，将 {form.fixedNodeCount} 个独立有效期节点一并延长{" "}
              {Math.round((renewalDelta / 86400000) * 10) / 10} 天
            </Checkbox>
          )}
        </>
      )}
    </section>
  );
});
