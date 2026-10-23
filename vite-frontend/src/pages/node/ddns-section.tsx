import { useState } from "react";
import toast from "react-hot-toast";

import {
  syncNodeDDNS,
  type DDNSRecordPayload,
  type DDNSRecordType,
  type DDNSSource,
  type DDNSStatus,
  type DNSProviderView,
  type NodeDDNSPayload,
  type NodeDDNSView,
} from "@/api/ddns";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Node form section 「DDNS」 (plan 142), placed under 附加 IP like the
// miaomiaowuX server form. Add-only: existing fields keep their layout.

export const MAX_DDNS_RECORDS = 8;

export const emptyDDNS = (): NodeDDNSPayload => ({
  enabled: false,
  providerId: 0,
  records: [],
});

export const ddnsFromView = (view?: NodeDDNSView | null): NodeDDNSPayload =>
  view
    ? {
        enabled: view.enabled,
        providerId: view.providerId,
        records: view.records.map((r) => ({
          domain: r.domain,
          type: r.type,
          role: r.role,
          source: r.source,
          prefix: r.prefix || "",
          takeover: r.takeover,
        })),
      }
    : emptyDDNS();

/** Domain of the 主地址 record of a family while DDNS is on. */
export const ddnsMainDomain = (
  ddns: NodeDDNSPayload,
  type: DDNSRecordType,
): string =>
  ddns.enabled
    ? ddns.records
        .find((r) => r.role === "main" && r.type === type)
        ?.domain.trim() || ""
    : "";

export const SOURCE_LABELS: Record<DDNSSource, string> = {
  auto: "自动（首个公网 IPv6）",
  ct: "电信 240e",
  cu: "联通 2408",
  cm: "移动 2409",
  prefix: "自定义前缀",
  panel: "面板看到的出口 IPv4",
  iface: "网卡公网 IPv4",
};

const V6_SOURCES: DDNSSource[] = ["auto", "ct", "cu", "cm", "prefix"];
const V4_SOURCES: DDNSSource[] = ["panel", "iface"];

export const STATUS_META: Record<
  DDNSStatus,
  { label: string; color: "success" | "warning" | "danger" }
> = {
  synced: { label: "已同步", color: "success" },
  pending: { label: "待同步", color: "warning" },
  failed: { label: "失败", color: "danger" },
  noaddr: { label: "没有地址", color: "danger" },
  upgrade: { label: "需要升级节点后生效", color: "warning" },
};

const fmtTime = (ms: number) =>
  ms ? new Date(ms).toLocaleString("zh-CN", { hour12: false }) : "—";

/** Inline validation mirroring the backend. */
export const validateDDNS = (ddns: NodeDDNSPayload): string => {
  if (!ddns.enabled) return "";
  if (!ddns.providerId) return "请选择 DDNS 服务商";
  if (ddns.records.length === 0) return "开启 DDNS 时至少要有一条记录";
  const seen = new Set<string>();
  const mains = new Set<string>();

  for (const r of ddns.records) {
    const domain = r.domain.trim().toLowerCase().replace(/\.$/, "");

    if (
      !/^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]*$/.test(domain)
    ) {
      return `域名「${r.domain || "（空）"}」无效`;
    }
    if (seen.has(domain + r.type)) return `记录 ${domain}（${r.type}）重复`;
    seen.add(domain + r.type);
    if (r.role === "main") {
      if (mains.has(r.type)) return `${r.type} 记录只能有一条「主地址」`;
      mains.add(r.type);
    }
    if (r.source === "prefix" && !r.prefix.trim())
      return `${domain}：请填写自定义前缀`;
  }

  return "";
};

interface Props {
  value: NodeDDNSPayload;
  onChange: (next: NodeDDNSPayload) => void;
  providers: DNSProviderView[];
  view?: NodeDDNSView | null; // edit mode: saved state + live status
  nodeId?: number | null;
  error?: string;
  onSynced?: (view: NodeDDNSView) => void;
}

