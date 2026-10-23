import { useCallback, useEffect, useState } from "react";
import toast from "react-hot-toast";

import {
  createDNSProvider,
  deleteDNSProvider,
  listDNSProviders,
  testDNSProvider,
  tokenValidity,
  updateDNSProvider,
  type DNSProviderTestResult,
  type DNSProviderView,
} from "@/api/ddns";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// 网站配置 → 「DNS 服务商」 (plan 142, admin only). Add-only card: the API
// token is write-only (never shown again), 测试 verifies it with Cloudflare
// and lists the zones it can edit.

const fmtTime = (ms: number) =>
  ms ? new Date(ms).toLocaleString("zh-CN", { hour12: false }) : "从未";

export function TokenValidityChip({
  status,
  expiresOn,
  error,
}: {
  status: string;
  expiresOn: number;
  error?: string;
}) {
  if (status === "invalid") {
    return (
      <Chip color="danger" size="sm" title={error} variant="flat">
        Token 无效
      </Chip>
    );
  }
  if (status === "error") {
    return (
      <Chip
        className="text-default-600"
        size="sm"
        title={error}
        variant="bordered"
      >
        暂时无法校验
      </Chip>
    );
  }
  if (status !== "active") {
    return (
      <Chip className="text-default-600" size="sm" variant="bordered">
        未校验
      </Chip>
    );
  }
  const v = tokenValidity(expiresOn);

  return (
    <Chip
      color={v.warn ? "warning" : "success"}
      size="sm"
      title={
        expiresOn ? `到期：${fmtTime(expiresOn)}` : "Cloudflare 未设置到期时间"
      }
      variant="flat"
    >
      {v.label}
    </Chip>
  );
}

function TestResultBox({ result }: { result: DNSProviderTestResult }) {
  return (
    <div
      className="space-y-2 rounded-lg border border-divider bg-default-50 p-3 text-xs"
      data-testid="dns-test-result"
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-default-600">Token：</span>
        <TokenValidityChip
          error={result.error}
          expiresOn={result.expiresOn}
          status={result.status}
        />
        {result.error ? (
          <span className="break-all text-danger">{result.error}</span>
        ) : null}
      </div>
      {result.zones.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-default-600">可管理的域名：</span>
          {result.zones.map((zone) => (
            <Chip
              key={zone.name}
              className={zone.canEdit ? "" : "text-default-600"}
              color={zone.canEdit ? "success" : "default"}
              size="sm"
              title={
                zone.canEdit ? "可编辑 DNS 记录" : "只读：不能编辑 DNS 记录"
              }
              variant={zone.canEdit ? "flat" : "bordered"}
            >
              {zone.name}
              {zone.canEdit ? "" : "（只读）"}
            </Chip>
          ))}
        </div>
      ) : result.status === "active" ? (
        <p className="text-warning">该 Token 看不到任何域名区域</p>
      ) : null}
    </div>
  );
}

interface DialogState {
  open: boolean;
  id: number | null;
  name: string;
  token: string;
  tokenSet: boolean;
}

const closedDialog: DialogState = {
  open: false,
  id: null,
  name: "",
  token: "",
  tokenSet: false,
};

