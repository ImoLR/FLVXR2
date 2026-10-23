import { useCallback, useEffect, useState } from "react";
import toast from "react-hot-toast";
import { Lock, Plus, RefreshCw } from "lucide-react";

import { LandingGlobalRules } from "./egress-global";
import { ProfileEditor } from "./egress-profile-editor";
import { OutboundEditor } from "./egress-outbound-editor";

import {
  EgressOutbound,
  EgressOverview,
  EgressProfile,
  abuseDefaultRules,
  abuseDefaultsOn,
  actionLabel,
  deleteEgressOutbound,
  deleteEgressProfile,
  egressOverview,
  ipVersionLabel,
  outboundTypeColor,
  outboundTypeLabel,
  proxyProtocolLabel,
  registerEgressWarp,
  saveEgressWhitelist,
} from "@/api/landing-egress";
import { landingDate } from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Textarea } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";

function outboundSummary(o: EgressOutbound, overview: EgressOverview) {
  const s = o.settings || {};

  switch (o.type) {
    case "direct":
      return "从落地服务器直接访问目标";
    case "warp":
      return s.warpMode === "manual"
        ? `手动填写 · ${s.endpoint || "端点自动选择"}`
        : `自动注册 · 每台服务器各自一个账号${s.warpLicense ? " · WARP+" : ""}`;
    case "proxy":
      return `${proxyProtocolLabel[s.protocol || "socks"]} · ${s.server}:${s.port}`;
    case "landing_node": {
      const n = overview.landingNodes.find((x) => x.id === s.landingNodeId);
      const via = overview.forwards.find((f) => f.id === s.viaForwardId);

      return `${n ? `${n.name} · ${n.serverName}` : "节点已删除"}${via ? ` · 经规则「${via.name}」` : ""}`;
    }
  }

  return "";
}

const warpStatus: Record<
  string,
  { label: string; color: "success" | "warning" | "danger" | "default" }
> = {
  ok: { label: "已注册", color: "success" },
  error: { label: "注册失败", color: "danger" },
  pending: { label: "等待注册", color: "warning" },
};

