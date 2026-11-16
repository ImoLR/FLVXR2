import type {
  LeiKwanRule,
  LeiKwanRuleList,
  LeiKwanRuleOptions,
} from "@/api/leikwan-line";

import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, Copy, Plus } from "lucide-react";
import toast from "react-hot-toast";

import {
  createLeiKwanRule,
  deleteLeiKwanRule,
  getLeiKwanRuleLink,
  getLeiKwanRuleOptions,
  getLeiKwanRules,
  newLeiKwanRequestId,
  retryLeiKwanBackup,
  updateLeiKwanRule,
} from "@/api/leikwan-line";
import {
  ExpandActions,
  ExpandChip,
  ExpandField,
  ExpandRow,
  useExpandOne,
} from "@/components/expand-row";
import {
  TreeGroupChevron,
  TreeGroupFooter,
  TreeGroupHeader,
  treeGroupHeaderClass,
  treeGroupWrapperClass,
} from "@/components/tree-group";
import { useH5Mode } from "@/hooks/useH5Mode";
import { Pill } from "@/pages/landing/pill";
import {
  LabeledSwitch,
  LineChip,
  QuotaPill,
  fmtBytes,
} from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// Plan 266: 利群 rules on the 规则 page, added above the normal groups and only
// when there is something to show (a 利群 grant or 利群 rules) — the page is
// unchanged for everybody else. One group per 利群专线, same look as the other
// rule groups; H5 uses the shared ＋/− row. Users see tunnel, landing, entry
// address, the 备线 chip and 复制链接; admins also see the LeiKwan forward,
// owner, 预热 and the 备线 details.

async function copy(text: string, what: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast.success(`已复制${what}`);
  } catch {
    const ta = document.createElement("textarea");

    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand("copy");
    ta.remove();
    toast.success(`已复制${what}`);
  }
}

const landingText = (r: LeiKwanRule) =>
  r.landing.kind === "node"
    ? `${r.landing.serverName || "落地"} · ${r.landing.name || ""}`
    : `${r.landing.host}:${r.landing.port}`;

function BackupChip({ rule, admin }: { rule: LeiKwanRule; admin: boolean }) {
  if (rule.backupError)
    return (
      <Pill
        icon={<AlertTriangle className="text-red-600" size={12} />}
        title={rule.backupError}
        tone="amber"
      >
        备线未建好
      </Pill>
    );
  if (!rule.backupOn) return null;
  const t = rule.backup?.tunnelName || "";
  const short = /深港ixp ?1/.test(t) ? "深港ixp1" : "";

  return (
    <Pill
      title={
        admin && rule.backup
          ? `${rule.backup.name} · ${rule.backup.tunnelName} · 入口 ${rule.backup.entry} → ${rule.backup.remoteAddr}`
          : "日志快切时自动走备线，地址不变"
      }
      tone="green"
    >
      备线{short ? ` ${short}` : ""}
    </Pill>
  );
}

