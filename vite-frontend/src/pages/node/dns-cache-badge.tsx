import { useSyncExternalStore } from "react";
import { TriangleAlert, Wrench } from "lucide-react";
import toast from "react-hot-toast";

import { ensureNodeDNSCache, type NodeDNSCacheView } from "@/api";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownItem,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// Plan 240: 本机 DNS 缓存 (flvx-dns-cache on the node). The agent re-checks it locally at most every
// 10 minutes; the admin node list carries the last result. 「修复 DNS 缓存」 runs `ensure` once on the node
// (no-op when already right). ⚠ only when the agent reports the cache missing or broken (plan 218 rule:
// the card carries the marker next to the name, the chip row leads to the action).

export interface DNSCacheNode {
  id: number;
  isRemote?: number;
  connectionStatus: "online" | "offline";
  dnsCache?: NodeDNSCacheView | null;
}

type Tone = "ok" | "neutral" | "warning";

interface DNSCacheState {
  tone: Tone;
  label: string;
  title: string;
  detail: string;
  canFix: boolean;
  fixHint?: string;
  attention: boolean;
}

const toneClass: Record<Tone, string> = {
  ok: "border border-success-300 bg-success-100/80 text-success-700 dark:border-success-500/40 dark:bg-success-500/15 dark:text-success-400",
  neutral:
    "border border-default-300 bg-default-100/70 text-default-600 dark:border-default-200/40 dark:bg-default-100/20 dark:text-default-400",
  warning:
    "border border-warning-300 bg-warning-100/80 text-warning-700 dark:border-warning-500/40 dark:bg-warning-500/15 dark:text-warning-400",
};

// Fix runs + the freshest result per node, shared by the chip row and the ⚠ marker of the same node.
const store = {
  fixing: new Set<number>(),
  latest: new Map<number, NodeDNSCacheView>(),
  listeners: new Set<() => void>(),
  version: 0,
};
const emit = () => {
  store.version += 1;
  store.listeners.forEach((l) => l());
};
const subscribe = (l: () => void) => {
  store.listeners.add(l);

  return () => store.listeners.delete(l);
};
const useStoreVersion = () =>
  useSyncExternalStore(subscribe, () => store.version);

const currentView = (node: DNSCacheNode): NodeDNSCacheView | null => {
  const fromList = node.dnsCache ?? null;
  const fixed = store.latest.get(node.id);

  if (!fixed) return fromList;
  if (!fromList || (fixed.checkedAt ?? 0) >= (fromList.checkedAt ?? 0))
    return fixed;

  return fromList;
};

const checkedText = (v: NodeDNSCacheView | null) =>
  v?.checkedAt
    ? new Date(v.checkedAt).toLocaleString("zh-CN", { hour12: false })
    : "暂无记录";

export function dnsCacheState(node: DNSCacheNode): DNSCacheState | null {
  if (node.isRemote === 1) return null;
  const v = currentView(node);
  const online = node.connectionStatus === "online";

  if (!online || !v) {
    return {
      tone: "neutral",
      label: "未知",
      title: "节点离线，上线后显示",
      detail: "节点离线，上线后显示本机 DNS 缓存状态。",
      canFix: false,
      fixHint: "节点离线",
      attention: false,
    };
  }
  if (!v.supported) {
    return {
      tone: "neutral",
      label: "未知",
      title: "节点程序版本过旧，升级后显示",
      detail:
        "节点程序版本过旧，不会上报 DNS 缓存状态。在「节点」页升级后显示。",
      canFix: false,
      fixHint: "节点程序需升级",
      attention: false,
    };
  }
  if (!v.mode) {
    return {
      tone: "neutral",
      label: "检查中",
      title: "节点刚上线，稍后显示",
      detail: "节点刚上线，第一次检查结果稍后显示。",
      canFix: true,
      attention: false,
    };
  }
  if (v.ok && v.mode === "minimal") {
    return {
      tone: "neutral",
      label: "精简模式",
      title: `精简模式：${v.reason || "未装本机缓存"}`,
      detail: `没有装本机缓存，只用了备用解析器和 1 秒超时。原因：${v.reason || "未知"}`,
      canFix: true,
      attention: false,
    };
  }
  if (v.ok) {
    const how =
      v.mode === "resolved"
        ? "系统自带的 systemd-resolved 缓存（127.0.0.53）"
        : "本机 dnsmasq 缓存（127.0.0.1，同时问 2–3 个上游，谁先答用谁）";

    return {
      tone: "ok",
      label: "正常",
      title: `正常：${how}`,
      detail: `正常：${how}。`,
      canFix: true,
      attention: false,
    };
  }

  return {
    tone: "warning",
    label: v.mode === "none" ? "未启用" : "未运行",
    title: v.reason || "本机 DNS 缓存未启用",
    detail: `${v.reason || "本机 DNS 缓存未启用"}。丢一个 DNS 包可能卡 1–5 秒，点「修复 DNS 缓存」重新配置。`,
    canFix: true,
    attention: true,
  };
}

