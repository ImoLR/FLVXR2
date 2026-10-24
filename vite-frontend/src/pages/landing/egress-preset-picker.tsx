import { useState } from "react";
import toast from "react-hot-toast";
import { Settings2, Sparkles } from "lucide-react";

import { PresetManager } from "./egress-preset-manager";
import { useEgressPresets } from "./use-egress-presets";

import {
  BuiltinActive,
  EgressPreset,
  EgressRule,
  applyPreset,
  presetCoveredByBuiltin,
} from "@/api/landing-egress";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownItem,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownMenuSeparator,
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

// Plan 140: the 「添加预设」 menu, reading the preset library from the panel.
// Admins get a 「管理预设…」 footer. A preset whose every rule repeats an
// active built-in rule is marked 「内置已封锁」 and not inserted again.

export type PresetChoice = { id: number; name: string };

export function PresetPicker({
  rules,
  onChange,
  choices,
  defaultChoice,
  hideSource = false,
  active,
  safety = [],
}: {
  rules: EgressRule[];
  onChange: (rules: EgressRule[]) => void;
  // Outbounds offered when a preset asks for one (besides 直连).
  choices: PresetChoice[];
  defaultChoice?: number;
  hideSource?: boolean;
  // Built-in anti-abuse rules in force where the rules are added.
  active: BuiltinActive;
  safety?: string[];
}) {
  const { presets, admin, loaded } = useEgressPresets();
  const [askFor, setAskFor] = useState<EgressPreset | null>(null);
  const [askAction, setAskAction] = useState("direct");
  const [managing, setManaging] = useState(false);
  const available = presets.filter(
    (p) =>
      !hideSource ||
      !p.rules.some(
        (r) =>
          (r.sourceIps || []).length ||
          r.sourcePorts ||
          // Plan 149: 节点协议 is an admin-scope matcher (not in node rules).
          (r.inboundProtocols || []).length,
      ),
  );

  const insert = (preset: EgressPreset, action: string) => {
    const result = applyPreset(rules, preset, action);

    onChange(result.rules);
    if (result.added === 0) toast("规则已存在，未重复添加");
    else if (result.skipped > 0)
      toast.success(
        `已添加 ${result.added} 条，${result.skipped} 条已存在未重复添加`,
      );
    else toast.success(`已添加「${preset.name}」`);
  };

  const pick = (preset: EgressPreset) => {
    if (presetCoveredByBuiltin(preset, active, safety)) {
      toast("内置规则已经封锁，无需重复添加");

      return;
    }
    if (preset.missingOutbound) {
      toast.error("该预设使用的出站已删除，请在「管理预设」里修改");

      return;
    }
    if (!preset.askOutbound) {
      insert(preset, "direct");

      return;
    }
    const preferred =
      defaultChoice && choices.some((c) => c.id === defaultChoice)
        ? defaultChoice
        : choices[0]?.id;

    setAskAction(preferred ? `outbound:${preferred}` : "direct");
    setAskFor(preset);
  };

  return (
    <>
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
          {[
            ...(!available.length
              ? [
                  <DropdownMenuLabel
                    key="empty"
                    className="py-2 text-sm font-normal text-default-500"
                  >
                    {loaded ? "还没有可用的预设" : "正在加载预设…"}
                  </DropdownMenuLabel>,
                ]
              : []),
            ...available.map((preset) => {
              const covered = presetCoveredByBuiltin(preset, active, safety);

              return (
                <DropdownItem key={preset.id} onPress={() => pick(preset)}>
                  <div className="flex flex-col py-0.5">
                    <span>{preset.name}</span>
                    {preset.description && (
                      <span className="max-w-72 text-xs text-default-500">
                        {preset.description}
                      </span>
                    )}
                    {covered && (
                      <span className="max-w-72 text-xs text-success">
                        内置已封锁，无需添加
                      </span>
                    )}
                    {preset.missingOutbound && (
                      <span className="max-w-72 text-xs text-danger">
                        使用的出站已删除
                      </span>
                    )}
                  </div>
                </DropdownItem>
              );
            }),
            ...(admin
              ? [
                  <DropdownMenuSeparator key="sep" />,
                  <DropdownItem
                    key="manage"
                    className="text-secondary"
                    startContent={<Settings2 size={16} />}
                    onPress={() => setManaging(true)}
                  >
                    管理预设…
                  </DropdownItem>,
                ]
              : []),
          ]}
        </DropdownMenu>
      </Dropdown>
      <Modal isOpen={Boolean(askFor)} onClose={() => setAskFor(null)}>
        <ModalContent className="flvx-landing">
          <ModalHeader>添加预设 · {askFor?.name}</ModalHeader>
          <ModalBody className="space-y-3">
            {askFor?.description && (
              <p className="text-sm text-default-500">{askFor.description}</p>
            )}
            <Select
              label="走哪个出站"
              selectedKeys={[askAction]}
              variant="bordered"
              onSelectionChange={(keys) =>
                setAskAction(String(Array.from(keys)[0]))
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
              onPress={() => setAskFor(null)}
            >
              取消
            </Button>
            <Button
              color="primary"
              onPress={() => {
                if (askFor) insert(askFor, askAction || "direct");
                setAskFor(null);
              }}
            >
              添加
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      {managing && (
        <PresetManager choices={choices} onClose={() => setManaging(false)} />
      )}
    </>
  );
}
