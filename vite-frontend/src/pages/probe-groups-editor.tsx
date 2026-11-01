import type { ProbeAdminGroup, ProbeGroupMode } from "@/api/probe-admin";

import { useState } from "react";
import toast from "react-hot-toast";

import {
  DropdownMenuCheckboxItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
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

// Plan 189 设置 → 公开探针页 → 探针分组: the group list (add / rename / delete
// with confirm / move up-down), the 默认分组方式 switch and the per-machine
// multi-select. Changes stay local until 「保存探针设置」.

export const PROBE_GROUPS_MAX = 30;
export const PROBE_GROUP_NAME_MAX = 20;

// Tinted pills (rules-page palette, no white chips); colour follows the group
// id, so it stays put when the order changes.
const TONES = [
  "bg-teal-500/15 text-teal-700 dark:bg-teal-400/20 dark:text-teal-200",
  "bg-violet-500/15 text-violet-700 dark:bg-violet-400/20 dark:text-violet-200",
  "bg-amber-500/15 text-amber-700 dark:bg-amber-400/20 dark:text-amber-200",
  "bg-sky-500/15 text-sky-700 dark:bg-sky-400/20 dark:text-sky-200",
  "bg-rose-500/15 text-rose-700 dark:bg-rose-400/20 dark:text-rose-200",
  "bg-emerald-500/15 text-emerald-700 dark:bg-emerald-400/20 dark:text-emerald-200",
  "bg-fuchsia-500/15 text-fuchsia-700 dark:bg-fuchsia-400/20 dark:text-fuchsia-200",
  "bg-lime-500/20 text-lime-800 dark:bg-lime-400/20 dark:text-lime-200",
];

export const groupTone = (id: number) =>
  TONES[(((Math.abs(id) - 1) % TONES.length) + TONES.length) % TONES.length];

const CHIP =
  "inline-flex max-w-[11rem] items-center truncate rounded-full px-2 py-0.5 text-[11px] font-semibold";
const SMALL =
  "rounded-full px-2.5 py-1 text-xs font-semibold transition-colors disabled:pointer-events-none disabled:opacity-40";

/** Trimmed name, or an error message (same rules as the panel). */
function checkName(
  raw: string,
  groups: ProbeAdminGroup[],
  selfId?: number,
): string | { error: string } {
  const name = raw.trim();

  if (!name) return { error: "分组名不能为空" };
  if ([...name].length > PROBE_GROUP_NAME_MAX)
    return { error: "分组名最多 20 个字" };
  const lower = name.toLowerCase();

  if (groups.some((g) => g.id !== selfId && g.name.toLowerCase() === lower))
    return { error: `分组名「${name}」已经有了` };

  return name;
}

export function ProbeGroupsEditor({
  groups,
  mode,
  shownIds,
  onGroups,
  onMode,
}: {
  groups: ProbeAdminGroup[];
  mode: ProbeGroupMode;
  /** Machines shown on the probe (counts exclude hidden ones). */
  shownIds: Set<number>;
  onGroups: (next: ProbeAdminGroup[]) => void;
  onMode: (mode: ProbeGroupMode) => void;
}) {
  const [newName, setNewName] = useState("");
  const [editing, setEditing] = useState<{ id: number; name: string } | null>(
    null,
  );
  const [confirmDelete, setConfirmDelete] = useState<ProbeAdminGroup | null>(
    null,
  );

  const add = () => {
    if (groups.length >= PROBE_GROUPS_MAX) {
      toast.error("探针分组最多 30 个");

      return;
    }
    const name = checkName(newName, groups);

    if (typeof name !== "string") {
      toast.error(name.error);

      return;
    }
    // Temporary ids are ≤ 0; the panel assigns real ones on save.
    const tmp = Math.min(0, ...groups.map((g) => g.id)) - 1;

    onGroups([...groups, { id: tmp, name, nodes: [] }]);
    setNewName("");
  };

  const rename = () => {
    if (!editing) return;
    const name = checkName(editing.name, groups, editing.id);

    if (typeof name !== "string") {
      toast.error(name.error);

      return;
    }
    onGroups(groups.map((g) => (g.id === editing.id ? { ...g, name } : g)));
    setEditing(null);
  };

  const move = (i: number, d: -1 | 1) => {
    const j = i + d;

    if (j < 0 || j >= groups.length) return;
    const next = [...groups];

    [next[i], next[j]] = [next[j], next[i]];
    onGroups(next);
  };

  return (
    <div>
      <div className="mb-2 flex items-start justify-between gap-3 text-sm">
        <div className="flex flex-col">
          <span className="font-semibold text-gray-700 dark:text-gray-300">
            探针分组
          </span>
          <span className="text-xs text-gray-400 dark:text-gray-500">
            除了按地区，还可以自己建分组（例如「IXP」「三网优化」），一台服务器可以同时在多个分组里。在下面的服务器列表里给每台选分组。
          </span>
        </div>
        <span className="shrink-0 text-xs text-gray-400">
          {groups.length} / {PROBE_GROUPS_MAX}
        </span>
      </div>
      <div className="rounded-lg border border-divider">
        {groups.map((g, i) => {
          const count = g.nodes.filter((id) => shownIds.has(id)).length;
          const isEditing = editing?.id === g.id;

          return (
            <div
              key={g.id}
              className="flex flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-divider px-3 py-2"
              data-probe-group={g.name}
            >
              <span className="w-5 shrink-0 text-center text-xs tabular-nums text-default-400">
                {i + 1}
              </span>
              {isEditing ? (
                <input
                  // eslint-disable-next-line jsx-a11y/no-autofocus
                  autoFocus
                  aria-label="新的分组名"
                  className="h-7 w-40 rounded-md border border-divider bg-transparent px-2 text-sm outline-none focus:border-teal-500"
                  maxLength={PROBE_GROUP_NAME_MAX}
                  value={editing.name}
                  onChange={(e) =>
                    setEditing({ id: g.id, name: e.target.value })
                  }
                  onKeyDown={(e) => {
                    if (e.key === "Enter") rename();
                    if (e.key === "Escape") setEditing(null);
                  }}
                />
              ) : (
                <span className={`${CHIP} ${groupTone(g.id)}`}>{g.name}</span>
              )}
              <span className="text-xs text-default-500">
                {count === 0 ? "还没有服务器" : `${count} 台`}
              </span>
              <div className="ml-auto flex items-center gap-1.5">
                {isEditing ? (
                  <>
                    <button
                      className={`${SMALL} bg-green-500/15 text-green-700 hover:bg-green-500/25 dark:bg-green-400/20 dark:text-green-300`}
                      type="button"
                      onClick={rename}
                    >
                      确定
                    </button>
                    <button
                      className={`${SMALL} bg-red-500/15 text-red-700 hover:bg-red-500/25 dark:bg-red-400/20 dark:text-red-300`}
                      type="button"
                      onClick={() => setEditing(null)}
                    >
                      取消
                    </button>
                  </>
                ) : (
                  <>
                    <button
                      aria-label={`上移 ${g.name}`}
                      className={`${SMALL} bg-slate-500/10 text-default-600 hover:bg-slate-500/20 dark:bg-slate-400/15`}
                      disabled={i === 0}
                      type="button"
                      onClick={() => move(i, -1)}
                    >
                      ↑
                    </button>
                    <button
                      aria-label={`下移 ${g.name}`}
                      className={`${SMALL} bg-slate-500/10 text-default-600 hover:bg-slate-500/20 dark:bg-slate-400/15`}
                      disabled={i === groups.length - 1}
                      type="button"
                      onClick={() => move(i, 1)}
                    >
                      ↓
                    </button>
                    <button
                      className={`${SMALL} bg-blue-500/15 text-blue-700 hover:bg-blue-500/25 dark:bg-blue-400/20 dark:text-blue-300`}
                      type="button"
                      onClick={() => setEditing({ id: g.id, name: g.name })}
                    >
                      改名
                    </button>
                    <button
                      className={`${SMALL} bg-red-500/15 text-red-700 hover:bg-red-500/25 dark:bg-red-400/20 dark:text-red-300`}
                      type="button"
                      onClick={() => setConfirmDelete(g)}
                    >
                      删除
                    </button>
                  </>
                )}
              </div>
            </div>
          );
        })}
        <div className="flex items-center gap-2 px-3 py-2">
          <input
            aria-label="新分组名称"
            className="h-8 w-0 min-w-0 flex-1 rounded-md border border-divider bg-transparent px-2 text-sm outline-none placeholder:text-default-400 focus:border-teal-500 sm:max-w-xs"
            disabled={groups.length >= PROBE_GROUPS_MAX}
            maxLength={PROBE_GROUP_NAME_MAX}
            placeholder={
              groups.length >= PROBE_GROUPS_MAX
                ? "已到 30 个上限"
                : "新分组名称，例如 IXP"
            }
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") add();
            }}
          />
          <Button
            color="success"
            isDisabled={!newName.trim() || groups.length >= PROBE_GROUPS_MAX}
            size="sm"
            variant="flat"
            onPress={add}
          >
            添加分组
          </Button>
        </div>
      </div>
      <div className="mt-3 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-col">
          <span className="text-sm font-semibold text-gray-700 dark:text-gray-300">
            默认分组方式
          </span>
          <span className="text-xs text-gray-400 dark:text-gray-500">
            访客第一次打开探针页时的查看方式，访客可以自己切换。没有分组时总是按地区。
          </span>
        </div>
        <div
          aria-label="默认分组方式"
          className="flex shrink-0 items-center gap-1 rounded-full bg-slate-500/10 p-0.5 dark:bg-slate-400/10"
          role="group"
        >
          {(
            [
              ["region", "按地区"],
              ["group", "按自定义分组"],
            ] as const
          ).map(([v, t]) => (
            <button
              key={v}
              aria-pressed={mode === v}
              className={`rounded-full px-3 py-1 text-xs font-semibold transition-colors ${mode === v ? "bg-teal-500/20 text-teal-700 dark:bg-teal-400/20 dark:text-teal-200" : "text-default-500 hover:text-foreground"}`}
              type="button"
              onClick={() => onMode(v)}
            >
              {t}
            </button>
          ))}
        </div>
      </div>

      <Modal
        isOpen={!!confirmDelete}
        placement="center"
        onOpenChange={(open) => !open && setConfirmDelete(null)}
      >
        <ModalContent>
          <ModalHeader>
            <span className="text-danger">删除分组</span>
          </ModalHeader>
          <ModalBody>
            <p className="text-sm">确定删除分组「{confirmDelete?.name}」吗？</p>
            <p className="text-xs text-default-500">
              其中的服务器只是移出这个分组，不会被隐藏或删除。点「保存探针设置」后生效。
            </p>
          </ModalBody>
          <ModalFooter>
            <Button variant="flat" onPress={() => setConfirmDelete(null)}>
              取消
            </Button>
            <Button
              color="danger"
              onPress={() => {
                if (confirmDelete)
                  onGroups(groups.filter((g) => g.id !== confirmDelete.id));
                if (editing?.id === confirmDelete?.id) setEditing(null);
                setConfirmDelete(null);
              }}
            >
              确认删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  );
}

