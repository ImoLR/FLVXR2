import { useState } from "react";
import {
  ChevronDown,
  ChevronUp,
  ChevronsDown,
  ChevronsUp,
  MoreHorizontal,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react";

import { StateIcon } from "./egress-builtin-list";
import {
  LayerRuleRow,
  NumberLead,
  actionWords,
  editableDetails,
} from "./layer-rule-row";
import { RuleRows } from "./rule-layers";
import { Pill, actionTone } from "./pill";
import { RuleEditor } from "./egress-rule-editor";
import { PresetPicker } from "./egress-preset-picker";

import {
  BuiltinActive,
  EgressRule,
  emptyRule,
  presetAskAction,
  ruleAttributeChips,
} from "@/api/landing-egress";
import { useMobileBreakpoint } from "@/hooks/useMobileBreakpoint";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownItem,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";
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
  if (action === presetAskAction) return "插入时选择";
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
  builtinActive = { smtp: true, mail: false, telnet: false, bt: true },
  safety = [],
  defaultChoice,
  savePreset = false,
  askOption = false,
  layout = "list",
  detailsOpen = false,
  inactiveReason,
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
  // Plan 140: built-in rules in force here (preset 「内置已封锁」 hint), the
  // outbound preselected for presets that ask, 「同时保存为预设」 in the rule
  // editor (admin lists) and the 「插入时选择」 action (preset editing).
  builtinActive?: BuiltinActive;
  safety?: string[];
  defaultChoice?: number;
  savePreset?: boolean;
  askOption?: boolean;
  // Plan 214 (路由规则 dialog): 「layer」 = the shared layer rows of the
  // dialog (tinted box, ✓ / ✗ at the far right, matchers opened by the
  // layer's 「展开详情」 = detailsOpen); every control stays the same.
  layout?: "list" | "layer";
  detailsOpen?: boolean;
  // Why an enabled rule does not apply (e.g. 链式代理: only block rules).
  inactiveReason?: (rule: EgressRule) => string | undefined;
}) {
  const isMobile = useMobileBreakpoint();
  const [editing, setEditing] = useState<{
    index: number;
    rule: EgressRule;
  } | null>(null);

  const moves = (index: number) =>
    (
      [
        ["top", "置顶", ChevronsUp, index > 0],
        ["up", "上移", ChevronUp, index > 0],
        ["down", "下移", ChevronDown, index < rules.length - 1],
        ["bottom", "置底", ChevronsDown, index < rules.length - 1],
      ] as const
    ).filter(([, , , possible]) => possible);

  // Row controls of the list layout: 停用/启用, 置顶…置底 (desktop) or the
  // 「更多」 menu (H5), 编辑, 删除.
  const controls = (rule: EgressRule, index: number) => (
    <>
      {!rule.enabled && (
        <StateIcon label="未生效：已停用，不会下发" on={false} />
      )}
      <Switch
        aria-label={rule.enabled ? "停用规则" : "启用规则"}
        isSelected={rule.enabled}
        onValueChange={(enabled) =>
          onChange(rules.map((r, i) => (i === index ? { ...r, enabled } : r)))
        }
      />
      {!isMobile &&
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
            <Button isIconOnly aria-label="更多操作" size="sm" variant="light">
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
                onPress={() => onChange(rules.filter((_, i) => i !== index))}
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
    </>
  );

  // Plan 214 r2: controls of the one-line layer row (desktop + H5): the h-5
  // switch and one compact 「更多」 menu with 编辑, 置顶…置底 and 删除, so the
  // row keeps the 内置 row height and never wraps.
  const layerControls = (rule: EgressRule, index: number) => (
    <>
      <Switch
        aria-label={rule.enabled ? "停用规则" : "启用规则"}
        isSelected={rule.enabled}
        onValueChange={(enabled) =>
          onChange(rules.map((r, i) => (i === index ? { ...r, enabled } : r)))
        }
      />
      <Dropdown placement="bottom-end">
        <DropdownTrigger>
          <button
            aria-label="更多操作"
            className="inline-flex h-5 w-6 items-center justify-center rounded-md text-default-500 hover:bg-default-200/70 hover:text-default-700"
            title="更多"
            type="button"
          >
            <MoreHorizontal size={16} />
          </button>
        </DropdownTrigger>
        <DropdownMenu aria-label="规则操作">
          {[
            <DropdownItem
              key="edit"
              startContent={<Pencil size={16} />}
              onPress={() => setEditing({ index, rule })}
            >
              编辑
            </DropdownItem>,
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
              onPress={() => onChange(rules.filter((_, i) => i !== index))}
            >
              删除
            </DropdownItem>,
          ]}
        </DropdownMenu>
      </Dropdown>
    </>
  );

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-end gap-2">
        {addHint && (
          <span className="mr-auto text-xs text-default-500">{addHint}</span>
        )}
        {presets && canAdd && (
          <PresetPicker
            active={builtinActive}
            choices={choices}
            defaultChoice={defaultChoice}
            hideSource={hideSource}
            rules={rules}
            safety={safety}
            onChange={onChange}
          />
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
      {layout === "layer" ? (
        rules.length ? (
          <RuleRows>
            {rules.map((rule, index) => {
              const label = choiceLabel(rule.action, labels || choices);
              const off = rule.enabled ? inactiveReason?.(rule) : undefined;
              const state = !rule.enabled
                ? { on: false, label: "未生效：已停用，不会下发" }
                : off
                  ? { on: false, label: off }
                  : { on: true, label: "已启用" };

              return (
                <LayerRuleRow
                  key={rule.id}
                  // H5: the switch + 「更多」 leave no room for the chip at
                  // 390 px; the action is in 展开详情 (「命中后」).
                  action={
                    isMobile
                      ? undefined
                      : { label, tone: actionTone(rule.action) }
                  }
                  controls={layerControls(rule, index)}
                  dataKey={rule.id}
                  details={[
                    ...editableDetails(rule),
                    { label: "命中后", value: actionWords(rule.action, label) },
                  ]}
                  dim={!state.on}
                  lead={<NumberLead n={numberFrom + index} />}
                  name={rule.name || `规则 ${index + 1}`}
                  offNote={state.on ? undefined : state.label}
                  open={detailsOpen}
                  state={state}
                />
              );
            })}
          </RuleRows>
        ) : (
          <p className="rounded-lg bg-default-100/60 px-3 py-2 text-xs text-default-500 dark:bg-default-50/60">
            {emptyText}
          </p>
        )
      ) : (
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
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span
                      className="break-all font-medium"
                      title={rule.name || `规则 ${index + 1}`}
                    >
                      {rule.name || `规则 ${index + 1}`}
                    </span>
                    <Pill tone={actionTone(rule.action)}>
                      → {choiceLabel(rule.action, labels || choices)}
                    </Pill>
                  </div>
                  <div className="flex flex-wrap gap-1">
                    {ruleAttributeChips(rule).map((chip) => (
                      <Pill key={chip} tone="slate">
                        {chip}
                      </Pill>
                    ))}
                  </div>
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-1 self-end sm:self-auto">
                {controls(rule, index)}
              </div>
            </div>
          ))}
          {!rules.length && (
            <p className="p-4 text-center text-sm text-default-500">
              {emptyText}
            </p>
          )}
        </div>
      )}
      {editing && (
        <RuleEditor
          hideNodeScope
          askOption={askOption}
          hideSource={hideSource}
          landingNodes={[]}
          outbounds={choices}
          rule={editing.rule}
          savePreset={savePreset}
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
    </div>
  );
}
