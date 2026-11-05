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
  const [deleting, setDeleting] = useState<string | null>(null);

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

  // Plan 214 r3: 我的规则 actions live in the layer's 「展开详情」, under each
  // rule's matchers: 启用 switch + flat tinted pills (编辑, 上移…置底, 删除).
  // The moves that can't happen at the ends are hidden, like the r2 menu.
  // 删除 asks once inline (still only applied by the dialog's 「保存」).
  const pill = "h-7 rounded-full px-2.5 gap-1";
  const layerActions = (rule: EgressRule, index: number) => (
    <div
      className="mt-2 flex flex-wrap items-center gap-1.5"
      data-rule-actions={rule.id}
    >
      <span className="mr-1 inline-flex items-center gap-1.5">
        <Switch
          aria-label={rule.enabled ? "停用规则" : "启用规则"}
          id={`rule-enable-${rule.id}`}
          isSelected={rule.enabled}
          onValueChange={(enabled) =>
            onChange(rules.map((r, i) => (i === index ? { ...r, enabled } : r)))
          }
        />
        <label
          className="cursor-pointer text-xs font-semibold text-default-600"
          htmlFor={`rule-enable-${rule.id}`}
        >
          启用
        </label>
      </span>
      <Button
        className={pill}
        color="primary"
        size="sm"
        startContent={<Pencil size={14} />}
        variant="flat"
        onPress={() => setEditing({ index, rule })}
      >
        编辑
      </Button>
      {(
        [
          ["up", "上移", index > 0],
          ["down", "下移", index < rules.length - 1],
          ["top", "置顶", index > 0],
          ["bottom", "置底", index < rules.length - 1],
        ] as const
      )
        .filter(([, , possible]) => possible)
        .map(([move, label]) => (
          // Text only, so the 7 actions fit 2 lines on a 390 px phone.
          <Button
            key={move}
            className={pill}
            size="sm"
            variant="flat"
            onPress={() => onChange(moveRule(rules, index, move))}
          >
            {label}
          </Button>
        ))}
      {deleting === rule.id ? (
        <>
          <Button
            className={pill}
            color="danger"
            size="sm"
            startContent={<Trash2 size={14} />}
            onPress={() => {
              setDeleting(null);
              onChange(rules.filter((_, i) => i !== index));
            }}
          >
            确认删除
          </Button>
          <Button
            className={pill}
            color="danger"
            size="sm"
            variant="flat"
            onPress={() => setDeleting(null)}
          >
            取消
          </Button>
        </>
      ) : (
        <Button
          className={pill}
          color="danger"
          size="sm"
          startContent={<Trash2 size={14} />}
          variant="flat"
          onPress={() => setDeleting(rule.id)}
        >
          删除
        </Button>
      )}
    </div>
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
                  // r3: the same row as 全局·强制 (chip on H5 too); the
                  // switch + actions only under 展开详情.
                  action={{ label, tone: actionTone(rule.action) }}
                  actions={layerActions(rule, index)}
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
