import type {
  LeiKwanEntryCaps,
  LeiKwanEntryNode,
  LeiKwanGrant,
  LeiKwanGrantPreview,
  LeiKwanQuota,
} from "@/api/leikwan-line";

import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import { previewLeiKwanGrant, saveLeiKwanGrant } from "@/api/leikwan-line";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import { Pill } from "@/pages/landing/pill";
import {
  CapPills,
  ClientPill,
  OfflinePill,
} from "@/pages/leikwan/panel/entry-state";
import { LabeledSwitch, QuotaPill } from "@/pages/leikwan/panel/shared";

// Plan 266: grant the tunnel group 「利群汇聚」 to one user. The 主入口 is picked
// here once for every 利群 line; changing it moves all of the user's 利群 rule
// addresses (the LeiKwan ports are the same on every entry machine).
// 备用入口 is parked (user 11-16), so there is no field for it.
// Plan 268 (user 11-17): the grant is only 主入口 + 备线. 利群 rules count
// toward the user's 总规则数 and follow the user's 到期; traffic is not shown
// until step 2 measures it.
// Plan 295: 换主入口 opens this dialog too (grant row button / 主入口 chip /
// host drawer 「设为主入口…」). Each entry option shows 在线 / 离线, the
// LeiKwan client and what it can run; offline entries cannot be picked; a
// different entry shows the backend preview of what each forward keeps / loses.

export type LeiKwanGrantDialogMode = "grant" | "entry";

function EntryOptionDetail({
  caps,
  current,
}: {
  caps?: LeiKwanEntryCaps;
  current: boolean;
}) {
  if (!caps && !current) return null;

  return (
    <span className="mt-1 flex flex-wrap gap-1">
      {current && <Pill tone="teal">当前主入口</Pill>}
      <ClientPill caps={caps} />
      <CapPills caps={caps} />
    </span>
  );
}

function PreviewBlock({
  preview,
  loading,
  ruleCount,
}: {
  preview: LeiKwanGrantPreview | null;
  loading: boolean;
  ruleCount: number;
}) {
  if (loading)
    return (
      <div className="flex items-center gap-2 rounded-xl border border-divider px-3 py-2.5 text-xs text-default-500">
        <Spinner size="sm" /> 正在按新入口核对这个用户的利群转发…
      </div>
    );
  if (!preview) return null;
  const to = preview.to.name;
  const changed = preview.rules.filter((r) => r.changes.length > 0);

  return (
    <div
      className="space-y-2 rounded-xl border border-divider p-3"
      data-leikwan-switch-preview=""
    >
      <div className="flex flex-wrap items-center gap-1.5 text-sm font-semibold">
        换到 {to} 后
        {preview.lostRules > 0 ? (
          <Pill tone="amber">{preview.lostRules} 条转发会少功能</Pill>
        ) : (
          <Pill tone="green">功能不变</Pill>
        )}
        {preview.gainRules > 0 && (
          <Pill tone="teal">{preview.gainRules} 条多了功能</Pill>
        )}
      </div>
      {preview.notes.map((n) => (
        <p
          key={n}
          className="rounded-lg bg-amber-500/10 px-2.5 py-1.5 text-xs text-amber-700 dark:bg-amber-400/10 dark:text-amber-300"
        >
          {n}
        </p>
      ))}
      {preview.rules.length === 0 && (
        <p className="text-xs text-default-500">
          {ruleCount > 0
            ? "读不到这个用户的利群转发"
            : "这个用户还没有利群转发"}
        </p>
      )}
      <ul className="divide-y divide-divider/60">
        {preview.rules.map((r) => (
          <li
            key={r.id}
            className="space-y-1 py-1.5"
            data-leikwan-preview-rule={r.forwardId}
          >
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <span className="font-semibold text-foreground">{r.name}</span>
              {r.lineName && (
                <span className="text-default-400">
                  {r.lineName.replace(/^利群·/, "")}
                </span>
              )}
              {r.changes.length === 0 && <Pill tone="green">不变</Pill>}
            </div>
            {r.changes.map((c) => (
              <div
                key={c.key}
                className={`rounded-md px-2 py-1 text-xs ${
                  c.lost
                    ? "bg-red-500/10 text-red-700 dark:bg-red-400/10 dark:text-red-300"
                    : c.gain
                      ? "bg-green-500/10 text-green-700 dark:bg-green-400/10 dark:text-green-300"
                      : "bg-amber-500/10 text-amber-700 dark:bg-amber-400/10 dark:text-amber-300"
                }`}
              >
                <span className="font-medium">
                  {c.from} → {c.to}
                </span>
                {c.why && <span className="block opacity-90">{c.why}</span>}
              </div>
            ))}
            {r.kept.length > 0 && (
              <div className="text-[11px] text-default-500">
                保留：{r.kept.join(" · ")}
              </div>
            )}
          </li>
        ))}
      </ul>
      {changed.length > 0 && (
        <p className="text-[11px] text-default-400">
          按新入口的利群客户端和面板接管的设置算（跟主机同步用的是同一套规则）。
        </p>
      )}
    </div>
  );
}

