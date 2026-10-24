import { useState } from "react";
import toast from "react-hot-toast";
import {
  ArrowDown,
  ArrowUp,
  Pencil,
  Plus,
  RotateCcw,
  Trash2,
} from "lucide-react";

import { EgressRuleList } from "./egress-rule-list";
import {
  invalidateEgressPresets,
  setEgressPresets,
  useEgressPresets,
} from "./use-egress-presets";

import {
  EgressPreset,
  EgressRule,
  deleteEgressPreset,
  newRuleId,
  presetAskAction,
  reorderEgressPresets,
  restoreEgressPresets,
  ruleAttributeChips,
  saveEgressPreset,
  toPresetRule,
} from "@/api/landing-egress";
import { useMobileBreakpoint } from "@/hooks/useMobileBreakpoint";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Input, Textarea } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Plan 140: admin 「管理预设」 — list with edit / delete / reorder, 新增预设 and
// 恢复默认预设 (re-adds deleted built-in presets, never duplicates).

type Choice = { id: number; name: string };

const withIds = (p: EgressPreset): EgressRule[] =>
  p.rules.map((r) => ({ ...r, id: newRuleId() }) as EgressRule);

export function PresetEditor({
  preset,
  choices,
  onClose,
  onSaved,
}: {
  preset: Partial<EgressPreset>;
  choices: Choice[];
  onClose: () => void;
  onSaved: (p: EgressPreset) => void;
}) {
  const [name, setName] = useState(preset.name || "");
  const [description, setDescription] = useState(preset.description || "");
  const [askOutbound, setAskOutbound] = useState(Boolean(preset.askOutbound));
  const [insertTop, setInsertTop] = useState(Boolean(preset.insertTop));
  const [rules, setRules] = useState<EgressRule[]>(
    preset.rules ? withIds(preset as EgressPreset) : [],
  );
  const [saving, setSaving] = useState(false);
  const asks = rules.filter((r) => r.action === presetAskAction).length;

  const save = async () => {
    setSaving(true);
    const res = await saveEgressPreset({
      id: preset.id,
      name,
      description,
      askOutbound,
      insertTop,
      rules: rules.map(toPresetRule),
    });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success(preset.id ? "预设已保存" : "预设已添加");
    onSaved(res.data);
  };

  return (
    <Modal isOpen scrollBehavior="inside" size="xl" onClose={onClose}>
      <ModalContent className="flvx-landing">
        <ModalHeader>
          {preset.id ? `编辑预设 · ${preset.name}` : "新增预设"}
        </ModalHeader>
        <ModalBody className="space-y-4">
          <Input
            label="预设名称"
            placeholder="例如：AI 解锁"
            value={name}
            variant="bordered"
            onChange={(event) => setName(event.target.value)}
          />
          <Textarea
            label="说明"
            minRows={2}
            placeholder="显示在「添加预设」菜单里"
            value={description}
            variant="bordered"
            onChange={(event) => setDescription(event.target.value)}
          />
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="flex items-start justify-between gap-3 rounded-lg border border-divider p-3">
              <div className="min-w-0">
                <p className="text-sm font-medium">插入时选择出站</p>
                <p className="text-xs text-default-500">
                  出站为「插入时选择」的规则，添加预设时再选走哪个出站
                </p>
              </div>
              <Switch
                aria-label="插入时选择出站"
                isSelected={askOutbound}
                onValueChange={setAskOutbound}
              />
            </div>
            <div className="flex items-start justify-between gap-3 rounded-lg border border-divider p-3">
              <div className="min-w-0">
                <p className="text-sm font-medium">插在最前</p>
                <p className="text-xs text-default-500">
                  添加时放在规则列表最前面，否则追加到末尾
                </p>
              </div>
              <Switch
                aria-label="插在最前"
                isSelected={insertTop}
                onValueChange={setInsertTop}
              />
            </div>
          </div>
          {askOutbound && asks === 0 && (
            <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10">
              请至少把一条规则的出站设为「插入时选择」
            </p>
          )}
          {!askOutbound && asks > 0 && (
            <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10">
              有规则的出站是「插入时选择」，请打开「插入时选择出站」
            </p>
          )}
          <section className="space-y-2">
            <h3 className="font-medium">规则</h3>
            <EgressRuleList
              askOption={askOutbound}
              choices={choices}
              emptyText="还没有规则"
              rules={rules}
              onChange={setRules}
            />
          </section>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="primary"
            isDisabled={!name.trim() || !rules.length}
            isLoading={saving}
            onPress={() => void save()}
          >
            保存预设
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}

export function PresetManager({
  choices,
  onClose,
}: {
  choices: Choice[];
  onClose: () => void;
}) {
  const { presets, loaded } = useEgressPresets();
  const isMobile = useMobileBreakpoint();
  const [editing, setEditing] = useState<Partial<EgressPreset> | null>(null);
  const [deleting, setDeleting] = useState<EgressPreset | null>(null);
  const [busy, setBusy] = useState("");

  const move = async (index: number, delta: number) => {
    const ids = presets.map((p) => p.id);
    const [id] = ids.splice(index, 1);

    ids.splice(index + delta, 0, id);
    setBusy(`move-${id}`);
    const res = await reorderEgressPresets(ids);

    setBusy("");
    if (res.code !== 0) {
      toast.error(res.msg || "调整顺序失败");
      void invalidateEgressPresets();

      return;
    }
    setEgressPresets(res.data.presets || []);
  };

  const restore = async () => {
    setBusy("restore");
    const res = await restoreEgressPresets();

    setBusy("");
    if (res.code !== 0) {
      toast.error(res.msg || "恢复失败");

      return;
    }
    setEgressPresets(res.data.presets || []);
    toast.success(
      res.data.added
        ? `已恢复 ${res.data.added} 个默认预设`
        : "默认预设都在，无需恢复",
    );
  };

  const remove = async () => {
    if (!deleting) return;
    setBusy("delete");
    const res = await deleteEgressPreset(deleting.id);

    setBusy("");
    if (res.code !== 0) {
      toast.error(res.msg || "删除失败");

      return;
    }
    toast.success(`已删除「${deleting.name}」`);
    setDeleting(null);
    void invalidateEgressPresets();
  };

  return (
    <>
      <Modal isOpen scrollBehavior="inside" size="xl" onClose={onClose}>
        <ModalContent className="flvx-landing">
          <ModalHeader>管理预设</ModalHeader>
          <ModalBody className="space-y-3">
            <p className="text-xs text-default-500">
              所有「添加预设」菜单都用这份列表；用户只看到不含来源 IP /
              来源端口、且出站已开放给用户的预设。删除或修改预设不影响已经添加的规则。
            </p>
            <div className="divide-y divide-divider rounded-lg border border-divider">
              {presets.map((p, index) => (
                <div
                  key={p.id}
                  className="flex flex-col gap-2 p-3 sm:flex-row sm:items-start sm:justify-between"
                >
                  <div className="flex min-w-0 flex-1 items-start gap-3">
                    <span className="mt-0.5 w-6 shrink-0 text-right text-sm text-default-400">
                      {index + 1}
                    </span>
                    <div className="min-w-0 space-y-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="break-all font-medium">{p.name}</span>
                        <Chip
                          className="text-default-600"
                          size="sm"
                          variant="bordered"
                        >
                          {p.rules.length} 条规则
                        </Chip>
                        {p.askOutbound && (
                          <Chip color="secondary" size="sm" variant="flat">
                            插入时选出站
                          </Chip>
                        )}
                        {p.insertTop && (
                          <Chip
                            className="text-default-600"
                            size="sm"
                            variant="bordered"
                          >
                            插在最前
                          </Chip>
                        )}
                        {p.seedKey ? (
                          <Chip
                            className="text-default-600"
                            size="sm"
                            variant="bordered"
                          >
                            默认
                          </Chip>
                        ) : (
                          <Chip color="success" size="sm" variant="flat">
                            自定义
                          </Chip>
                        )}
                        {p.missingOutbound && (
                          <Chip color="danger" size="sm" variant="flat">
                            出站已删除
                          </Chip>
                        )}
                      </div>
                      {p.description && (
                        <p className="text-xs text-default-500">
                          {p.description}
                        </p>
                      )}
                      <div className="flex flex-wrap gap-1">
                        {p.rules.slice(0, 3).flatMap((r, i) =>
                          ruleAttributeChips({ ...r, id: `${i}` }).map(
                            (chip) => (
                              <Chip
                                key={`${i}-${chip}`}
                                className="text-default-500"
                                size="sm"
                                variant="bordered"
                              >
                                {chip}
                              </Chip>
                            ),
                          ),
                        )}
                      </div>
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-1 self-end sm:self-auto">
                    <Button
                      isIconOnly
                      aria-label="上移预设"
                      isDisabled={index === 0 || Boolean(busy)}
                      size="sm"
                      title="上移"
                      variant="light"
                      onPress={() => void move(index, -1)}
                    >
                      <ArrowUp size={16} />
                    </Button>
                    <Button
                      isIconOnly
                      aria-label="下移预设"
                      isDisabled={index === presets.length - 1 || Boolean(busy)}
                      size="sm"
                      title="下移"
                      variant="light"
                      onPress={() => void move(index, 1)}
                    >
                      <ArrowDown size={16} />
                    </Button>
                    <Button
                      color="primary"
                      size="sm"
                      startContent={isMobile ? undefined : <Pencil size={14} />}
                      variant="flat"
                      onPress={() => setEditing(p)}
                    >
                      编辑
                    </Button>
                    <Button
                      color="danger"
                      size="sm"
                      startContent={isMobile ? undefined : <Trash2 size={14} />}
                      variant="flat"
                      onPress={() => setDeleting(p)}
                    >
                      删除
                    </Button>
                  </div>
                </div>
              ))}
              {!presets.length && (
                <p className="p-4 text-center text-sm text-default-500">
                  {loaded ? "还没有预设" : "正在加载…"}
                </p>
              )}
            </div>
          </ModalBody>
          <ModalFooter className="flex-wrap">
            <Button
              className="sm:mr-auto"
              color="warning"
              isLoading={busy === "restore"}
              startContent={<RotateCcw size={16} />}
              variant="flat"
              onPress={() => void restore()}
            >
              恢复默认预设
            </Button>
            <Button color="danger" variant="flat" onPress={onClose}>
              关闭
            </Button>
            <Button
              color="success"
              startContent={<Plus size={16} />}
              onPress={() =>
                setEditing({
                  name: "",
                  description: "",
                  rules: [],
                  askOutbound: false,
                  insertTop: false,
                })
              }
            >
              新增预设
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      {editing && (
        <PresetEditor
          choices={choices}
          preset={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void invalidateEgressPresets();
          }}
        />
      )}
      <Modal isOpen={Boolean(deleting)} onClose={() => setDeleting(null)}>
        <ModalContent className="flvx-landing">
          <ModalHeader>删除预设</ModalHeader>
          <ModalBody>
            <p>
              删除预设「{deleting?.name}」？已经添加到规则列表里的规则不受影响。
              {deleting?.seedKey ? "以后可用「恢复默认预设」找回。" : ""}
            </p>
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setDeleting(null)}
            >
              取消
            </Button>
            <Button
              color="danger"
              isLoading={busy === "delete"}
              onPress={() => void remove()}
            >
              删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  );
}
