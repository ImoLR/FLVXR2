import type {
  LeiKwanGrant,
  LeiKwanHostList,
  LeiKwanHostManage,
  LeiKwanImportPlan,
  LeiKwanLine,
  LeiKwanLineList,
} from "@/api/leikwan-line";

import { useCallback, useEffect, useState } from "react";
import { Download, Plus, RefreshCw } from "lucide-react";
import toast from "react-hot-toast";

import { getAllUsers } from "@/api";
import {
  applyLeiKwanImport,
  deleteLeiKwanGrant,
  getLeiKwanGrants,
  getLeiKwanHosts,
  getLeiKwanLines,
  previewLeiKwanImport,
} from "@/api/leikwan-line";
import { useH5Mode } from "@/hooks/useH5Mode";
import { AttentionIcon } from "@/pages/landing/admin-attention";
import { Pill } from "@/pages/landing/pill";
import { LeiKwanGrantDialog } from "@/pages/leikwan/panel/grant-dialog";
import {
  HostManageBlock,
  HostNameMarker,
  hostAttentionLevel,
  hostsAttentionLevel,
} from "@/pages/leikwan/panel/host-manage";
import { LeiKwanLineDialog } from "@/pages/leikwan/panel/line-dialog";
import { LeiKwanLinesTable } from "@/pages/leikwan/panel/lines-table";
import {
  HostValuesTable,
  KV,
  OnOff,
  QuotaPill,
  SectionTitle,
  TriggerList,
} from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import { Tab, Tabs } from "@/shadcn-bridge/heroui/tabs";

// Plan 266: the new part of the 利群汇聚 page (admin): 专线 (tunnel group
// 「利群汇聚」 + 利群加速), 利群入口 (node group + read-only host status), 授权
// (主入口 + 备线 per user) and 导入为规则 (dry-run preview first).

