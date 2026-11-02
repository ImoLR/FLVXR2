import type {
  ProbeAdminGroup,
  ProbeAdminNode,
  ProbeGroupMode,
} from "@/api/probe-admin";

import { useCallback, useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";

import { getProbeSettings, saveProbeSettings } from "@/api/probe-admin";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Divider } from "@/shadcn-bridge/heroui/divider";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Switch } from "@/shadcn-bridge/heroui/switch";
import {
  REGION_CODES,
  compareRegions,
  regionFlag,
  regionLabel,
  regionName,
} from "@/utils/region";
import { useMobileBreakpoint } from "@/hooks/useMobileBreakpoint";
import {
  ProbeGroupPicker,
  ProbeGroupsEditor,
} from "@/pages/probe-groups-editor";

// Plan 187 设置 → 公开探针页: title, optional probe-only domain, latency on/off
// and per-node 「在探针显示」 + 探针显示名. The on/off switch itself stays in
// 快捷开关 (login_monitor_link). Plan 189 adds 探针分组 + 默认分组方式.
// Plan 195: machines that only run in the 救命版 are listed too (negative ids,
// default hidden) with a source chip, a 地区 select, search and bulk show/hide.

const REGION_OPTIONS = [...REGION_CODES].sort(compareRegions);
const SOURCE_CHIP =
  "inline-flex shrink-0 items-center rounded-full px-1.5 py-px align-middle text-[10px] font-semibold leading-4";
const BULK =
  "rounded-full px-2.5 py-1 text-xs font-semibold transition-colors disabled:pointer-events-none disabled:opacity-40";

