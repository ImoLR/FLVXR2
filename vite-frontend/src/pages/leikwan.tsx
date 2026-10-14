import type { NodeApiItem } from "@/api/types";
import type {
  LeiKwanAccount,
  LeiKwanForward,
  LeiKwanOverview,
  LeiKwanPipe,
} from "@/api/leikwan";

import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, RefreshCw, Settings2 } from "lucide-react";
import toast from "react-hot-toast";

import { getNodeList } from "@/api";
import {
  createLeiKwanPipe,
  deleteLeiKwanAccount,
  formatLeiKwanTarget,
  leiKwanNodeHost,
  getLeiKwanAccounts,
  getLeiKwanForward,
  getLeiKwanOverview,
  retargetLeiKwanPipe,
  saveLeiKwanAccount,
  takeoverLeiKwanPipe,
} from "@/api/leikwan";
import { AnimatedPage } from "@/components/animated-page";
import { LeiKwanProgressModal } from "@/pages/leikwan/progress-modal";
import { Alert } from "@/shadcn-bridge/heroui/alert";
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
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import {
  Table,
  TableBody,
  TableCell,
  TableColumn,
  TableHeader,
  TableRow,
} from "@/shadcn-bridge/heroui/table";
import { isAdmin } from "@/utils/auth";

interface PipeDialog {
  mode: "create" | "takeover" | "retarget";
  forward?: LeiKwanForward;
  etag: string;
  loading: boolean;
  confirm: boolean;
  error: string;
  nodeId: string;
  port: string;
  routeId: string;
  transport: string;
}