export function LeiKwanPanelSection() {
  const h5 = useH5Mode();
  const [tab, setTab] = useState("lines");
  const [data, setData] = useState<LeiKwanLineList | null>(null);
  const [hosts, setHosts] = useState<LeiKwanHostList | null>(null);
  const [grants, setGrants] = useState<LeiKwanGrant[]>([]);
  const [users, setUsers] = useState<{ id: number; name: string }[]>([]);
  const [loading, setLoading] = useState(false);
  const [editLine, setEditLine] = useState<LeiKwanLine | null>(null);
  const [lineOpen, setLineOpen] = useState(false);
  const [grantOpen, setGrantOpen] = useState(false);
  const [editGrant, setEditGrant] = useState<LeiKwanGrant | null>(null);
  const [plan, setPlan] = useState<LeiKwanImportPlan | null>(null);
  const [planLoading, setPlanLoading] = useState(false);
  const [applying, setApplying] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    const [l, hst, g] = await Promise.all([
      getLeiKwanLines(),
      getLeiKwanHosts(),
      getLeiKwanGrants(),
    ]);

    setLoading(false);
    if (l.code === 0) setData(l.data);
    if (hst.code === 0) setHosts(hst.data);
    if (g.code === 0) setGrants(g.data.grants || []);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!grantOpen || users.length) return;
    void getAllUsers({}).then((res) => {
      if (res.code === 0 && Array.isArray(res.data))
        setUsers(res.data.map((u) => ({ id: u.id, name: u.user })));
    });
  }, [grantOpen, users.length]);

  const loadPlan = async () => {
    setPlanLoading(true);
    const res = await previewLeiKwanImport();

    setPlanLoading(false);
    if (res.code !== 0) toast.error(res.msg || "预览失败");
    else setPlan(res.data);
  };
  const apply = async () => {
    if (!plan) return;
    setApplying(true);
    const ids = plan.items
      .filter((i) => i.action === "import")
      .map((i) => i.forwardId);
    const res = await applyLeiKwanImport(ids);

    setApplying(false);
    if (res.code !== 0) {
      toast.error(res.msg || "导入失败");

      return;
    }
    toast.success(
      `已导入 ${res.data.rulesImported.length} 条规则，新建 ${res.data.linesCreated.length} 条专线`,
    );
    (res.data.skipped || []).forEach((s) => toast(s));
    await load();
    await loadPlan();
  };

  const lineNames = (data?.lines || []).map((l) => l.name);
  const entries = data?.entryNodes || [];
  // Plan 269: one host's new 面板接管 view (from a host/* action reply).
  const patchHost = (nodeId: number, view: LeiKwanHostManage) =>
    setHosts((cur) => {
      if (!cur) return cur;
      const list = cur.hosts.map((x) =>
        x.node.id === nodeId ? { ...x, manage: view } : x,
      );

      return {
        ...cur,
        hosts: list,
        attention: list.filter((x) => hostAttentionLevel(x.manage)).length,
      };
    });
  const entryLevel = hostsAttentionLevel(hosts);

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="font-semibold">利群专线入面板</h2>
          <p className="mt-1 text-sm text-default-500">
            一条利群路线 = 一条「利群专线」隧道，都在隧道组「
            {data?.setting.groupName || "利群汇聚"}」；
            入口在授权时指定。第一步只登记，不改利群目标、不改主机。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <QuotaPill quota={data?.quota} />
          <Button
            isLoading={loading}
            size="sm"
            startContent={<RefreshCw size={15} />}
            variant="flat"
            onPress={() => void load()}
          >
            刷新
          </Button>
        </div>
      </CardHeader>
      <CardBody>
        {!data ? (
          <div className="flex justify-center py-8">
            <Spinner />
          </div>
        ) : (
          <Tabs selectedKey={tab} onSelectionChange={(k) => setTab(String(k))}>
            <Tab key="lines" title="专线">
              <div className="space-y-3 pt-2">
                <SectionTitle
                  hint="入口全部「按授权」；规则 = 利群转发号"
                  right={
                    <Button
                      color="success"
                      size="sm"
                      startContent={<Plus size={15} />}
                      variant="flat"
                      onPress={() => {
                        setEditLine(null);
                        setLineOpen(true);
                      }}
                    >
                      新建利群专线
                    </Button>
                  }
                  title={`隧道组「${data.setting.groupName}」 · ${data.lines.length} 条`}
                />
                <LeiKwanLinesTable
                  data={data}
                  h5={h5}
                  onChanged={() => void load()}
                  onEdit={(l) => {
                    setEditLine(l);
                    setLineOpen(true);
                  }}
                />
                <div className="rounded-xl border border-divider p-3">
                  <SectionTitle
                    hint="触发条件全局一套（只读）；每条线路只有开关和切回复查"
                    title="日志快切 · 触发条件"
                  />
                  <div className="mt-2">
                    <TriggerList triggers={data.triggers} />
                  </div>
                  <p className="mt-2 text-xs text-default-500">
                    保持 {data.setting.global.holdS} s，再次触发翻倍 ·
                    新租约稳定 {data.setting.global.leaseSettleS} s · 厂商宽限{" "}
                    {data.setting.global.grace} · 切回复查{" "}
                    {data.setting.global.returnCheckS} s · 首包合并默认「
                    {data.setting.global.coalesce}
                    」（每台入口机一个值，见「利群入口」）
                  </p>
                </div>
              </div>
            </Tab>
            <Tab
              key="entry"
              title={
                <span
                  className="inline-flex items-center gap-1.5"
                  data-attention-tab={entryLevel || undefined}
                  title={
                    entryLevel
                      ? `${hosts?.attention} 台入口机需要处理`
                      : undefined
                  }
                >
                  利群入口
                  {entryLevel && <AttentionIcon level={entryLevel} />}
                </span>
              }
            >
              <EntryTab hosts={hosts} onHostView={patchHost} />
            </Tab>
            <Tab key="grants" title="授权">
              <div className="space-y-3 pt-2">
                <SectionTitle
                  hint="给用户分配隧道组「利群汇聚」：主入口必选，备线单独开关"
                  right={
                    <Button
                      color="success"
                      isDisabled={!entries.length}
                      size="sm"
                      startContent={<Plus size={15} />}
                      variant="flat"
                      onPress={() => {
                        setEditGrant(null);
                        setGrantOpen(true);
                      }}
                    >
                      新增授权
                    </Button>
                  }
                  title={`授权 · ${grants.length} 人`}
                />
                <div className="overflow-x-auto rounded-xl border border-divider">
                  <table className="w-full min-w-[480px] text-sm">
                    <thead className="bg-default-100/60 text-xs text-default-500">
                      <tr>
                        <th className="px-3 py-2 text-left font-medium">
                          用户
                        </th>
                        <th className="px-3 py-2 text-left font-medium">
                          主入口
                        </th>
                        <th className="px-3 py-2 text-left font-medium">
                          备线
                        </th>
                        <th className="px-3 py-2 text-left font-medium">
                          规则
                        </th>
                        <th className="px-3 py-2 text-right font-medium">
                          操作
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {grants.map((g) => (
                        <tr key={g.id} className="border-t border-divider">
                          <td className="px-3 py-2.5 font-medium">
                            {g.userName}
                          </td>
                          <td className="px-3 py-2.5">
                            {g.mainEntryName}{" "}
                            <span className="text-xs text-default-400">
                              {g.mainEntryHost}
                            </span>
                          </td>
                          <td className="px-3 py-2.5">
                            <OnOff on={g.backupLine} />
                          </td>
                          <td className="px-3 py-2.5 tabular-nums">
                            {g.ruleCount} 条
                          </td>
                          <td className="space-x-1.5 whitespace-nowrap px-3 py-2.5 text-right">
                            <Button
                              color="primary"
                              size="sm"
                              variant="flat"
                              onPress={() => {
                                setEditGrant(g);
                                setGrantOpen(true);
                              }}
                            >
                              编辑
                            </Button>
                            <Button
                              color="danger"
                              isDisabled={g.ruleCount > 0}
                              size="sm"
                              variant="flat"
                              onPress={async () => {
                                const res = await deleteLeiKwanGrant(g.userId);

                                if (res.code !== 0)
                                  toast.error(res.msg || "取消失败");
                                else {
                                  toast.success("已取消授权");
                                  void load();
                                }
                              }}
                            >
                              取消授权
                            </Button>
                          </td>
                        </tr>
                      ))}
                      {grants.length === 0 && (
                        <tr>
                          <td
                            className="px-3 py-6 text-center text-default-500"
                            colSpan={7}
                          >
                            还没有授权
                          </td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              </div>
            </Tab>
            <Tab key="import" title="导入为规则">
              <ImportTab
                applying={applying}
                loading={planLoading}
                plan={plan}
                onApply={() => void apply()}
                onPreview={() => void loadPlan()}
              />
            </Tab>
          </Tabs>
        )}
      </CardBody>
      <LeiKwanLineDialog
        data={data}
        isOpen={lineOpen}
        line={editLine}
        onClose={() => setLineOpen(false)}
        onSaved={() => void load()}
      />
      <LeiKwanGrantDialog
        entries={entries}
        grant={editGrant}
        isOpen={grantOpen}
        lineNames={lineNames}
        quota={data?.quota}
        users={users}
        onClose={() => setGrantOpen(false)}
        onSaved={() => void load()}
      />
    </Card>
  );
}

