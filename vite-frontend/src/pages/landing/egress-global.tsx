import { useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";
import { ArrowDown, Globe2 } from "lucide-react";

import { BuiltinRuleList, builtinScopeText } from "./egress-builtin-list";
import { EgressRuleList } from "./egress-rule-list";

import {
  AbuseValues,
  EgressBuiltin,
  EgressGlobal,
  EgressOverview,
  EgressRule,
  LayerServerStatus,
  layerOrder,
  saveEgressBuiltin,
  saveEgressGlobal,
  saveEgressUserCaps,
} from "@/api/landing-egress";
import { useH5Mode } from "@/hooks/useH5Mode";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// Plan 134: admin 「全局规则」 — 全局·强制 / 全局·默认 lists, 全局默认出站,
// per-server sync state and the node-rule caps for users.

const stateChip: Record<
  LayerServerStatus["state"],
  {
    label: string;
    color: "success" | "secondary" | "danger" | "warning" | "default";
  }
> = {
  synced: { label: "已同步", color: "success" },
  syncing: { label: "同步中", color: "secondary" },
  failed: { label: "失败", color: "danger" },
  restart: { label: "待重启", color: "warning" },
  unsupported: { label: "不支持", color: "default" },
};

export function LayerLegend({
  highlight = [],
  compact = false,
}: {
  highlight?: string[];
  compact?: boolean;
}) {
  if (compact) {
    return (
      <div className="flex flex-wrap items-center gap-1 text-xs">
        <span className="text-default-500">匹配顺序</span>
        {layerOrder.map((step, i) => (
          <span key={step.key} className="flex items-center gap-1">
            {i > 0 && <span className="text-default-400">→</span>}
            <Chip
              className={
                highlight.includes(step.key) ? undefined : "text-default-600"
              }
              color={highlight.includes(step.key) ? "secondary" : "default"}
              size="sm"
              variant={highlight.includes(step.key) ? "flat" : "bordered"}
            >
              {step.label}
            </Chip>
          </span>
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-default-500">匹配顺序</p>
      <ol className="space-y-1">
        {layerOrder.map((step, i) => (
          <li key={step.key}>
            {i > 0 && (
              <ArrowDown
                className="mx-auto my-0.5 text-default-300"
                size={12}
              />
            )}
            <div
              className={`rounded-lg border px-2 py-1.5 ${highlight.includes(step.key) ? "border-primary/40 bg-primary-50" : "border-divider"}`}
            >
              <div className="flex items-center gap-1.5 text-sm font-medium">
                <span className="w-4 text-xs text-default-400">{i + 1}</span>
                {step.label}
              </div>
              <p className="pl-5 text-xs text-default-500">{step.hint}</p>
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
}

const snapshot = (g: {
  forced: EgressRule[];
  default: EgressRule[];
  defaultAction: string;
}) => JSON.stringify([g.forced, g.default, g.defaultAction || "direct"]);

const fallbackBuiltin: EgressBuiltin = {
  safety: [],
  smtp: true,
  mail: false,
  telnet: false,
  bt: true,
  profilesOff: [],
};

export function LandingGlobalRules({
  overview,
  onSaved,
  onWhitelistSaved,
}: {
  overview: EgressOverview;
  onSaved: () => void;
  // Plan 140: the 内置 list edits the source whitelist in place.
  onWhitelistSaved?: (list: string[]) => void;
}) {
  const h5 = useH5Mode();
  const initial: EgressGlobal = overview.global || {
    forced: [],
    default: [],
    defaultAction: "direct",
    updatedTime: 0,
    userCaps: { perNode: 5, total: 10 },
    nodesWithRules: 0,
    servers: [],
  };
  const [forced, setForced] = useState<EgressRule[]>(initial.forced || []);
  const [deflt, setDeflt] = useState<EgressRule[]>(initial.default || []);
  const [action, setAction] = useState(initial.defaultAction || "direct");
  const [servers, setServers] = useState(initial.servers || []);
  const [saved, setSaved] = useState(snapshot(initial));
  const [saving, setSaving] = useState("");
  const [perNode, setPerNode] = useState(String(initial.userCaps.perNode));
  const [total, setTotal] = useState(String(initial.userCaps.total));
  const [builtin, setBuiltin] = useState<EgressBuiltin>(
    initial.builtin || fallbackBuiltin,
  );

  useEffect(() => {
    const g = overview.global;

    if (!g) return;
    setForced(g.forced || []);
    setDeflt(g.default || []);
    setAction(g.defaultAction || "direct");
    setServers(g.servers || []);
    setSaved(snapshot(g));
    setPerNode(String(g.userCaps.perNode));
    setTotal(String(g.userCaps.total));
    setBuiltin(g.builtin || fallbackBuiltin);
  }, [overview.global]);

  const choices = useMemo(
    () => overview.outbounds.map((o) => ({ id: o.id, name: o.name })),
    [overview.outbounds],
  );
  const dirty =
    snapshot({ forced, default: deflt, defaultAction: action }) !== saved;
  const warned = servers.filter(
    (s) => (s.notes && s.notes.length) || s.state !== "synced",
  ).length;

  const save = async () => {
    setSaving("global");
    const res = await saveEgressGlobal({
      forced,
      default: deflt,
      defaultAction: action,
    });

    setSaving("");
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success("全局规则已保存，正在同步到所有落地服务器");
    (res.data.warnings || []).forEach((w) => toast(w, { duration: 6000 }));
    setForced(res.data.forced || []);
    setDeflt(res.data.default || []);
    setAction(res.data.defaultAction || "direct");
    setServers(res.data.servers || []);
    setSaved(snapshot(res.data));
    onSaved();
  };

  // Built-in rules in force on every node (a profile may still switch the
  // defaults off for its nodes): drives the 「内置已封锁」 preset hint.
  const noProfileOff = !(builtin.profilesOff || []).length;
  const builtinActive = {
    smtp: builtin.smtp && noProfileOff,
    mail: builtin.mail && noProfileOff,
    telnet: builtin.telnet && noProfileOff,
    bt: builtin.bt && noProfileOff,
  };
  const warp = overview.outbounds.find((o) => o.type === "warp");

  const saveBuiltin = async (next: AbuseValues) => {
    setSaving("builtin");
    const res = await saveEgressBuiltin(next);

    setSaving("");
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    if (res.data.builtin) setBuiltin(res.data.builtin);
    setServers(res.data.servers || []);
    toast.success("内置规则已保存，正在同步到所有落地服务器");
  };

  const saveCaps = async () => {
    setSaving("caps");
    const res = await saveEgressUserCaps(Number(perNode), Number(total));

    setSaving("");
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    setPerNode(String(res.data.perNode));
    setTotal(String(res.data.total));
    toast.success("用户规则上限已保存");
  };

  return (
    <Card>
      <CardHeader className="flex flex-col items-stretch gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 font-semibold">
            <Globe2 className="text-primary" size={18} />
            全局规则
          </h2>
          <p className="mt-1 text-xs text-default-500">
            自动作用于所有落地服务器（自有和妙妙屋共享，包括以后新启用的），保存后自动同步，无需在服务器里选择方案
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {dirty && (
            <Chip color="warning" size="sm" variant="flat">
              未保存
            </Chip>
          )}
          <Button
            color="primary"
            isDisabled={!dirty}
            isLoading={saving === "global"}
            size="sm"
            onPress={() => void save()}
          >
            保存全局规则
          </Button>
        </div>
      </CardHeader>
      <CardBody className="space-y-5">
        <div
          className={
            h5
              ? "space-y-4"
              : "grid gap-5 lg:grid-cols-[210px_1fr] lg:items-start"
          }
        >
          {h5 ? (
            <LayerLegend
              compact
              highlight={["forced", "default", "outbound"]}
            />
          ) : (
            <LayerLegend highlight={["forced", "default", "outbound"]} />
          )}
          <div className="min-w-0 space-y-5">
            <section className="space-y-2">
              <div>
                <h3 className="font-medium">内置</h3>
                <p className="text-xs text-default-500">
                  {builtinScopeText}
                  。安全规则锁定；防滥用规则可在这里逐条全局开关（路由方案里的「默认防滥用规则」也要开着才生效），开关后自动同步。
                </p>
              </div>
              <BuiltinRuleList
                admin
                exemptNodeCount={overview.exemptNodeCount}
                profilesOff={builtin.profilesOff}
                safety={builtin.safety}
                switching={saving === "builtin"}
                values={{
                  smtp: builtin.smtp,
                  mail: !!builtin.mail,
                  telnet: !!builtin.telnet,
                  bt: builtin.bt,
                }}
                whitelist={overview.whitelist || []}
                onSwitch={(next) => void saveBuiltin(next)}
                onWhitelistSaved={onWhitelistSaved}
              />
            </section>
            <section className="space-y-2">
              <div>
                <h3 className="font-medium">全局·强制</h3>
                <p className="text-xs text-default-500">
                  作用于所有落地节点：自有、妙妙屋共享和线路机节点，紧跟内置规则匹配，用户的节点规则不能覆盖。例如：强制
                  IPv4、屏蔽某些网站。停用的规则不会下发；保存后自动同步。
                </p>
                <p className="text-xs text-default-400">
                  线路机节点需要节点程序 3.0.27-fork.75
                  及以上才会应用，旧版本自动跳过，不影响转发。
                </p>
              </div>
              <EgressRuleList
                presets
                savePreset
                builtinActive={builtinActive}
                choices={choices}
                defaultChoice={warp?.id}
                emptyText="没有强制规则"
                rules={forced}
                safety={builtin.safety}
                onChange={setForced}
              />
            </section>
            <section className="space-y-2">
              <div>
                <h3 className="font-medium">全局·默认</h3>
                <p className="text-xs text-default-500">
                  作用于自有和妙妙屋共享的落地节点，不作用于线路机节点；节点规则和服务器路由方案都没命中时才匹配，可被它们覆盖。保存后自动同步。
                </p>
              </div>
              <EgressRuleList
                presets
                savePreset
                builtinActive={builtinActive}
                choices={choices}
                defaultChoice={warp?.id}
                emptyText="没有默认规则"
                rules={deflt}
                safety={builtin.safety}
                onChange={setDeflt}
              />
            </section>
            <Select
              className="max-w-md"
              description="节点和服务器路由方案都没指定默认出站时使用；直连 = 跟随系统，与现在一样"
              label="全局默认出站"
              selectedKeys={[action]}
              variant="bordered"
              onSelectionChange={(keys) =>
                setAction(String(Array.from(keys)[0]))
              }
            >
              {[
                <SelectItem key="direct">直连（跟随系统）</SelectItem>,
                <SelectItem key="block">封锁</SelectItem>,
                ...overview.outbounds.map((o) => (
                  <SelectItem key={`outbound:${o.id}`}>{o.name}</SelectItem>
                )),
              ]}
            </Select>
          </div>
        </div>

        <section className="space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="font-medium">同步状态</h3>
            <Chip className="text-default-600" size="sm" variant="bordered">
              {servers.length} 台服务器
            </Chip>
            {warned > 0 && (
              <Chip color="warning" size="sm" variant="flat">
                {warned} 台需要留意
              </Chip>
            )}
          </div>
          <div className="divide-y divide-divider rounded-lg border border-divider">
            {servers.map((s) => {
              const chip = stateChip[s.state] || stateChip.syncing;

              return (
                <div
                  key={s.serverId}
                  className="flex flex-col gap-1 p-3 sm:flex-row sm:items-start sm:justify-between"
                >
                  <div className="min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="break-all font-medium">
                        {s.serverName}
                      </span>
                      <Chip
                        className="text-default-600"
                        size="sm"
                        variant="bordered"
                      >
                        {s.source === "shared" ? "妙妙屋共享" : "自有"}
                      </Chip>
                    </div>
                    {s.reason && (
                      <p
                        className={`text-xs ${s.state === "failed" ? "text-danger" : "text-default-500"}`}
                      >
                        {s.reason}
                      </p>
                    )}
                    {(s.notes || []).map((note) => (
                      <p key={note} className="text-xs text-warning">
                        {note}
                      </p>
                    ))}
                  </div>
                  <Chip
                    className={
                      s.state === "unsupported"
                        ? "shrink-0 self-start text-default-600 sm:self-auto"
                        : "shrink-0 self-start sm:self-auto"
                    }
                    color={chip.color}
                    size="sm"
                    variant={s.state === "unsupported" ? "bordered" : "flat"}
                  >
                    {s.state === "unsupported"
                      ? "不支持"
                      : (s.notes || []).length && s.state === "synced"
                        ? "已同步（有提示）"
                        : chip.label}
                  </Chip>
                </div>
              );
            })}
            {!servers.length && (
              <p className="p-4 text-center text-sm text-default-500">
                还没有落地服务器
              </p>
            )}
          </div>
        </section>

        <section className="space-y-2">
          <div>
            <h3 className="font-medium">用户规则</h3>
            <p className="text-xs text-default-500">
              用户可在「我的节点 →
              路由规则」为自己的节点添加规则（只作用于该节点）。可单独为某个用户在用户编辑「落地」里改；0
              = 不开放；调低不会删除已有规则。当前 {initial.nodesWithRules}{" "}
              个节点设置了节点规则。
            </p>
          </div>
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
            <Input
              className="sm:max-w-44"
              label="每个节点最多"
              min="0"
              type="number"
              value={perNode}
              variant="bordered"
              onChange={(event) => setPerNode(event.target.value)}
            />
            <Input
              className="sm:max-w-44"
              label="每个用户总计最多"
              min="0"
              type="number"
              value={total}
              variant="bordered"
              onChange={(event) => setTotal(event.target.value)}
            />
            <Button
              color="success"
              isLoading={saving === "caps"}
              size="sm"
              variant="flat"
              onPress={() => void saveCaps()}
            >
              保存上限
            </Button>
          </div>
        </section>
      </CardBody>
    </Card>
  );
}
