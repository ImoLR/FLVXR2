import { useState } from "react";
import toast from "react-hot-toast";
import {
  ChevronDown,
  ChevronUp,
  ChevronsDown,
  ChevronsUp,
  Lock,
  MoreHorizontal,
  Pencil,
  Plus,
  Sparkles,
  Trash2,
} from "lucide-react";

import { RuleEditor } from "./egress-rule-editor";

import {
  EgressOverview,
  EgressPreset,
  EgressProfile,
  EgressRule,
  actionColor,
  actionLabel,
  applyPreset,
  egressPresets,
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
  });
  const [editing, setEditing] = useState<{
    index: number;
    rule: EgressRule;
  } | null>(null);
  const [presetFor, setPresetFor] = useState<EgressPreset | null>(null);
  const warp = overview.outbounds.find((o) => o.type === "warp");
  const [presetAction, setPresetAction] = useState("");
  const [saving, setSaving] = useState(false);
  const rules = form.rules || [];
  const setRules = (next: EgressRule[]) =>
    setForm((prev) => ({ ...prev, rules: next }));

  const addPreset = (preset: EgressPreset, action: string) => {
    const result = applyPreset(rules, preset, action);

    setRules(result.rules);
    if (result.added === 0) toast("规则已存在，未重复添加");
    else if (result.skipped > 0)
      toast.success(
        `已添加 ${result.added} 条，${result.skipped} 条已存在未重复添加`,
      );
    else toast.success(`已添加「${preset.label}」`);
  };

  const choosePreset = (preset: EgressPreset) => {
    if (!preset.needsOutbound) {
      addPreset(preset, "block");

      return;
    }
    setPresetAction(
      warp
        ? `outbound:${warp.id}`
        : overview.outbounds[0]
          ? `outbound:${overview.outbounds[0].id}`
          : "direct",
    );
    setPresetFor(preset);
  };

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
        <ModalContent className="flvx-landing-text">
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
                  <Dropdown placement="bottom-end">
                    <DropdownTrigger>
                      <Button
                        size="sm"
                        startContent={<Sparkles size={16} />}
                        variant="flat"
                      >
                        添加预设
                      </Button>
                    </DropdownTrigger>
                    <DropdownMenu aria-label="规则预设">
                      {egressPresets.map((preset) => (
                        <DropdownItem
                          key={preset.key}
                          onPress={() => choosePreset(preset)}
                        >
                          <div className="flex flex-col py-0.5">
                            <span>{preset.label}</span>
                            <span className="max-w-72 text-xs text-default-500">
                              {preset.description}
                            </span>
                          </div>
                        </DropdownItem>
                      ))}
                    </DropdownMenu>
                  </Dropdown>
                  <Button
                    color="primary"
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
              <div className="divide-y divide-divider rounded-lg border border-divider">
                <div className="flex items-start gap-3 bg-default-50 p-3">
                  <Lock className="mt-1 shrink-0 text-default-400" size={16} />
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">安全规则</span>
                      <Chip
                        className="text-default-600"
                        size="sm"
                        variant="bordered"
                      >
                        锁定
                      </Chip>
                      <Chip color="danger" size="sm" variant="flat">
                        → 封锁
                      </Chip>
                    </div>
                    <p className="text-xs text-default-500">
                      内网、回环、链路本地、云元数据等地址一律封锁，不能移动或删除。
                    </p>
                  </div>
                </div>
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
                          {!rule.enabled && (
                            <Chip
                              className="text-default-600"
                              size="sm"
                              variant="bordered"
                            >
                              已停用
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
                        size="sm"
                        title="编辑"
                        variant="light"
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
                          variant="light"
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
            <Button variant="light" onPress={onClose}>
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
      <Modal isOpen={Boolean(presetFor)} onClose={() => setPresetFor(null)}>
        <ModalContent className="flvx-landing-text">
          <ModalHeader>添加预设 · {presetFor?.label}</ModalHeader>
          <ModalBody className="space-y-3">
            <p className="text-sm text-default-500">{presetFor?.description}</p>
            <Select
              label="走哪个出站"
              selectedKeys={presetAction ? [presetAction] : []}
              variant="bordered"
              onSelectionChange={(keys) =>
                setPresetAction(String(Array.from(keys)[0]))
              }
            >
              {[
                ...overview.outbounds.map((o) => (
                  <SelectItem key={`outbound:${o.id}`}>{o.name}</SelectItem>
                )),
                <SelectItem key="direct">直连（默认）</SelectItem>,
              ]}
            </Select>
            {!overview.outbounds.length && (
              <p className="text-xs text-warning">
                还没有出站模板，可先在「出站」里新建 WARP 或代理。
              </p>
            )}
          </ModalBody>
          <ModalFooter>
            <Button variant="light" onPress={() => setPresetFor(null)}>
              取消
            </Button>
            <Button
              color="primary"
              onPress={() => {
                if (presetFor) addPreset(presetFor, presetAction || "direct");
                setPresetFor(null);
              }}
            >
              添加
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  );
}