function EntryTab({
  hosts,
  onHostView,
}: {
  hosts: LeiKwanHostList | null;
  onHostView: (nodeId: number, view: LeiKwanHostManage) => void;
}) {
  if (!hosts) return null;
  const fromAgent = hosts.hosts.some((h) => h.source === "agent");

  return (
    <div className="space-y-3 pt-2">
      <SectionTitle
        hint="组里的节点都跑着利群客户端，同一个利群账号的所有转发端口在每台上都开着。以后加入口机，只要把节点拉进这个组（节点页「节点分组」）"
        title={`节点分组 · ${hosts.entryGroupName || "利群入口（还没建，导入时自动建）"}`}
      />
      <div className="overflow-x-auto rounded-xl border border-divider">
        <table className="w-full min-w-[560px] text-sm">
          <thead className="bg-default-100/60 text-xs text-default-500">
            <tr>
              <th className="px-3 py-2 text-left font-medium">节点</th>
              <th className="px-3 py-2 text-left font-medium">地址</th>
              <th className="px-3 py-2 text-left font-medium">利群客户端</th>
              <th className="px-3 py-2 text-left font-medium">被指定为</th>
            </tr>
          </thead>
          <tbody>
            {hosts.hosts.map((h) => (
              <tr key={h.node.id} className="border-t border-divider">
                <td className="px-3 py-2.5 font-medium">{h.node.name}</td>
                <td className="px-3 py-2.5 tabular-nums">{h.node.host}</td>
                <td className="px-3 py-2.5">
                  <Pill
                    tone={
                      h.node.leikwanStatus === "running" ? "green" : "slate"
                    }
                  >
                    {h.node.leikwanStatus === "running"
                      ? "运行中"
                      : h.node.leikwanStatus || "未检测到"}{" "}
                    · {h.node.leikwanForwards} 条
                  </Pill>
                </td>
                <td className="px-3 py-2.5">
                  {h.mainEntryUsers > 0 ? (
                    <Pill tone="purple">主入口 {h.mainEntryUsers} 人</Pill>
                  ) : (
                    "—"
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-xs text-default-500">
        {fromAgent
          ? "下面是每台入口机现在跑的东西（agent 上报，密钥不出主机）。"
          : "下面是每台入口机现在跑的东西（只读 SSH 快照，密钥已去掉；第二步改由 agent 上报）。"}
      </p>
      {(hosts.attention || 0) > 0 && (
        <div
          data-attention-notice
          className={`flex items-center gap-2 rounded-lg px-3 py-2 text-sm ${
            hostsAttentionLevel(hosts) === "error"
              ? "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300"
              : "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300"
          }`}
        >
          <AttentionIcon level={hostsAttentionLevel(hosts) || "warn"} />
          <span className="min-w-0 flex-1">
            {hosts.attention} 台入口机需要处理：
            {hosts.hosts
              .filter((h) => hostAttentionLevel(h.manage))
              .map((h) => h.node.name)
              .join("、")}
          </span>
        </div>
      )}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        {hosts.hosts.map((h) => (
          <HostCard
            key={h.node.id}
            host={h}
            nightOnly={!!hosts.global?.nightOnly}
            onView={onHostView}
          />
        ))}
      </div>
    </div>
  );
}

function HostCard({
  host,
  nightOnly,
  onView,
}: {
  host: LeiKwanHostList["hosts"][number];
  nightOnly: boolean;
  onView: (nodeId: number, view: LeiKwanHostManage) => void;
}) {
  const s = host.snapshot;

  return (
    <div
      className="rounded-xl border border-divider p-3"
      data-leikwan-host={host.node.id}
    >
      <div className="mb-1 flex flex-wrap items-center justify-between gap-2">
        <span className="flex items-center gap-1.5 font-semibold">
          {host.node.name}
          <HostNameMarker manage={host.manage} />
        </span>
        {s ? (
          <span className="text-xs text-default-400">
            快照{" "}
            {new Date(s.capturedAt || host.capturedAt || 0).toLocaleString(
              "zh-CN",
              {
                timeZone: "Asia/Taipei",
              },
            )}{" "}
            台湾
          </span>
        ) : (
          <Pill tone="amber">还没有快照</Pill>
        )}
      </div>
      {s && (
        <div className="divide-y divide-divider/60">
          <KV label="厂商版本">
            <Pill
              tone={s.agentVersion.startsWith("2026.11") ? "green" : "slate"}
            >
              agent {s.agentVersion}
            </Pill>
            <Pill
              tone={s.coreVersion.startsWith("2026.11") ? "green" : "slate"}
            >
              核心 {s.coreVersion}
            </Pill>
          </KV>
          <KV label="更新锁 / 原生 UDP">
            <OnOff label="更新锁" on={s.updateLock} />
            <OnOff label="原生 UDP" on={s.nativeUdp} />
            {s.selectionMode && (
              <Pill tone="slate">选路 {s.selectionMode}</Pill>
            )}
          </KV>
          <KV label="前端通道">
            <Pill tone={s.frontChannel === "test" ? "amber" : "slate"}>
              {s.frontChannel === "test"
                ? "测试版"
                : s.frontChannel === "stable"
                  ? "稳定版"
                  : s.frontChannel || "—"}
            </Pill>
            {s.frontBuild && (
              <span className="text-xs text-default-500">{s.frontBuild}</span>
            )}
          </KV>
          <KV label="快切 v4">
            <Pill tone={s.fastFailover === "off" ? "slate" : "green"}>
              {s.fastFailover === "test"
                ? "测试版运行中"
                : s.fastFailover === "stable"
                  ? "稳定版运行中"
                  : "关"}
            </Pill>
          </KV>
          <KV label="日志快切">
            <Pill tone={s.logswitch ? "green" : "slate"}>
              {s.logswitch ? `${s.logswitch} 运行中` : "没装"}
            </Pill>
            {s.logswitchRecordOnly && s.logswitchRecordOnly.length > 0 && (
              <Pill title={s.logswitchRecordOnly.join(" ")} tone="amber">
                全部触发只记录
              </Pill>
            )}
            <span className="text-xs text-default-500">
              接线 {s.lines?.length || 0} 条
            </span>
          </KV>
          <KV label="首包合并">
            <Pill tone={s.coalesce.startsWith("on") ? "green" : "slate"}>
              {s.coalesce || "—"}
            </Pill>
          </KV>
          <KV label="手机备线端口">
            {s.phoneBackup && s.phoneBackup.length > 0 ? (
              <span className="text-xs">
                {s.phoneBackup.map((p) => p.port).join(" · ")} → ixp1
              </span>
            ) : (
              "—"
            )}
          </KV>
          <KV label="转发">
            <span className="text-xs">{s.forwardsReady || "—"}</span>
          </KV>
          <KV label="在线设备名额">
            <span className="text-xs text-default-500">
              {s.deviceQuota || "主机上读不到（11-16 记录 5 / 5）"}
            </span>
          </KV>
          {s.extra && s.extra.length > 0 && (
            <KV label="其他（清单外）">
              <ul className="list-disc pl-4 text-xs text-default-600">
                {s.extra.map((x) => (
                  <li key={x}>{x}</li>
                ))}
              </ul>
            </KV>
          )}
        </div>
      )}
      <HostManageBlock host={host} nightOnly={nightOnly} onView={onView} />
    </div>
  );
}

function ImportTab({
  plan,
  loading,
  applying,
  onPreview,
  onApply,
}: {
  plan: LeiKwanImportPlan | null;
  loading: boolean;
  applying: boolean;
  onPreview: () => void;
  onApply: () => void;
}) {
  const toImport =
    (plan?.items || []).filter((i) => i.action === "import") || [];

  return (
    <div className="space-y-3 pt-2">
      <SectionTitle
        hint="把现有利群转发登记成管理员的规则：落地 = 现在的目标（手动），备线 = 链接现有 FLVX 规则，不新建；不改利群目标、不改主机；可重复执行"
        right={
          <div className="flex gap-2">
            <Button
              color="secondary"
              isLoading={loading}
              size="sm"
              startContent={<Download size={15} />}
              variant="flat"
              onPress={onPreview}
            >
              {plan ? "重新预览" : "预览（不写入）"}
            </Button>
            {plan && (
              <Button
                color="success"
                isDisabled={toImport.length === 0}
                isLoading={applying}
                size="sm"
                variant="flat"
                onPress={onApply}
              >
                确认导入 {toImport.length} 条
              </Button>
            )}
          </div>
        }
        title="导入为规则"
      />
      {plan && (
        <>
          {(plan.notes || []).map((n) => (
            <p
              key={n}
              className="rounded-lg bg-amber-500/15 px-3 py-2 text-xs text-amber-700 dark:text-amber-300"
            >
              {n}
            </p>
          ))}
          <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
            <div className="rounded-xl border border-divider p-3 text-sm">
              <div className="mb-1 font-medium">
                节点分组「{plan.entryGroup.name}」
              </div>
              <div className="flex flex-wrap gap-1.5">
                {(plan.entryGroup.nodes || []).map((n) => (
                  <Pill key={n.id} tone={n.inGroup ? "teal" : "amber"}>
                    {n.name}
                    {n.inGroup ? "" : " · 将加入"}
                  </Pill>
                ))}
              </div>
            </div>
            <div className="rounded-xl border border-divider p-3 text-sm">
              <div className="mb-1 font-medium">管理员授权（主入口）</div>
              {plan.adminGrant.exists ? (
                <span className="text-default-500">已有</span>
              ) : (
                <span>
                  将新建：{plan.adminGrant.userName} → 主入口{" "}
                  <b>{plan.adminGrant.mainEntryName}</b> · 备线开
                </span>
              )}
              <p className="mt-1 text-xs text-default-500">
                主机值参考：{plan.referenceHost || "—"}
              </p>
            </div>
            <div className="rounded-xl border border-divider p-3 text-sm">
              <div className="mb-1 font-medium">利群名额</div>
              <QuotaPill quota={plan.quota} />
              <p className="mt-1 text-xs text-default-500">
                导入不占新名额（转发本来就在）
              </p>
            </div>
          </div>
          <div className="space-y-1.5">
            <div className="text-sm font-medium">专线</div>
            <div className="flex flex-wrap gap-2">
              {(plan.lines || []).map((l) => (
                <div
                  key={l.routeId}
                  className="rounded-xl border border-divider px-3 py-2 text-xs"
                >
                  <div className="flex items-center gap-1.5">
                    <span className="text-sm font-medium">{l.name}</span>
                    <Pill tone={l.exists ? "slate" : "green"}>
                      {l.exists ? "已有" : "新建"}
                    </Pill>
                  </div>
                  <div className="mt-1 text-default-500">
                    {l.routeLabel} · 预热 {l.accel.warm.main}/
                    {l.accel.warm.backup} · 待命 {l.accel.warm.standby} · 间隔{" "}
                    {l.accel.warm.paceMs} ms · 首包过滤 {l.accel.ssFilter} ·
                    日志快切 {l.accel.logswitch ? "开" : "关"}
                    {l.accel.returnCheck ? " · 复查" : ""}
                  </div>
                  <div className="text-default-500">
                    备线隧道：{l.backupTunnelName || "—"}
                  </div>
                </div>
              ))}
            </div>
          </div>
          <div className="overflow-x-auto rounded-xl border border-divider">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="bg-default-100/60 text-xs text-default-500">
                <tr>
                  <th className="px-3 py-2 text-left font-medium">利群转发</th>
                  <th className="px-3 py-2 text-left font-medium">隧道</th>
                  <th className="px-3 py-2 text-left font-medium">
                    落地（手动）
                  </th>
                  <th className="px-3 py-2 text-left font-medium">备线</th>
                  <th className="px-3 py-2 text-left font-medium">
                    加速（与专线默认不同的）
                  </th>
                  <th className="px-3 py-2 text-left font-medium">动作</th>
                </tr>
              </thead>
              <tbody>
                {(plan.items || []).map((it) => (
                  <tr
                    key={it.forwardId}
                    className="border-t border-divider align-top"
                  >
                    <td className="whitespace-nowrap px-3 py-2.5">
                      <div className="font-medium">#{it.forwardId}</div>
                      <div className="text-xs text-default-500">
                        :{it.listenPort}
                      </div>
                    </td>
                    <td className="whitespace-nowrap px-3 py-2.5">
                      {it.lineName || (
                        <span className="text-default-400">—</span>
                      )}
                    </td>
                    <td className="px-3 py-2.5">
                      <div className="text-xs">{it.target}</div>
                      {it.landingHint && (
                        <div className="mt-0.5 text-[11px] text-amber-600 dark:text-amber-300">
                          {it.landingHint}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-2.5 text-xs">
                      {it.backupForwardId ? (
                        <>
                          <Pill tone="purple">链接 #{it.backupForwardId}</Pill>{" "}
                          {it.backupSharedWith &&
                            it.backupSharedWith.length > 0 && (
                              <span className="text-default-500">
                                （跟 {it.backupSharedWith.join("、")} 共用）
                              </span>
                            )}
                          <div className="mt-0.5 text-default-500">
                            {it.backupName}
                          </div>
                        </>
                      ) : (
                        <span className="text-default-400">
                          {it.action === "import" ? "无" : "—"}
                        </span>
                      )}
                    </td>
                    <td className="px-3 py-2.5 text-xs">
                      {it.action === "import" ? (
                        <div className="space-y-1">
                          <div className="flex flex-wrap gap-1">
                            {it.accel.warmMain !== undefined && (
                              <Pill tone="amber">预热 {it.accel.warmMain}</Pill>
                            )}
                            {it.accel.ssFilter !== undefined && (
                              <Pill tone="amber">
                                首包过滤 {it.accel.ssFilter}
                              </Pill>
                            )}
                            {it.accel.logswitch !== undefined && (
                              <Pill tone="amber">
                                日志快切 {it.accel.logswitch ? "开" : "关"}
                              </Pill>
                            )}
                            {it.accel.returnCheck !== undefined && (
                              <Pill tone="amber">
                                复查 {it.accel.returnCheck ? "开" : "关"}
                              </Pill>
                            )}
                            {it.accel.warmMain === undefined &&
                              it.accel.ssFilter === undefined &&
                              it.accel.logswitch === undefined &&
                              it.accel.returnCheck === undefined && (
                                <span className="text-default-400">同专线</span>
                              )}
                          </div>
                          {it.accel.note && (
                            <div className="text-default-500">
                              {it.accel.note}
                            </div>
                          )}
                          {it.hostValues && (
                            <HostValuesTable compact values={it.hostValues} />
                          )}
                        </div>
                      ) : null}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2.5">
                      <Pill tone={it.action === "import" ? "green" : "slate"}>
                        {it.action === "import"
                          ? "导入"
                          : it.action === "registered"
                            ? "已登记"
                            : "跳过"}
                      </Pill>
                      {it.reason && (
                        <div className="mt-1 max-w-[180px] whitespace-normal text-[11px] text-default-500">
                          {it.reason}
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  );
}
