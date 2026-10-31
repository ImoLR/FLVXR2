import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link2 } from "lucide-react";
import toast from "react-hot-toast";

import { Pill } from "./pill";

import { LandingNode } from "@/api/landing";
import {
  ChainProbeResult,
  ExternalList,
  ExternalNode,
  ExternalPreview,
  SubFetchResult,
  chainProbe,
  chainSave,
  externalCreate,
  externalDelete,
  externalList,
  externalParse,
  externalRename,
  subscriptionFetch,
  subscriptionImport,
} from "@/api/landing-external";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
import { Input, Textarea } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Radio, RadioGroup } from "@/shadcn-bridge/heroui/radio";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import { Tab, Tabs } from "@/shadcn-bridge/heroui/tabs";

// Plan 177 「链式代理」: a landing node sends all its traffic out through one
// of its owner's own external nodes. Small on purpose: one dialog with
// 直连 / 外部节点, a dropdown + 添加 (paste or subscription), a tiny
// 管理 list and 检测.

const SHARED_UNSUPPORTED = "此服务器不支持该协议";

/** Card chip: 链式 → 名称 · 协议 (teal), 同步中 (amber) or ⚠ 未生效 (red). */
export function ChainPill({ node }: { node: LandingNode }) {
  const chain = node.chain;

  if (!chain) return null;
  const label = `链式 → ${chain.name || "外部节点"}${chain.protocolLabel ? ` · ${chain.protocolLabel}` : ""}`;

  if (chain.state === "error") {
    return (
      <Pill title={chain.reason} tone="red">
        ⚠ 链式代理未生效
      </Pill>
    );
  }

  return (
    <Pill
      icon={<Link2 aria-hidden className="h-3 w-3 shrink-0" />}
      title={label}
      tone={chain.state === "syncing" ? "amber" : "teal"}
    >
      {label}
      {chain.state === "syncing" ? " · 同步中" : ""}
    </Pill>
  );
}

/** The ⚠ reason line under the card chips. */
export function ChainNote({ node }: { node: LandingNode }) {
  if (node.chain?.state !== "error" || !node.chain.reason) return null;

  return (
    <span className="w-full text-xs text-danger">
      链式代理未生效：{node.chain.reason}
    </span>
  );
}

function probeLine(res: ChainProbeResult) {
  if (!res.probed) {
    return (
      <span className="text-default-500">{res.message || "无法检测"}</span>
    );
  }
  if (res.ok) {
    return (
      <span className="text-success-600 dark:text-success-400">
        ✓ {res.server} → {res.target} 可达
        {res.latencyMs != null ? ` ${res.latencyMs} ms` : ""}
      </span>
    );
  }

  return (
    <span className="text-danger">
      ✗ {res.server} → {res.target} 不通：{res.message || "连接失败"}
    </span>
  );
}

