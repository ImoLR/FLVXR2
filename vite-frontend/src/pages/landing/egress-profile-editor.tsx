import { useState } from "react";
import toast from "react-hot-toast";
import {
  ChevronDown,
  ChevronUp,
  ChevronsDown,
  ChevronsUp,
  MoreHorizontal,
  Pencil,
  Plus,
  ShieldCheck,
  Trash2,
} from "lucide-react";

import { BuiltinRuleList, StateIcon } from "./egress-builtin-list";
import { PresetPicker } from "./egress-preset-picker";
import { RuleEditor } from "./egress-rule-editor";

import {
  EgressOverview,
  EgressProfile,
  EgressRule,
  abuseDefaultsOn,
  actionColor,
  actionLabel,
  builtinCover,
  emptyRule,
  ruleAttributeChips,
  saveEgressProfile,
} from "@/api/landing-egress";
import { useMobileBreakpoint } from "@/hooks/useMobileBreakpoint";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import {
  Dropdown,
  DropdownItem,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Switch } from "@/shadcn-bridge/heroui/switch";

type Move = "top" | "up" | "down" | "bottom";

function moveRule(rules: EgressRule[], index: number, move: Move) {
  const next = [...rules];
  const [rule] = next.splice(index, 1);
  const target =
    move === "top"
      ? 0
      : move === "bottom"
        ? next.length
        : move === "up"
          ? Math.max(0, index - 1)
          : Math.min(next.length, index + 1);

  next.splice(target, 0, rule);

  return next;
}