export function LeiKwanGrantDialog({
  isOpen,
  onClose,
  onSaved,
  user,
  users,
  grant,
  grants,
  entries,
  caps,
  quota,
  lineNames,
  entryLock = false,
  mode = "grant",
  presetEntry,
}: {
  isOpen: boolean;
  onClose: () => void;
  onSaved: () => void;
  user?: { id: number; name: string } | null;
  users?: { id: number; name: string }[];
  grant?: LeiKwanGrant | null;
  /** Plan 295: 「设为主入口…」 from a host — pick one of these grants */
  grants?: LeiKwanGrant[];
  entries: LeiKwanEntryNode[];
  /** Plan 295: what each entry can run (host list), by node id */
  caps?: Record<number, LeiKwanEntryCaps>;
  quota?: LeiKwanQuota;
  lineNames: string[];
  /** Plan 272: global 「只开主入口」 (the grace note depends on it) */
  entryLock?: boolean;
  /** Plan 295: 「entry」 = opened as 换主入口 */
  mode?: LeiKwanGrantDialogMode;
  /** Plan 295: entry to preselect (host drawer 「设为主入口…」) */
  presetEntry?: number;
}) {
  const [userId, setUserId] = useState("");
  const [entry, setEntry] = useState("");
  const [backupLine, setBackupLine] = useState(true);
  const [saving, setSaving] = useState(false);
  const [preview, setPreview] = useState<LeiKwanGrantPreview | null>(null);
  const [previewing, setPreviewing] = useState(false);
  // Plan 295: the grant picked inside the dialog (from a host drawer).
  const [picked, setPicked] = useState<LeiKwanGrant | null>(null);
  const pickGrant = !grant && !!grants && presetEntry !== undefined;
  const g = grant || picked;

  useEffect(() => {
    if (!isOpen) return;
    setPicked(null);
    setPreview(null);
    setUserId(user ? String(user.id) : grant ? String(grant.userId) : "");
    setEntry(
      presetEntry !== undefined
        ? String(presetEntry)
        : grant
          ? String(grant.mainEntryNodeId)
          : "",
    );
    setBackupLine(grant ? grant.backupLine : true);
    // Plan 295: 「设为主入口…」 with one granted user picks that user.
    if (!grant && presetEntry !== undefined && grants?.length === 1) {
      setPicked(grants[0]);
      setUserId(String(grants[0].userId));
      setBackupLine(grants[0].backupLine);
    }
  }, [isOpen, user, grant, presetEntry, grants]);

  const entryNode = entries.find((e) => String(e.id) === entry);
  const changedEntry =
    !!g && entry !== "" && String(g.mainEntryNodeId) !== entry;
  const title =
    user?.name ||
    g?.userName ||
    users?.find((u) => String(u.id) === userId)?.name;
  // Offline entries cannot be picked; the current one stays listed.
  const disabledKeys = entries
    .filter((e) => !e.online && String(e.id) !== String(g?.mainEntryNodeId))
    .map((e) => String(e.id));
  const entryOffline = !!entryNode && !entryNode.online;

  // Plan 295: the backend preview of the forwards on the picked entry.
  useEffect(() => {
    if (!isOpen || !changedEntry || !g) {
      setPreview(null);

      return;
    }
    let live = true;

    setPreviewing(true);
    void previewLeiKwanGrant({
      userId: g.userId,
      mainEntryNodeId: parseInt(entry, 10),
    }).then((res) => {
      if (!live) return;
      setPreviewing(false);
      if (res.code === 0) setPreview(res.data);
      else {
        setPreview(null);
        toast.error(res.msg || "核对失败");
      }
    });

    return () => {
      live = false;
    };
  }, [isOpen, changedEntry, entry, g]);

  const pickUser = (id: string) => {
    const next = (grants || []).find((x) => String(x.userId) === id) || null;

    setPicked(next);
    setUserId(id);
    if (next) setBackupLine(next.backupLine);
  };

  const save = async () => {
    const uid = parseInt(userId, 10);
    const eid = parseInt(entry, 10);

    if (!uid || !eid) {
      toast.error(pickGrant ? "请选择用户" : "请选择用户和主入口");

      return;
    }
    setSaving(true);
    const res = await saveLeiKwanGrant({
      userId: uid,
      mainEntryNodeId: eid,
      backupLine,
    });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    if (res.data?.movedRules)
      toast.success(
        `已保存：${res.data.movedRules} 条利群转发的地址改到 ${entryNode?.name}`,
      );
    else toast.success("授权已保存");
    (res.data?.warnings || []).forEach((w) => toast.error(w));
    onSaved();
    onClose();
  };

  const entryTitle = mode === "entry" || pickGrant;

  return (
    <Modal
      classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-xl rounded-2xl" }}
      isOpen={isOpen}
      placement="center"
      scrollBehavior="inside"
      size="xl"
      onClose={onClose}
    >
      <ModalContent>
        <ModalHeader>
          {entryTitle ? "换主入口" : "分配隧道组"}
          {title ? ` · 用户 ${title}` : ""}
        </ModalHeader>
        <ModalBody className="space-y-4">
          {pickGrant && (
            <Select
              label="用户"
              listboxVariant="grouped"
              placeholder="选择已授权的用户"
              selectedKeys={userId ? [userId] : []}
              variant="bordered"
              onSelectionChange={(keys) =>
                pickUser(String(Array.from(keys)[0] || ""))
              }
            >
              {(grants || []).map((x) => (
                <SelectItem
                  key={String(x.userId)}
                  description={`现在的主入口：${x.mainEntryName || "—"}`}
                >
                  {x.userName || `#${x.userId}`}
                </SelectItem>
              ))}
            </Select>
          )}
          {!user && !grant && !pickGrant && users && (
            <Select
              isSearchable
              label="用户"
              placeholder="选择用户"
              selectedKeys={userId ? [userId] : []}
              variant="bordered"
              onSelectionChange={(keys) =>
                setUserId(String(Array.from(keys)[0] || ""))
              }
            >
              {users.map((u) => (
                <SelectItem key={String(u.id)}>{u.name}</SelectItem>
              ))}
            </Select>
          )}
          {!entryTitle && (
            <div className="flex flex-wrap items-center gap-2 rounded-xl bg-teal-500/10 px-3 py-2 dark:bg-teal-400/10">
              <span className="text-sm font-medium">隧道组</span>
              <Pill tone="teal">利群汇聚</Pill>
              <span className="text-xs text-default-500">
                {lineNames.length
                  ? lineNames.map((n) => n.replace(/^利群·/, "")).join(" · ")
                  : "利群专线"}
                ，全线
              </span>
            </div>
          )}
          <Select
            description="从节点分组「利群入口」里选；离线的入口不能选"
            disabledKeys={disabledKeys}
            label="主入口"
            listboxVariant="grouped"
            placeholder="选择主入口"
            selectedKeys={entry ? [entry] : []}
            variant="bordered"
            onSelectionChange={(keys) =>
              setEntry(String(Array.from(keys)[0] || ""))
            }
          >
            {entries.map((e) => (
              <SelectItem
                key={String(e.id)}
                description={
                  <EntryOptionDetail
                    caps={caps?.[e.id]}
                    current={String(e.id) === String(g?.mainEntryNodeId)}
                  />
                }
                endContent={
                  e.online ? (
                    <Pill tone="green">在线</Pill>
                  ) : (
                    <OfflinePill since={e.offlineSince} />
                  )
                }
                textValue={`${e.name} · ${e.host}`}
              >
                {`${e.name} · ${e.host}${!e.online || e.leikwanStatus === "running" ? "" : "（利群客户端未检测到）"}`}
              </SelectItem>
            ))}
          </Select>
          {entryNode && (
            <div
              className="-mt-2 flex flex-wrap items-center gap-1"
              data-leikwan-entry-picked={entryNode.id}
            >
              {entryNode.online ? (
                <Pill tone="green">在线</Pill>
              ) : (
                <OfflinePill inline since={entryNode.offlineSince} />
              )}
              <ClientPill caps={caps?.[entryNode.id]} />
              <CapPills caps={caps?.[entryNode.id]} />
            </div>
          )}
          {entryOffline && !changedEntry && (
            <p className="-mt-2 rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-700 dark:bg-red-400/15 dark:text-red-300">
              现在的主入口 {entryNode?.name}{" "}
              离线：这个用户的利群转发连不上，换到一台在线的入口即可。
            </p>
          )}
          {changedEntry && (
            <p className="-mt-2 rounded-lg bg-amber-500/15 px-3 py-2 text-xs text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
              换主入口后，这个用户所有利群转发的地址都改成 {entryNode?.host}
              :端口 （共 {g?.ruleCount || 0}{" "}
              条）。利群那边不用改；用户刷新订阅或重新复制链接即可。
              {entryLock
                ? `旧入口 ${g?.mainEntryName || ""} 会保留到这个用户刷新任一订阅（最多 24 小时），然后停用。`
                : "旧入口暂时不会停用（设置里「只开主入口」没开）。"}
            </p>
          )}
          {changedEntry && (
            <PreviewBlock
              loading={previewing}
              preview={preview}
              ruleCount={g?.ruleCount || 0}
            />
          )}
          <div className="rounded-xl border border-divider p-3">
            <LabeledSwitch
              isSelected={backupLine}
              onValueChange={setBackupLine}
            >
              备线 {backupLine ? "开" : "关"}
            </LabeledSwitch>
            <p className="mt-1 text-xs text-default-500">
              开：这个用户的每条利群转发都自动带一条备线（每条专线自己设的备线隧道，到同一落地）；日志快切时走它，经主入口转发，用户地址不变。
              关：自动备线删掉（导入时链接的现有备线不动）。
            </p>
          </div>
          {!entryTitle && (
            <>
              <div className="flex flex-wrap items-center gap-2 text-xs text-default-500">
                <QuotaPill quota={quota} />
                名额是整个利群账号共用的；授权时不扣，添加转发时才扣。
              </div>
              <p className="text-xs text-default-500">
                利群转发数和到期跟用户：利群转发数在用户编辑里设，不占总转发数；用户到期后不能再添加。
              </p>
              <p className="text-xs text-default-400">
                备用入口：暂不做（以后再定）。
              </p>
            </>
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="success"
            isDisabled={entryOffline && changedEntry}
            isLoading={saving}
            variant="flat"
            onPress={() => void save()}
          >
            {changedEntry ? "保存，换主入口" : "保存授权"}
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