export function NodeDDNSSection({
  value,
  onChange,
  providers,
  view,
  nodeId,
  error,
  onSynced,
}: Props) {
  const [syncing, setSyncing] = useState(false);
  const saved = new Map(
    (view?.records || []).map((r) => [r.domain + "|" + r.type, r]),
  );
  const setRecord = (i: number, patch: Partial<DDNSRecordPayload>) =>
    onChange({
      ...value,
      records: value.records.map((r, j) => (j === i ? { ...r, ...patch } : r)),
    });
  const addRecord = () => {
    const hasMainV6 = value.records.some(
      (r) => r.role === "main" && r.type === "AAAA",
    );

    onChange({
      ...value,
      records: [
        ...value.records,
        {
          domain: "",
          type: "AAAA",
          role: hasMainV6 ? "extra" : "main",
          source: "auto",
          prefix: "",
          takeover: false,
        },
      ],
    });
  };
  const runSync = async () => {
    if (!nodeId) return;
    setSyncing(true);
    try {
      const res = await syncNodeDDNS(nodeId);

      if (res.code === 0 && res.data) {
        onSynced?.(res.data);
        const failed = res.data.records.filter(
          (r) => r.status === "failed" || r.status === "noaddr",
        );

        if (failed.length)
          toast.error(`同步失败：${failed[0].domain} ${failed[0].lastError}`);
        else toast.success("已同步");
      } else toast.error(res.msg || "同步失败");
    } catch {
      toast.error("网络错误，请重试");
    } finally {
      setSyncing(false);
    }
  };

  const agentLine = (() => {
    if (!view) return "";
    if (!view.online) return "节点离线：上线后会按网卡地址同步。";
    if (!view.addrReport) {
      return "当前节点版本不上报网卡地址：AAAA 和「网卡公网 IPv4」需要升级节点后生效；「面板看到的出口 IPv4」可直接用。";
    }
    const v6 = view.agentAddrs?.v6 || [];
    const v4 = view.agentAddrs?.v4 || [];

    if (!view.agentAddrs) return "等待节点上报网卡地址…";

    return `节点上报的网卡地址：${[...v6, ...v4].join("、") || "没有公网地址"}${view.panelIp ? `；面板看到的出口：${view.panelIp}` : ""}`;
  })();

  return (
    <div
      className="space-y-3 rounded-xl border border-divider p-3"
      data-testid="ddns-section"
    >
      <div className="flex items-start gap-3">
        <Switch
          aria-label="DDNS"
          isSelected={value.enabled}
          onValueChange={(enabled) =>
            onChange({
              ...value,
              enabled,
              providerId:
                value.providerId ||
                (providers.length === 1 ? providers[0].id : 0),
              records:
                enabled && value.records.length === 0
                  ? [
                      {
                        domain: "",
                        type: "AAAA",
                        role: "main",
                        source: "auto",
                        prefix: "",
                        takeover: false,
                      },
                    ]
                  : value.records,
            })
          }
        />
        <div className="min-w-0">
          <span className="text-sm font-medium">DDNS</span>
          <p className="text-xs text-default-500">
            开启后节点网卡地址变化时自动更新 Cloudflare 的 A/AAAA 记录（TTL 2
            分钟，不开代理）；首次同步成功后，节点地址改用域名，之后隧道和规则里的地址不再变。关闭
            DDNS 或删除节点都不会删除 DNS 记录。
          </p>
        </div>
      </div>

      {value.enabled ? (
        <div className="space-y-3">
          <Select
            description={
              providers.length === 0
                ? "还没有 DNS 服务商：请先到「网站配置 → DNS 服务商」添加"
                : undefined
            }
            label="DDNS 服务商"
            placeholder="选择 DNS 服务商"
            selectedKeys={value.providerId ? [String(value.providerId)] : []}
            variant="bordered"
            onSelectionChange={(keys) => {
              const id = Number(Array.from(keys)[0] || 0);

              onChange({ ...value, providerId: id });
            }}
          >
            {providers.map((p) => (
              <SelectItem key={String(p.id)} textValue={p.name}>
                {p.name}
                {p.zones.length ? `（${p.zones.join("、")}）` : ""}
              </SelectItem>
            ))}
          </Select>

          {value.records.map((rec, i) => {
            const state = saved.get(
              rec.domain.trim().toLowerCase() + "|" + rec.type,
            );
            const sources = rec.type === "A" ? V4_SOURCES : V6_SOURCES;
            const meta = state ? STATUS_META[state.status] : null;

            return (
              <div
                key={i}
                className="space-y-2 rounded-lg border border-divider bg-default-50 p-2.5 dark:bg-default-100/40"
                data-testid="ddns-record"
              >
                <div className="grid grid-cols-1 gap-2 md:grid-cols-[minmax(0,1.6fr)_minmax(0,0.7fr)_minmax(0,0.9fr)]">
                  <Input
                    label="域名"
                    placeholder="例如：leikwan-hutai-ct.imgamer.top"
                    size="sm"
                    value={rec.domain}
                    variant="bordered"
                    onChange={(e) => setRecord(i, { domain: e.target.value })}
                  />
                  <Select
                    label="类型"
                    selectedKeys={[rec.type]}
                    size="sm"
                    variant="bordered"
                    onSelectionChange={(keys) => {
                      const type = (Array.from(keys)[0] ||
                        "AAAA") as DDNSRecordType;

                      setRecord(i, {
                        type,
                        source: type === "A" ? "panel" : "auto",
                        prefix: "",
                      });
                    }}
                  >
                    <SelectItem key="AAAA">AAAA（IPv6）</SelectItem>
                    <SelectItem key="A">A（IPv4）</SelectItem>
                  </Select>
                  <Select
                    label="用途"
                    selectedKeys={[rec.role]}
                    size="sm"
                    variant="bordered"
                    onSelectionChange={(keys) =>
                      setRecord(i, {
                        role: (Array.from(keys)[0] ||
                          "main") as DDNSRecordPayload["role"],
                      })
                    }
                  >
                    <SelectItem key="main">主地址</SelectItem>
                    <SelectItem key="extra">附加入口</SelectItem>
                  </Select>
                </div>
                <div className="grid grid-cols-1 gap-2 md:grid-cols-[minmax(0,1.6fr)_minmax(0,1.6fr)]">
                  <Select
                    label="地址来源"
                    selectedKeys={[rec.source]}
                    size="sm"
                    variant="bordered"
                    onSelectionChange={(keys) =>
                      setRecord(i, {
                        source: (Array.from(keys)[0] ||
                          sources[0]) as DDNSSource,
                      })
                    }
                  >
                    {sources.map((s) => (
                      <SelectItem key={s}>{SOURCE_LABELS[s]}</SelectItem>
                    ))}
                  </Select>
                  {rec.source === "prefix" ? (
                    <Input
                      label="自定义前缀"
                      placeholder="例如：2408:820c 或 2408:820c::/32"
                      size="sm"
                      value={rec.prefix}
                      variant="bordered"
                      onChange={(e) => setRecord(i, { prefix: e.target.value })}
                    />
                  ) : (
                    <p className="self-center text-xs text-default-500">
                      {rec.role === "main"
                        ? `填入节点的${rec.type === "A" ? " IPv4" : " IPv6"} 地址`
                        : "加入节点的附加 IP（可在隧道里勾选为入口）"}
                    </p>
                  )}
                </div>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <Checkbox
                    isSelected={rec.takeover}
                    onValueChange={(takeover) => setRecord(i, { takeover })}
                  >
                    <span className="text-xs">
                      接管已有记录（Cloudflare 里已有同名记录时才需要）
                    </span>
                  </Checkbox>
                  <Button
                    color="danger"
                    size="sm"
                    variant="flat"
                    onPress={() =>
                      onChange({
                        ...value,
                        records: value.records.filter((_, j) => j !== i),
                      })
                    }
                  >
                    删除
                  </Button>
                </div>
                {state && meta ? (
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-divider/60 pt-2 text-xs text-default-500">
                    <Chip color={meta.color} size="sm" variant="flat">
                      {meta.label}
                    </Chip>
                    <span>
                      当前 IP：
                      <span className="font-mono text-foreground">
                        {state.currentIp || "—"}
                      </span>
                    </span>
                    <span>TTL：{state.ttl ? `${state.ttl} 秒` : "—"}</span>
                    <span>上次同步：{fmtTime(state.lastSyncAt)}</span>
                    {!state.applied && state.status === "synced" ? (
                      <span>节点地址即将改为该域名</span>
                    ) : null}
                    {state.lastError ? (
                      <span className="w-full break-all text-danger">
                        {state.lastError}
                      </span>
                    ) : null}
                  </div>
                ) : null}
              </div>
            );
          })}

          <div className="flex flex-wrap items-center gap-2">
            <Button
              color="success"
              isDisabled={value.records.length >= MAX_DDNS_RECORDS}
              size="sm"
              variant="flat"
              onPress={addRecord}
            >
              添加记录
            </Button>
            {view?.enabled && nodeId ? (
              <Button
                color="success"
                isLoading={syncing}
                size="sm"
                variant="flat"
                onPress={runSync}
              >
                立即同步
              </Button>
            ) : null}
            <span className="text-xs text-default-500">
              最多 {MAX_DDNS_RECORDS}{" "}
              条；保存时会检查域名是否在服务商可管理的区域内
            </span>
          </div>
          {agentLine ? (
            <p
              className="text-xs text-default-500"
              data-testid="ddns-agent-line"
            >
              {agentLine}
            </p>
          ) : null}
          {error ? <p className="text-xs text-danger">{error}</p> : null}
        </div>
      ) : view?.records.length ? (
        <p className="text-xs text-default-500">
          DDNS 已关闭：{view.records.map((r) => r.domain).join("、")}{" "}
          不再自动更新，记录仍保留在 Cloudflare。
        </p>
      ) : null}
    </div>
  );
}

/** Description under an address field owned by a DDNS 主地址 record. */
export const ddnsOwnedHint = (
  ddns: NodeDDNSPayload,
  type: DDNSRecordType,
  stored: string,
  view?: NodeDDNSView | null,
): string => {
  const domain = ddnsMainDomain(ddns, type);
  const saved = view?.records.find(
    (r) =>
      r.type === type && r.role === "main" && r.domain === domain.toLowerCase(),
  );
  const family = type === "A" ? "IPv4" : "IPv6";

  if (
    saved?.applied ||
    (stored && stored.toLowerCase() === domain.toLowerCase())
  ) {
    return `由 DDNS 管理：节点 ${family} 变化时自动更新 DNS 记录，这里保持域名不变`;
  }

  return `由 DDNS 管理：首次同步成功后自动改为此域名${stored ? `（当前仍是 ${stored}）` : ""}`;
};
