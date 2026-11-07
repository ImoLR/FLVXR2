import { useEffect, useState } from "react";
import toast from "react-hot-toast";
import { ChevronDown, ChevronUp, CircleCheck, CircleX } from "lucide-react";

import { Pill, type LandingTone } from "./pill";

import {
  addRealityDomain,
  blockRealityDomain,
  deleteRealityDomain,
  LandingServer,
  landingRealityProbeStatus,
  probeLandingReality,
  RealityDomain,
  RealityDomainSource,
  RealityDomainsView,
  RealityMode,
  RealityPin,
  RealityProbeRow,
  RealityProbeStats,
  RealityTrialRule,
  realityDomains,
  refreshRealityPool,
} from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// Plan 227: Reality 伪装域名 — the global candidate list (全局设置) and the
// per-server 「伪装域名」 block (落地服务器设置: 自动（最快）/ 手动, 测速
// table, the pinned IP of each dest).
// Plan 229: re-samples (次数 / 抖动), 「试用」 small sites, pacing help text.

const defaultTrialRule: RealityTrialRule = {
  minSamples: 3,
  minSpanHours: 6,
  jitterMs: 10,
  jitterPct: 30,
  marginMs: 5,
  resampleTop: 5,
  resampleTrial: 2,
  resampleRounds: 2,
  resampleGapSec: 5,
  keep: 8,
  switchMarginMs: 5,
};

const trialRuleText = (r: RealityTrialRule) =>
  `「试用」= 妙妙屋域名池里的小网站（不是内置那几家大厂，也不属于自动屏蔽的几类）。` +
  `它要在那台机器上先证明稳定才会被自动选用：至少测 ${r.minSamples} 次、` +
  `前后跨度 ≥ ${r.minSpanHours} 小时、每次都可用、抖动（最慢 − 最快）不超过 ` +
  `${r.jitterMs} ms 或中位数的 ${r.jitterPct}%（取大的），而且不比非试用域名里最快的慢 ` +
  `${r.marginMs} ms 以上。不稳定的会写出原因，只能手动选用。`;

const sourceLabel: Record<RealityDomainSource | "current", string> = {
  builtin: "内置",
  custom: "自定义",
  pool: "妙妙屋域名池",
  current: "当前",
};
const sourceTone: Record<RealityDomainSource | "current", LandingTone> = {
  builtin: "cyan",
  custom: "teal",
  pool: "purple",
  current: "slate",
};

