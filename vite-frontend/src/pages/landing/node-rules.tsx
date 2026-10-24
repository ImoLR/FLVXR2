import { useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";
import { Lock } from "lucide-react";

import { BuiltinRuleList } from "./egress-builtin-list";
import { LayerLegend } from "./egress-global";
import { EgressRuleList } from "./egress-rule-list";

import {
  EgressRule,
  NodeRulesView,
  RuleSummary,
  actionColor,
  getNodeRules,
  saveNodeRules,
} from "@/api/landing-egress";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";

// Plan 134: 「路由规则」 of one landing node — the owner's own rules between
// the locked 全局·强制 rows and the overridable 服务器 / 全局·默认 rows.

const stateChip: Record<
  NodeRulesView["state"],
  {
    label: string;
    color: "success" | "secondary" | "warning" | "danger" | "default";
  }
> = {
  applied: { label: "已生效", color: "success" },
  syncing: { label: "同步中", color: "secondary" },
  queued: { label: "下次重启生效", color: "warning" },
  failed: { label: "同步失败", color: "danger" },
  unsupported: { label: "不支持", color: "default" },
};

function summaryChips(r: RuleSummary) {
  const chips: string[] = [];

  if (r.domains) chips.push(`目标域名 ${r.domains}`);
  if (r.ips) chips.push(`目标 IP ${r.ips}`);
  if (r.ports) chips.push(`目标端口 ${r.ports}`);
  if (r.sourceIps) chips.push(`来源 IP ${r.sourceIps}`);
  if (r.network) chips.push(r.network === "tcp" ? "TCP" : "UDP");
  if (r.protocols && r.protocols.length)
    chips.push(`协议 ${r.protocols.join("/")}`);

  return chips;
}

function FixedRow({
  rule,
  tag,
  locked,
}: {
  rule: RuleSummary;
  tag: string;
  locked: boolean;
}) {
  return (
    <div
      className={`flex items-start gap-3 p-3 ${locked ? "bg-default-50" : "opacity-60"}`}
    >
      <Lock
        className={`mt-1 shrink-0 ${locked ? "text-default-500" : "text-default-300"}`}
        size={14}
      />
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="break-all font-medium">{rule.name}</span>
          <Chip color={actionColor(rule.action)} size="sm" variant="flat">
            → {rule.actionLabel}
          </Chip>
          <Chip className="text-default-600" size="sm" variant="bordered">
            {tag}
          </Chip>
        </div>
        <div className="flex flex-wrap gap-1">
          {summaryChips(rule).map((chip) => (
            <Chip
              key={chip}
              className="text-default-600"
              size="sm"
              variant="bordered"
            >
              {chip}
            </Chip>
          ))}
        </div>
      </div>
    </div>
  );
}

export function NodeRulesDialog({
  nodeId,
  onClose,
  onSaved,
}: {
  nodeId: number;
  onClose: () => void;
  onSaved?: () => void;
}) {
  const [view, setView] = useState<NodeRulesView | null>(null);
  const [error, setError] = useState("");
  const [rules, setRules] = useState<EgressRule[]>([]);
  const [def, setDef] = useState("");
  const [saving, setSaving] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  useEffect(() => {
    let disposed = false;

    void getNodeRules(nodeId).then((res) => {
      if (disposed) return;
      if (res.code !== 0) {
        setError(res.msg || "读取失败");

        return;
      }
      setView(res.data);
      setRules(res.data.rules || []);
      setDef(res.data.defaultAction || "");
    });

    return () => {
      disposed = true;
    };
  }, [nodeId]);

  const choices = useMemo(
    () =>
      (view?.outbounds || [])
        .filter((o) => o.allowed)
        .map((o) => ({ id: o.id, name: o.name })),
    [view],
  );
  const labelChoices = useMemo(
    () => (view?.outbounds || []).map((o) => ({ id: o.id, name: o.name })),
    [view],
  );

  if (!view) {
    return (
      <Modal isOpen size="xl" onClose={onClose}>
        <ModalContent className="flvx-landing">
          <ModalHeader>路由规则</ModalHeader>
          <ModalBody>
            <div className="flex justify-center p-6">
              {error ? <p className="text-danger">{error}</p> : <Spinner />}
            </div>
          </ModalBody>
          <ModalFooter>
            <Button color="danger" variant="flat" onPress={onClose}>
              关闭
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    );
  }

  // Closing with unsaved edits asks first (QA F28: added rules were dropped
  // silently on 取消).
  const dirty =
    JSON.stringify(rules) !== JSON.stringify(view.rules || []) ||
    def !== (view.defaultAction || "");
  const requestClose = () => {
    if (dirty && !saving) setConfirmDiscard(true);
    else onClose();
  };
  const caps = view.caps;
  const othersUsed = caps.used - (view.rules || []).length;
  const totalUsed = othersUsed + rules.length;
  const open = caps.unlimited || caps.total > 0;
  const canAdd =
    view.supported &&
    (caps.unlimited || (rules.length < caps.perNode && totalUsed < caps.total));
  const chip = stateChip[view.state] || stateChip.syncing;
  // Plan 140: whether each built-in rule applies to this node.
  const builtin = view.builtin || {
    safety: [],
    smtp: true,
    mail: false,
    telnet: false,
    bt: true,
  };
  const builtinValues = {
    smtp: builtin.smtp,
    mail: !!builtin.mail,
    telnet: !!builtin.telnet,
    bt: builtin.bt,
  };
  const counters = caps.unlimited
    ? `本节点 ${rules.length} 条 · 管理员不限`
    : `本节点 ${rules.length}/${caps.perNode} · 总计 ${totalUsed}/${caps.total}`;
  const overCap =
    !caps.unlimited && (rules.length > caps.perNode || totalUsed > caps.total);

  const save = async () => {
    setSaving(true);
    const res = await saveNodeRules(view.nodeId, rules, def);

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success(
      res.data.state === "queued"
        ? "已保存，下次落地 Core 重启后生效"
        : "路由规则已保存，正在同步",
    );
    (res.data.warnings || []).forEach((w) => toast(w));
    onSaved?.();
    onClose();
  };

  return (
    <>
      <Modal isOpen scrollBehavior="inside" size="xl" onClose={requestClose}>
        <ModalContent className="flvx-landing">
          <ModalHeader className="flex flex-col gap-1">
            <span>路由规则 · {view.nodeName}</span>
            <span className="text-xs font-normal text-default-500">
              {view.serverName} · 只作用于这个节点
            </span>
          </ModalHeader>
          <ModalBody className="space-y-4">
            <div className="flex flex-wrap items-center gap-2">
              <Chip
                className={
                  view.state === "unsupported" ? "text-default-600" : undefined
                }
                color={chip.color}
                size="sm"
                variant={view.state === "unsupported" ? "bordered" : "flat"}
              >
                {chip.label}
              </Chip>
              <Chip
                className={overCap ? undefined : "text-default-600"}
                color={overCap ? "warning" : "default"}
                size="sm"
                variant={overCap ? "flat" : "bordered"}
              >
                {counters}
              </Chip>
            </div>
            {view.stateReason && (
              <p
                className={`text-xs ${view.state === "failed" ? "text-danger" : "text-default-500"}`}
              >
                {view.stateReason}
              </p>
            )}
            {(view.notes || []).map((note) => (
              <p
                key={note}
                className="rounded-lg bg-warning-50 p-2 text-xs text-warning"
              >
                {note}
              </p>
            ))}
            {overCap && (
              <p className="text-xs text-warning">
                已超过当前上限：现有规则继续生效，但要先删减到上限以内才能再添加。
              </p>
            )}
            <LayerLegend compact highlight={["node"]} />

            <section className="space-y-2">
              <div>
                <h3 className="font-medium">内置规则</h3>
                <p className="text-xs text-default-500">
                  所有落地节点最先匹配，节点规则不能放行。
                </p>
              </div>
              <BuiltinRuleList
                offReasons={builtin.offReasons}
                safety={builtin.safety}
                values={builtinValues}
              />
            </section>
            {view.global.active && view.global.forced.length > 0 && (
              <div className="divide-y divide-divider rounded-lg border border-divider">
                {view.global.forced.map((rule) => (
                  <FixedRow key={rule.id} locked rule={rule} tag="全局·强制" />
                ))}
              </div>
            )}

            <section className="space-y-2">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-medium">我的规则</h3>
              </div>
              {open || rules.length ? (
                <EgressRuleList
                  hideSource
                  presets
                  addHint={
                    canAdd || !view.supported
                      ? undefined
                      : "已达到规则上限，删除后才能再添加"
                  }
                  builtinActive={builtinValues}
                  canAdd={canAdd}
                  choices={choices}
                  emptyText="还没有规则：按下面的服务器规则和默认出站转发"
                  labels={labelChoices}
                  numberFrom={1}
                  rules={rules}
                  safety={builtin.safety}
                  onChange={setRules}
                />
              ) : (
                <p className="rounded-lg border border-divider p-4 text-center text-sm text-default-500">
                  管理员未开放节点规则
                </p>
              )}
              {!view.admin && open && (
                <p className="text-xs text-default-500">
                  可选出站：直连、封锁
                  {choices.length
                    ? `，以及管理员开放的 ${choices.map((c) => c.name).join("、")}`
                    : ""}
                  。
                </p>
              )}
              {labelChoices.length > choices.length && (
                <p className="text-xs text-default-500">
                  带「已停止开放」出站的规则可以保留，但不能新加。
                </p>
              )}
            </section>

            <div className="divide-y divide-divider rounded-lg border border-divider">
              <div className="flex items-start gap-3 p-3 opacity-60">
                <Lock className="mt-1 shrink-0 text-default-300" size={14} />
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">服务器规则</span>
                    <Chip color="success" size="sm" variant="flat">
                      {view.server.profileName
                        ? `${view.server.profileName} · ${view.server.label}`
                        : view.server.label}
                    </Chip>
                    <Chip
                      className="text-default-600"
                      size="sm"
                      variant="bordered"
                    >
                      可被你的规则覆盖
                    </Chip>
                  </div>
                  {view.server.ruleCount !== undefined && (
                    <p className="text-xs text-default-500">
                      {view.server.ruleCount} 条服务器规则
                    </p>
                  )}
                </div>
              </div>
              {(view.server.rules || []).map((rule) => (
                <FixedRow
                  key={`s-${rule.id}`}
                  locked={false}
                  rule={rule}
                  tag="服务器"
                />
              ))}
              {view.global.active &&
                view.global.default.map((rule) => (
                  <FixedRow
                    key={`d-${rule.id}`}
                    locked={false}
                    rule={rule}
                    tag="全局·默认 · 可被你的规则覆盖"
                  />
                ))}
            </div>
            {view.globalPending && (
              <p className="text-xs text-default-500">{view.globalPending}</p>
            )}

            <Select
              description="没有命中任何规则的流量走这里"
              isDisabled={!view.supported || (!open && !def)}
              label="默认出站"
              selectedKeys={[def || "inherit"]}
              variant="bordered"
              onSelectionChange={(keys) => {
                const v = String(Array.from(keys)[0] || "inherit");

                setDef(v === "inherit" ? "" : v);
              }}
            >
              {[
                <SelectItem key="inherit">
                  {`跟随上级（当前：${view.inheritedLabel}，来自${view.inheritedFrom === "server" ? "服务器方案" : "全局默认出站"}）`}
                </SelectItem>,
                <SelectItem key="direct">直连</SelectItem>,
                <SelectItem key="block">封锁</SelectItem>,
                ...labelChoices
                  .filter(
                    (o) =>
                      choices.some((c) => c.id === o.id) ||
                      def === `outbound:${o.id}`,
                  )
                  .map((o) => (
                    <SelectItem key={`outbound:${o.id}`}>{o.name}</SelectItem>
                  )),
              ]}
            </Select>
          </ModalBody>
          <ModalFooter>
            <Button color="danger" variant="flat" onPress={requestClose}>
              取消
            </Button>
            <Button
              color="primary"
              isDisabled={!view.supported}
              isLoading={saving}
              onPress={() => void save()}
            >
              保存
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      <Modal
        isOpen={confirmDiscard}
        placement="center"
        size="sm"
        onClose={() => setConfirmDiscard(false)}
      >
        <ModalContent className="flvx-landing">
          <ModalHeader>放弃未保存的修改？</ModalHeader>
          <ModalBody>
            <p className="text-sm">规则的修改还没有保存，关闭后会丢失。</p>
          </ModalBody>
          <ModalFooter>
            <Button
              color="primary"
              variant="flat"
              onPress={() => setConfirmDiscard(false)}
            >
              继续编辑
            </Button>
            <Button
              color="danger"
              variant="flat"
              onPress={() => {
                setConfirmDiscard(false);
                onClose();
              }}
            >
              放弃修改
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  );
}
