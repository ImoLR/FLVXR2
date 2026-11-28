import type { NodePageGroups } from "@/api/node-groups";
import type { ProbeAdminGroup } from "@/api/probe-admin";

import { useCallback, useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";
import { Check, Lock, Plus, TriangleAlert, X } from "lucide-react";

import {
  LEIKWAN_GROUP_ID,
  LEIKWAN_TONE,
  LeiKwanGroupMark,
  type UnifiedGroup,
  buildUnifiedGroups,
  unifiedToSave,
  unifiedTone,
} from "./node-group-tabs";

import { saveNodePageGroups } from "@/api/node-groups";
import { ProbeGroupsEditor } from "@/pages/probe-groups-editor";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";

// Plan 324: 「管理分组」 on the 节点 page — the shared group editor (same groups
// as the 探针) + who is in each group, with the 利群入口 rules: rename / move /
// add / remove only; a machine 利群汇聚 uses can't leave; never deleted.

export const LEIKWAN_NOTE = "这是利群汇聚的入口机分组";
export const LEIKWAN_DELETE_HINT =
  "利群汇聚正在用这个分组当入口机，不能删除；可以改名、加减机器";

export interface GroupMemberNode {
  id: number;
  name: string;
  isRemote?: number;
  groupId?: number | null;
}

type GuardState =
  | { kind: "blocked"; name: string; uses: string[] }
  | {
      kind: "remove";
      name: string;
      notes: string[];
      onOk: () => void;
    }
  | {
      kind: "add";
      name: string;
      oldGroup: string;
      onOk: () => void;
    }
  | { kind: "error"; message: string }
  | null;

/**
 * The 利群入口 member rules, shared by 「管理分组」 and the node edit dialog:
 * a machine 利群汇聚 uses can't leave (message), leaving otherwise needs a
 * confirm, joining from another (hidden) old node group needs a confirm.
 */
export function useLeiKwanMemberGuard(
  data: NodePageGroups | null,
  oldGroupName: (groupId: number) => string,
) {
  const [state, setState] = useState<GuardState>(null);
  const lk = data?.leikwan || null;

  const requestRemove = useCallback(
    (node: GroupMemberNode, onOk: () => void) => {
      const uses = lk?.uses?.[String(node.id)] || [];

      if (uses.length > 0) {
        setState({ kind: "blocked", name: node.name, uses });

        return;
      }
      setState({
        kind: "remove",
        name: node.name,
        notes: lk?.notes?.[String(node.id)] || [],
        onOk,
      });
    },
    [lk],
  );

  const requestAdd = useCallback(
    (node: GroupMemberNode, onOk: () => void) => {
      const gid = Number(node.groupId || 0);

      if (gid > 0 && lk && gid !== lk.id) {
        setState({
          kind: "add",
          name: node.name,
          oldGroup: oldGroupName(gid) || `#${gid}`,
          onOk,
        });

        return;
      }
      onOk();
    },
    [lk, oldGroupName],
  );

  const showError = useCallback(
    (message: string) => setState({ kind: "error", message }),
    [],
  );

  const close = () => setState(null);
  const modal = (
    <Modal
      isOpen={!!state}
      placement="center"
      size="md"
      onOpenChange={(open) => !open && close()}
    >
      <ModalContent>
        {state?.kind === "blocked" && (
          <>
            <ModalHeader className="flex items-center gap-1.5 text-danger">
              <TriangleAlert aria-hidden="true" className="h-4 w-4" />
              不能移出利群入口
            </ModalHeader>
            <ModalBody>
              <p className="text-sm" data-guard-message="true">
                {state.name} 正在被利群汇聚用作 {state.uses.join("、")}
                ，请先到利群汇聚换主入口
                {state.uses.some((u) => u.includes("宽限"))
                  ? "（宽限中的旧入口等宽限结束后再移出）"
                  : ""}
                。
              </p>
              {state.uses.length > 1 && (
                <ul className="list-disc space-y-0.5 pl-5 text-xs text-default-600">
                  {state.uses.map((u) => (
                    <li key={u}>{u}</li>
                  ))}
                </ul>
              )}
            </ModalBody>
            <ModalFooter>
              <Button color="danger" variant="flat" onPress={close}>
                知道了
              </Button>
            </ModalFooter>
          </>
        )}
        {state?.kind === "remove" && (
          <>
            <ModalHeader>移出利群入口</ModalHeader>
            <ModalBody>
              <p className="text-sm">
                把 {state.name} 移出「{lk?.name || "利群入口"}」？
              </p>
              <p className="text-xs text-amber-700 dark:text-amber-300">
                移出后利群汇聚不能再选它当入口机。
              </p>
              {state.notes.length > 0 && (
                <ul className="list-disc space-y-0.5 pl-5 text-xs text-default-500">
                  {state.notes.map((n) => (
                    <li key={n}>{n}</li>
                  ))}
                </ul>
              )}
            </ModalBody>
            <ModalFooter>
              <Button color="danger" variant="flat" onPress={close}>
                取消
              </Button>
              <Button
                color="warning"
                variant="flat"
                onPress={() => {
                  state.onOk();
                  close();
                }}
              >
                移出
              </Button>
            </ModalFooter>
          </>
        )}
        {state?.kind === "add" && (
          <>
            <ModalHeader>加入利群入口</ModalHeader>
            <ModalBody>
              <p className="text-sm">
                {state.name} 现在在旧节点分组「{state.oldGroup}」里，加入「
                {lk?.name || "利群入口"}」后会离开那个旧分组。
              </p>
              <p className="text-xs text-default-500">
                旧节点分组已经不在节点页显示，只有利群入口还用它。
              </p>
            </ModalBody>
            <ModalFooter>
              <Button color="danger" variant="flat" onPress={close}>
                取消
              </Button>
              <Button
                color="success"
                variant="flat"
                onPress={() => {
                  state.onOk();
                  close();
                }}
              >
                加入
              </Button>
            </ModalFooter>
          </>
        )}
        {state?.kind === "error" && (
          <>
            <ModalHeader className="flex items-center gap-1.5 text-danger">
              <TriangleAlert aria-hidden="true" className="h-4 w-4" />
              没有保存
            </ModalHeader>
            <ModalBody>
              <p className="text-sm" data-guard-message="true">
                {state.message}
              </p>
            </ModalBody>
            <ModalFooter>
              <Button color="danger" variant="flat" onPress={close}>
                知道了
              </Button>
            </ModalFooter>
          </>
        )}
      </ModalContent>
    </Modal>
  );

  return { requestRemove, requestAdd, showError, modal };
}

const MEMBER =
  "inline-flex max-w-[12rem] items-center gap-0.5 truncate rounded-full py-0.5 pl-2 pr-1 text-[11px] font-medium";

function MemberEditor({
  group,
  nodes,
  open,
  inUse,
  onToggleOpen,
  onAdd,
  onRemove,
}: {
  group: UnifiedGroup;
  nodes: GroupMemberNode[];
  open: boolean;
  inUse: (id: number) => boolean;
  onToggleOpen: () => void;
  onAdd: (n: GroupMemberNode) => void;
  onRemove: (n: GroupMemberNode) => void;
}) {
  const members = nodes.filter((n) => group.nodes.includes(n.id));
  const others = nodes.filter((n) => !group.nodes.includes(n.id));
  const tone = unifiedTone(group.id);

  return (
    <div className="basis-full pl-7" data-group-members={group.name}>
      <div className="flex flex-wrap items-center gap-1">
        {members.map((n) => (
          <span key={n.id} className={`${MEMBER} ${tone}`}>
            <span className="truncate">{n.name}</span>
            {inUse(n.id) && (
              <span
                className="ml-0.5 inline-flex shrink-0 items-center gap-0.5 rounded-full bg-background/60 px-1 text-[9px] font-semibold"
                title="利群汇聚正在用它当入口机"
              >
                <Lock aria-hidden="true" className="h-2.5 w-2.5" />
                在用
              </span>
            )}
            <button
              aria-label={`把 ${n.name} 移出 ${group.name}`}
              className="ml-0.5 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full hover:bg-background/70"
              type="button"
              onClick={() => onRemove(n)}
            >
              <X aria-hidden="true" className="h-3 w-3" />
            </button>
          </span>
        ))}
        <button
          aria-expanded={open}
          className="inline-flex items-center gap-0.5 rounded-full bg-green-500/15 px-2 py-0.5 text-[11px] font-semibold text-green-700 hover:bg-green-500/25 dark:bg-green-400/20 dark:text-green-300"
          type="button"
          onClick={onToggleOpen}
        >
          <Plus aria-hidden="true" className="h-3 w-3" />
          {open ? "收起" : "加机器"}
        </button>
      </div>
      {open && (
        <div
          className="mt-1.5 flex flex-wrap gap-1 rounded-lg bg-default-100/60 p-1.5 dark:bg-default-50/40"
          data-group-add-panel="true"
        >
          {others.length === 0 ? (
            <span className="px-1 text-[11px] text-default-500">
              所有机器都已经在这个分组里
            </span>
          ) : (
            others.map((n) => (
              <button
                key={n.id}
                className={`${MEMBER} bg-slate-500/10 pr-2 text-default-700 hover:bg-slate-500/20 dark:bg-slate-400/15`}
                type="button"
                onClick={() => onAdd(n)}
              >
                <Plus aria-hidden="true" className="h-3 w-3 shrink-0" />
                <span className="truncate">{n.name}</span>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
}

export function NodeGroupsManager({
  isOpen,
  onOpenChange,
  data,
  nodes,
  oldGroupName,
  onSaved,
}: {
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
  data: NodePageGroups | null;
  nodes: GroupMemberNode[];
  oldGroupName: (groupId: number) => string;
  onSaved: (next: NodePageGroups) => void;
}) {
  const [draft, setDraft] = useState<UnifiedGroup[]>([]);
  const [openRow, setOpenRow] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const guard = useLeiKwanMemberGuard(data, oldGroupName);
  // Machines that may be in a group: not remote (same set as the probe card).
  const members = useMemo(() => nodes.filter((n) => n.isRemote !== 1), [nodes]);
  const savedLeiKwan = useMemo(
    () => new Set(data?.leikwan?.nodes || []),
    [data],
  );

  useEffect(() => {
    if (!isOpen) return;
    setDraft(buildUnifiedGroups(data));
    setOpenRow(null);
  }, [isOpen, data]);

  const setMembers = (id: number, next: number[]) =>
    setDraft((prev) =>
      prev.map((g) => (g.id === id ? { ...g, nodes: next } : g)),
    );

  const save = async () => {
    setSaving(true);
    try {
      const res = await saveNodePageGroups(
        unifiedToSave(draft, !!data?.leikwan),
      );

      if (res.code === 0 && res.data) {
        toast.success("分组已保存");
        onSaved(res.data);
        onOpenChange(false);
      } else {
        guard.showError(res.msg || "保存失败");
      }
    } catch {
      toast.error("保存失败，请重试");
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <Modal
        isOpen={isOpen}
        placement="center"
        scrollBehavior="inside"
        size="2xl"
        onOpenChange={onOpenChange}
      >
        <ModalContent>
          <ModalHeader className="flex flex-col gap-0.5">
            管理分组
            <span className="text-xs font-normal text-default-500">
              和探针页用同一套分组（在 设置 → 公开探针页 改也一样）
            </span>
          </ModalHeader>
          <ModalBody>
            <ProbeGroupsEditor
              countOf={(g) => g.nodes.length}
              description="一台机器可以在多个分组里；不在任何分组的机器在「其他」。点 × 移出，点「加机器」加入。"
              groups={draft as ProbeAdminGroup[]}
              locked={
                data?.leikwan
                  ? {
                      id: LEIKWAN_GROUP_ID,
                      badge: <LeiKwanGroupMark />,
                      note: LEIKWAN_NOTE,
                      deleteHint: LEIKWAN_DELETE_HINT,
                      tone: LEIKWAN_TONE,
                    }
                  : null
              }
              renderRowExtra={(g) => (
                <MemberEditor
                  group={g}
                  inUse={(id) =>
                    g.id === LEIKWAN_GROUP_ID &&
                    savedLeiKwan.has(id) &&
                    (data?.leikwan?.uses?.[String(id)]?.length || 0) > 0
                  }
                  nodes={members}
                  open={openRow === g.id}
                  onAdd={(n) => {
                    const add = () => setMembers(g.id, [...g.nodes, n.id]);

                    if (g.id === LEIKWAN_GROUP_ID && !savedLeiKwan.has(n.id))
                      guard.requestAdd(n, add);
                    else add();
                  }}
                  onRemove={(n) => {
                    const remove = () =>
                      setMembers(
                        g.id,
                        g.nodes.filter((id) => id !== n.id),
                      );

                    if (g.id === LEIKWAN_GROUP_ID && savedLeiKwan.has(n.id))
                      guard.requestRemove(n, remove);
                    else remove();
                  }}
                  onToggleOpen={() =>
                    setOpenRow((cur) => (cur === g.id ? null : g.id))
                  }
                />
              )}
              saveHint="点「保存」后生效。"
              title="分组"
              onGroups={(next) => setDraft(next as UnifiedGroup[])}
            />
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => onOpenChange(false)}
            >
              取消
            </Button>
            <Button color="primary" isLoading={saving} onPress={save}>
              保存
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      {guard.modal}
    </>
  );
}

/** Node edit dialog: the unified groups as toggle chips (multi-select). */
export function NodeEditGroupPicker({
  groups,
  selected,
  onToggle,
}: {
  groups: UnifiedGroup[];
  selected: number[];
  onToggle: (g: UnifiedGroup, on: boolean) => void;
}) {
  return (
    <div
      className="flex flex-col gap-1.5 rounded-xl border-2 border-default-200 px-3 py-2"
      data-node-edit-groups="true"
    >
      <span className="text-xs font-medium text-default-600">
        分组（可多选）
      </span>
      {groups.length === 0 ? (
        <span className="text-xs text-default-400">
          还没有分组：在节点页点「管理分组」新建（和探针页同一套）。
        </span>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {groups.map((g) => {
            const on = selected.includes(g.id);
            const lk = g.id === LEIKWAN_GROUP_ID;

            return (
              <button
                key={g.id}
                aria-pressed={on}
                className={`inline-flex max-w-[12rem] items-center gap-1 rounded-full px-2.5 py-1 text-xs font-semibold transition-colors ${on ? `${unifiedTone(g.id)} ring-2 ring-current/40` : "bg-slate-500/10 text-default-500 hover:bg-slate-500/20 dark:bg-slate-400/10"}`}
                data-edit-group={g.name}
                type="button"
                onClick={() => onToggle(g, !on)}
              >
                {on ? (
                  <Check aria-hidden="true" className="h-3 w-3 shrink-0" />
                ) : (
                  <Plus aria-hidden="true" className="h-3 w-3 shrink-0" />
                )}
                <span className="truncate">{g.name}</span>
                {lk && <Lock aria-label="利群" className="h-3 w-3 shrink-0" />}
              </button>
            );
          })}
        </div>
      )}
      <span className="text-[11px] text-default-400">
        和探针页同一套分组；不在任何分组的机器在「其他」。
        {groups.some((g) => g.id === LEIKWAN_GROUP_ID)
          ? "带 🔒 的是利群汇聚的入口机分组。"
          : ""}
      </span>
    </div>
  );
}
