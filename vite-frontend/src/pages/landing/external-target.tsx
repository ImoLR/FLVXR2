import { useCallback, useEffect, useRef, useState } from "react";
import toast from "react-hot-toast";

import {
  ExternalNode,
  externalList,
} from "@/api/landing-external";
import { AddExternalDialog } from "@/pages/landing/chain-proxy";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// Plan 284 「我的外部节点」 as a 转发 target: pick one of the owner's 外部节点
// (or paste a new one, saved into the same list, cap 20). On a normal tunnel
// the forward stays a plain port forward to that node's address; on a 线路机
// tunnel it is the plan 108 chain (the exit proxies to the node).

/** Why a tunnel's entry filter blocks an external node ("" = it passes). */
export function externalBlockedReason(
  blockedFilters: string[] | undefined,
  feature: string | undefined,
  protocolLabel: string,
) {
  if (!feature || !blockedFilters?.includes(feature)) return "";
  switch (feature) {
    case "tls":
      return `此隧道屏蔽了 TLS，${protocolLabel} 不能用`;
    case "quic":
      return `此隧道屏蔽了 TLS（含 QUIC），${protocolLabel} 不能用`;
    case "http":
      return `此隧道屏蔽了 HTTP，${protocolLabel} 不能用`;
    case "socks":
      return `此隧道屏蔽了 SOCKS，${protocolLabel} 不能用`;
    default:
      return `此隧道屏蔽了其它协议，${protocolLabel} 不能用`;
  }
}

/** Short names of what a tunnel lets through (专线 hint). */
function passHint(blockedFilters: string[] | undefined) {
  const b = blockedFilters || [];

  if (!b.length) return "";
  const names = [
    b.includes("tls") && "TLS",
    b.includes("http") && "HTTP",
    b.includes("socks") && "SOCKS",
  ].filter(Boolean);

  if (!names.length) return "";

  return `此隧道屏蔽了 ${names.join(" / ")}，只能选 SS、VLESS Encryption 这类协议的节点`;
}

export function ExternalTargetPicker({
  userId,
  value,
  onChange,
  blockedFilters,
  chain = false,
  onListLoaded,
}: {
  userId: number;
  value?: number;
  onChange: (id: number | undefined, node?: ExternalNode) => void;
  /** tunnel entry filter kinds (normal tunnels only) */
  blockedFilters?: string[];
  /** 线路机 chain: the exit connects to the node, the entry filter is not involved */
  chain?: boolean;
  /** count / cap of the list (the 线路机 fallback paste box needs it) */
  onListLoaded?: (count: number, cap: number) => void;
}) {
  const [items, setItems] = useState<ExternalNode[]>([]);
  const [cap, setCap] = useState(20);
  const [loading, setLoading] = useState(false);
  const [adding, setAdding] = useState(false);
  // The callback may be a new function every render: keep it out of load's deps.
  const listLoaded = useRef(onListLoaded);

  listLoaded.current = onListLoaded;

  const reasonOf = useCallback(
    (node: ExternalNode) =>
      chain
        ? ""
        : externalBlockedReason(
            blockedFilters,
            node.filterFeature,
            node.protocolLabel,
          ),
    [blockedFilters, chain],
  );

  const load = useCallback(async () => {
    if (userId <= 0) return [] as ExternalNode[];
    setLoading(true);
    const res = await externalList(userId);

    setLoading(false);
    if (res.code !== 0 || !res.data) {
      toast.error(res.msg || "外部节点读取失败");

      return [] as ExternalNode[];
    }
    setItems(res.data.items || []);
    setCap(res.data.cap);
    listLoaded.current?.(res.data.count, res.data.cap);

    return res.data.items || [];
  }, [userId]);

  useEffect(() => {
    void load();
  }, [load]);

  const selected = items.find((node) => node.id === value);
  const selectedReason = selected ? reasonOf(selected) : "";
  const full = cap > 0 && items.length >= cap;
  const hint = chain ? "" : passHint(blockedFilters);

  return (
    <div className="space-y-2" data-external-target>
      <Select
        classNames={{
          base: "min-w-0 max-w-full",
          trigger: "max-w-full [&>span]:whitespace-normal",
          listbox: "static min-w-0 max-w-full !max-h-60",
        }}
        description={
          chain
            ? "经隧道出口连接你的节点；只显示这条转发所属用户的外部节点"
            : "只显示这条转发所属用户的外部节点"
        }
        disabledKeys={items
          .filter((node) => reasonOf(node))
          .map((node) => String(node.id))}
        isDisabled={loading}
        label="我的外部节点"
        listboxVariant="grouped"
        placeholder={
          loading
            ? "正在加载…"
            : items.length
              ? "请选择外部节点"
              : "还没有外部节点，点下面「＋ 粘贴新节点」"
        }
        selectedKeys={selected ? [String(selected.id)] : []}
        variant="bordered"
        onSelectionChange={(keys) => {
          const id = Number(Array.from(keys)[0]);
          const node = items.find((item) => item.id === id);

          onChange(node ? node.id : undefined, node);
        }}
      >
        {items.map((node) => (
          <SelectItem
            key={String(node.id)}
            description={
              reasonOf(node) || `${node.host}:${node.port}`
            }
            textValue={node.name}
          >
            {node.name} · {node.protocolLabel}
          </SelectItem>
        ))}
      </Select>
      {hint && <p className="text-xs text-default-500">{hint}</p>}
      {selectedReason && (
        <p className="text-sm text-danger">{selectedReason}</p>
      )}
      {selected && !chain && (
        <div
          className="flex min-w-0 flex-wrap items-center gap-2 rounded-lg border border-divider px-3 py-2 text-sm"
          data-external-filled
        >
          <span className="shrink-0 text-default-500">目标地址</span>
          <span className="min-w-0 break-all font-mono text-foreground">
            {selected.host.includes(":") ? `[${selected.host}]` : selected.host}
            :{selected.port}
          </span>
          <span className="text-xs text-default-400">跟随外部节点，不能改</span>
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          color="success"
          isDisabled={loading || userId <= 0 || full}
          size="sm"
          variant="flat"
          onPress={() => setAdding(true)}
        >
          ＋ 粘贴新节点
        </Button>
        {full && (
          <span className="text-xs text-default-500">
            外部节点已满 {cap} 个，先在订阅页「外部节点」里删掉不用的
          </span>
        )}
      </div>
      {!chain && (
        <p className="text-xs text-default-500" data-external-hint>
          这是端口转发：流量原样转到你的节点，设置路由管不到。复制链接 /
          订阅里的地址会自动换成入口地址。
        </p>
      )}
      {adding && (
        <AddExternalDialog
          owner={userId}
          shared={false}
          onAdded={async (added) => {
            const list = await load();
            const pick = added
              .map((a) => list.find((item) => item.id === a.id))
              .find((node) => node && !reasonOf(node));

            if (pick) onChange(pick.id, pick);
            else if (added.length)
              toast.error("已存进外部节点，但这个节点的协议不能用在此隧道上");
          }}
          onClose={() => setAdding(false)}
        />
      )}
    </div>
  );
}