export default function LandingEgressTab() {
  const [overview, setOverview] = useState<EgressOverview | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [editingOutbound, setEditingOutbound] =
    useState<Partial<EgressOutbound> | null>(null);
  const [editingProfile, setEditingProfile] =
    useState<Partial<EgressProfile> | null>(null);
  const [confirm, setConfirm] = useState<{
    title: string;
    text: string;
    run: () => Promise<void>;
  } | null>(null);
  const [whitelist, setWhitelist] = useState("");
  const [busy, setBusy] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    const res = await egressOverview();

    setLoading(false);
    if (res.code !== 0) {
      setError(res.msg);

      return;
    }
    setError("");
    setOverview(res.data);
    setWhitelist((res.data.whitelist || []).join("\n"));
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!overview) {
    return (
      <div className="mt-5 flex justify-center p-6">
        {loading ? <Spinner /> : <p className="text-danger">{error}</p>}
      </div>
    );
  }
  const outboundName = (id: number) =>
    overview.outbounds.find((o) => o.id === id)?.name || `出站 ${id}`;

  return (
    <div className="mt-5 space-y-4">
      <div className="flex items-start justify-between gap-3">
        <p className="text-sm text-default-500">
          出站模板和路由方案在这里统一管理，可被所有落地服务器复用；在「落地服务器
          →
          设置」中选择方案，节点详情里可单独覆盖。用户只能看到「出站：…」摘要。
        </p>
        <Button
          isIconOnly
          aria-label="刷新出站与路由"
          isLoading={loading}
          variant="flat"
          onPress={() => void load()}
        >
          <RefreshCw size={18} />
        </Button>
      </div>
      {error && (
        <p className="rounded-lg bg-danger-50 p-3 text-sm text-danger">
          {error}
        </p>
      )}
      <LandingGlobalRules
        overview={overview}
        onSaved={() => void load()}
        onWhitelistSaved={(list) => {
          setWhitelist(list.join("\n"));
          setOverview((prev) => (prev ? { ...prev, whitelist: list } : prev));
        }}
      />
      <div className="grid gap-4 xl:grid-cols-2 xl:items-start">
        <Card>
          <CardHeader className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <h2 className="font-semibold">出站</h2>
              <p className="mt-1 text-xs text-default-500">
                直连、WARP、代理或另一个 FLVX 落地节点
              </p>
            </div>
            <Button
              color="success"
              size="sm"
              startContent={<Plus size={16} />}
              onPress={() =>
                setEditingOutbound({ type: "warp", ipVersion: "system" })
              }
            >
              新建出站
            </Button>
          </CardHeader>
          <CardBody>
            <div className="divide-y divide-divider">
              {[
                ["直连（默认）", "内置 · 跟随系统 · 所有服务器默认使用"],
                ["封锁", "内置 · 丢弃连接"],
              ].map(([name, text]) => (
                <div key={name} className="flex items-start gap-3 py-3">
                  <Lock className="mt-1 shrink-0 text-default-400" size={16} />
                  <div className="min-w-0">
                    <span className="font-medium">{name}</span>
                    <p className="text-xs text-default-500">{text}</p>
                  </div>
                </div>
              ))}
              {overview.outbounds.map((o) => (
                <div
                  key={o.id}
                  className="flex flex-col gap-2 py-3 sm:flex-row sm:items-start sm:justify-between"
                >
                  <div className="min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="break-all font-medium">{o.name}</span>
                      <Chip
                        color={outboundTypeColor[o.type]}
                        size="sm"
                        variant="flat"
                      >
                        {outboundTypeLabel[o.type]}
                      </Chip>
                      <Chip
                        className={
                          o.ipVersion.startsWith("force")
                            ? undefined
                            : "text-default-600"
                        }
                        color={
                          o.ipVersion.startsWith("force")
                            ? "warning"
                            : "default"
                        }
                        size="sm"
                        variant={
                          o.ipVersion.startsWith("force") ? "flat" : "bordered"
                        }
                      >
                        {ipVersionLabel(o.ipVersion)}
                      </Chip>
                      {o.userAllowed && (
                        <Chip color="secondary" size="sm" variant="flat">
                          允许用户使用
                        </Chip>
                      )}
                    </div>
                    <p className="break-all text-xs text-default-500">
                      {outboundSummary(o, overview)}
                    </p>
                    {o.usedBy && o.usedBy.length > 0 && (
                      <p className="text-xs text-default-500">
                        用于方案：{o.usedBy.join("、")}
                      </p>
                    )}
                    {(o.usedByGlobal || Boolean(o.usedByNodes)) && (
                      <p className="text-xs text-default-500">
                        {[
                          o.usedByGlobal ? "用于全局规则" : "",
                          o.usedByNodes
                            ? `${o.usedByNodes} 个节点的路由规则`
                            : "",
                        ]
                          .filter(Boolean)
                          .join(" · ")}
                      </p>
                    )}
                  </div>
                  <div className="flex shrink-0 gap-2">
                    <Button
                      color="primary"
                      size="sm"
                      variant="flat"
                      onPress={() => setEditingOutbound(o)}
                    >
                      编辑
                    </Button>
                    <Button
                      color="danger"
                      size="sm"
                      variant="flat"
                      onPress={() =>
                        setConfirm({
                          title: "删除出站",
                          text: `删除「${o.name}」？正在被路由方案使用的出站不能删除。`,
                          run: async () => {
                            const res = await deleteEgressOutbound(o.id);

                            if (res.code !== 0) throw new Error(res.msg);
                          },
                        })
                      }
                    >
                      删除
                    </Button>
                  </div>
                </div>
              ))}
            </div>
            {!overview.outbounds.length && (
              <p className="pt-2 text-sm text-default-500">
                还没有出站模板。新建 WARP 或代理后，在路由方案里使用它。
              </p>
            )}
          </CardBody>
        </Card>

        <Card>
          <CardHeader className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <h2 className="font-semibold">服务器路由方案</h2>
              <p className="mt-1 text-xs text-default-500">
                有序规则 + 默认出站，按落地服务器或节点选择使用
              </p>
            </div>
            <Button
              color="success"
              size="sm"
              startContent={<Plus size={16} />}
              onPress={() =>
                setEditingProfile({
                  name: "",
                  defaultAction: "direct",
                  rules: [],
                })
              }
            >
              新建方案
            </Button>
          </CardHeader>
          <CardBody>
            <div className="divide-y divide-divider">
              <div className="flex items-start gap-3 py-3">
                <Lock className="mt-1 shrink-0 text-default-400" size={16} />
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">
                      默认（跟随全局默认出站）
                    </span>
                    <Chip
                      className="text-default-600"
                      size="sm"
                      variant="bordered"
                    >
                      内置
                    </Chip>
                  </div>
                  <p className="text-xs text-default-500">
                    安全规则 +
                    默认防滥用规则，其余走全局默认出站（未设置时直连）。现有服务器和节点都在使用它。
                  </p>
                  <div className="flex flex-wrap gap-1">
                    {abuseDefaultRules
                      // Plan 140 / 143: only the default rules switched on
                      // globally (SMTP / BT default on, the others off).
                      .filter((rule) => {
                        const b = overview.global?.builtin;

                        return rule.key === "smtp" || rule.key === "bt"
                          ? b?.[rule.key] !== false
                          : !!b?.[rule.key];
                      })
                      .map((rule) => (
                        <Chip
                          key={rule.name}
                          color="danger"
                          size="sm"
                          variant="flat"
                        >
                          {rule.name.replace("默认：", "")}
                        </Chip>
                      ))}
                  </div>
                </div>
              </div>
              {overview.profiles.map((p) => (
                <div
                  key={p.id}
                  className="flex flex-col gap-2 py-3 sm:flex-row sm:items-start sm:justify-between"
                >
                  <div className="min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="break-all font-medium">{p.name}</span>
                      <Chip
                        className="text-default-600"
                        size="sm"
                        variant="bordered"
                      >
                        {p.rules.filter((r) => r.enabled).length} 条规则
                      </Chip>
                      <Chip color="success" size="sm" variant="flat">
                        默认出站：
                        {p.defaultAction === "direct"
                          ? "直连"
                          : actionLabel(p.defaultAction, overview.outbounds)}
                      </Chip>
                      {!abuseDefaultsOn(p) && (
                        <Chip color="warning" size="sm" variant="flat">
                          默认防滥用：已关闭
                        </Chip>
                      )}
                    </div>
                    <p className="text-xs text-default-500">
                      {p.serverCount || p.nodeCount
                        ? `${p.serverCount || 0} 台服务器 · ${p.nodeCount || 0} 个节点单独使用`
                        : "尚未使用"}
                    </p>
                  </div>
                  <div className="flex shrink-0 gap-2">
                    <Button
                      color="primary"
                      size="sm"
                      variant="flat"
                      onPress={() => setEditingProfile(p)}
                    >
                      编辑
                    </Button>
                    <Button
                      color="danger"
                      size="sm"
                      variant="flat"
                      onPress={() =>
                        setConfirm({
                          title: "删除路由方案",
                          text: `删除「${p.name}」？正在被服务器或节点使用的方案不能删除。`,
                          run: async () => {
                            const res = await deleteEgressProfile(p.id);

                            if (res.code !== 0) throw new Error(res.msg);
                          },
                        })
                      }
                    >
                      删除
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          </CardBody>
        </Card>
      </div>

      <div className="grid gap-4 xl:grid-cols-2 xl:items-start">
        <Card>
          <CardHeader>
            <div className="min-w-0">
              <h2 className="font-semibold">来源豁免</h2>
              <p className="mt-1 text-xs text-default-500">
                「来源 IP」规则（例如禁止大陆直连）不作用于这些来源
              </p>
            </div>
          </CardHeader>
          <CardBody className="space-y-4">
            <div className="space-y-2">
              <p className="text-sm">
                FLVX 节点地址：已自动包含全部 {overview.exemptNodeCount}{" "}
                个，节点增删改后自动更新
              </p>
              <div className="flex flex-wrap gap-1">
                {overview.exemptPreview.map((cidr) => (
                  <Chip
                    key={cidr}
                    className="text-default-600"
                    size="sm"
                    variant="bordered"
                  >
                    {cidr}
                  </Chip>
                ))}
                {overview.exemptNodeCount > overview.exemptPreview.length && (
                  <Chip
                    className="text-default-600"
                    size="sm"
                    variant="bordered"
                  >
                    等 {overview.exemptNodeCount} 个
                  </Chip>
                )}
              </div>
            </div>
            <Textarea
              description="每行一个 IP 或 CIDR，例如第三方中转的出口地址；保存后约 1 分钟内下发"
              label="来源白名单"
              minRows={3}
              placeholder={"203.0.113.10\n198.51.100.0/24"}
              value={whitelist}
              variant="bordered"
              onChange={(event) => setWhitelist(event.target.value)}
            />
            <div className="flex justify-end">
              <Button
                color="success"
                isLoading={busy === "whitelist"}
                size="sm"
                onPress={async () => {
                  setBusy("whitelist");
                  const res = await saveEgressWhitelist(
                    whitelist
                      .split(/[\n,，\s]+/)
                      .map((v) => v.trim())
                      .filter(Boolean),
                  );

                  setBusy("");
                  if (res.code !== 0) {
                    toast.error(res.msg);

                    return;
                  }
                  setWhitelist(res.data.whitelist.join("\n"));
                  setOverview((prev) =>
                    prev ? { ...prev, whitelist: res.data.whitelist } : prev,
                  );
                  toast.success("来源白名单已保存");
                }}
              >
                保存白名单
              </Button>
            </div>
          </CardBody>
        </Card>

        <Card>
          <CardHeader>
            <div className="min-w-0">
              <h2 className="font-semibold">WARP 账号</h2>
              <p className="mt-1 text-xs text-default-500">
                每台落地服务器各自注册，首次使用时自动完成
              </p>
            </div>
          </CardHeader>
          <CardBody>
            <div className="divide-y divide-divider">
              {overview.warpAccounts.map((a) => {
                const status = warpStatus[a.status] || warpStatus.pending;
                const key = `${a.serverId}-${a.outboundId}`;

                return (
                  <div
                    key={key}
                    className="flex flex-col gap-2 py-3 sm:flex-row sm:items-start sm:justify-between"
                  >
                    <div className="min-w-0 space-y-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">{a.serverName}</span>
                        <Chip color="secondary" size="sm" variant="flat">
                          {outboundName(a.outboundId)}
                        </Chip>
                        <Chip color={status.color} size="sm" variant="flat">
                          {status.label}
                        </Chip>
                        {a.accountType && (
                          <Chip
                            className="text-default-600"
                            size="sm"
                            variant="bordered"
                          >
                            {a.accountType === "free" ? "免费" : "WARP+"}
                          </Chip>
                        )}
                      </div>
                      {(a.addressV4 || a.addressV6) && (
                        <p className="break-all text-xs text-default-500">
                          {[a.addressV4, a.addressV6]
                            .filter(Boolean)
                            .join(" · ")}
                        </p>
                      )}
                      {a.lastError && (
                        <p className="text-xs text-danger">{a.lastError}</p>
                      )}
                      <p className="text-xs text-default-500">
                        更新于 {landingDate(a.updatedTime)}
                      </p>
                    </div>
                    <Button
                      color="warning"
                      isLoading={busy === key}
                      size="sm"
                      variant="flat"
                      onPress={async () => {
                        setBusy(key);
                        const res = await registerEgressWarp(
                          a.serverId,
                          a.outboundId,
                        );

                        setBusy("");
                        if (res.code !== 0) {
                          toast.error(res.msg);
                        } else {
                          toast.success("已重新注册");
                        }
                        void load();
                      }}
                    >
                      重新注册
                    </Button>
                  </div>
                );
              })}
            </div>
            {!overview.warpAccounts.length && (
              <p className="text-sm text-default-500">
                还没有 WARP 账号。服务器的路由方案用到 WARP 出站时会自动注册。
              </p>
            )}
          </CardBody>
        </Card>
      </div>

      {editingOutbound && (
        <OutboundEditor
          outbound={editingOutbound}
          overview={overview}
          onClose={() => setEditingOutbound(null)}
          onSaved={() => void load()}
        />
      )}
      {editingProfile && (
        <ProfileEditor
          overview={overview}
          profile={editingProfile}
          onClose={() => setEditingProfile(null)}
          onSaved={() => void load()}
        />
      )}
      <Modal isOpen={Boolean(confirm)} onClose={() => setConfirm(null)}>
        <ModalContent className="flvx-landing-text">
          <ModalHeader>{confirm?.title}</ModalHeader>
          <ModalBody>
            <p>{confirm?.text}</p>
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setConfirm(null)}
            >
              取消
            </Button>
            <Button
              color="danger"
              isLoading={busy === "confirm"}
              onPress={async () => {
                if (!confirm) return;
                setBusy("confirm");
                try {
                  await confirm.run();
                  toast.success("已删除");
                  setConfirm(null);
                  void load();
                } catch (err) {
                  toast.error(err instanceof Error ? err.message : "删除失败");
                } finally {
                  setBusy("");
                }
              }}
            >
              删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  );
}