export function ChainProxyDialog({
  node,
  admin,
  onClose,
  onSaved,
}: {
  node: LandingNode;
  admin: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const support = node.chainSupport || {
    supported: true,
    reason: "",
    shared: false,
  };
  const owner = admin ? node.userId : undefined;
  const [list, setList] = useState<ExternalList | null>(null);
  const [loadError, setLoadError] = useState("");
  const [mode, setMode] = useState(node.chainExternalId ? "chain" : "direct");
  const [selected, setSelected] = useState(
    node.chainExternalId ? String(node.chainExternalId) : "",
  );
  const [probe, setProbe] = useState<ChainProbeResult | null>(null);
  const [probing, setProbing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [adding, setAdding] = useState(false);
  const [managing, setManaging] = useState(false);

  const load = useCallback(async () => {
    const res = await externalList(owner);

    if (res.code === 0 && res.data) {
      setList(res.data);
      setLoadError("");
    } else setLoadError(res.msg || "读取外部节点失败");
  }, [owner]);

  useEffect(() => {
    void load();
  }, [load]);

  const items = list?.items || [];
  const usable = (item: ExternalNode) => !support.shared || item.sharedOk;
  const current = items.find((item) => String(item.id) === selected);

  useEffect(() => {
    // Pick the first usable node when switching to 外部节点 with none chosen.
    if (mode === "chain" && !selected && items.length) {
      const first = items.find(usable);

      if (first) setSelected(String(first.id));
    }
  }, [mode, items.length]);

  useEffect(() => setProbe(null), [selected]);

  const chainBlocked = !support.supported;
  const unchanged =
    (mode === "direct" && !node.chainExternalId) ||
    (mode === "chain" && String(node.chainExternalId || "") === selected);

  const runProbe = async () => {
    if (!current) return;
    setProbing(true);
    const res = await chainProbe(node.id, current.id);

    setProbing(false);
    if (res.code !== 0 || !res.data) {
      toast.error(res.msg || "检测失败");

      return;
    }
    setProbe(res.data);
  };

  const save = async () => {
    const externalId = mode === "chain" ? Number(selected) : 0;

    if (mode === "chain" && !current) {
      toast.error("请选择外部节点");

      return;
    }
    setSaving(true);
    const res = await chainSave(node.id, externalId);

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    if (res.data?.probe) setProbe(res.data.probe);
    toast.success(
      externalId
        ? support.shared
          ? "已保存，正在同步到妙妙屋服务器"
          : "已开启链式代理"
        : "已改回直连",
    );
    if (res.data?.syncError) toast(res.data.syncError);
    onSaved();
    onClose();
  };

  return (
    <>
      <Modal isOpen scrollBehavior="inside" size="lg" onClose={onClose}>
        <ModalContent className="flvx-landing">
          <ModalHeader className="flex flex-col gap-1">
            <span>链式代理 — {node.name}</span>
            <span className="text-xs font-normal text-default-500">
              {node.serverName} · 只作用于这个节点
            </span>
          </ModalHeader>
          <ModalBody data-chain-dialog className="space-y-4">
            {chainBlocked && (
              <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10">
                {support.reason || "该服务器暂不能使用链式代理"}
              </p>
            )}
            <RadioGroup
              label="出站"
              orientation="horizontal"
              value={mode}
              onValueChange={(value) => {
                if (value === "chain" && chainBlocked) return;
                setMode(value);
              }}
            >
              <Radio value="direct">直连（默认）</Radio>
              {!chainBlocked && <Radio value="chain">走我的外部节点</Radio>}
            </RadioGroup>

            {mode === "chain" && !chainBlocked && (
              <div className="space-y-3">
                {loadError && (
                  <p className="text-xs text-danger">{loadError}</p>
                )}
                {!list && !loadError && <Spinner size="sm" />}
                {list && (
                  <div className="flex items-end gap-2">
                    <Select
                      isSearchable
                      className="min-w-0 flex-1"
                      disabledKeys={items
                        .filter((item) => !usable(item))
                        .map((item) => String(item.id))}
                      label="外部节点"
                      placeholder={
                        items.length ? "选择外部节点" : "还没有外部节点，先添加"
                      }
                      searchPlaceholder="搜索名称或地址"
                      selectedKeys={selected ? [selected] : []}
                      variant="bordered"
                      onSelectionChange={(keys) =>
                        setSelected(String(Array.from(keys)[0] || ""))
                      }
                    >
                      {items.map((item) => (
                        <SelectItem
                          key={String(item.id)}
                          description={
                            usable(item) ? undefined : SHARED_UNSUPPORTED
                          }
                          textValue={`${item.name} ${item.host}`}
                        >
                          {item.name} · {item.protocolLabel} · {item.host}:
                          {item.port}
                        </SelectItem>
                      ))}
                    </Select>
                    <Button
                      color="success"
                      variant="flat"
                      onPress={() => setAdding(true)}
                    >
                      + 添加
                    </Button>
                  </div>
                )}
                {list && (
                  <div className="flex flex-wrap items-center gap-2 text-xs">
                    <Button
                      color="secondary"
                      isDisabled={!current || probing}
                      isLoading={probing}
                      size="sm"
                      variant="flat"
                      onPress={() => void runProbe()}
                    >
                      检测
                    </Button>
                    {probe && probeLine(probe)}
                    <span className="ml-auto" />
                    <Button
                      color="warning"
                      size="sm"
                      variant="flat"
                      onPress={() => setManaging((v) => !v)}
                    >
                      {managing ? "收起管理" : "管理"}（{list.count}
                      {list.cap ? `/${list.cap}` : ""}）
                    </Button>
                  </div>
                )}
                {managing && list && (
                  <ExternalManageList
                    items={items}
                    owner={owner}
                    onChanged={() => void load()}
                  />
                )}
                {current?.udpOnly && support.shared === false && (
                  <p className="text-xs text-default-500">
                    {current.protocolLabel} 走
                    UDP，无法预先检测，保存后请用客户端实际测试。
                  </p>
                )}
              </div>
            )}
            {node.chain && mode === "chain" && (
              <p className="text-xs text-default-500">
                当前：链式 → {node.chain.name}
                {node.chain.state === "applied"
                  ? " · 已生效"
                  : node.chain.state === "syncing"
                    ? " · 正在同步"
                    : ` · 未生效：${node.chain.reason || ""}`}
              </p>
            )}
            <p className="text-xs text-default-500">
              开启后此节点的流量全部从外部节点出去；防滥用等拦截规则仍然生效。外部节点不通时连接会失败，不会改走直连。计费不变。
            </p>
          </ModalBody>
          <ModalFooter>
            <Button color="danger" variant="flat" onPress={onClose}>
              取消
            </Button>
            <Button
              color="primary"
              isDisabled={
                unchanged || (mode === "chain" && (!current || chainBlocked))
              }
              isLoading={saving}
              onPress={() => void save()}
            >
              保存
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      {adding && (
        <AddExternalDialog
          owner={owner}
          shared={support.shared}
          onAdded={async (added) => {
            await load();
            const pick = added.find((item) => !support.shared || item.sharedOk);

            if (pick) setSelected(String(pick.id));
          }}
          onClose={() => setAdding(false)}
        />
      )}
    </>
  );
}

function ExternalManageList({
  items,
  owner,
  onChanged,
}: {
  items: ExternalNode[];
  owner?: number;
  onChanged: () => void;
}) {
  const [editing, setEditing] = useState<number | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState<number | null>(null);

  if (!items.length) {
    return (
      <p className="rounded-lg border border-divider p-3 text-xs text-default-500">
        还没有外部节点。
      </p>
    );
  }

  const rename = async (item: ExternalNode) => {
    setBusy(item.id);
    const res = await externalRename(item.id, name, owner);

    setBusy(null);
    if (res.code !== 0) {
      toast.error(res.msg || "改名失败");

      return;
    }
    setEditing(null);
    onChanged();
  };
  const remove = async (item: ExternalNode) => {
    setBusy(item.id);
    const res = await externalDelete(item.id, owner);

    setBusy(null);
    if (res.code !== 0) {
      toast.error(res.msg || "删除失败", { duration: 6000 });

      return;
    }
    toast.success(`已删除「${item.name}」`);
    onChanged();
  };

  return (
    <div
      data-external-manage
      className="max-h-64 divide-y divide-divider overflow-auto rounded-lg border border-divider"
    >
      {items.map((item) => (
        <div key={item.id} className="flex flex-wrap items-center gap-2 p-2">
          <div className="min-w-0 flex-1">
            {editing === item.id ? (
              <Input
                aria-label="名称"
                maxLength={40}
                size="sm"
                value={name}
                variant="bordered"
                onChange={(e) => setName(e.target.value)}
              />
            ) : (
              <p className="truncate text-sm font-medium">{item.name}</p>
            )}
            <p className="truncate text-xs text-default-500">
              {item.protocolLabel} · {item.host}:{item.port}
              {item.inUse.length > 0 &&
                ` · 用于 ${item.inUse.map((u) => u.name).join("、")}`}
            </p>
          </div>
          {editing === item.id ? (
            <>
              <Button
                color="primary"
                isLoading={busy === item.id}
                size="sm"
                variant="flat"
                onPress={() => void rename(item)}
              >
                保存
              </Button>
              <Button
                color="danger"
                size="sm"
                variant="flat"
                onPress={() => setEditing(null)}
              >
                取消
              </Button>
            </>
          ) : (
            <>
              <Button
                color="primary"
                size="sm"
                variant="flat"
                onPress={() => {
                  setEditing(item.id);
                  setName(item.name);
                }}
              >
                改名
              </Button>
              <Button
                color="danger"
                isLoading={busy === item.id}
                size="sm"
                variant="flat"
                onPress={() => void remove(item)}
              >
                删除
              </Button>
            </>
          )}
        </div>
      ))}
    </div>
  );
}

export function AddExternalDialog({
  owner,
  shared,
  onAdded,
  onClose,
}: {
  owner?: number;
  shared: boolean;
  onAdded: (added: ExternalNode[]) => void | Promise<void>;
  onClose: () => void;
}) {
  const [tab, setTab] = useState("paste");

  return (
    <Modal isOpen scrollBehavior="inside" size="lg" onClose={onClose}>
      <ModalContent className="flvx-landing">
        <ModalHeader>添加外部节点</ModalHeader>
        <ModalBody data-external-add className="space-y-3">
          <Tabs
            aria-label="添加方式"
            selectedKey={tab}
            onSelectionChange={(key) => setTab(String(key))}
          >
            <Tab key="paste" title="粘贴链接">
              <PasteTab
                owner={owner}
                shared={shared}
                onAdded={onAdded}
                onClose={onClose}
              />
            </Tab>
            <Tab key="sub" title="从订阅导入">
              <SubscriptionTab
                owner={owner}
                shared={shared}
                onAdded={onAdded}
                onClose={onClose}
              />
            </Tab>
          </Tabs>
        </ModalBody>
      </ModalContent>
    </Modal>
  );
}

function PasteTab({
  owner,
  shared,
  onAdded,
  onClose,
}: {
  owner?: number;
  shared: boolean;
  onAdded: (added: ExternalNode[]) => void | Promise<void>;
  onClose: () => void;
}) {
  const [link, setLink] = useState("");
  const [name, setName] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [parsed, setParsed] = useState<ExternalPreview | null>(null);
  const [parseError, setParseError] = useState("");
  const [checking, setChecking] = useState(false);
  const [saving, setSaving] = useState(false);
  const seq = useRef(0);

  useEffect(() => {
    const value = link.trim();

    setParsed(null);
    setParseError("");
    if (!value) {
      setChecking(false);

      return;
    }
    const current = ++seq.current;

    setChecking(true);
    const timer = window.setTimeout(() => {
      void externalParse(value).then((res) => {
        if (current !== seq.current) return;
        setChecking(false);
        if (res.code === 0 && res.data) {
          setParsed(res.data);
          if (!nameTouched) setName(res.data.name || "");
        } else setParseError(res.msg || "链接无法解析");
      });
    }, 600);

    return () => window.clearTimeout(timer);
  }, [link]);

  const add = async () => {
    setSaving(true);
    const res = await externalCreate(link.trim(), name.trim(), owner);

    setSaving(false);
    if (res.code !== 0 || !res.data) {
      toast.error(res.msg || "添加失败");

      return;
    }
    toast.success(`已添加「${res.data.name}」`);
    await onAdded([res.data]);
    onClose();
  };

  return (
    <div className="space-y-3 pt-2">
      <Textarea
        description="支持 ss://（含 SS2022）、vless://、vmess://、trojan://、hysteria2://、socks5://、http(s)://"
        label="节点链接"
        maxRows={4}
        minRows={2}
        placeholder="粘贴你自己的节点链接，例如 vless://… 或 ss://…"
        value={link}
        variant="bordered"
        onChange={(e) => setLink(e.target.value)}
      />
      {checking && <p className="text-xs text-default-500">正在解析链接…</p>}
      {parseError && !checking && (
        <p className="text-xs text-danger">{parseError}</p>
      )}
      {parsed && !checking && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-divider p-2 text-sm">
          <span className="text-default-500">将连接到</span>
          <Pill tone="pink">{parsed.protocolLabel}</Pill>
          <span className="font-mono text-foreground">
            {parsed.summary.split(" · ").slice(1).join(" · ")}
          </span>
          {parsed.resolved && (
            <span className="text-xs text-default-500">
              （解析为 {parsed.resolved}）
            </span>
          )}
          {shared && !parsed.sharedOk && (
            <span className="w-full text-xs text-warning">
              {SHARED_UNSUPPORTED}：这台妙妙屋共享服务器只支持 SOCKS5 / HTTP /
              Shadowsocks / VLESS Reality，可以先添加，在 FLVX
              自有服务器上使用。
            </span>
          )}
        </div>
      )}
      <Input
        label="名称"
        maxLength={40}
        placeholder="默认用链接里的名称"
        value={name}
        variant="bordered"
        onChange={(e) => {
          setName(e.target.value);
          setNameTouched(true);
        }}
      />
      <div className="flex justify-end gap-2 pt-1">
        <Button color="danger" variant="flat" onPress={onClose}>
          取消
        </Button>
        <Button
          color="success"
          isDisabled={!parsed || checking}
          isLoading={saving}
          onPress={() => void add()}
        >
          添加
        </Button>
      </div>
    </div>
  );
}

function SubscriptionTab({
  owner,
  shared,
  onAdded,
  onClose,
}: {
  owner?: number;
  shared: boolean;
  onAdded: (added: ExternalNode[]) => void | Promise<void>;
  onClose: () => void;
}) {
  const [url, setURL] = useState("");
  const [fetching, setFetching] = useState(false);
  const [result, setResult] = useState<SubFetchResult | null>(null);
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [query, setQuery] = useState("");
  const [importing, setImporting] = useState(false);

  const selectable = (item: SubFetchResult["items"][number]) =>
    item.supported && !item.duplicate;
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    const items = result?.items || [];

    if (!q) return items;

    return items.filter((item) =>
      `${item.name} ${item.summary}`.toLowerCase().includes(q),
    );
  }, [result, query]);
  const visibleSelectable = visible.filter(selectable);
  const allPicked =
    visibleSelectable.length > 0 &&
    visibleSelectable.every((item) => picked.has(item.index));
  const room = result?.cap ? Math.max(0, result.cap - result.count) : Infinity;

  const fetchSub = async () => {
    setFetching(true);
    const res = await subscriptionFetch(url.trim(), owner);

    setFetching(false);
    if (res.code !== 0 || !res.data) {
      toast.error(res.msg || "获取失败");

      return;
    }
    setResult(res.data);
    setPicked(new Set());
    setQuery("");
  };

  const runImport = async () => {
    if (!result) return;
    setImporting(true);
    const res = await subscriptionImport(
      result.fetchId,
      Array.from(picked),
      owner,
    );

    setImporting(false);
    if (res.code !== 0 || !res.data) {
      toast.error(res.msg || "导入失败");

      return;
    }
    const d = res.data;
    const skipped: string[] = [];

    if (d.duplicates.length) skipped.push(`${d.duplicates.length} 个已添加过`);
    if (d.overCap) skipped.push(`${d.overCap} 个超出上限（最多 ${d.cap} 个）`);
    if (d.failed.length)
      skipped.push(
        `${d.failed.length} 个地址检查未通过（${d.failed
          .slice(0, 2)
          .map((f) => `${f.name}：${f.reason}`)
          .join("；")}${d.failed.length > 2 ? "…" : ""}）`,
      );
    if (d.imported) toast.success(`已导入 ${d.imported} 个外部节点`);
    if (skipped.length)
      toast(`跳过：${skipped.join("，")}`, { duration: 8000 });
    if (!d.imported && !skipped.length) toast.error("没有导入任何节点");
    if (d.imported) {
      await onAdded(d.items);
      onClose();
    }
  };

  return (
    <div data-sub-import className="space-y-3 pt-2">
      <div className="flex items-end gap-2">
        <Input
          className="min-w-0 flex-1"
          label="订阅地址"
          placeholder="https://…"
          value={url}
          variant="bordered"
          onChange={(e) => setURL(e.target.value)}
        />
        <Button
          color="secondary"
          isDisabled={!url.trim()}
          isLoading={fetching}
          variant="flat"
          onPress={() => void fetchSub()}
        >
          获取
        </Button>
      </div>
      <p className="text-xs text-default-500">
        只拉取一次，不保存订阅地址，也不会自动更新；支持 Base64 / 节点链接列表 /
        Clash YAML。
      </p>
      {result && (
        <>
          <p className="text-xs text-default-500">
            共 {result.items.length} 个节点
            {result.info > 0 &&
              `，已自动过滤 ${result.info} 条信息（剩余流量 / 到期等）`}
            {result.truncated && "，只显示前 500 个"}
            {result.cap > 0 &&
              ` · 还能添加 ${Math.max(0, result.cap - result.count)} 个`}
          </p>
          <div className="flex flex-wrap items-center gap-3">
            <Input
              aria-label="搜索"
              className="min-w-0 flex-1"
              placeholder="搜索名称或地址"
              size="sm"
              value={query}
              variant="bordered"
              onChange={(e) => setQuery(e.target.value)}
            />
            <Checkbox
              isDisabled={!visibleSelectable.length}
              isSelected={allPicked}
              onValueChange={(on) => {
                const next = new Set(picked);

                visibleSelectable.forEach((item) =>
                  on ? next.add(item.index) : next.delete(item.index),
                );
                setPicked(next);
              }}
            >
              全选
            </Checkbox>
          </div>
          <div
            data-sub-list
            className="max-h-72 divide-y divide-divider overflow-auto rounded-lg border border-divider"
          >
            {visible.map((item) => {
              const ok = selectable(item);
              const note = !item.supported
                ? item.reason
                : item.duplicate
                  ? `已添加（${item.duplicate}）`
                  : shared && item.sharedOk === false
                    ? `${SHARED_UNSUPPORTED}（可在 FLVX 自有服务器使用）`
                    : "";

              return (
                <div
                  key={item.index}
                  className={`flex items-start gap-2 p-2 ${ok ? "" : "opacity-50"}`}
                >
                  <Checkbox
                    aria-label={item.name}
                    isDisabled={!ok}
                    isSelected={picked.has(item.index)}
                    onValueChange={(on) => {
                      const next = new Set(picked);

                      if (on) next.add(item.index);
                      else next.delete(item.index);
                      setPicked(next);
                    }}
                  />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm">{item.name}</p>
                    <p className="truncate text-xs text-default-500">
                      {item.summary}
                    </p>
                    {note && (
                      <p
                        className={`text-xs ${!item.supported ? "text-danger" : "text-warning"}`}
                      >
                        {note}
                      </p>
                    )}
                  </div>
                </div>
              );
            })}
            {!visible.length && (
              <p className="p-3 text-center text-xs text-default-500">
                没有匹配的节点
              </p>
            )}
          </div>
          {picked.size > room && (
            <p className="text-xs text-warning">
              已选 {picked.size} 个，超出还能添加的 {room} 个，多出的会跳过。
            </p>
          )}
        </>
      )}
      <div className="flex justify-end gap-2 pt-1">
        <Button color="danger" variant="flat" onPress={onClose}>
          取消
        </Button>
        <Button
          color="success"
          isDisabled={!picked.size}
          isLoading={importing}
          onPress={() => void runImport()}
        >
          导入选中（{picked.size}）
        </Button>
      </div>
    </div>
  );
}
