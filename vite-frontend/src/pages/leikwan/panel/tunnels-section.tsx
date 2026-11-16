import type {
  LeiKwanGrant,
  LeiKwanLine,
  LeiKwanLineList,
} from "@/api/leikwan-line";

import { useCallback, useEffect, useState } from "react";
import { Plus } from "lucide-react";

import { getLeiKwanGrants, getLeiKwanLines } from "@/api/leikwan-line";
import {
  TreeGroupChevron,
  TreeGroupFooter,
  TreeGroupHeader,
  treeGroupHeaderClass,
  treeGroupWrapperClass,
} from "@/components/tree-group";
import { useH5Mode } from "@/hooks/useH5Mode";
import { Pill } from "@/pages/landing/pill";
import { LeiKwanGrantDialog } from "@/pages/leikwan/panel/grant-dialog";
import { LeiKwanLineDialog } from "@/pages/leikwan/panel/line-dialog";
import { LeiKwanLinesTable } from "@/pages/leikwan/panel/lines-table";
import { LineChip, OnOff } from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";

// Plan 266: the tunnel group 「利群汇聚」 on the 隧道 page (admin), added above
// the normal tunnel groups only once a 利群 account exists. Normal tunnels,
// their dialogs and grouping are untouched.

export function LeiKwanTunnelsSection() {
  const h5 = useH5Mode();
  const [data, setData] = useState<LeiKwanLineList | null>(null);
  const [collapsed, setCollapsed] = useState(true);
  const [edit, setEdit] = useState<LeiKwanLine | null>(null);
  const [open, setOpen] = useState(false);

  const load = useCallback(async () => {
    const res = await getLeiKwanLines();

    if (res.code === 0) setData(res.data);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // Nothing until the first 利群专线 exists: the page stays as it was.
  if (!data || !data.accountReady || data.lines.length === 0) return null;

  return (
    <div className="mb-4" data-leikwan-tunnels="">
      <div className={treeGroupWrapperClass(collapsed)}>
        <TreeGroupHeader
          className={treeGroupHeaderClass(collapsed)}
          collapsed={collapsed}
          onToggle={() => setCollapsed((c) => !c)}
        >
          <div className="flex min-w-0 items-center gap-2">
            <TreeGroupChevron collapsed={collapsed} />
            <span className="truncate text-sm font-semibold">
              {data.setting.groupName}
            </span>
            <LineChip />
            {!h5 && (
              <span className="text-xs text-default-500">
                入口在授权时指定 · 节点分组「
                {data.setting.entryGroupName || "利群入口"}」
              </span>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <span className="rounded bg-default-100 px-1.5 py-0.5 text-[10px] text-default-500">
              {data.lines.length} 条
            </span>
          </div>
        </TreeGroupHeader>
        {!collapsed && (
          <div className="space-y-2 p-3">
            <div className="flex justify-end">
              <Button
                color="success"
                size="sm"
                startContent={<Plus size={15} />}
                variant="flat"
                onPress={() => {
                  setEdit(null);
                  setOpen(true);
                }}
              >
                新建利群专线
              </Button>
            </div>
            <LeiKwanLinesTable
              data={data}
              h5={h5}
              onChanged={() => void load()}
              onEdit={(l) => {
                setEdit(l);
                setOpen(true);
              }}
            />
          </div>
        )}
        {!collapsed && (
          <TreeGroupFooter
            name={data.setting.groupName}
            onToggle={() => setCollapsed(true)}
          />
        )}
      </div>
      <LeiKwanLineDialog
        data={data}
        isOpen={open}
        line={edit}
        onClose={() => setOpen(false)}
        onSaved={() => void load()}
      />
    </div>
  );
}

/**
 * Plan 266: 「利群汇聚」 part of the user's 隧道权限 dialog (user page): the
 * grant with its 主入口 + 备线. Shown only when 利群专线 exist.
 */
export function LeiKwanUserGrantSection({
  user,
}: {
  user: { id: number; user: string } | null;
}) {
  const [data, setData] = useState<LeiKwanLineList | null>(null);
  const [grant, setGrant] = useState<LeiKwanGrant | null>(null);
  const [open, setOpen] = useState(false);

  const load = useCallback(async () => {
    if (!user) return;
    const [l, g] = await Promise.all([getLeiKwanLines(), getLeiKwanGrants()]);

    if (l.code === 0) setData(l.data);
    if (g.code === 0)
      setGrant((g.data.grants || []).find((x) => x.userId === user.id) || null);
  }, [user]);

  useEffect(() => {
    void load();
  }, [load]);

  if (!user || !data || data.lines.length === 0) return null;

  return (
    <div
      className="space-y-2 rounded-xl border border-teal-500/30 bg-teal-500/5 p-3"
      data-leikwan-grant=""
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="text-base font-semibold">利群汇聚</h3>
          <LineChip />
          <span className="text-xs text-default-500">
            {data.lines.map((l) => l.name.replace(/^利群·/, "")).join(" · ")}
          </span>
        </div>
        <Button
          color={grant ? "primary" : "success"}
          size="sm"
          variant="flat"
          onPress={() => setOpen(true)}
        >
          {grant ? "修改授权" : "分配隧道组"}
        </Button>
      </div>
      {grant ? (
        <div className="flex flex-wrap items-center gap-1.5 text-sm">
          <Pill tone="teal">主入口 {grant.mainEntryName}</Pill>
          <OnOff label="备线" on={grant.backupLine} />
          <Pill tone="purple">利群规则 {grant.ruleCount} 条</Pill>
        </div>
      ) : (
        <p className="text-xs text-default-500">
          还没有授权：分配时选主入口和备线开关。
        </p>
      )}
      <LeiKwanGrantDialog
        entries={data.entryNodes}
        grant={grant}
        isOpen={open}
        lineNames={data.lines.map((l) => l.name)}
        quota={data.quota}
        user={{ id: user.id, name: user.user }}
        onClose={() => setOpen(false)}
        onSaved={() => void load()}
      />
    </div>
  );
}