export function DNSProviderCard() {
  const [providers, setProviders] = useState<DNSProviderView[]>([]);
  const [loading, setLoading] = useState(true);
  const [dialog, setDialog] = useState<DialogState>(closedDialog);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState<number | "dialog" | null>(null);
  const [dialogTest, setDialogTest] = useState<DNSProviderTestResult | null>(
    null,
  );
  const [rowTests, setRowTests] = useState<
    Record<number, DNSProviderTestResult>
  >({});
  const [deleting, setDeleting] = useState<DNSProviderView | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await listDNSProviders();

      if (res.code === 0) setProviders(res.data || []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const openCreate = () => {
    setDialogTest(null);
    setDialog({ ...closedDialog, open: true, name: "Cloudflare" });
  };
  const openEdit = (p: DNSProviderView) => {
    setDialogTest(null);
    setDialog({
      open: true,
      id: p.id,
      name: p.name,
      token: "",
      tokenSet: p.tokenSet,
    });
  };

  const runDialogTest = async () => {
    setTesting("dialog");
    try {
      const res = await testDNSProvider(
        dialog.token.trim()
          ? { id: dialog.id ?? undefined, token: dialog.token.trim() }
          : { id: dialog.id ?? undefined },
      );

      if (res.code === 0) setDialogTest(res.data);
      else toast.error(res.msg || "测试失败");
    } catch {
      toast.error("网络错误，请重试");
    } finally {
      setTesting(null);
    }
  };

  const runRowTest = async (p: DNSProviderView) => {
    setTesting(p.id);
    try {
      const res = await testDNSProvider({ id: p.id });

      if (res.code === 0) {
        setRowTests((prev) => ({ ...prev, [p.id]: res.data }));
        load();
      } else toast.error(res.msg || "测试失败");
    } catch {
      toast.error("网络错误，请重试");
    } finally {
      setTesting(null);
    }
  };

  const save = async () => {
    if (!dialog.name.trim()) {
      toast.error("请填写名称");

      return;
    }
    if (dialog.id === null && !dialog.token.trim()) {
      toast.error("请填写 API Token");

      return;
    }
    setSaving(true);
    try {
      const res =
        dialog.id === null
          ? await createDNSProvider({
              name: dialog.name.trim(),
              providerType: "cloudflare",
              token: dialog.token.trim(),
            })
          : await updateDNSProvider({
              id: dialog.id,
              name: dialog.name.trim(),
              token: dialog.token.trim(),
            });

      if (res.code === 0) {
        toast.success(dialog.id === null ? "已添加" : "已保存");
        setDialog(closedDialog);
        load();
      } else toast.error(res.msg || "保存失败");
    } catch {
      toast.error("网络错误，请重试");
    } finally {
      setSaving(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    try {
      const res = await deleteDNSProvider(deleting.id);

      if (res.code === 0) {
        toast.success("已删除");
        setDeleting(null);
        load();
      } else toast.error(res.msg || "删除失败");
    } catch {
      toast.error("网络错误，请重试");
    }
  };

  return (
    <Card
      className="mt-6 shadow-md dark:bg-content1"
      data-testid="dns-provider-card"
    >
      <CardHeader className="pb-4">
        <div className="flex w-full items-start justify-between gap-3">
          <div>
            <h2 className="text-xl font-semibold">DNS 服务商</h2>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              节点 DDNS 用它更新 A/AAAA 记录。Token
              只保存在面板里，保存后不再显示。
            </p>
          </div>
          <Button color="success" size="sm" variant="flat" onPress={openCreate}>
            添加
          </Button>
        </div>
      </CardHeader>
      <CardBody className="space-y-3 pt-0">
        {loading ? (
          <p className="text-sm text-default-500">加载中…</p>
        ) : providers.length === 0 ? (
          <p className="text-sm text-default-500">
            还没有 DNS 服务商。添加后，在节点的「DDNS」里选择它。
          </p>
        ) : (
          providers.map((p) => (
            <div
              key={p.id}
              className="space-y-2 rounded-xl border border-divider p-3"
              data-testid="dns-provider-row"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium text-foreground">{p.name}</span>
                <Chip className="text-default-600" size="sm" variant="bordered">
                  Cloudflare
                </Chip>
                <TokenValidityChip
                  error={p.tokenError}
                  expiresOn={p.tokenExpiresOn}
                  status={p.tokenStatus}
                />
                {p.usedBy > 0 ? (
                  <Chip
                    className="text-default-600"
                    size="sm"
                    variant="bordered"
                  >
                    {p.usedBy} 个节点在用
                  </Chip>
                ) : null}
              </div>
              <div className="grid gap-1 text-xs text-default-500 sm:grid-cols-2">
                <span>API Token：{p.tokenSet ? "已设置" : "未设置"}</span>
                <span>可管理：{p.zones.length ? p.zones.join("、") : "—"}</span>
                <span className="sm:col-span-2">
                  上次校验：{fmtTime(p.tokenCheckedAt)}（每天最多自动校验一次）
                </span>
                {p.tokenError ? (
                  <span className="break-all text-danger sm:col-span-2">
                    {p.tokenError}
                  </span>
                ) : null}
              </div>
              {rowTests[p.id] ? (
                <TestResultBox result={rowTests[p.id]} />
              ) : null}
              <div className="flex flex-wrap gap-2">
                <Button
                  color="secondary"
                  isLoading={testing === p.id}
                  size="sm"
                  variant="flat"
                  onPress={() => runRowTest(p)}
                >
                  测试
                </Button>
                <Button
                  color="primary"
                  size="sm"
                  variant="flat"
                  onPress={() => openEdit(p)}
                >
                  编辑
                </Button>
                <Button
                  color="danger"
                  size="sm"
                  variant="flat"
                  onPress={() => setDeleting(p)}
                >
                  删除
                </Button>
              </div>
            </div>
          ))
        )}
      </CardBody>

      <Modal
        backdrop="blur"
        classNames={{
          base: "!w-[calc(100%-32px)] !mx-auto sm:!w-full rounded-2xl overflow-hidden",
        }}
        isOpen={dialog.open}
        placement="center"
        size="lg"
        onClose={() => setDialog(closedDialog)}
      >
        <ModalContent>
          <ModalHeader>
            {dialog.id === null ? "添加 DNS 服务商" : "编辑 DNS 服务商"}
          </ModalHeader>
          <ModalBody>
            <div className="space-y-4">
              <Input
                label="名称"
                placeholder="例如：Cloudflare imgamer.top"
                value={dialog.name}
                variant="bordered"
                onChange={(e) =>
                  setDialog((prev) => ({ ...prev, name: e.target.value }))
                }
              />
              <Select
                description="目前支持 Cloudflare"
                label="服务商"
                selectedKeys={["cloudflare"]}
                variant="bordered"
              >
                <SelectItem key="cloudflare">Cloudflare</SelectItem>
              </Select>
              <Input
                autoComplete="off"
                description="Cloudflare → My Profile → API Tokens，权限选 Zone · DNS · Edit（只给要用的域名）"
                label="API Token"
                placeholder={
                  dialog.tokenSet ? "已设置，留空则不修改" : "粘贴 API Token"
                }
                type="password"
                value={dialog.token}
                variant="bordered"
                onChange={(e) => {
                  setDialogTest(null);
                  setDialog((prev) => ({ ...prev, token: e.target.value }));
                }}
              />
              <div className="flex items-center gap-2">
                <Button
                  color="secondary"
                  isDisabled={!dialog.token.trim() && !dialog.tokenSet}
                  isLoading={testing === "dialog"}
                  size="sm"
                  variant="flat"
                  onPress={runDialogTest}
                >
                  测试
                </Button>
                <span className="text-xs text-default-500">
                  检查 Token 是否有效、还剩多久，以及能管理哪些域名
                </span>
              </div>
              {dialogTest ? <TestResultBox result={dialogTest} /> : null}
            </div>
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setDialog(closedDialog)}
            >
              取消
            </Button>
            <Button color="primary" isLoading={saving} onPress={save}>
              保存
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      <Modal
        backdrop="blur"
        classNames={{
          base: "!w-[calc(100%-32px)] !mx-auto sm:!w-full rounded-2xl overflow-hidden",
        }}
        isOpen={!!deleting}
        placement="center"
        size="md"
        onClose={() => setDeleting(null)}
      >
        <ModalContent>
          <ModalHeader>删除 DNS 服务商</ModalHeader>
          <ModalBody>
            <p className="text-sm">
              确定删除「{deleting?.name}」？面板会忘记这个 Token；Cloudflare
              里的记录不会被删除。
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
            <Button color="danger" onPress={confirmDelete}>
              删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </Card>
  );
}