export function LeiKwanRulesSection() {
  const h5 = useH5Mode();
  const [data, setData] = useState<LeiKwanRuleList | null>(null);
  const [collapsed, setCollapsed] = useState<Record<number, boolean>>({});
  const [dialog, setDialog] = useState<{
    mode: "create" | "landing";
    rule?: LeiKwanRule;
  } | null>(null);
  const [del, setDel] = useState<LeiKwanRule | null>(null);
  const [keep, setKeep] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const res = await getLeiKwanRules();

    if (res.code === 0) setData(res.data);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const groups = useMemo(() => {
    const out = new Map<
      number,
      { name: string; route: string; rules: LeiKwanRule[] }
    >();

    (data?.rules || []).forEach((r) => {
      const g = out.get(r.lineId) || {
        name: r.lineName,
        route: r.routeLabel,
        rules: [],
      };

      g.rules.push(r);
      out.set(r.lineId, g);
    });

    return Array.from(out.entries());
  }, [data]);
  const allIds = useMemo(() => (data?.rules || []).map((r) => r.id), [data]);
  const expand = useExpandOne(allIds);

  if (!data || (!data.grant && data.rules.length === 0 && !data.admin))
    return null;
  if (data.admin && data.rules.length === 0 && !(data.lines || []).length)
    return null;

  const isCollapsed = (id: number) => collapsed[id] ?? true;
  const link = async (r: LeiKwanRule) => {
    const res = await getLeiKwanRuleLink(r.id);

    if (res.code !== 0) toast.error(res.msg || "复制失败");
    else if (res.data.url) await copy(res.data.url, "链接");
    else {
      await copy(res.data.address, "入口地址");
      if (res.data.note) toast(res.data.note);
    }
  };
  const remove = async () => {
    if (!del) return;
    setBusy(true);
    const res = await deleteLeiKwanRule(del.id, keep);

    setBusy(false);
    if (res.code !== 0) {
      toast.error(res.msg || "删除失败");

      return;
    }
    toast.success(keep ? "已解除登记" : "已删除（利群转发和备线一起删了）");
    if (res.data?.backupNote) toast(res.data.backupNote);
    setDel(null);
    void load();
  };
  const retry = async (r: LeiKwanRule) => {
    const res = await retryLeiKwanBackup(r.id);

    if (res.code !== 0) toast.error(res.msg || "重试失败");
    else toast.success("备线已建好");
    void load();
  };
  const actions = (r: LeiKwanRule) => (
    <>
      <Button
        color="secondary"
        size="sm"
        variant="flat"
        onPress={() => void link(r)}
      >
        复制链接
      </Button>
      <Button
        color="primary"
        size="sm"
        variant="flat"
        onPress={() => setDialog({ mode: "landing", rule: r })}
      >
        换落地
      </Button>
      {data.admin && r.backupError && (
        <Button
          color="warning"
          size="sm"
          variant="flat"
          onPress={() => void retry(r)}
        >
          重试备线
        </Button>
      )}
      <Button
        color="danger"
        size="sm"
        variant="flat"
        onPress={() => {
          setKeep(false);
          setDel(r);
        }}
      >
        删除
      </Button>
    </>
  );
  const adminMeta = (r: LeiKwanRule) =>
    data.admin && (
      <>
        <Pill tone="purple">利群转发 #{r.forwardId}</Pill>
        <Pill tone="slate">用户 {r.userName}</Pill>
        {r.accel && (
          <Pill
            title={r.accelOverride?.note}
            tone={r.accel.warmMain > 0 ? "green" : "slate"}
          >
            预热 {r.accel.warmMain > 0 ? r.accel.warmMain : "关"}
          </Pill>
        )}
        {r.source === "import" && <Pill tone="slate">导入</Pill>}
      </>
    );
  const attention = (data.rules || []).some((r) => r.backupError);

  return (
    <div className="mb-5 space-y-2" data-leikwan-rules="">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="text-sm font-semibold">利群汇聚</span>
          <LineChip />
          {attention && data.admin && (
            <AlertTriangle
              aria-label="有备线未建好"
              className="text-red-700"
              size={16}
            />
          )}
          {data.grant && (
            <span className="text-xs text-default-500">
              入口 {data.grant.mainEntryName}（授权时指定）· 备线{" "}
              {data.grant.backupLine ? "开" : "关"}
            </span>
          )}
        </div>
        {(data.grant || data.admin) && (
          <Button
            color="success"
            size="sm"
            startContent={<Plus size={15} />}
            variant="flat"
            onPress={() => setDialog({ mode: "create" })}
          >
            新增利群规则
          </Button>
        )}
      </div>
      {groups.length === 0 && (
        <div className="rounded-lg border border-dashed border-divider px-4 py-5 text-center text-sm text-default-500">
          还没有利群规则，点「新增利群规则」
        </div>
      )}
      {groups.map(([lineId, g]) => {
        const c = isCollapsed(lineId);

        return (
          <div
            key={lineId}
            className={treeGroupWrapperClass(c)}
            data-leikwan-line={g.name}
          >
            <TreeGroupHeader
              className={treeGroupHeaderClass(c)}
              collapsed={c}
              onToggle={() => setCollapsed((s) => ({ ...s, [lineId]: !c }))}
            >
              <div className="flex min-w-0 items-center gap-2">
                <TreeGroupChevron collapsed={c} />
                <div className="flex min-w-0 flex-col gap-1 md:flex-row md:items-center md:gap-2">
                  <span className="truncate text-sm font-semibold">
                    {g.name}
                  </span>
                  <div className="flex flex-wrap items-center gap-1.5">
                    <LineChip />
                    <span className="shrink-0 whitespace-nowrap rounded bg-default-100 px-1.5 py-0.5 text-[10px] font-normal text-default-500">
                      {g.route}
                    </span>
                    {data.admin && g.rules.some((r) => r.backupError) && (
                      <AlertTriangle className="text-red-700" size={14} />
                    )}
                  </div>
                </div>
              </div>
              <span className="shrink-0 rounded bg-default-100 px-1.5 py-0.5 text-[10px] text-default-500">
                {g.rules.length} 条
              </span>
            </TreeGroupHeader>
            {!c && (
              <div
                className={
                  h5 ? "divide-y divide-divider" : "divide-y divide-divider"
                }
              >
                {g.rules.map((r) =>
                  h5 ? (
                    <ExpandRow
                      key={r.id}
                      chip={
                        <ExpandChip>
                          {r.landing.protocol === "ss2022"
                            ? "SS2022"
                            : r.landing.kind === "manual"
                              ? "手动"
                              : r.landing.protocol || "利群"}
                        </ExpandChip>
                      }
                      name={
                        <span className="inline-flex items-center gap-1">
                          {r.backupError && data.admin && (
                            <AlertTriangle className="text-red-700" size={13} />
                          )}
                          {r.name}
                          {r.userExpired && <Pill tone="red">已到期</Pill>}
                        </span>
                      }
                      open={expand.open === r.id}
                      status={r.backupOn ? "ok" : undefined}
                      statusTitle="备线已开"
                      onToggle={() => expand.toggle(r.id)}
                    >
                      <ExpandField label="入口">
                        <span className="font-mono">
                          {r.entry?.address || "授权没有主入口"}
                        </span>
                        <span className="text-default-500">
                          {" "}
                          · {r.entry?.nodeName}
                        </span>
                      </ExpandField>
                      <ExpandField label="落地">{landingText(r)}</ExpandField>
                      <ExpandField label="备线">
                        <BackupChip admin={data.admin} rule={r} />
                        {!r.backupOn && !r.backupError && "没开"}
                      </ExpandField>
                      {data.admin && (
                        <ExpandField label="管理">
                          <div className="flex flex-wrap gap-1">
                            {adminMeta(r)}
                          </div>
                        </ExpandField>
                      )}
                      <ExpandActions>{actions(r)}</ExpandActions>
                    </ExpandRow>
                  ) : (
                    <div
                      key={r.id}
                      className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3"
                      data-leikwan-rule={r.name}
                    >
                      <div className="min-w-[180px] flex-1">
                        <div className="flex flex-wrap items-center gap-1.5">
                          {r.backupError && data.admin && (
                            <AlertTriangle className="text-red-700" size={15} />
                          )}
                          <span className="font-semibold">{r.name}</span>
                          <Pill tone="cyan">主线</Pill>
                          {r.userExpired && (
                            <Pill
                              title="用户已到期：不能新建利群规则"
                              tone="red"
                            >
                              已到期
                            </Pill>
                          )}
                          <BackupChip admin={data.admin} rule={r} />
                        </div>
                        <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-default-500">
                          {g.name}
                          {data.admin && adminMeta(r)}
                        </div>
                      </div>
                      <div className="min-w-[200px]">
                        <div className="text-[11px] text-default-400">
                          入口 · {r.entry?.nodeName || "—"}
                        </div>
                        <button
                          className="flex items-center gap-1 font-mono text-sm hover:text-primary"
                          type="button"
                          onClick={() =>
                            r.entry && void copy(r.entry.address, "入口地址")
                          }
                        >
                          {r.entry?.address || "授权没有主入口"}
                          {r.entry && (
                            <Copy className="text-default-400" size={12} />
                          )}
                        </button>
                      </div>
                      <div className="min-w-[180px]">
                        <div className="text-[11px] text-default-400">落地</div>
                        <div className="text-sm">{landingText(r)}</div>
                        {data.admin && r.landingHint && (
                          <div className="max-w-[260px] text-[11px] text-amber-600 dark:text-amber-300">
                            {r.landingHint}
                          </div>
                        )}
                        {data.admin && r.backup && (
                          <div className="text-[11px] text-default-400">
                            备线 #{r.backup.forwardId}{" "}
                            {r.backup.mode === "linked"
                              ? "（导入链接）"
                              : "（自动）"}{" "}
                            ·{" "}
                            {fmtBytes(
                              (r.backup.inFlow || 0) + (r.backup.outFlow || 0),
                            )}
                            {r.backup.sharedWith &&
                            r.backup.sharedWith.length > 0
                              ? ` · 跟 ${r.backup.sharedWith.join("、")} 共用`
                              : ""}
                          </div>
                        )}
                      </div>
                      <div className="flex flex-wrap items-center gap-1.5">
                        {actions(r)}
                      </div>
                    </div>
                  ),
                )}
              </div>
            )}
            {!c && (
              <TreeGroupFooter
                name={g.name}
                onToggle={() => setCollapsed((s) => ({ ...s, [lineId]: true }))}
              />
            )}
          </div>
        );
      })}
      <LeiKwanRuleDialog
        admin={data.admin}
        mode={dialog?.mode || "create"}
        open={!!dialog}
        rule={dialog?.rule}
        onClose={() => setDialog(null)}
        onSaved={() => void load()}
      />
      <Modal
        isOpen={!!del}
        placement="center"
        size="md"
        onClose={() => setDel(null)}
      >
        <ModalContent>
          <ModalHeader>删除利群规则</ModalHeader>
          <ModalBody className="space-y-2 text-sm">
            <p>
              删除「{del?.name}」：利群那边的转发一起删掉，释放 1
              个利群名额；面板自动建的备线也一起删。
            </p>
            {data.admin && del?.source === "import" && (
              <LabeledSwitch isSelected={keep} onValueChange={setKeep}>
                只解除登记（利群转发和链接的备线都保留）
              </LabeledSwitch>
            )}
          </ModalBody>
          <ModalFooter>
            <Button color="default" variant="flat" onPress={() => setDel(null)}>
              取消
            </Button>
            <Button
              color="danger"
              isLoading={busy}
              variant="flat"
              onPress={() => void remove()}
            >
              {keep ? "解除登记" : "删除"}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  );
}

