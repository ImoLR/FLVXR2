import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import toast from "react-hot-toast";

import { getNodeList } from "@/api";
import {
  LandingUserSetting,
  ServerQuota,
  getLandingUser,
  landingGB,
  saveLandingUser,
} from "@/api/landing";
import { NodeApiItem } from "@/api/types";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

export interface LandingUserSettingsHandle {
  renew: () => Promise<void>;
}
export const LandingUserSettings = forwardRef<
  LandingUserSettingsHandle,
  { userId: number; originalExpiry: number; expiry: number }
>(function LandingUserSettings({ userId, originalExpiry, expiry }, ref) {
  const [form, setForm] = useState<LandingUserSetting | null>(null);
  const [nodes, setNodes] = useState<NodeApiItem[]>([]);
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
    void Promise.all([getLandingUser(userId), getNodeList()]).then(
      ([settings, nodeRes]) => {
        if (disposed) return;
        if (settings.code === 0) setForm(settings.data);
        else setError(settings.msg);
        if (nodeRes.code === 0) setNodes(nodeRes.data || []);
      },
    );
    return () => {
      disposed = true;
    };
  }, [userId]);
  useImperativeHandle(
    ref,
    () => ({
      renew: async () => {
        if (!extend || !renewalDelta || !form) return;
        const res = await saveLandingUser(userId, {
          ...form,
          extendFixedByMs: renewalDelta,
        });
        if (res.code === 0) {
          toast.success("独立有效期落地节点已一并延长");
          setExtend(false);
        } else toast.error(`用户已保存，但落地节点续期失败：${res.msg}`);
      },
    }),
    [extend, renewalDelta, form, userId],
  );
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
        以下节点数和服务器额度单独保存。服务器额度合并统计该用户经过此服务器的规则与落地流量。
      </p>
      {error && <p className="text-sm text-danger">{error}</p>}
      {form && (
        <>
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
          <div className="space-y-2">
            <div className="grid grid-cols-[minmax(0,1fr)_100px_80px] gap-2 text-xs text-default-500">
              <span>服务器</span>
              <span>限额 (GB)</span>
              <span>已用 (GB)</span>
            </div>
            {form.quotas.map((quota, index) => (
              <div
                key={`${quota.serverKind}-${quota.serverRefId}`}
                className="grid grid-cols-[minmax(0,1fr)_100px_80px] items-center gap-2"
              >
                <div className="min-w-0 text-sm">
                  <p className="truncate">
                    {quota.serverName ||
                      nodes.find((node) => node.id === quota.serverRefId)
                        ?.name ||
                      `${quota.serverKind === "node" ? "节点" : "落地"} ${quota.serverRefId}`}
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
          </p>
          <div className="flex items-end gap-2">
            <Select
              className="min-w-0 flex-1"
              label="添加服务器限额"
              placeholder="选择 FLVX 服务器"
              selectedKeys={adding ? [adding] : []}
              onSelectionChange={(keys) =>
                setAdding(String(Array.from(keys)[0] || ""))
              }
            >
              {nodes
                .filter(
                  (node) =>
                    !form.quotas.some(
                      (quota) =>
                        quota.serverKind === "node" &&
                        quota.serverRefId === node.id,
                    ),
                )
                .map((node) => (
                  <SelectItem key={String(node.id)}>{node.name}</SelectItem>
                ))}
            </Select>
            <Button
              isDisabled={!adding}
              variant="flat"
              onPress={() => {
                const node = nodes.find((item) => item.id === Number(adding));
                if (!node) return;
                const quota: ServerQuota = {
                  serverKind: "node",
                  serverRefId: node.id,
                  serverName: node.name,
                  limitBytes: Number(node.defaultUserQuotaBytes || 0),
                  usedIn: 0,
                  usedOut: 0,
                };
                setForm({ ...form, quotas: [...form.quotas, quota] });
                setAdding("");
              }}
            >
              添加
            </Button>
          </div>
          <Button
            color="primary"
            isLoading={saving}
            size="sm"
            variant="flat"
            onPress={async () => {
              setSaving(true);
              const res = await saveLandingUser(userId, form);
              setSaving(false);
              if (res.code === 0) toast.success("落地设置已保存");
              else toast.error(res.msg);
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