export function ProfileEditor({
  profile,
  overview,
  onClose,
  onSaved,
}: {
  profile: Partial<EgressProfile> | null;
  overview: EgressOverview;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isMobile = useMobileBreakpoint();
  const [form, setForm] = useState<Partial<EgressProfile>>({
    name: "",
    defaultAction: "direct",
    rules: [],
    ...profile,
    abuseDefaults: abuseDefaultsOn(profile),
  });
  const abuseOn = abuseDefaultsOn(form);
  const [editing, setEditing] = useState<{
    index: number;
    rule: EgressRule;
  } | null>(null);
  const warp = overview.outbounds.find((o) => o.type === "warp");
  const [saving, setSaving] = useState(false);
  const rules = form.rules || [];
  const setRules = (next: EgressRule[]) =>
    setForm((prev) => ({ ...prev, rules: next }));
  // Plan 140: the global switches AND this profile's toggle decide which
  // default rules apply to its nodes.
  const builtin = overview.global?.builtin;
  const safety = builtin?.safety || [];
  const globalOn = {
    smtp: builtin?.smtp !== false,
    mail: !!builtin?.mail,
    telnet: !!builtin?.telnet,
    bt: builtin?.bt !== false,
  };
  const active = {
    smtp: abuseOn && globalOn.smtp,
    mail: abuseOn && globalOn.mail,
    telnet: abuseOn && globalOn.telnet,
    bt: abuseOn && globalOn.bt,
  };
  const choices = overview.outbounds.map((o) => ({ id: o.id, name: o.name }));

  const save = async () => {
    setSaving(true);
    const res = await saveEgressProfile(form);

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success("路由方案已保存");
    (res.data?.warnings || []).forEach((w) => toast(w));
    onSaved();
    onClose();
  };

  const moveButtons = (index: number) =>
    (
      [
        ["top", "置顶", ChevronsUp, index === 0],
        ["up", "上移", ChevronUp, index === 0],
        ["down", "下移", ChevronDown, index === rules.length - 1],
        ["bottom", "置底", ChevronsDown, index === rules.length - 1],
      ] as const
    ).map(([move, label, Icon, disabled]) => (
      <Button
        key={move}
        isIconOnly
        aria-label={`${label}规则`}
        isDisabled={disabled}
        size="sm"
        title={label}
        variant="light"
        onPress={() => setRules(moveRule(rules, index, move))}
      >
        <Icon size={16} />
      </Button>
    ));

  return (
    <>
      <Modal isOpen scrollBehavior="inside" size="xl" onClose={onClose}>
        <ModalContent className="flvx-landing">
          <ModalHeader>
            {profile?.id ? `编辑路由方案 · ${profile.name}` : "新建路由方案"}
          </ModalHeader>
          <ModalBody className="space-y-5">
            <div className="grid gap-3 sm:grid-cols-2">
              <Input
                label="方案名称"
                placeholder="例如：解锁 + 屏蔽大陆"
                value={form.name || ""}
                variant="bordered"
                onChange={(event) =>
                  setForm((prev) => ({ ...prev, name: event.target.value }))
                }
              />
              <Select
                description="没有命中任何规则的流量走这里"
                label="默认出站"
                selectedKeys={[form.defaultAction || "direct"]}
                variant="bordered"
                onSelectionChange={(keys) =>
                  setForm((prev) => ({
                    ...prev,
                    defaultAction: String(Array.from(keys)[0]),
                  }))
                }
              >
                {[
                  <SelectItem key="direct">直连（默认）</SelectItem>,
                  <SelectItem key="block">封锁</SelectItem>,
                  ...overview.outbounds.map((o) => (
                    <SelectItem key={`outbound:${o.id}`}>{o.name}</SelectItem>
                  )),
                ]}
              </Select>
            </div>

            <section className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-medium">规则</h3>
                <div className="flex flex-wrap gap-2">
                  <PresetPicker
                    active={active}
                    choices={choices}
                    defaultChoice={warp?.id}
                    rules={rules}
                    safety={safety}
                    onChange={setRules}
                  />
                  <Button
                    color="success"
                    size="sm"
                    startContent={<Plus size={16} />}
                    onPress={() =>
                      setEditing({ index: -1, rule: emptyRule("block") })
                    }
                  >
                    添加规则
                  </Button>
                </div>
              </div>
              <p className="text-xs text-default-500">
                从上到下依次匹配，命中第一条即停止。安全规则固定在最前。
              </p>
              <div className="flex items-start justify-between gap-3 rounded-lg border border-divider p-3">
                <div className="flex min-w-0 items-start gap-2">
                  <ShieldCheck
                    className={`mt-0.5 shrink-0 ${abuseOn ? "text-success" : "text-default-400"}`}
                    size={16}
                  />
                  <div className="min-w-0 space-y-0.5">
                    <p className="text-sm font-medium">默认防滥用规则</p>
                    <p className="text-xs text-default-500">
                      紧跟安全规则：内置的 4 条防滥用规则（SMTP 25、发信端口
                      465/587/2525、Telnet/SMB、BT，以全局开关为准）。仅管理员可见；关闭后使用本方案的节点不再封锁这些。
                    </p>
                  </div>
                </div>
                <Switch
                  aria-label="默认防滥用规则"
                  isSelected={abuseOn}
                  onValueChange={(abuseDefaults) =>
                    setForm((prev) => ({ ...prev, abuseDefaults }))
                  }
                />
              </div>
              <BuiltinRuleList
                admin
                profileOff={!abuseOn}
                safety={safety}
                values={globalOn}
              />
              <div className="divide-y divide-divider rounded-lg border border-divider">
                {rules.map((rule, index) => (
                  <div
                    key={rule.id}
                    className={`flex flex-col gap-2 p-3 sm:flex-row sm:items-start sm:justify-between ${rule.enabled ? "" : "opacity-60"}`}
                  >
                    <div className="flex min-w-0 flex-1 items-start gap-3">
                      <span className="mt-0.5 w-6 shrink-0 text-right text-sm text-default-400">
                        {index + 1}
                      </span>
                      <div className="min-w-0 space-y-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="break-all font-medium">
                            {rule.name || `规则 ${index + 1}`}
                          </span>
                          <Chip
                            color={actionColor(rule.action)}
                            size="sm"
                            variant="flat"
                          >
                            → {actionLabel(rule.action, overview.outbounds)}
                          </Chip>
                          {rule.enabled &&
                            builtinCover(rule, active, safety) && (
                              <Chip color="success" size="sm" variant="flat">
                                已由内置规则覆盖
                              </Chip>
                            )}
                        </div>
                        <div className="flex flex-wrap gap-1">
                          {ruleAttributeChips(rule).map((chip) => (
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
                    <div className="flex shrink-0 items-center gap-1 self-end sm:self-auto">
                      {!rule.enabled && (
                        <StateIcon
                          label="未生效：已停用，不会下发"
                          on={false}
                        />
                      )}
                      <Switch
                        aria-label={rule.enabled ? "停用规则" : "启用规则"}
                        isSelected={rule.enabled}
                        onValueChange={(enabled) =>
                          setRules(
                            rules.map((r, i) =>
                              i === index ? { ...r, enabled } : r,
                            ),
                          )
                        }
                      />
                      {!isMobile && moveButtons(index)}
                      <Button
                        isIconOnly
                        aria-label="编辑规则"
                        color="primary"
                        size="sm"
                        title="编辑"
                        variant="flat"
                        onPress={() => setEditing({ index, rule })}
                      >
                        <Pencil size={16} />
                      </Button>
                      {isMobile ? (
                        <Dropdown placement="bottom-end">
                          <DropdownTrigger>
                            <Button
                              isIconOnly
                              aria-label="更多操作"
                              size="sm"
                              variant="light"
                            >
                              <MoreHorizontal size={18} />
                            </Button>
                          </DropdownTrigger>
                          <DropdownMenu aria-label="规则操作">
                            {(
                              [
                                ["top", "置顶", ChevronsUp, index > 0],
                                ["up", "上移", ChevronUp, index > 0],
                                [
                                  "down",
                                  "下移",
                                  ChevronDown,
                                  index < rules.length - 1,
                                ],
                                [
                                  "bottom",
                                  "置底",
                                  ChevronsDown,
                                  index < rules.length - 1,
                                ],
                              ] as const
                            )
                              .filter(([, , , possible]) => possible)
                              .map(([move, label, Icon]) => (
                                <DropdownItem
                                  key={move}
                                  startContent={<Icon size={16} />}
                                  onPress={() =>
                                    setRules(moveRule(rules, index, move))
                                  }
                                >
                                  {label}
                                </DropdownItem>
                              ))}
                            <DropdownItem
                              color="danger"
                              startContent={<Trash2 size={16} />}
                              onPress={() =>
                                setRules(rules.filter((_, i) => i !== index))
                              }
                            >
                              删除
                            </DropdownItem>
                          </DropdownMenu>
                        </Dropdown>
                      ) : (
                        <Button
                          isIconOnly
                          aria-label="删除规则"
                          color="danger"
                          size="sm"
                          title="删除"
                          variant="flat"
                          onPress={() =>
                            setRules(rules.filter((_, i) => i !== index))
                          }
                        >
                          <Trash2 size={16} />
                        </Button>
                      )}
                    </div>
                  </div>
                ))}
                {!rules.length && (
                  <p className="p-4 text-center text-sm text-default-500">
                    还没有规则：全部流量走默认出站。可以先添加一个预设。
                  </p>
                )}
              </div>
            </section>
          </ModalBody>
          <ModalFooter>
            <Button color="danger" variant="flat" onPress={onClose}>
              取消
            </Button>
            <Button
              color="primary"
              isDisabled={!form.name?.trim()}
              isLoading={saving}
              onPress={() => void save()}
            >
              保存方案
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      {editing && (
        <RuleEditor
          savePreset
          landingNodes={overview.landingNodes}
          outbounds={overview.outbounds}
          rule={editing.rule}
          onClose={() => setEditing(null)}
          onDone={(rule) => {
            setRules(
              editing.index < 0
                ? [...rules, rule]
                : rules.map((r, i) => (i === editing.index ? rule : r)),
            );
            setEditing(null);
          }}
        />
      )}
    </>
  );
}