/** The 「分组」 cell of one machine: its chips + a multi-select menu. */
export function ProbeGroupPicker({
  nodeId,
  nodeName,
  groups,
  dim,
  onToggle,
}: {
  nodeId: number;
  nodeName: string;
  groups: ProbeAdminGroup[];
  dim: boolean;
  onToggle: (groupId: number, nodeId: number, on: boolean) => void;
}) {
  const mine = groups.filter((g) => g.nodes.includes(nodeId));

  return (
    <div
      className={`flex min-w-0 flex-wrap items-center gap-1 ${dim ? "opacity-50" : ""}`}
    >
      <Dropdown placement="bottom-start">
        <DropdownTrigger>
          <button
            aria-label={`${nodeName} 的分组`}
            className="shrink-0 rounded-full bg-blue-500/15 px-2 py-0.5 text-[11px] font-semibold text-blue-700 hover:bg-blue-500/25 dark:bg-blue-400/20 dark:text-blue-300"
            type="button"
          >
            分组 ▾
          </button>
        </DropdownTrigger>
        <DropdownMenu aria-label="选择分组">
          <DropdownMenuLabel className="text-xs text-default-500">
            选择分组（可多选）
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          {groups.map((g) => {
            const on = g.nodes.includes(nodeId);

            return (
              <DropdownMenuCheckboxItem
                key={g.id}
                checked={on}
                onCheckedChange={(v) => onToggle(g.id, nodeId, v === true)}
                onSelect={(e) => e.preventDefault()}
              >
                <span className={`${CHIP} ${groupTone(g.id)}`}>{g.name}</span>
              </DropdownMenuCheckboxItem>
            );
          })}
        </DropdownMenu>
      </Dropdown>
      {mine.map((g) => (
        <span key={g.id} className={`${CHIP} ${groupTone(g.id)}`}>
          {g.name}
        </span>
      ))}
      {mine.length === 0 && (
        <span className="text-[11px] text-default-400">未分组</span>
      )}
    </div>
  );
}