const timeText = (ms?: number) =>
  ms
    ? new Date(ms).toLocaleString("zh-CN", {
        hour12: false,
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";

function SourcePills({
  source,
  inPool,
}: {
  source: RealityDomainSource | "current";
  inPool: boolean;
}) {
  return (
    <>
      <Pill tone={sourceTone[source]}>{sourceLabel[source]}</Pill>
      {inPool && source !== "pool" && <Pill tone="purple">妙妙屋域名池</Pill>}
    </>
  );
}

function DomainRow({
  domain,
  busy,
  onBlock,
  onDelete,
}: {
  domain: RealityDomain;
  busy: boolean;
  onBlock: (blocked: boolean) => void;
  onDelete: () => void;
}) {
  return (
    <div
      className={`flex items-start gap-2 rounded-lg border border-default-200 px-2 py-1.5 ${
        domain.blocked ? "bg-default-50" : ""
      }`}
      data-reality-domain={domain.domain}
    >
      <div className="min-w-0 flex-1 space-y-0.5">
        <p
          className={`break-all font-mono text-sm ${
            domain.blocked ? "text-default-400 line-through" : ""
          }`}
        >
          {domain.domain}
        </p>
        <div className="flex flex-wrap items-center gap-1">
          <SourcePills inPool={domain.inPool} source={domain.source} />
          {domain.trial && !domain.blocked && <Pill tone="amber">试用</Pill>}
          {domain.blocked && <Pill tone="red">已屏蔽</Pill>}
        </div>
        {domain.note && (
          <p className="text-xs leading-snug text-default-500">{domain.note}</p>
        )}
      </div>
      <div className="flex shrink-0 flex-col items-end gap-1 sm:flex-row">
        {domain.blocked ? (
          <Button
            color="success"
            isDisabled={busy}
            size="sm"
            variant="flat"
            onPress={() => onBlock(false)}
          >
            解除屏蔽
          </Button>
        ) : (
          <Button
            color="danger"
            isDisabled={busy}
            size="sm"
            variant="flat"
            onPress={() => onBlock(true)}
          >
            屏蔽
          </Button>
        )}
        {domain.source === "custom" && (
          <Button
            color="danger"
            isDisabled={busy}
            size="sm"
            variant="flat"
            onPress={onDelete}
          >
            删除
          </Button>
        )}
      </div>
    </div>
  );
}

/** 全局设置 → Reality 伪装域名: candidates, 屏蔽, custom domains, 妙妙屋 pool. */
export function RealityDomainSettings() {
  const [view, setView] = useState<RealityDomainsView | null>(null);
  const [error, setError] = useState("");
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [poolOpen, setPoolOpen] = useState(false);
  const [blockedOpen, setBlockedOpen] = useState(false);

  useEffect(() => {
    let disposed = false;

    void realityDomains().then((res) => {
      if (disposed) return;
      if (res.code === 0) setView(res.data);
      else setError(res.msg);
    });

    return () => {
      disposed = true;
    };
  }, []);

  const run = async (
    action: () => Promise<{
      code: number;
      msg: string;
      data: RealityDomainsView;
    }>,
    done?: string,
  ) => {
    setBusy(true);
    try {
      const res = await action();

      if (res.code !== 0) {
        toast.error(res.msg);

        return false;
      }
      setView(res.data);
      if (done) toast.success(done);

      return true;
    } finally {
      setBusy(false);
    }
  };

  if (error) return <p className="text-sm text-danger">{error}</p>;
  if (!view) return <p className="text-sm text-default-500">正在读取…</p>;

  const own = view.domains.filter(
    (d) => d.source !== "pool" && (!d.blocked || d.source === "custom"),
  );
  const ownBlocked = view.domains.filter(
    (d) => d.source === "builtin" && d.blocked,
  );
  const pool = view.domains.filter((d) => d.source === "pool");
  const poolBlocked = pool.filter((d) => d.blocked).length;
  const poolTrial = pool.filter((d) => d.trial && !d.blocked).length;
  const row = (d: RealityDomain) => (
    <DomainRow
      key={d.id}
      busy={busy}
      domain={d}
      onBlock={(blocked) =>
        void run(
          () => blockRealityDomain(d.id, blocked),
          blocked ? `已屏蔽 ${d.domain}` : `已解除屏蔽 ${d.domain}`,
        )
      }
      onDelete={() =>
        void run(() => deleteRealityDomain(d.id), `已删除 ${d.domain}`)
      }
    />
  );

  return (
    <div data-reality-domains className="space-y-2">
      <p className="text-xs leading-relaxed text-default-500">
        新建 VLESS Reality
        节点用的伪装目标。服务器设为「自动（最快）」时，每天在那台机器上测速一次，
        选最快、可用、没被屏蔽的域名给<b>新建</b>
        节点用；已有节点和用户的链接不变。被屏蔽的域名不测速、不会被自动选中。
        现在会测 {view.probeCount} 个（最多 {view.probeMax} 个）。
      </p>
      <div className="flex items-start gap-2">
        <Input
          aria-label="添加自定义伪装域名"
          placeholder="添加自定义域名，例如 www.example.com"
          size="sm"
          value={input}
          variant="bordered"
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && input.trim()) {
              void run(() => addRealityDomain(input), "已添加").then(
                (ok) => ok && setInput(""),
              );
            }
          }}
        />
        <Button
          className="shrink-0"
          color="success"
          isDisabled={busy || !input.trim()}
          size="sm"
          variant="flat"
          onPress={() =>
            void run(() => addRealityDomain(input), "已添加").then(
              (ok) => ok && setInput(""),
            )
          }
        >
          添加
        </Button>
      </div>
      <div className="space-y-1.5">{own.map(row)}</div>
      <button
        className="flex w-full items-center justify-between rounded-lg bg-default-100 px-2 py-1.5 text-left text-sm"
        type="button"
        onClick={() => setBlockedOpen((open) => !open)}
      >
        <span>默认屏蔽的内置域名 · {ownBlocked.length} 个</span>
        {blockedOpen ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
      </button>
      {blockedOpen && <div className="space-y-1.5">{ownBlocked.map(row)}</div>}
      <div className="flex items-center justify-between gap-2 rounded-lg bg-default-100 px-2 py-1.5">
        <button
          className="flex min-w-0 flex-1 items-center justify-between gap-2 text-left text-sm"
          type="button"
          onClick={() => setPoolOpen((open) => !open)}
        >
          <span className="min-w-0 truncate">
            妙妙屋域名池 · {pool.length} 个
            {(poolBlocked > 0 || poolTrial > 0) &&
              `（${[
                poolBlocked > 0 ? `${poolBlocked} 个已屏蔽` : "",
                poolTrial > 0 ? `${poolTrial} 个试用` : "",
              ]
                .filter(Boolean)
                .join("，")}）`}
          </span>
          {poolOpen ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
        </button>
        <Button
          className="shrink-0"
          color="secondary"
          isDisabled={busy || !view.poolAvailable}
          size="sm"
          variant="flat"
          onPress={() => void run(refreshRealityPool, "域名池已刷新")}
        >
          刷新
        </Button>
      </div>
      {!view.poolAvailable && (
        <p className="text-xs text-default-500">未接入妙妙屋，没有域名池。</p>
      )}
      {view.poolError && (
        <p className="text-xs text-warning">妙妙屋域名池：{view.poolError}</p>
      )}
      {view.poolFetchedAt > 0 && (
        <p className="text-xs text-default-400">
          域名池更新于 {timeText(view.poolFetchedAt)}（每 6 小时自动更新）
        </p>
      )}
      {poolTrial > 0 && (
        <p
          data-reality-trial-rule
          className="text-xs leading-relaxed text-default-500"
        >
          {trialRuleText(view.trialRule || defaultTrialRule)}
        </p>
      )}
      {poolOpen && <div className="space-y-1.5">{pool.map(row)}</div>}
    </div>
  );
}

const msText = (ms: number) => (ms < 0 ? "×" : `${ms}`);

function sampleInfo(row: RealityProbeRow) {
  const samples = row.samples?.length
    ? row.samples
    : row.latencyMs > 0
      ? [row.latencyMs]
      : [];
  const ok = samples.filter((ms) => ms > 0);
  const jitter = ok.length > 1 ? Math.max(...ok) - Math.min(...ok) : 0;
  const shown = row.medianMs || row.latencyMs;
  let title = samples.length
    ? `本次 ${samples.length} 次：${samples.map(msText).join(" / ")} ms`
    : "";

  if (row.worstMs && samples.length > 1)
    title += `；最慢 ${row.worstMs} ms（按它排名）`;
  if (row.historyCount)
    title += `；这台机器最近 ${row.historyCount} 次，抖动 ${row.historyJitterMs ?? 0} ms`;

  return { samples, jitter, shown, title };
}

function ProbeTable({
  rows,
  current,
  manual,
  margin,
  onPick,
}: {
  rows: RealityProbeRow[];
  current: string;
  manual: boolean;
  margin: number;
  onPick: (row: RealityProbeRow) => void;
}) {
  const [all, setAll] = useState(false);
  const limit = 10;
  // The current pick always shows, even beyond the first rows.
  const shown = all
    ? rows
    : rows.filter((row, i) => i < limit || row.target === current);

  return (
    <div data-reality-probe-table className="space-y-1">
      <div className="hidden grid-cols-[minmax(0,1fr)_88px_64px_88px_120px] gap-2 px-2 text-xs text-default-500 sm:grid">
        <span>域名</span>
        <span>可用</span>
        <span className="text-right">握手</span>
        <span>次数 · 抖动</span>
        <span>最快 IP</span>
      </div>
      {shown.map((row) => {
        const isCurrent = row.target === current;
        const pickable = manual && row.feasible && !row.blocked && !isCurrent;
        const info = sampleInfo(row);
        const countText =
          info.samples.length > 1
            ? `${info.samples.length} 次 · ${info.jitter} ms`
            : info.samples.length === 1
              ? "1 次"
              : "—";

        return (
          <div
            key={row.target}
            className={`grid grid-cols-[minmax(0,1fr)_auto] gap-x-2 gap-y-0.5 rounded-lg border px-2 py-1.5 text-sm sm:grid-cols-[minmax(0,1fr)_88px_64px_88px_120px] sm:items-center ${
              isCurrent
                ? "border-teal-500/60 bg-teal-500/5"
                : "border-default-200"
            } ${row.blocked ? "opacity-50" : ""}`}
            data-reality-probe-row={row.domain}
          >
            <div className="flex min-w-0 flex-wrap items-center gap-1">
              <span className="min-w-0 truncate font-mono" title={row.target}>
                {row.domain}
              </span>
              {isCurrent && <Pill tone="teal">当前</Pill>}
              {isCurrent && !manual && row.pickHeld && (
                <span
                  data-reality-pick-held
                  className="text-xs text-default-400"
                  title={`排名第一的 ${row.pickHeldBy || "另一个域名"} ${row.pickHeldByMs ?? "?"} ms，当前 ${row.pickHeldMs ?? "?"} ms，差不到 ${margin} ms 不换`}
                >
                  保持（差 &lt; {margin} ms）
                </span>
              )}
              {row.blocked && <Pill tone="red">已屏蔽</Pill>}
              {row.trial && !row.blocked && (
                <Pill tone={row.trialOk ? "green" : "amber"}>
                  {row.trialOk ? "试用 · 稳定" : "试用"}
                </Pill>
              )}
              {pickable && (
                <Button
                  className="h-6 min-w-0 px-2"
                  color="primary"
                  size="sm"
                  variant="flat"
                  onPress={() => onPick(row)}
                >
                  选用
                </Button>
              )}
              {row.trial && !row.trialOk && row.feasible && !row.blocked && (
                <span
                  data-reality-trial-reason
                  className={`w-full text-xs ${
                    row.trialPending ? "text-default-400" : "text-warning"
                  }`}
                >
                  {row.trialPending
                    ? `积累中：${row.trialReason || "还要再测"}`
                    : `${row.trialReason || "不稳定"}，只能手动选用`}
                </span>
              )}
            </div>
            <span
              className={`flex items-center gap-1 text-xs ${
                row.feasible ? "text-success" : "text-danger"
              } sm:order-none`}
              title={row.reason || ""}
            >
              {row.feasible ? <CircleCheck size={14} /> : <CircleX size={14} />}
              <span className="truncate">
                {row.feasible ? "可用" : row.reason || "不可用"}
              </span>
            </span>
            <span
              className="font-mono text-xs text-default-600 sm:text-right"
              title={info.title}
            >
              {info.shown > 0 ? `${info.shown} ms` : "—"}
              <span className="text-default-400 sm:hidden">
                {info.samples.length > 1 && ` · ${countText}`}
              </span>
            </span>
            <span
              data-reality-samples
              className="hidden font-mono text-xs text-default-500 sm:block"
              title={info.title}
            >
              {countText}
            </span>
            <span className="truncate text-right font-mono text-xs text-default-500 sm:text-left">
              {row.bestIp || "—"}
            </span>
          </div>
        );
      })}
      {rows.length > shown.length && (
        <button
          className="w-full rounded-lg bg-default-100 px-2 py-1.5 text-sm"
          type="button"
          onClick={() => setAll(true)}
        >
          展开全部 {rows.length} 个
        </button>
      )}
    </div>
  );
}

/** How a 测速 runs (exact pacing) and what the last one cost. */
function probeHowText(
  rule: RealityTrialRule,
  shared: boolean,
  stats?: RealityProbeStats | null,
) {
  const how =
    `测速方式：先把全部域名各测 1 次（每个 IP 连 1 次 TCP，最快的 IP 做 1 次 TLS 握手）；` +
    `再挑最快的 ${rule.resampleTop} 个（另加最多 ${rule.resampleTrial} 个试用网站）各补测 ${rule.resampleRounds} 次，` +
    `一次只测一个域名，同一域名两次之间隔 ${rule.resampleGapSec} 秒，按 3 次里最慢的那次排名。` +
    `自动模式下，别的域名要快 ${rule.switchMarginMs ?? 5} ms 以上才换掉当前域名（当前域名不能用或被屏蔽时立刻换）。` +
    (shared
      ? "共享服务器由妙妙屋 Helper 测，同时连接数由 Helper 控制。"
      : "同一时间最多 2 个连接，连接用 RST 关闭，不留 TIME_WAIT。");

  if (!stats) return how;
  let last = `上次：${stats.passes} 轮`;

  if (stats.resampled > 0) last += `（补测 ${stats.resampled} 个）`;
  if (stats.measured && stats.sockets)
    last += `，共 ${stats.sockets} 个连接，最多同时 ${stats.maxOpen} 个`;
  last += `，用时 ${Math.max(1, Math.round(stats.durationMs / 1000))} 秒。`;
  if (!shared && !stats.measured)
    last += "节点程序升级后才会限制为最多同时 2 个连接、不留 TIME_WAIT。";

  return `${how}${last}`;
}

function PinList({
  pins,
  supported,
  shared,
}: {
  pins: RealityPin[];
  supported: boolean;
  shared: boolean;
}) {
  if (!supported) {
    return (
      <p className="text-xs text-default-500">
        当前 IP / 延迟：
        {shared ? "妙妙屋升级后显示" : "节点升级后显示"}
      </p>
    );
  }
  if (!pins.length) {
    return (
      <p className="text-xs text-default-500">
        当前 IP / 延迟：还没有 VLESS Reality 节点用到伪装目标
      </p>
    );
  }

  return (
    <div data-reality-pins className="space-y-1">
      <p className="text-xs font-semibold text-default-600">
        当前 IP / 延迟（服务器连伪装目标用的 IP，每 6 小时复查）
      </p>
      {pins.map((pin) => (
        <div
          key={`${pin.host}:${pin.port ?? 443}`}
          className="flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-lg bg-default-50 px-2 py-1 text-xs"
          data-reality-pin={pin.host}
        >
          <span className="min-w-0 truncate font-mono">{pin.host}</span>
          {pin.pinned_ip ? (
            <>
              <Pill tone="green">
                {pin.pinned_ip} · {pin.pinned_ms ?? 0} ms
              </Pill>
              {pin.best_ip && pin.best_ip !== pin.pinned_ip && (
                <span className="text-default-500">
                  最快 {pin.best_ip} · {pin.best_ms} ms
                </span>
              )}
            </>
          ) : (
            <Pill tone="amber">未钉选：{pin.error || "按域名解析"}</Pill>
          )}
          {pin.checked_at > 0 && (
            <span className="text-default-400">
              检查于 {timeText(pin.checked_at)}
            </span>
          )}
        </div>
      ))}
    </div>
  );
}

/** 落地服务器设置 → 伪装域名. */
export function RealityServerSection({
  server,
  form,
  namesInput,
  setNamesInput,
  set,
  onProbed,
}: {
  server: LandingServer;
  form: LandingServer;
  namesInput: string;
  setNamesInput: (value: string) => void;
  set: <K extends keyof LandingServer>(key: K, value: LandingServer[K]) => void;
  onProbed: (server: LandingServer) => void;
}) {
  const [probing, setProbing] = useState(false);
  const [probe, setProbe] = useState<RealityProbeRow[]>(
    server.realityProbe || [],
  );
  const [probedAt, setProbedAt] = useState(server.realityProbedAt || 0);
  const [probeError, setProbeError] = useState(server.realityProbeError || "");
  const [probeStats, setProbeStats] = useState(server.realityProbeStats);
  const rule = server.realityTrialRule || defaultTrialRule;
  const mode: RealityMode = form.realityMode === "manual" ? "manual" : "auto";
  const supported = Boolean(server.realityProbeSupported);
  const shared = server.source === "shared";

  const runProbe = async () => {
    setProbing(true);
    try {
      const res = await probeLandingReality(server.id);

      if (res.code !== 0) {
        setProbeError(res.msg);
        toast.error(res.msg);

        return;
      }
      let next = res.data;

      // A slower 测速 (妙妙屋 Helper) finishes in the background: poll.
      for (let i = 0; next.realityProbePending && i < 60; i++) {
        await new Promise((resolve) => setTimeout(resolve, 5000));
        const poll = await landingRealityProbeStatus(server.id);

        if (poll.code !== 0) break;
        next = poll.data;
      }
      if (next.realityProbePending) {
        toast("测速还在进行，稍后重新打开设置查看结果");

        return;
      }
      if (next.realityProbeError && next.realityProbedAt === probedAt) {
        setProbeError(next.realityProbeError);
        toast.error(next.realityProbeError);

        return;
      }

      setProbe(next.realityProbe || []);
      setProbeStats(next.realityProbeStats);
      setProbedAt(next.realityProbedAt || 0);
      setProbeError(next.realityProbeError || "");
      if (mode === "auto") {
        set("realityDest", next.realityDest);
        set("realityServerNames", next.realityServerNames);
        setNamesInput((next.realityServerNames || []).join(","));
      }
      toast.success(
        next.realityPicked
          ? `测速完成，新节点改用 ${next.realityPicked}`
          : "测速完成",
      );
      onProbed(next);
    } finally {
      setProbing(false);
    }
  };

  return (
    <div
      data-reality-section
      className="space-y-3 rounded-lg border border-default-200 p-3"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-semibold">伪装域名</span>
        <Button
          color="secondary"
          isDisabled={!supported}
          isLoading={probing}
          size="sm"
          variant="flat"
          onPress={() => void runProbe()}
        >
          {probing ? "测速中…" : "测速"}
        </Button>
      </div>
      <Select
        description={
          mode === "auto"
            ? "每天在这台机器上测速一次（3 轮），选 3 次里最慢那次也最快、可用、没被屏蔽的域名；试用网站稳定后才会被选；只影响新建节点"
            : "手动指定；已有节点不变"
        }
        label="选择方式"
        selectedKeys={[mode]}
        onSelectionChange={(keys) => {
          const value = Array.from(keys)[0] as RealityMode | undefined;

          if (value) set("realityMode", value);
        }}
      >
        <SelectItem key="auto">自动（最快）</SelectItem>
        <SelectItem key="manual">手动</SelectItem>
      </Select>
      {mode === "auto" ? (
        <p data-reality-current className="break-all text-sm">
          新节点用：<span className="font-mono">{form.realityDest}</span>
          <span className="text-xs text-default-500">
            （serverNames：{(form.realityServerNames || []).join(", ")}）
          </span>
        </p>
      ) : (
        <>
          <Input
            label="Reality 目标 (host:port)"
            value={form.realityDest}
            variant="bordered"
            onChange={(event) => set("realityDest", event.target.value)}
          />
          <Input
            description="多个域名用逗号分隔；在下面的测速结果里点「选用」会自动填好"
            label="Reality Server Names"
            value={namesInput}
            variant="bordered"
            onChange={(event) => setNamesInput(event.target.value)}
          />
        </>
      )}
      {!supported && (
        <p data-reality-unsupported className="text-xs text-warning">
          {server.realityProbeUnsupportedReason || "暂时不能测速"}
        </p>
      )}
      {probeError && (
        <p className="text-xs text-danger">上次测速失败：{probeError}</p>
      )}
      {probe.length > 0 && (
        <>
          <ProbeTable
            current={form.realityDest}
            manual={mode === "manual"}
            margin={rule.switchMarginMs ?? 5}
            rows={probe}
            onPick={(row) => {
              set("realityDest", row.target);
              set("realityServerNames", row.serverNames);
              setNamesInput(row.serverNames.join(","));
            }}
          />
          <p
            data-reality-probe-how
            className="text-xs leading-relaxed text-default-400"
          >
            测速于 {timeText(probedAt)} · 在
            {shared ? "这台共享服务器" : "这台机器"}上测。
            {probeHowText(rule, shared, probeStats)}
          </p>
        </>
      )}
      <PinList
        pins={server.realityPins || []}
        shared={shared}
        supported={Boolean(server.realityPinSupported)}
      />
    </div>
  );
}