export default function LeiKwanPage() {
  const navigate = useNavigate();
  const admin = isAdmin();
  const [accounts, setAccounts] = useState<LeiKwanAccount[]>([]);
  const [accountId, setAccountId] = useState(0);
  const [nodes, setNodes] = useState<NodeApiItem[]>([]);
  const [overview, setOverview] = useState<LeiKwanOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [accountForm, setAccountForm] = useState<{
    id?: number;
    name: string;
    serviceId: string;
    token: string;
  } | null>(null);
  const [deleteAccount, setDeleteAccount] = useState<LeiKwanAccount | null>(
    null,
  );
  const [saving, setSaving] = useState(false);
  const [pipeDialog, setPipeDialog] = useState<PipeDialog | null>(null);
  const [progressPipeId, setProgressPipeId] = useState<number | null>(null);
  const overviewGeneration = useRef(0);
  const dialogGeneration = useRef(0);

  const loadAccounts = useCallback(async () => {
    const response = await getLeiKwanAccounts();

    if (response.code !== 0) {
      setError(response.msg || "加载账号失败");
      setLoading(false);

      return;
    }
    setAccounts(response.data || []);
    setAccountId((current) =>
      response.data?.some((account) => account.id === current)
        ? current
        : response.data?.[0]?.id || 0,
    );
    if (!response.data?.length) setLoading(false);
  }, []);

  const loadOverview = useCallback(async (id: number) => {
    const generation = ++overviewGeneration.current;

    setLoading(true);
    setError("");
    const response = await getLeiKwanOverview(id);

    if (generation !== overviewGeneration.current) return;
    if (response.code === 0) setOverview(response.data);
    else setError(response.msg || "加载利群服务失败");
    setLoading(false);
  }, []);

  useEffect(() => {
    if (!admin) return;
    void loadAccounts();
    void getNodeList().then((response) => {
      if (response.code === 0) setNodes(response.data || []);
    });
  }, [admin, loadAccounts]);

  useEffect(() => {
    setOverview(null);
    if (admin && accountId) void loadOverview(accountId);

    return () => {
      overviewGeneration.current += 1;
    };
  }, [admin, accountId, loadOverview]);

  const saveAccount = async () => {
    if (
      !accountForm ||
      !accountForm.name.trim() ||
      !/^\d+$/.test(accountForm.serviceId) ||
      Number(accountForm.serviceId) < 1 ||
      (!accountForm.id && !accountForm.token.trim())
    ) {
      toast.error("请填写账号名称、有效服务 ID 和 API Token");

      return;
    }
    setSaving(true);
    const response = await saveLeiKwanAccount({
      id: accountForm.id,
      name: accountForm.name.trim(),
      serviceId: Number(accountForm.serviceId),
      ...(accountForm.token.trim() ? { token: accountForm.token.trim() } : {}),
    });

    setSaving(false);
    if (response.code !== 0) {
      toast.error(response.msg);

      return;
    }
    setAccountForm(null);
    toast.success("账号已保存");
    await loadAccounts();
    if (accountId === response.data.id) void loadOverview(accountId);
    else setAccountId(response.data.id);
  };

  const openPipeDialog = async (
    mode: PipeDialog["mode"],
    forward?: LeiKwanForward,
  ) => {
    const generation = ++dialogGeneration.current;
    const pipe = forward?.pipe;

    setPipeDialog({
      mode,
      forward,
      etag: "",
      loading: !!forward,
      confirm: false,
      error: "",
      nodeId: pipe ? String(pipe.landingNodeId) : "",
      port: pipe ? String(pipe.landingPort) : "",
      routeId: forward ? String(forward.route.id) : "",
      transport: "tcp",
    });
    if (!forward) return;
    const response = await getLeiKwanForward(accountId, forward.id);

    if (generation !== dialogGeneration.current) return;
    setPipeDialog((current) =>
      current
        ? {
            ...current,
            loading: false,
            forward:
              response.code === 0
                ? { ...response.data.forward, pipe: forward.pipe }
                : forward,
            etag: response.code === 0 ? response.data.etag : "",
            error: response.code === 0 ? "" : response.msg,
          }
        : null,
    );
  };

  const closePipeDialog = () => {
    dialogGeneration.current += 1;
    setPipeDialog(null);
  };
  const changePipeField = (
    field: "nodeId" | "port" | "routeId" | "transport",
    value: string,
  ) =>
    setPipeDialog((current) =>
      current
        ? { ...current, [field]: value, confirm: false, error: "" }
        : null,
    );
  const route = overview?.options.routes.find(
    (item) => item.id === Number(pipeDialog?.routeId),
  );
  const landingNode = nodes.find(
    (node) => node.id === Number(pipeDialog?.nodeId),
  );
  const newTarget = formatLeiKwanTarget(
    leiKwanNodeHost(landingNode),
    pipeDialog?.port || "—",
  );

  const savePipe = async () => {
    if (!pipeDialog) return;
    if (
      !landingNode ||
      !leiKwanNodeHost(landingNode) ||
      !/^\d+$/.test(pipeDialog.port) ||
      Number(pipeDialog.port) < 1 ||
      Number(pipeDialog.port) > 65535
    ) {
      toast.error("请选择落地节点并填写 1–65535 的隧道服务端口");

      return;
    }
    if (
      pipeDialog.mode === "create" &&
      (!route || !route.transports[pipeDialog.transport]?.available)
    ) {
      toast.error("请选择可用线路及传输方式");

      return;
    }
    if (!pipeDialog.confirm) {
      setPipeDialog({ ...pipeDialog, confirm: true });

      return;
    }
    setSaving(true);
    const target = {
      landingNodeId: landingNode.id,
      landingPort: Number(pipeDialog.port),
    };
    let response;
    let requestKey = "";

    if (pipeDialog.mode === "create") {
      const payload = {
        accountId,
        routeId: Number(pipeDialog.routeId),
        transport: pipeDialog.transport,
        ...target,
      };

      requestKey = `flvx-leikwan-create:${JSON.stringify(payload)}`;
      let requestId = localStorage.getItem(requestKey);

      if (!requestId) {
        requestId =
          globalThis.crypto?.randomUUID?.() ||
          `flvx-${Date.now()}-${Math.random().toString(36).slice(2)}`;
        localStorage.setItem(requestKey, requestId);
      }
      response = await createLeiKwanPipe({ ...payload, requestId });
    } else {
      const forward = pipeDialog.forward!;
      const payload = {
        ...target,
        expectedTarget: {
          host: forward.target.host,
          port: forward.target.port,
        },
        etag: pipeDialog.etag,
        confirmed: true,
      };

      response =
        pipeDialog.mode === "takeover"
          ? await takeoverLeiKwanPipe({
              ...payload,
              accountId,
              forwardId: forward.id,
            })
          : await retargetLeiKwanPipe({ ...payload, pipeId: forward.pipe!.id });
    }
    setSaving(false);
    if (response.code !== 0) {
      setPipeDialog((current) =>
        current
          ? { ...current, error: response.msg || "保存失败，请重试" }
          : null,
      );

      return;
    }
    if (requestKey) localStorage.removeItem(requestKey);
    closePipeDialog();
    toast.success("管道已保存");
    setProgressPipeId(response.data.id);
    void loadOverview(accountId);
  };

  const editTunnel = (pipe: LeiKwanPipe) =>
    navigate("/tunnel", {
      state: { leikwanEditTunnelId: pipe.tunnelId, leikwanPipe: pipe },
    });
  const forwardActions = (forward: LeiKwanForward) => (
    <div className="flex flex-wrap gap-2">
      {!forward.pipe ? (
        <Button
          color="primary"
          size="sm"
          variant="flat"
          onPress={() => void openPipeDialog("takeover", forward)}
        >
          接管为管道
        </Button>
      ) : (
        <>
          <Button
            size="sm"
            variant="flat"
            onPress={() =>
              forward.pipe!.tunnelId
                ? editTunnel(forward.pipe!)
                : void openPipeDialog("retarget", forward)
            }
          >
            换落地
          </Button>
          {forward.pipe.tunnelId ? (
            <Button
              color="primary"
              size="sm"
              variant="flat"
              onPress={() => editTunnel(forward.pipe!)}
            >
              编辑隧道
            </Button>
          ) : (
            <Button
              color="primary"
              size="sm"
              variant="flat"
              onPress={() =>
                navigate("/tunnel", { state: { leikwanPipe: forward.pipe } })
              }
            >
              创建隧道
            </Button>
          )}
          <Button
            size="sm"
            variant="light"
            onPress={() => setProgressPipeId(forward.pipe!.id)}
          >
            检查
          </Button>
        </>
      )}
    </div>
  );
  const managedLabel = (forward: LeiKwanForward) =>
    forward.pipe?.tunnelId
      ? `隧道：${forward.tunnelName || `#${forward.pipe.tunnelId}`}`
      : forward.pipe
        ? "管道待绑定隧道"
        : "非 FLVX 管理 · 只读";

  if (!admin)
    return (
      <AnimatedPage className="px-3 py-8 lg:px-6">
        <Alert color="danger" title="仅管理员可访问" />
      </AnimatedPage>
    );

  return (
    <AnimatedPage className="space-y-6 px-3 py-8 lg:px-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold">利群汇聚</h1>
          <p className="mt-1 text-sm text-default-500">
            将利群专线作为普通 FLVX 隧道中的管道。
          </p>
        </div>
        <Button
          color="primary"
          size="sm"
          startContent={<Plus size={16} />}
          onPress={() => setAccountForm({ name: "", serviceId: "", token: "" })}
        >
          新增账号
        </Button>
      </div>
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <h2 className="font-semibold">账号设置</h2>
        </CardHeader>
        <CardBody className="space-y-3">
          {!accounts.length && !loading ? (
            <p className="py-4 text-center text-sm text-default-500">
              添加 API Token 和服务 ID 后查看配额、线路和转发。
            </p>
          ) : (
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
              {accounts.map((account) => (
                <div
                  key={account.id}
                  className={`rounded-xl border p-4 ${accountId === account.id ? "border-primary/50 bg-primary/5" : "border-divider"}`}
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <p className="truncate font-medium">{account.name}</p>
                      <p className="mt-1 text-xs text-default-500">
                        服务 #{account.serviceId} · Token{" "}
                        {account.tokenConfigured ? "••••••••" : "未配置"}
                      </p>
                    </div>
                    <Button
                      isIconOnly
                      aria-label={`编辑账号 ${account.name}`}
                      size="sm"
                      variant="light"
                      onPress={() =>
                        setAccountForm({
                          id: account.id,
                          name: account.name,
                          serviceId: String(account.serviceId),
                          token: "",
                        })
                      }
                    >
                      <Settings2 size={16} />
                    </Button>
                  </div>
                  <div className="mt-3 flex gap-2">
                    <Button
                      color={accountId === account.id ? "primary" : "default"}
                      size="sm"
                      variant="flat"
                      onPress={() => setAccountId(account.id)}
                    >
                      {accountId === account.id ? "当前服务" : "查看服务"}
                    </Button>
                    <Button
                      color="danger"
                      size="sm"
                      variant="light"
                      onPress={() => setDeleteAccount(account)}
                    >
                      删除账号
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardBody>
      </Card>
      {error && <Alert color="danger" description={error} title="加载失败" />}
      {accountId > 0 && (
        <Card>
          <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="font-semibold">服务概览</h2>
              <p className="mt-1 text-sm text-default-500">
                {accounts.find((account) => account.id === accountId)?.name} ·
                服务 #
                {
                  accounts.find((account) => account.id === accountId)
                    ?.serviceId
                }
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                isLoading={loading}
                size="sm"
                startContent={<RefreshCw size={15} />}
                variant="flat"
                onPress={() => void loadOverview(accountId)}
              >
                刷新
              </Button>
              <Button
                color="primary"
                isDisabled={
                  !overview ||
                  overview.options.active_forward_count >=
                    overview.options.forward_limit
                }
                size="sm"
                startContent={<Plus size={15} />}
                onPress={() => void openPipeDialog("create")}
              >
                新建管道
              </Button>
            </div>
          </CardHeader>
          <CardBody className="space-y-5">
            {loading && !overview ? (
              <div className="flex justify-center py-10">
                <Spinner />
              </div>
            ) : (
              overview && (
                <>
                  <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-default-50 p-4">
                    <span className="text-sm">转发配额</span>
                    <span className="text-2xl font-semibold tabular-nums">
                      {overview.options.active_forward_count}
                      <span className="text-base font-normal text-default-500">
                        {" "}
                        / {overview.options.forward_limit}
                      </span>
                    </span>
                  </div>
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
                    {overview.options.routes.map((item) => (
                      <div
                        key={item.id}
                        className="rounded-xl border border-divider p-3"
                      >
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-medium">{item.label}</span>
                          <span className="text-xs text-default-400">
                            #{item.id}
                          </span>
                        </div>
                        {item.description && (
                          <p className="mt-1 text-xs text-default-500">
                            {item.description}
                          </p>
                        )}
                        <div className="mt-2 flex flex-wrap gap-1.5">
                          {Object.entries(item.transports).map(
                            ([transport, status]) => (
                              <Chip
                                key={transport}
                                color={status.available ? "success" : "warning"}
                                size="sm"
                                title={
                                  status.unavailable_reason ||
                                  `${status.healthy_node_count} 个健康节点`
                                }
                                variant="flat"
                              >
                                {transport} ·{" "}
                                {status.available ? "可用" : "不可用"}
                              </Chip>
                            ),
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                  <p className="text-xs text-default-500">
                    同一账号的所有设备共用转发和监听端口。未接管的转发仅展示，接管或换落地会影响手机等所有设备。
                  </p>
                  <div className="hidden md:block">
                    <Table aria-label="利群转发列表">
                      <TableHeader>
                        {[
                          "转发 / 线路",
                          "监听 / 传输",
                          "目标",
                          "状态 / 更新时间",
                          "归属",
                          "操作",
                        ].map((label) => (
                          <TableColumn key={label}>{label}</TableColumn>
                        ))}
                      </TableHeader>
                      <TableBody emptyContent="暂无转发">
                        {overview.forwards.map((forward) => (
                          <TableRow key={forward.id}>
                            <TableCell>
                              <p className="font-medium">#{forward.id}</p>
                              <p className="mt-1 text-xs text-default-500">
                                {forward.route.label}
                              </p>
                            </TableCell>
                            <TableCell>
                              <p className="font-mono">
                                :{forward.listen_port}
                              </p>
                              <p className="mt-1 text-xs text-default-500">
                                {forward.transport}
                              </p>
                            </TableCell>
                            <TableCell className="max-w-60 break-all font-mono text-xs">
                              {formatLeiKwanTarget(
                                forward.target.host,
                                forward.target.port,
                              )}
                            </TableCell>
                            <TableCell>
                              <Chip
                                color={
                                  forward.state === "active"
                                    ? "success"
                                    : "default"
                                }
                                size="sm"
                                variant="flat"
                              >
                                {forward.state}
                              </Chip>
                              <p className="mt-1 whitespace-nowrap text-xs text-default-500">
                                {forward.updated_at}
                              </p>
                            </TableCell>
                            <TableCell className="max-w-44 break-words text-xs">
                              {managedLabel(forward)}
                            </TableCell>
                            <TableCell>{forwardActions(forward)}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                  <div className="space-y-3 md:hidden">
                    {overview.forwards.map((forward) => (
                      <div
                        key={forward.id}
                        className="space-y-3 rounded-xl border border-divider p-4"
                      >
                        <div className="flex items-center justify-between gap-3">
                          <span className="font-medium">
                            #{forward.id} · {forward.route.label}
                          </span>
                          <Chip
                            color={
                              forward.state === "active" ? "success" : "default"
                            }
                            size="sm"
                            variant="flat"
                          >
                            {forward.state}
                          </Chip>
                        </div>
                        <p className="break-all font-mono text-xs">
                          :{forward.listen_port} · {forward.transport} →{" "}
                          {formatLeiKwanTarget(
                            forward.target.host,
                            forward.target.port,
                          )}
                        </p>
                        <p className="text-xs text-default-500">
                          {managedLabel(forward)}
                        </p>
                        <p className="text-xs text-default-400">
                          更新：{forward.updated_at}
                        </p>
                        {forwardActions(forward)}
                      </div>
                    ))}
                  </div>
                </>
              )
            )}
          </CardBody>
        </Card>
      )}
      <Modal
        isOpen={!!accountForm}
        placement="center"
        scrollBehavior="inside"
        size="lg"
        onOpenChange={(open) => {
          if (!open) setAccountForm(null);
        }}
      >
        <ModalContent>
          <ModalHeader>{accountForm?.id ? "编辑账号" : "新增账号"}</ModalHeader>
          <ModalBody className="space-y-3">
            <Input
              label="账号名称"
              value={accountForm?.name || ""}
              variant="bordered"
              onChange={(event) =>
                setAccountForm((current) =>
                  current ? { ...current, name: event.target.value } : null,
                )
              }
            />
            <Input
              inputMode="numeric"
              label="服务 ID"
              value={accountForm?.serviceId || ""}
              variant="bordered"
              onChange={(event) =>
                setAccountForm((current) =>
                  current
                    ? { ...current, serviceId: event.target.value }
                    : null,
                )
              }
            />
            <Input
              autoComplete="new-password"
              description={
                accountForm?.id
                  ? "已保存的 Token 不会返回；留空保留原值。"
                  : "Token 仅用于面板请求利群 API，保存后只显示掩码。"
              }
              label="API Token"
              placeholder={
                accountForm?.id ? "••••••••（留空不变）" : "输入 API Token"
              }
              type="password"
              value={accountForm?.token || ""}
              variant="bordered"
              onChange={(event) =>
                setAccountForm((current) =>
                  current ? { ...current, token: event.target.value } : null,
                )
              }
            />
          </ModalBody>
          <ModalFooter>
            <Button variant="flat" onPress={() => setAccountForm(null)}>
              取消
            </Button>
            <Button
              color="primary"
              isLoading={saving}
              onPress={() => void saveAccount()}
            >
              保存
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      <Modal
        isOpen={!!deleteAccount}
        placement="center"
        size="lg"
        onOpenChange={(open) => {
          if (!open) setDeleteAccount(null);
        }}
      >
        <ModalContent>
          <ModalHeader>删除账号</ModalHeader>
          <ModalBody>
            <p>
              确认删除账号「{deleteAccount?.name}」及其保存在面板的
              Token？利群平台上的转发会保留。存在管道绑定时不可删除账号。
            </p>
          </ModalBody>
          <ModalFooter>
            <Button variant="flat" onPress={() => setDeleteAccount(null)}>
              取消
            </Button>
            <Button
              color="danger"
              isLoading={saving}
              onPress={async () => {
                if (!deleteAccount) return;
                setSaving(true);
                const response = await deleteLeiKwanAccount(deleteAccount.id);

                setSaving(false);
                if (response.code !== 0) {
                  toast.error(response.msg);

                  return;
                }
                setDeleteAccount(null);
                toast.success("账号已删除");
                void loadAccounts();
              }}
            >
              删除
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      <Modal
        isDismissable={!saving}
        isOpen={!!pipeDialog}
        placement="center"
        scrollBehavior="inside"
        size="xl"
        onOpenChange={(open) => {
          if (!open && !saving) closePipeDialog();
        }}
      >
        <ModalContent>
          <ModalHeader>
            {pipeDialog?.confirm
              ? "确认管道变更"
              : pipeDialog?.mode === "create"
                ? "新建管道"
                : pipeDialog?.mode === "takeover"
                  ? "接管为管道"
                  : "换落地"}
          </ModalHeader>
          <ModalBody className="space-y-4">
            {pipeDialog?.loading ? (
              <div className="flex justify-center py-6">
                <Spinner />
              </div>
            ) : (
              pipeDialog && (
                <>
                  {pipeDialog.confirm ? (
                    <>
                      <Alert
                        color="warning"
                        description="包括 VPS 和手机。平台每次更新都会重建连接，现有连接可能中断。"
                        title="影响所有共用此转发的设备"
                      />
                      <div className="space-y-3 rounded-xl border border-divider p-4 text-sm">
                        <p>
                          转发：
                          {pipeDialog.forward
                            ? `#${pipeDialog.forward.id}`
                            : "新建"}{" "}
                          · {pipeDialog.forward?.route.label || route?.label}
                        </p>
                        <p className="break-all">
                          原目标：
                          {pipeDialog.forward
                            ? formatLeiKwanTarget(
                                pipeDialog.forward.target.host,
                                pipeDialog.forward.target.port,
                              )
                            : "无（新建转发）"}
                        </p>
                        <p className="break-all">新目标：{newTarget}</p>
                        <p>落地节点：{landingNode?.name}</p>
                      </div>
                    </>
                  ) : (
                    <>
                      {pipeDialog.mode === "create" && (
                        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                          <Select
                            label="线路"
                            placeholder="选择线路"
                            selectedKeys={
                              pipeDialog.routeId ? [pipeDialog.routeId] : []
                            }
                            variant="bordered"
                            onSelectionChange={(keys) =>
                              changePipeField(
                                "routeId",
                                String(Array.from(keys)[0] || ""),
                              )
                            }
                          >
                            {(overview?.options.routes || []).map((item) => (
                              <SelectItem key={String(item.id)}>
                                {item.label}
                              </SelectItem>
                            ))}
                          </Select>
                          <Select
                            disabledKeys={["tcp", "tcp+udp"].filter(
                              (transport) =>
                                !route?.transports[transport]?.available,
                            )}
                            label="传输方式"
                            selectedKeys={[pipeDialog.transport]}
                            variant="bordered"
                            onSelectionChange={(keys) =>
                              changePipeField(
                                "transport",
                                String(Array.from(keys)[0] || "tcp"),
                              )
                            }
                          >
                            {["tcp", "tcp+udp"].map((transport) => (
                              <SelectItem key={transport}>
                                {transport}
                              </SelectItem>
                            ))}
                          </Select>
                        </div>
                      )}
                      {pipeDialog.forward && (
                        <p className="text-sm text-default-500">
                          转发 #{pipeDialog.forward.id} ·{" "}
                          {pipeDialog.forward.route.label} · 监听 :
                          {pipeDialog.forward.listen_port}
                        </p>
                      )}
                      <Select
                        label="管道落地节点"
                        placeholder="选择 FLVX 节点"
                        selectedKeys={
                          pipeDialog.nodeId ? [pipeDialog.nodeId] : []
                        }
                        variant="bordered"
                        onSelectionChange={(keys) =>
                          changePipeField(
                            "nodeId",
                            String(Array.from(keys)[0] || ""),
                          )
                        }
                      >
                        {nodes
                          .filter(
                            (node) => !node.isRemote && leiKwanNodeHost(node),
                          )
                          .map((node) => (
                            <SelectItem
                              key={String(node.id)}
                              textValue={node.name}
                            >
                              {node.name} · {leiKwanNodeHost(node)}
                            </SelectItem>
                          ))}
                      </Select>
                      <Input
                        description="填写此节点在隧道第一跳的服务端口。创建隧道时会自动带入，可在隧道编辑器中调整。"
                        inputMode="numeric"
                        label="落地隧道服务端口"
                        placeholder="1–65535"
                        value={pipeDialog.port}
                        variant="bordered"
                        onChange={(event) =>
                          changePipeField("port", event.target.value)
                        }
                      />
                      {landingNode && (
                        <p className="break-all text-xs text-default-500">
                          新目标：{newTarget}
                        </p>
                      )}
                      <Alert
                        color="warning"
                        description={
                          pipeDialog.mode === "create"
                            ? "删除过的转发仍计入每服务 512 条历史记录上限。创建失败可重试，面板会复用同一个幂等请求。"
                            : "接管或换落地会修改利群转发目标，影响该账号所有设备。下一步请核对原目标和新目标。"
                        }
                      />
                    </>
                  )}
                  {pipeDialog.error && (
                    <Alert color="danger" description={pipeDialog.error} />
                  )}
                  {pipeDialog.error && pipeDialog.forward && (
                    <Button
                      size="sm"
                      variant="flat"
                      onPress={() =>
                        void openPipeDialog(pipeDialog.mode, pipeDialog.forward)
                      }
                    >
                      重新读取并核对转发
                    </Button>
                  )}
                </>
              )
            )}
          </ModalBody>
          <ModalFooter>
            {pipeDialog?.confirm && (
              <Button
                isDisabled={saving}
                variant="flat"
                onPress={() => setPipeDialog({ ...pipeDialog, confirm: false })}
              >
                返回修改
              </Button>
            )}
            <Button
              isDisabled={saving}
              variant="flat"
              onPress={closePipeDialog}
            >
              取消
            </Button>
            <Button
              color={pipeDialog?.confirm ? "warning" : "primary"}
              isDisabled={
                pipeDialog?.loading ||
                (pipeDialog?.mode !== "create" && !pipeDialog?.etag)
              }
              isLoading={saving}
              onPress={() => void savePipe()}
            >
              {pipeDialog?.confirm ? "确认保存" : "下一步"}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      <LeiKwanProgressModal
        pipeId={progressPipeId}
        onClose={() => setProgressPipeId(null)}
      />
    </AnimatedPage>
  );
}