/** ⚠ next to the node name: only when the agent reports the cache missing / broken. */
export function hasDNSCacheAttention(node: DNSCacheNode): boolean {
  return Boolean(dnsCacheState(node)?.attention);
}

async function runFix(node: DNSCacheNode) {
  if (store.fixing.has(node.id)) return;
  store.fixing.add(node.id);
  emit();
  try {
    const res = await ensureNodeDNSCache(node.id);

    if (res.code === 0 && res.data) {
      const v = res.data.dnsCache;

      if (v) store.latest.set(node.id, v);
      if (v?.ok) {
        toast.success(res.data.summary || "DNS 缓存正常");
      } else {
        toast.error(`DNS 缓存仍未启用：${v?.reason || "未知原因"}`);
      }
    } else {
      toast.error(res.msg || "修复失败");
    }
  } catch {
    toast.error("网络错误，请重试");
  } finally {
    store.fixing.delete(node.id);
    emit();
  }
}

function DNSCacheMenu({
  node,
  state,
  fixing,
}: {
  node: DNSCacheNode;
  state: DNSCacheState;
  fixing: boolean;
}) {
  const v = currentView(node);

  return (
    <DropdownMenu aria-label="DNS 缓存详情">
      <DropdownMenuLabel className="max-w-[min(20rem,calc(100vw-2rem))] whitespace-normal text-xs font-normal">
        <p
          className={`flex items-center gap-1 font-medium ${state.attention ? "text-warning" : "text-foreground"}`}
        >
          {state.attention ? (
            <TriangleAlert
              aria-hidden="true"
              className="h-3.5 w-3.5 shrink-0"
            />
          ) : null}
          DNS 缓存：{state.label}
        </p>
        <p className="mt-1.5 break-words text-foreground">{state.detail}</p>
        <p className="mt-2 text-default-500">检查记录：{checkedText(v)}</p>
      </DropdownMenuLabel>
      <DropdownMenuSeparator />
      {state.canFix && !fixing ? (
        <DropdownItem
          className="text-xs"
          startContent={<Wrench aria-hidden="true" className="h-3.5 w-3.5" />}
          onPress={() => void runFix(node)}
        >
          修复 DNS 缓存
        </DropdownItem>
      ) : (
        <DropdownMenuLabel className="text-xs font-normal text-default-400">
          {fixing
            ? "正在修复…（一般几秒，最长约 3 分钟）"
            : `修复 DNS 缓存（${state.fixHint || "不可用"}）`}
        </DropdownMenuLabel>
      )}
    </DropdownMenu>
  );
}

/** The 「DNS 缓存」 row of the node card. */
export function NodeDNSCacheRow({ node }: { node: DNSCacheNode }) {
  useStoreVersion();
  const state = dnsCacheState(node);

  if (!state) return null;
  const fixing = store.fixing.has(node.id);

  return (
    <div className="flex justify-between items-center text-sm">
      <span className="text-default-600">DNS 缓存</span>
      <Dropdown placement="bottom-end">
        <DropdownTrigger>
          <button
            aria-label="DNS 缓存详情"
            className={`inline-flex h-6 shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 text-xs font-medium transition-colors hover:brightness-95 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary ${toneClass[state.tone]}`}
            data-testid="node-dns-cache-chip"
            title={state.title}
            type="button"
            onClick={(event) => event.stopPropagation()}
          >
            {state.attention ? (
              <TriangleAlert aria-hidden="true" className="h-3.5 w-3.5" />
            ) : null}
            <span>{fixing ? "修复中…" : state.label}</span>
          </button>
        </DropdownTrigger>
        <DNSCacheMenu fixing={fixing} node={node} state={state} />
      </Dropdown>
    </div>
  );
}

/** ⚠ after the node name (grid card + list row) when the cache is missing / broken. */
export function NodeDNSCacheAlert({ node }: { node: DNSCacheNode }) {
  useStoreVersion();
  const state = dnsCacheState(node);

  if (!state?.attention) return null;
  const fixing = store.fixing.has(node.id);

  return (
    <Dropdown>
      <DropdownTrigger>
        <Button
          aria-label="DNS 缓存未启用"
          className="h-6 min-w-0 shrink-0 gap-1 px-1 text-xs text-warning"
          color="warning"
          data-testid="node-dns-cache-alert"
          size="sm"
          title={`DNS 缓存${state.label}：${state.title}`}
          variant="light"
          onClick={(event) => event.stopPropagation()}
        >
          <TriangleAlert aria-hidden="true" className="h-4 w-4 shrink-0" />
          <span className="hidden sm:inline">DNS</span>
        </Button>
      </DropdownTrigger>
      <DNSCacheMenu fixing={fixing} node={node} state={state} />
    </Dropdown>
  );
}
