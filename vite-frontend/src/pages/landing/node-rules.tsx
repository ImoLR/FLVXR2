import { useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";

import { BuiltinCompactRows, BuiltinRuleList } from "./egress-builtin-list";
import { EgressRuleList } from "./egress-rule-list";
import { LandingTone, Pill } from "./pill";
import {
  CompactRuleRow,
  EmptyLayer,
  LayerStep,
  LayerToggle,
  RuleRows,
  layerTones,
} from "./rule-layers";

import {
  EgressRule,
  NodeRulesView,
  getNodeRules,
  saveNodeRules,
} from "@/api/landing-egress";
import { useMobileBreakpoint } from "@/hooks/useMobileBreakpoint";
import { Button } from "@/shadcn-bridge/heroui/button";
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
// Plan 149: shown as a numbered waterfall ① 内置 → ⑥ 默认出站; every layer
// is always visible (「暂无」 when empty), rows are one line with the matchers
// behind 「详情」 (collapsed on H5), and 内置 collapses to one line per rule on
// H5. Rule semantics, order, editing and saving are unchanged.

const stateChip: Record<
  NodeRulesView["state"],
  { label: string; tone: LandingTone }
> = {
  applied: { label: "已生效", tone: "green" },
  syncing: { label: "同步中", tone: "purple" },
  queued: { label: "下次重启生效", tone: "amber" },
  failed: { label: "同步失败", tone: "red" },
  unsupported: { label: "不支持", tone: "slate" },
};

const countText = (n: number) => (n > 0 ? `${n} 条` : "暂无");

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
  const isMobile = useMobileBreakpoint();
  const [builtinOpen, setBuiltinOpen] = useState(false);

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

  // Plan 149 waterfall data (same rules as before, only laid out per layer).
  const globalWaiting = !view.global.active;
  const forced = view.global.active ? view.global.forced : [];
  const globalDefault = view.global.active ? view.global.default : [];
  const serverRules = view.server.rules || [];
  const serverCount = view.server.rules
    ? serverRules.length
    : view.server.ruleCount || 0;
  const builtinOn =
    1 + (Object.values(builtinValues).filter(Boolean).length as number);

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
              <Pill tone={chip.tone}>{chip.label}</Pill>
              <Pill tone={overCap ? "amber" : "slate"}>{counters}</Pill>
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

            <ol
              data-rule-layers
              aria-label="匹配顺序（从上到下，先命中先生效）"
            >
              <LayerStep
                aside={
                  isMobile ? (
                    <LayerToggle
                      label={["展开详情", "收起"]}
                      open={builtinOpen}
                      onToggle={() => setBuiltinOpen((v) => !v)}
                    />
                  ) : undefined
                }
                count={`${builtinOn} 条`}
                hint="最先匹配，不能放行"
                index={1}
                title="内置"
                tone={layerTones.builtin}
              >
                {isMobile && !builtinOpen ? (
                  <BuiltinCompactRows
                    offReasons={builtin.offReasons}
                    values={builtinValues}
                  />
                ) : (
                  <BuiltinRuleList
                    offReasons={builtin.offReasons}
                    safety={builtin.safety}
                    values={builtinValues}
                  />
                )}
              </LayerStep>

              <LayerStep
                count={countText(forced.length)}
                hint="管理员强制，不能覆盖"
                index={2}
                title="全局·强制"
                tone={layerTones.forced}
              >
                {forced.length ? (
                  <RuleRows>
                    {forced.map((rule) => (
                      <CompactRuleRow
                        key={rule.id}
                        locked
                        defaultOpen={!isMobile}
                        rule={rule}
                      />
                    ))}
                  </RuleRows>
                ) : (
                  <EmptyLayer
                    text={
                      globalWaiting
                        ? view.globalPending || "全局规则暂未应用到这台服务器"
                        : "管理员没有设置强制规则"
                    }
                  />
                )}
              </LayerStep>

              <LayerStep
                count={countText(rules.length)}
                hint="只作用于这个节点，可覆盖下面的规则"
                index={3}
                title="我的规则"
                tone={layerTones.node}
              >
                {open || rules.length ? (
                  <EgressRuleList
                    collapseDetails
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
                  <EmptyLayer text="管理员未开放节点规则" />
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
              </LayerStep>

              <LayerStep
                aside={
                  <Pill
                    title="服务器的路由方案（没有方案时为默认）"
                    tone={layerTones.server}
                  >
                    {view.server.profileName
                      ? `${view.server.profileName} · ${view.server.label}`
                      : view.server.label}
                  </Pill>
                }
                count={countText(serverCount)}
                hint="可被你的规则覆盖"
                index={4}
                title="服务器规则"
                tone={layerTones.server}
              >
                {serverRules.length ? (
                  <RuleRows>
                    {serverRules.map((rule) => (
                      <CompactRuleRow
                        key={`s-${rule.id}`}
                        defaultOpen={!isMobile}
                        locked={false}
                        rule={rule}
                      />
                    ))}
                  </RuleRows>
                ) : (
                  <EmptyLayer
                    text={
                      serverCount
                        ? `${serverCount} 条服务器规则（由管理员设置）`
                        : "这台服务器没有额外规则"
                    }
                  />
                )}
              </LayerStep>

              <LayerStep
                count={countText(globalDefault.length)}
                hint="前面都没命中时使用，可被你的规则覆盖"
                index={5}
                title="全局·默认"
                tone={layerTones.default}
              >
                {globalDefault.length ? (
                  <RuleRows>
                    {globalDefault.map((rule) => (
                      <CompactRuleRow
                        key={`d-${rule.id}`}
                        defaultOpen={!isMobile}
                        locked={false}
                        rule={rule}
                      />
                    ))}
                  </RuleRows>
                ) : (
                  <EmptyLayer
                    text={
                      globalWaiting
                        ? view.globalPending || "全局规则暂未应用到这台服务器"
                        : "管理员没有设置全局默认规则"
                    }
                  />
                )}
              </LayerStep>

              <LayerStep
                last
                hint="没有命中任何规则的流量走这里"
                index={6}
                title="默认出站"
                tone={layerTones.outbound}
              >
                <Select
                  aria-label="默认出站"
                  isDisabled={!view.supported || (!open && !def)}
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
                        <SelectItem key={`outbound:${o.id}`}>
                          {o.name}
                        </SelectItem>
                      )),
                  ]}
                </Select>
              </LayerStep>
            </ol>
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