function LeiKwanRuleDialog({
  open,
  mode,
  rule,
  admin,
  onClose,
  onSaved,
}: {
  open: boolean;
  mode: "create" | "landing";
  rule?: LeiKwanRule;
  admin: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [opts, setOpts] = useState<LeiKwanRuleOptions | null>(null);
  const [name, setName] = useState("");
  const [lineId, setLineId] = useState("");
  const [landing, setLanding] = useState("");
  const [host, setHost] = useState("");
  const [port, setPort] = useState("");
  const [requestId, setRequestId] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setOpts(null);
    setName(rule?.name || "");
    setLineId(rule ? String(rule.lineId) : "");
    setLanding(
      rule?.landing.kind === "node"
        ? String(rule.landing.landingNodeId)
        : rule
          ? "manual"
          : "",
    );
    setHost(rule?.landing.kind === "manual" ? rule.landing.host : "");
    setPort(rule?.landing.kind === "manual" ? String(rule.landing.port) : "");
    setRequestId(newLeiKwanRequestId());
    void getLeiKwanRuleOptions(rule?.userId || 0).then((res) => {
      if (res.code === 0) {
        setOpts(res.data);
        if (!rule && res.data.lines.length)
          setLineId((v) => v || String(res.data.lines[0].id));
      }
    });
  }, [open, rule]);

  const full =
    !!opts && opts.quota.limit > 0 && opts.quota.active >= opts.quota.limit;
  const save = async () => {
    const manual = landing === "manual";
    const target = manual
      ? { targetHost: host.trim(), targetPort: parseInt(port, 10) || 0 }
      : { landingNodeId: parseInt(landing, 10) || 0 };

    if (
      mode === "create" &&
      (!name.trim() || !lineId || (!manual && !target.landingNodeId))
    ) {
      toast.error("请填写规则名称、选择隧道和落地");

      return;
    }
    setSaving(true);
    const res =
      mode === "create"
        ? await createLeiKwanRule({
            lineId: parseInt(lineId, 10),
            name: name.trim(),
            requestId,
            ...target,
          })
        : await updateLeiKwanRule({
            id: rule!.id,
            name: name.trim(),
            changeTarget: true,
            ...target,
          });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success(mode === "create" ? "利群规则已建好" : "已换落地");
    (res.data?.warnings || []).forEach((w) => toast.error(w));
    onSaved();
    onClose();
  };

  return (
    <Modal
      classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-lg rounded-2xl" }}
      isOpen={open}
      placement="center"
      scrollBehavior="inside"
      size="lg"
      onClose={onClose}
    >
      <ModalContent>
        <ModalHeader>
          {mode === "create" ? "新增规则" : `换落地 · ${rule?.name}`}
        </ModalHeader>
        <ModalBody className="space-y-4">
          {opts?.grantError && mode === "create" && (
            <p className="rounded-lg bg-amber-500/15 px-3 py-2 text-sm text-amber-700 dark:text-amber-300">
              {opts.grantError}
            </p>
          )}
          <Input
            label="规则名称"
            placeholder="我的广港"
            value={name}
            variant="bordered"
            onChange={(e) => setName(e.target.value)}
          />
          {mode === "create" && (
            <Select
              label="选择隧道"
              placeholder="选择利群专线"
              selectedKeys={lineId ? [lineId] : []}
              variant="bordered"
              onSelectionChange={(keys) =>
                setLineId(String(Array.from(keys)[0] || ""))
              }
            >
              {(opts?.lines || []).map((l) => (
                <SelectItem
                  key={String(l.id)}
                  description={`利群专线 · ${l.routeLabel}`}
                >
                  {l.name}
                </SelectItem>
              ))}
            </Select>
          )}
          <Select
            label="落地"
            placeholder="选择我的落地节点"
            selectedKeys={landing ? [landing] : []}
            variant="bordered"
            onSelectionChange={(keys) =>
              setLanding(String(Array.from(keys)[0] || ""))
            }
          >
            {[
              ...(opts?.landings || []).map((n) => (
                <SelectItem
                  key={String(n.id)}
                  description={`${n.protocol === "ss2022" ? "SS2022" : n.protocol} · 自己的密钥`}
                >
                  {`${n.serverName} · ${n.name}`}
                </SelectItem>
              )),
              ...(admin
                ? [
                    <SelectItem key="manual" description="仅管理员">
                      手动填写 地址:端口
                    </SelectItem>,
                  ]
                : []),
            ]}
          </Select>
          {landing === "manual" && admin && (
            <div className="grid grid-cols-3 gap-2">
              <Input
                className="col-span-2"
                label="落地地址"
                size="sm"
                value={host}
                variant="bordered"
                onChange={(e) => setHost(e.target.value)}
              />
              <Input
                inputMode="numeric"
                label="端口"
                size="sm"
                value={port}
                variant="bordered"
                onChange={(e) => setPort(e.target.value.replace(/\D/g, ""))}
              />
            </div>
          )}
          {opts && opts.landings.length === 0 && (
            <p className="-mt-2 text-xs text-default-500">
              还没有自己的落地节点：先到「落地」页新建一个 SS2022
              节点（自己的密钥，别的规则用不了）。
            </p>
          )}
          {mode === "create" && (
            <div className="space-y-1.5 rounded-xl bg-default-100/60 p-3 text-sm dark:bg-default-50/40">
              <div className="flex items-start gap-2">
                <span className="w-16 shrink-0 text-default-500">入口</span>
                {opts?.grant ? (
                  <span>
                    主 <b>{opts.grant.mainEntryName}</b>
                    <span className="text-xs text-default-500">
                      （授权时指定）
                    </span>
                  </span>
                ) : (
                  <span className="text-default-400">—</span>
                )}
              </div>
              <div className="flex items-start gap-2">
                <span className="w-16 shrink-0 text-default-500">入口端口</span>
                <span>由利群分配</span>
              </div>
              <div className="flex items-start gap-2">
                <span className="w-16 shrink-0 text-default-500">备线</span>
                <span>
                  {opts?.grant?.backupLine
                    ? "自动建一条（深港ixp1 → 同一落地）"
                    : "授权没开备线"}
                </span>
              </div>
              <div className="flex flex-wrap items-center gap-2 pt-1">
                <Pill tone={full ? "amber" : "purple"}>
                  将占用 1 个利群名额
                </Pill>
                <QuotaPill quota={opts?.quota} />
              </div>
              {full && (
                <p className="text-xs text-amber-700 dark:text-amber-300">
                  名额已满，利群名额提升前建不了新规则。
                </p>
              )}
            </div>
          )}
          {mode === "landing" && (
            <p className="text-xs text-default-500">
              换落地会改利群那边的目标；面板自动建的备线跟着换到新落地。入口地址不变。
            </p>
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="success"
            isDisabled={mode === "create" && (full || !!opts?.grantError)}
            isLoading={saving}
            variant="flat"
            onPress={() => void save()}
          >
            {mode === "create" ? "建规则" : "保存"}
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