export function ProbeSettingsCard({ enabled }: { enabled: boolean }) {
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [title, setTitle] = useState("");
  const [defaultTitle, setDefaultTitle] = useState("FLVX 探针");
  const [domain, setDomain] = useState("");
  const [latency, setLatency] = useState(true);
  const [expiry, setExpiry] = useState(true);
  const [nodes, setNodes] = useState<ProbeAdminNode[]>([]);
  const [groups, setGroups] = useState<ProbeAdminGroup[]>([]);
  const [groupMode, setGroupMode] = useState<ProbeGroupMode>("region");
  const [oldPanel, setOldPanel] = useState<"" | "ok" | "error">("");
  const [query, setQuery] = useState("");
  const [initial, setInitial] = useState("");
  // Phones: 显示名 goes under the name (plan 189), so the name keeps its room.
  const narrow = useMobileBreakpoint(640);

  const snapshot = useCallback(
    (
      t: string,
      d: string,
      l: boolean,
      n: ProbeAdminNode[],
      g: ProbeAdminGroup[],
      m: ProbeGroupMode,
      e: boolean,
    ) =>
      JSON.stringify([
        t.trim(),
        d.trim(),
        l,
        n.map((x) => [
          x.id,
          x.hidden,
          x.probeName.trim(),
          x.regionOverride ?? "",
        ]),
        g.map((x) => [x.id, x.name, [...x.nodes].sort((a, b) => a - b)]),
        m,
        e,
      ]),
    [],
  );

  useEffect(() => {
    void getProbeSettings().then((res) => {
      if (res.code !== 0 || !res.data) return;
      const d = res.data;

      setTitle(d.title);
      setDefaultTitle(d.defaultTitle);
      setDomain(d.domain);
      setLatency(d.latency);
      setExpiry(d.expiry ?? true);
      setNodes(d.nodes);
      setGroups(d.groups ?? []);
      setGroupMode(d.groupMode ?? "region");
      setOldPanel(d.oldPanel ?? "");
      setInitial(
        snapshot(
          d.title,
          d.domain,
          d.latency,
          d.nodes,
          d.groups ?? [],
          d.groupMode ?? "region",
          d.expiry ?? true,
        ),
      );
      setLoaded(true);
    });
  }, [snapshot]);

  const dirty =
    loaded &&
    snapshot(title, domain, latency, nodes, groups, groupMode, expiry) !==
      initial;
  const shownCount = useMemo(
    () => nodes.filter((n) => !n.hidden).length,
    [nodes],
  );
  const shownIds = useMemo(
    () => new Set(nodes.filter((n) => !n.hidden).map((n) => n.id)),
    [nodes],
  );
  const toggleGroup = useCallback(
    (groupId: number, nodeId: number, on: boolean) =>
      setGroups((list) =>
        list.map((g) =>
          g.id !== groupId
            ? g
            : {
                ...g,
                nodes: on
                  ? g.nodes.includes(nodeId)
                    ? g.nodes
                    : [...g.nodes, nodeId]
                  : g.nodes.filter((id) => id !== nodeId),
              },
        ),
      ),
    [],
  );
  const sameHost =
    domain.trim() !== "" &&
    domain
      .trim()
      .toLowerCase()
      .replace(/^https?:\/\//, "")
      .split("/")[0] === window.location.hostname;

  const save = async () => {
    setSaving(true);
    try {
      const res = await saveProbeSettings({
        title: title.trim(),
        domain: domain.trim(),
        latency,
        nodes: nodes.map((n) =>
          n.source === "old"
            ? {
                id: n.id,
                hidden: n.hidden,
                probeName: n.probeName.trim(),
                region: n.regionOverride ?? "",
              }
            : { id: n.id, hidden: n.hidden, probeName: n.probeName.trim() },
        ),
        groups: groups.map((g) => ({
          id: g.id > 0 ? g.id : 0,
          name: g.name.trim(),
          nodes: g.nodes,
        })),
        groupMode,
        expiry,
      });

      if (res.code !== 0) {
        toast.error(res.msg || "保存失败");

        return;
      }
      toast.success("探针设置已保存");
      const fresh = await getProbeSettings();

      if (fresh.code === 0 && fresh.data) {
        setDomain(fresh.data.domain);
        setNodes(fresh.data.nodes);
        setGroups(fresh.data.groups ?? []);
        setGroupMode(fresh.data.groupMode ?? "region");
        setOldPanel(fresh.data.oldPanel ?? "");
        setInitial(
          snapshot(
            fresh.data.title,
            fresh.data.domain,
            fresh.data.latency,
            fresh.data.nodes,
            fresh.data.groups ?? [],
            fresh.data.groupMode ?? "region",
            fresh.data.expiry ?? true,
          ),
        );
      }
    } finally {
      setSaving(false);
    }
  };

  const setNode = (id: number, patch: Partial<ProbeAdminNode>) =>
    setNodes((list) => list.map((n) => (n.id === id ? { ...n, ...patch } : n)));
  // Plan 195: chips / search / bulk only matter once the 救命版 source is set.
  const oldOn = oldPanel !== "";
  const oldCount = useMemo(
    () => nodes.filter((n) => n.source === "old").length,
    [nodes],
  );
  const q = query.trim().toLowerCase();
  const visible = useMemo(
    () =>
      q === ""
        ? nodes
        : nodes.filter((n) =>
            [
              n.name,
              n.probeName,
              n.oldMatch ?? "",
              regionName(n.region),
              n.source === "old" ? "救命版" : "转发面板",
            ].some((v) => v.toLowerCase().includes(q)),
          ),
    [nodes, q],
  );
  const setAllVisible = (show: boolean) => {
    const ids = new Set(visible.map((n) => n.id));

    setNodes((list) =>
      list.map((n) => (ids.has(n.id) ? { ...n, hidden: !show } : n)),
    );
  };
  const sourceChips = (n: ProbeAdminNode) => {
    if (!oldOn) return null;
    if (n.source === "old")
      return (
        <span
          className={`${SOURCE_CHIP} ml-1.5 bg-amber-500/15 text-amber-700 dark:bg-amber-400/20 dark:text-amber-200`}
        >
          救命版
        </span>
      );

    return (
      <>
        <span
          className={`${SOURCE_CHIP} ml-1.5 border border-divider text-default-500`}
        >
          转发面板
        </span>
        {n.oldMatch && (
          <span
            className={`${SOURCE_CHIP} ml-1 border border-amber-500/40 text-amber-700 dark:text-amber-300`}
            title={`救命版里也有这台（叫「${n.oldMatch}」），探针只显示一张卡片，用转发面板的数据`}
          >
            救命版同机
          </span>
        )}
      </>
    );
  };
  // 救命版 has no region: GeoIP by default, admin may pick one (plan 195).
  const regionSelect = (n: ProbeAdminNode, cls: string) => (
    <select
      aria-label={`${n.name} 的地区`}
      className={`rounded-md border border-divider bg-transparent px-1.5 outline-none focus:border-teal-500 disabled:opacity-50 dark:[color-scheme:dark] ${cls}`}
      disabled={n.hidden}
      title="救命版没有地区信息，默认按 IP 自动识别，不准可以在这里改"
      value={n.regionOverride ?? ""}
      onChange={(e) =>
        setNode(n.id, {
          regionOverride: e.target.value,
          region: e.target.value || n.regionAuto || "",
        })
      }
    >
      <option value="">
        {n.regionAuto ? `自动 · ${regionLabel(n.regionAuto)}` : "自动 · 未识别"}
      </option>
      {REGION_OPTIONS.map((code) => (
        <option key={code} value={code}>
          {regionLabel(code)}
        </option>
      ))}
    </select>
  );
  // 显示名: right of the name on wide screens, under it on phones (plan 189).
  const nameInput = (n: ProbeAdminNode, cls: string) => (
    <input
      aria-label={`${n.name} 的探针显示名`}
      className={`h-8 rounded-md border border-divider bg-transparent px-2 text-sm outline-none placeholder:text-default-400 focus:border-teal-500 disabled:opacity-50 ${cls}`}
      disabled={n.hidden}
      maxLength={30}
      placeholder="显示名（默认原名）"
      value={n.probeName}
      onChange={(e) => setNode(n.id, { probeName: e.target.value })}
    />
  );

  return (
    <Card className="mt-6 shadow-md dark:bg-content1">
      <CardHeader className="pb-4">
        <div className="flex w-full items-start justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold">公开探针页</h2>
            <p className="text-xs text-gray-500 dark:text-gray-400">
              {enabled ? "已开启：" : "未开启（在「快捷开关」里打开）："}
              任何人都能打开 <span className="font-mono">/tz</span>{" "}
              查看下面勾选的服务器：CPU、内存、硬盘、负载、网速、流量、TCP
              连接数和机器之间的延迟{expiry ? "，以及到期剩余天数" : ""}。
              不公开 IP、端口、节点编号、程序版本、隧道、规则和用户。
            </p>
          </div>
          <a
            className="shrink-0 rounded-full bg-teal-500/15 px-3 py-1 text-xs font-semibold text-teal-700 hover:bg-teal-500/25 dark:bg-teal-400/15 dark:text-teal-300"
            href="/tz"
            rel="noreferrer"
            target="_blank"
          >
            打开探针页
          </a>
        </div>
      </CardHeader>
      <Divider />
      <CardBody className="space-y-4 py-4">
        <Input
          description={`留空显示「${defaultTitle}」，最多 40 个字`}
          label="探针标题"
          maxLength={40}
          placeholder={defaultTitle}
          value={title}
          onChange={(e) => setTitle(e.target.value)}
        />
        <Input
          description="可选。把一个专门给探针用的域名指向本面板（在 Cloudflare 里自己添加），填在这里后：这个域名只能打开探针页，后台和所有其它接口在这个域名下都是 404。不能填你现在登录用的域名。"
          errorMessage={
            sameHost
              ? "这是你现在正在使用的域名，保存后会进不了后台"
              : undefined
          }
          isInvalid={sameHost}
          label="探针专用域名"
          placeholder="例如 status.example.com"
          value={domain}
          onChange={(e) => setDomain(e.target.value)}
        />
        <div className="flex items-center justify-between gap-4">
          <div className="flex flex-col">
            <span className="text-sm font-semibold text-gray-700 dark:text-gray-300">
              显示延迟
            </span>
            <span className="text-xs text-gray-400 dark:text-gray-500">
              显示机器之间的 TCP 延迟（来自面板每分钟一次的线路测速，不额外测）
            </span>
          </div>
          <Switch
            color="primary"
            isSelected={latency}
            size="sm"
            onValueChange={setLatency}
          />
        </div>
        <div className="flex items-center justify-between gap-4">
          <div className="flex flex-col">
            <span className="text-sm font-semibold text-gray-700 dark:text-gray-300">
              显示到期天数
            </span>
            <span className="text-xs text-gray-400 dark:text-gray-500">
              在每台服务器上显示「月付 · 剩 12
              天」这样的小标签（按节点的到期时间和续费周期，和节点页一样到期后自动顺延到下一期；按台湾时间算天数，只显示天数，不公开具体日期）
            </span>
          </div>
          <Switch
            color="primary"
            isSelected={expiry}
            size="sm"
            onValueChange={setExpiry}
          />
        </div>
        <ProbeGroupsEditor
          groups={groups}
          mode={groupMode}
          shownIds={shownIds}
          onGroups={setGroups}
          onMode={setGroupMode}
        />
        <div>
          <div className="mb-2 flex items-center justify-between text-sm">
            <span className="font-semibold text-gray-700 dark:text-gray-300">
              在探针显示的服务器
            </span>
            <span className="text-xs text-gray-400">
              显示 {shownCount} / {nodes.length}
            </span>
          </div>
          {oldOn && (
            <p className="mb-2 text-xs text-gray-400 dark:text-gray-500">
              {oldPanel === "error" ? (
                <span className="text-amber-600 dark:text-amber-300">
                  救命版的数据暂时读不到，它的机器先不列出；已经选好的设置会保留。
                </span>
              ) : (
                <>
                  也列出了只在救命版里的 {oldCount}{" "}
                  台机器（只读救命版的数据，不用装新程序），默认不显示，打开想要的即可。两边都有的机器只算一台，用转发面板的数据。
                </>
              )}
            </p>
          )}
          {(oldOn || nodes.length > 12) && (
            <div className="mb-2 flex flex-wrap items-center gap-2">
              <input
                aria-label="搜索服务器"
                className="h-8 w-0 min-w-0 flex-1 rounded-md border border-divider bg-transparent px-2 text-sm outline-none placeholder:text-default-400 focus:border-teal-500 sm:max-w-xs"
                placeholder="搜索名称、地区或「救命版」"
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              <button
                className={`${BULK} bg-green-500/15 text-green-700 hover:bg-green-500/25 dark:bg-green-400/20 dark:text-green-300`}
                disabled={visible.length === 0}
                type="button"
                onClick={() => setAllVisible(true)}
              >
                {q ? "这些全部显示" : "全部显示"}
              </button>
              <button
                className={`${BULK} border border-divider text-default-600 hover:bg-slate-500/10`}
                disabled={visible.length === 0}
                type="button"
                onClick={() => setAllVisible(false)}
              >
                {q ? "这些全部隐藏" : "全部隐藏"}
              </button>
            </div>
          )}
          <div
            className={`${groups.length > 0 ? "max-h-[28rem]" : "max-h-80"} overflow-y-auto rounded-lg border border-divider`}
          >
            {visible.map((n) => (
              <div
                key={n.id}
                className="flex items-center gap-3 border-b border-divider px-3 py-2 last:border-b-0"
                data-probe-node={n.name}
                data-probe-source={n.source ?? "flvx"}
              >
                <Switch
                  aria-label={`在探针显示 ${n.name}`}
                  color="success"
                  isSelected={!n.hidden}
                  size="sm"
                  onValueChange={(v) => setNode(n.id, { hidden: !v })}
                />
                <div className="min-w-0 flex-1">
                  <div
                    className={`truncate text-sm ${n.hidden ? "text-default-400 line-through" : "text-foreground"}`}
                  >
                    {regionFlag(n.region)} {n.name}
                    <span
                      className={`ml-2 inline-block h-1.5 w-1.5 rounded-full align-middle ${n.online ? "bg-green-500" : "bg-default-300"}`}
                    />
                    {sourceChips(n)}
                  </div>
                  {(groups.length > 0 || (!narrow && n.source === "old")) && (
                    <div className="mt-1 flex flex-wrap items-center gap-1.5">
                      {groups.length > 0 && (
                        <ProbeGroupPicker
                          dim={n.hidden}
                          groups={groups}
                          nodeId={n.id}
                          nodeName={n.name}
                          onToggle={toggleGroup}
                        />
                      )}
                      {/* Wide screens: the card is narrow, so 地区 sits on the
                          second line and the name keeps its room. */}
                      {!narrow &&
                        n.source === "old" &&
                        regionSelect(n, "h-6 w-32 text-xs")}
                    </div>
                  )}
                  {narrow && (
                    <div className="mt-1.5 flex gap-1.5">
                      {nameInput(n, "min-w-0 flex-1")}
                      {n.source === "old" &&
                        regionSelect(n, "h-8 w-28 shrink-0 text-sm")}
                    </div>
                  )}
                </div>
                {!narrow && nameInput(n, "w-40 shrink-0")}
              </div>
            ))}
            {loaded && nodes.length === 0 && (
              <div className="px-3 py-4 text-center text-xs text-default-400">
                还没有节点
              </div>
            )}
            {loaded && nodes.length > 0 && visible.length === 0 && (
              <div className="px-3 py-4 text-center text-xs text-default-400">
                没有找到「{query.trim()}」
              </div>
            )}
          </div>
        </div>
        <div className="flex justify-end gap-2">
          <Button
            color="success"
            isDisabled={!dirty || sameHost}
            isLoading={saving}
            size="sm"
            variant="flat"
            onPress={() => void save()}
          >
            保存探针设置
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}
