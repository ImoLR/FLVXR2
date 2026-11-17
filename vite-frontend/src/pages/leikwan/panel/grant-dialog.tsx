import type {
  LeiKwanEntryNode,
  LeiKwanGrant,
  LeiKwanQuota,
} from "@/api/leikwan-line";

import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import { saveLeiKwanGrant } from "@/api/leikwan-line";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Pill } from "@/pages/landing/pill";
import { LabeledSwitch, QuotaPill } from "@/pages/leikwan/panel/shared";

// Plan 266: grant the tunnel group 「利群汇聚」 to one user. The 主入口 is picked
// here once for every 利群 line; changing it moves all of the user's 利群 rule
// addresses (the LeiKwan ports are the same on every entry machine).
// 备用入口 is parked (user 11-16), so there is no field for it.
// Plan 268 (user 11-17): the grant is only 主入口 + 备线. 利群 rules count
// toward the user's 总规则数 and follow the user's 到期; traffic is not shown
// until step 2 measures it.

export function LeiKwanGrantDialog({
  isOpen,
  onClose,
  onSaved,
  user,
  users,
  grant,
  entries,
  quota,
  lineNames,
  entryLock = false,
}: {
  isOpen: boolean;
  onClose: () => void;
  onSaved: () => void;
  user?: { id: number; name: string } | null;
  users?: { id: number; name: string }[];
  grant?: LeiKwanGrant | null;
  entries: LeiKwanEntryNode[];
  quota?: LeiKwanQuota;
  lineNames: string[];
  /** Plan 272: global 「只开主入口」 (the grace note depends on it) */
  entryLock?: boolean;
}) {
  const [userId, setUserId] = useState("");
  const [entry, setEntry] = useState("");
  const [backupLine, setBackupLine] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!isOpen) return;
    setUserId(user ? String(user.id) : grant ? String(grant.userId) : "");
    setEntry(grant ? String(grant.mainEntryNodeId) : "");
    setBackupLine(grant ? grant.backupLine : true);
  }, [isOpen, user, grant]);

  const entryNode = entries.find((e) => String(e.id) === entry);
  const changedEntry =
    !!grant && entry !== "" && String(grant.mainEntryNodeId) !== entry;
  const title =
    user?.name ||
    grant?.userName ||
    users?.find((u) => String(u.id) === userId)?.name;

  const save = async () => {
    const uid = parseInt(userId, 10);
    const eid = parseInt(entry, 10);

    if (!uid || !eid) {
      toast.error("请选择用户和主入口");

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
        `已保存：${res.data.movedRules} 条利群规则的地址改到 ${entryNode?.name}`,
      );
    else toast.success("授权已保存");
    (res.data?.warnings || []).forEach((w) => toast.error(w));
    onSaved();
    onClose();
  };

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
        <ModalHeader>分配隧道组{title ? ` · 用户 ${title}` : ""}</ModalHeader>
        <ModalBody className="space-y-4">
          {!user && !grant && users && (
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
          <Select
            description="必选；从节点分组「利群入口」里选"
            label="主入口"
            placeholder="选择主入口"
            selectedKeys={entry ? [entry] : []}
            variant="bordered"
            onSelectionChange={(keys) =>
              setEntry(String(Array.from(keys)[0] || ""))
            }
          >
            {entries.map((e) => (
              <SelectItem key={String(e.id)}>
                {`${e.name} · ${e.host}${e.leikwanStatus === "running" ? "" : "（利群客户端未检测到）"}`}
              </SelectItem>
            ))}
          </Select>
          {changedEntry && (
            <p className="-mt-2 rounded-lg bg-amber-500/15 px-3 py-2 text-xs text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
              换主入口后，这个用户所有利群规则的地址都改成 {entryNode?.host}
              :端口 （共 {grant?.ruleCount || 0}{" "}
              条）。利群那边不用改；用户刷新订阅或重新复制链接即可。
              {entryLock
                ? `旧入口 ${grant?.mainEntryName || ""} 会保留到这个用户刷新任一订阅（最多 24 小时），然后停用。`
                : "旧入口暂时不会停用（设置里「只开主入口」没开）。"}
            </p>
          )}
          <div className="rounded-xl border border-divider p-3">
            <LabeledSwitch
              isSelected={backupLine}
              onValueChange={setBackupLine}
            >
              备线 {backupLine ? "开" : "关"}
            </LabeledSwitch>
            <p className="mt-1 text-xs text-default-500">
              开：这个用户的每条利群规则都自动带一条备线（每条专线自己设的备线隧道，到同一落地）；日志快切时走它，经主入口转发，用户地址不变。
              关：自动备线删掉（导入时链接的现有备线不动）。
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs text-default-500">
            <QuotaPill quota={quota} />
            名额是整个利群账号共用的；授权时不扣，建规则时才扣。
          </div>
          <p className="text-xs text-default-500">
            规则数和到期跟用户：利群规则算进用户的总规则数，用户到期后不能再建。
          </p>
          <p className="text-xs text-default-400">
            备用入口：暂不做（以后再定）。
          </p>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="success"
            isLoading={saving}
            variant="flat"
            onPress={() => void save()}
          >
            保存授权
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
