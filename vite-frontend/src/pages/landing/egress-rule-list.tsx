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
  Sparkles,
  Trash2,
} from "lucide-react";

import { RuleEditor } from "./egress-rule-editor";

import {
  EgressPreset,
  EgressRule,
  applyPreset,
  actionColor,
  egressPresets,
  emptyRule,
  ruleAttributeChips,
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
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Plan 134: an editable, ordered rule list (global 强制 / 默认 lists and node
// rules), styled like the 路由方案 editor rows.

type Move = "top" | "up" | "down" | "bottom";

export type RuleChoice = { id: number; name: string };

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

export const choiceLabel = (action: string, choices: RuleChoice[]) => {
  if (action === "block") return "封锁";
  if (!action || action === "direct") return "直连";
  const id = Number(action.replace("outbound:", ""));

  return choices.find((o) => o.id === id)?.name || "已删除的出站";
};

export function EgressRuleList({
  rules,
  onChange,
  choices,
  hideSource = false,
  presets = false,
  canAdd = true,
  addHint,
  emptyText,
  numberFrom = 1,
  labels,
}: {
  rules: EgressRule[];
  onChange: (rules: EgressRule[]) => void;
  // Outbound templates selectable as actions (besides 直连 / 封锁).
  choices: RuleChoice[];
  hideSource?: boolean;
  presets?: boolean;
  canAdd?: boolean;
  addHint?: string;
  emptyText: string;
  numberFrom?: number;
  // Names for action labels when they differ from the selectable choices
  // (e.g. a template no longer published to users).
  labels?: RuleChoice[];
}) {
  const isMobile = useMobileBreakpoint();
  const [editing, setEditing] = useState<{
    index: number;
    rule: EgressRule;
  } | null>(null);
  const [presetFor, setPresetFor] = useState<EgressPreset | null>(null);
  const [presetAction, setPresetAction] = useState("direct");
  const available = presets
    ? egressPresets.filter((p) => !hideSource || p.key !== "block-cn")
    : [];

  const addPreset = (preset: EgressPreset, action: string) => {
    const result = applyPreset(rules, preset, action);

    onChange(result.rules);
    if (result.added === 0) toast("规则已存在，未重复添加");
    else if (result.skipped > 0)
      toast.success(
        `已添加 ${result.added} 条，${result.skipped} 条已存在未重复添加`,
      );
    else toast.success(`已添加「${preset.label}」`);
  };

  const moves = (index: number) =>
    (
      [
        ["top", "置顶", ChevronsUp, index > 0],
        ["up", "上移", ChevronUp, index > 0],
        ["down", "下移", ChevronDown, index < rules.length - 1],
        ["bottom", "置底", ChevronsDown, index < rules.length - 1],
      ] as const
    ).filter(([, , , possible]) => possible);

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-end gap-2">
        {addHint && (
          <span className="mr-auto text-xs text-default-500">{addHint}</span>
        )}
        {presets && canAdd && (
          <Dropdown placement="bottom-end">
            <DropdownTrigger>
              <Button
                color="secondary"
                size="sm"
                startContent={<Sparkles size={16} />}
                variant="flat"
              >
                添加预设
              </Button>
            </DropdownTrigger>
            <DropdownMenu aria-label="规则预设">
              {available.map((preset) => (
                <DropdownItem
                  key={preset.key}
                  onPress={() => {
                    if (!preset.needsOutbound) {
                      addPreset(preset, "block");

                      return;
                    }
                    setPresetAction(
                      choices[0] ? `outbound:${choices[0].id}` : "direct",
                    );
                    setPresetFor(preset);
                  }}
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
        )}
        {canAdd && (
          <Button
            color="success"
            size="sm"
            startContent={<Plus size={16} />}
            onPress={() => setEditing({ index: -1, rule: emptyRule("block") })}
          >
            添加规则
          </Button>
        )}
      </div>
      <div className="divide-y divide-divider rounded-lg border border-divider">
        {rules.map((rule, index) => (
          <div
            key={rule.id}
            className={`flex flex-col gap-2 p-3 sm:flex-row sm:items-start sm:justify-between ${rule.enabled ? "" : "opacity-60"}`}
          >
            <div className="flex min-w-0 flex-1 items-start gap-3">
              <span className="mt-0.5 w-6 shrink-0 text-right text-sm text-default-400">
                {numberFrom + index}
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
                    → {choiceLabel(rule.action, labels || choices)}
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
                  onChange(
                    rules.map((r, i) => (i === index ? { ...r, enabled } : r)),
                  )
                }
              />
              {!isMobile &&
                (
                  [
                    ["top", "置顶", ChevronsUp, index === 0],
                    ["up", "上移", ChevronUp, index === 0],
                    ["down", "下移", ChevronDown, index === rules.length - 1],
                    [
                      "bottom",
                      "置底",
                      ChevronsDown,
                      index === rules.length - 1,
                    ],
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
                    onPress={() => onChange(moveRule(rules, index, move))}
                  >
                    <Icon size={16} />
                  </Button>
                ))}
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
                    {[
                      ...moves(index).map(([move, label, Icon]) => (
                        <DropdownItem
                          key={move}
                          startContent={<Icon size={16} />}
                          onPress={() => onChange(moveRule(rules, index, move))}
                        >
                          {label}
                        </DropdownItem>
                      )),
                      <DropdownItem
                        key="delete"
                        color="danger"
                        startContent={<Trash2 size={16} />}
                        onPress={() =>
                          onChange(rules.filter((_, i) => i !== index))
                        }
                      >
                        删除
                      </DropdownItem>,
                    ]}
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
                  onPress={() => onChange(rules.filter((_, i) => i !== index))}
                >
                  <Trash2 size={16} />
                </Button>
              )}
            </div>
          </div>
        ))}
        {!rules.length && (
          <p className="p-4 text-center text-sm text-default-500">
            {emptyText}
          </p>
        )}
      </div>
      {editing && (
        <RuleEditor
          hideNodeScope
          hideSource={hideSource}
          landingNodes={[]}
          outbounds={choices}
          rule={editing.rule}
          onClose={() => setEditing(null)}
          onDone={(rule) => {
            onChange(
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
              selectedKeys={[presetAction]}
              variant="bordered"
              onSelectionChange={(keys) =>
                setPresetAction(String(Array.from(keys)[0]))
              }
            >
              {[
                ...choices.map((o) => (
                  <SelectItem key={`outbound:${o.id}`}>{o.name}</SelectItem>
                )),
                <SelectItem key="direct">直连</SelectItem>,
              ]}
            </Select>
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setPresetFor(null)}
            >
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
    </div>
  );
}
