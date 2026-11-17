import type { LeiKwanAccount } from "@/api/leikwan";
import type { LeiKwanLineList } from "@/api/leikwan-line";

import { useState } from "react";
import { Plus, Settings2 } from "lucide-react";
import toast from "react-hot-toast";

import { deleteLeiKwanAccount, saveLeiKwanAccount } from "@/api/leikwan";
import { Pill } from "@/pages/landing/pill";
import { SectionTitle, TriggerList } from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";

// Plan 270: 「设置」 of the 利群汇聚 page — the 利群 account + API Token (the old
// 账号设置 card) and the global settings, read-only as before: 日志快切 triggers
// and timings, 首包合并 default, entry group and the host write window.

interface AccountForm {
  id?: number;
  name: string;
  serviceId: string;
  token: string;
}

export function LeiKwanSettingsModal({
  open,
  accounts,
  data,
  onClose,
  onAccountsChanged,
}: {
  open: boolean;
  accounts: LeiKwanAccount[];
  data: LeiKwanLineList | null;
  onClose: () => void;
  onAccountsChanged: () => void;
}) {
  const [form, setForm] = useState<AccountForm | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const g = data?.setting.global;

  const save = async () => {
    if (
      !form ||
      !form.name.trim() ||
      !/^\d+$/.test(form.serviceId) ||
      Number(form.serviceId) < 1 ||
      (!form.id && !form.token.trim())
    ) {
      toast.error("请填写账号名称、有效服务 ID 和 API Token");

      return;
    }
    setSaving(true);
    const res = await saveLeiKwanAccount({
      id: form.id,
      name: form.name.trim(),
      serviceId: Number(form.serviceId),
      ...(form.token.trim() ? { token: form.token.trim() } : {}),
    });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success("账号已保存");
    setForm(null);
    onAccountsChanged();
  };
  const remove = async (account: LeiKwanAccount) => {
    if (confirmDelete !== account.id) {
      setConfirmDelete(account.id);

      return;
    }
    setSaving(true);
    const res = await deleteLeiKwanAccount(account.id);

    setSaving(false);
    setConfirmDelete(null);
    if (res.code !== 0) {
      toast.error(res.msg || "删除失败");

      return;
    }
    toast.success("账号已删除");
    onAccountsChanged();
  };
  const close = () => {
    setForm(null);
    setConfirmDelete(null);
    onClose();
  };

  return (
    <Modal
      classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-2xl rounded-2xl" }}
      isOpen={open}
      placement="center"
      scrollBehavior="inside"
      size="2xl"
      onClose={close}
    >
      <ModalContent>
        <ModalHeader>利群设置</ModalHeader>
        <ModalBody className="space-y-5" data-leikwan-settings="">
          <div className="space-y-2">
            <SectionTitle
              hint="API Token 只用于面板请求利群 API，保存后只显示掩码"
              right={
                !form && (
                  <Button
                    color="success"
                    size="sm"
                    startContent={<Plus size={15} />}
                    variant="flat"
                    onPress={() =>
                      setForm({ name: "", serviceId: "", token: "" })
                    }
                  >
                    新增账号
                  </Button>
                )
              }
              title="账号 / Token"
            />
            {accounts.length === 0 && !form && (
              <p className="rounded-xl border border-dashed border-divider px-4 py-4 text-center text-sm text-default-500">
                还没有利群账号：添加 API Token 和服务 ID 后才能建专线和规则。
              </p>
            )}
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              {accounts.map((account) => (
                <div
                  key={account.id}
                  className="rounded-xl border border-divider p-3"
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <p className="break-words font-medium">{account.name}</p>
                      <div className="mt-1 flex flex-wrap gap-1">
                        <Pill tone="slate">服务 {account.serviceId}</Pill>
                        <Pill
                          tone={account.tokenConfigured ? "green" : "amber"}
                        >
                          Token {account.tokenConfigured ? "已配置" : "未配置"}
                        </Pill>
                      </div>
                    </div>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-2">
                    <Button
                      color="primary"
                      size="sm"
                      startContent={<Settings2 size={14} />}
                      variant="flat"
                      onPress={() => {
                        setConfirmDelete(null);
                        setForm({
                          id: account.id,
                          name: account.name,
                          serviceId: String(account.serviceId),
                          token: "",
                        });
                      }}
                    >
                      修改
                    </Button>
                    <Button
                      color="danger"
                      isLoading={saving && confirmDelete === account.id}
                      size="sm"
                      variant="flat"
                      onPress={() => void remove(account)}
                    >
                      {confirmDelete === account.id ? "确认删除" : "删除账号"}
                    </Button>
                  </div>
                  {confirmDelete === account.id && (
                    <p className="mt-2 text-xs text-amber-700 dark:text-amber-300">
                      只删面板保存的
                      Token，利群平台上的转发会保留；有专线或管道绑定时删不了。
                    </p>
                  )}
                </div>
              ))}
            </div>
            {form && (
              <div className="space-y-3 rounded-xl bg-default-100/60 p-3 dark:bg-default-50/40">
                <div className="text-sm font-medium">
                  {form.id ? "修改账号" : "新增账号"}
                </div>
                <Input
                  label="账号名称"
                  value={form.name}
                  variant="bordered"
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                />
                <Input
                  inputMode="numeric"
                  label="服务 ID"
                  value={form.serviceId}
                  variant="bordered"
                  onChange={(e) =>
                    setForm({ ...form, serviceId: e.target.value })
                  }
                />
                <Input
                  autoComplete="new-password"
                  description={
                    form.id
                      ? "已保存的 Token 不会返回；留空保留原值。"
                      : "Token 仅用于面板请求利群 API，保存后只显示掩码。"
                  }
                  label="API Token"
                  placeholder={
                    form.id ? "••••••••（留空不变）" : "输入 API Token"
                  }
                  type="password"
                  value={form.token}
                  variant="bordered"
                  onChange={(e) => setForm({ ...form, token: e.target.value })}
                />
                <div className="flex justify-end gap-2">
                  <Button
                    color="danger"
                    size="sm"
                    variant="flat"
                    onPress={() => setForm(null)}
                  >
                    取消
                  </Button>
                  <Button
                    color="success"
                    isLoading={saving}
                    size="sm"
                    variant="flat"
                    onPress={() => void save()}
                  >
                    保存
                  </Button>
                </div>
              </div>
            )}
          </div>
          {data && g && (
            <>
              <div className="space-y-2">
                <SectionTitle
                  hint="全局一套，只读；每条专线只有开关和切回复查"
                  title="日志快切 · 触发条件"
                />
                <TriggerList triggers={data.triggers} />
                <p className="text-xs text-default-500">
                  保持 {g.holdS} s，再次触发翻倍 · 新租约稳定 {g.leaseSettleS} s
                  · 厂商宽限 {g.grace} · 切回复查 {g.returnCheckS} s
                </p>
              </div>
              <div className="space-y-2">
                <SectionTitle title="其他全局设置" />
                <div className="flex flex-wrap gap-1.5">
                  <Pill tone="teal">
                    入口机分组「{data.setting.entryGroupName || "利群入口"}」
                  </Pill>
                  <Pill tone="slate">隧道组「{data.setting.groupName}」</Pill>
                  <Pill tone={g.coalesce.startsWith("on") ? "green" : "slate"}>
                    首包合并默认 {g.coalesce}
                  </Pill>
                  <Pill tone={g.nightOnly ? "amber" : "slate"}>
                    主机写入 {g.nightOnly ? "只在 02:00–06:00 台湾" : "随时"}
                  </Pill>
                </div>
                <p className="text-xs text-default-500">
                  每台入口机自己的首包合并值在「入口机」里看；加入口机 =
                  在节点页把节点拉进分组「
                  {data.setting.entryGroupName || "利群入口"}」。
                </p>
              </div>
            </>
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={close}>
            关闭
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
