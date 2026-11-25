import type { LeiKwanRule, LeiKwanRuleList } from "@/api/leikwan-line";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AlertTriangle, Copy, Link2, Plus } from "lucide-react";
import toast from "react-hot-toast";

import {
  getLeiKwanRules,
  markLeiKwanNavSeen,
  refreshLeiKwanNav,
} from "@/api/leikwan-line";
import { useExpandOne } from "@/components/expand-row";
import { useH5Mode } from "@/hooks/useH5Mode";
import { usePullToRefresh } from "@/hooks/usePullToRefresh";
import { Pill } from "@/pages/landing/pill";
import {
  LkCard,
  LkCardTitle,
  LkExpandCard,
  LkField,
  LkTab,
} from "@/pages/leikwan/panel/layout-parts";
import {
  BlockedPill,
  BackupFlowPill,
  FlowPill,
  PendingBlock,
  RuleButtons,
  RuleDot,
  UserBackupChip,
  blockedNote,
  copyText,
  externalNote,
  externalProtocol,
  protocolLabel,
  routeShort,
  useLeiKwanRuleActions,
  type LeiKwanRuleActions,
} from "@/pages/leikwan/panel/rule-actions";
import { fmtBytes } from "@/pages/leikwan/panel/shared";
import { OfflinePill, offlineText } from "@/pages/leikwan/panel/entry-state";
import { VendorFlowPill } from "@/pages/leikwan/panel/usage-parts";
import { LeiKwanSubDialog } from "@/pages/leikwan/panel/sub-dialog";
import {
  RuleLatencyField,
  RuleLatencyTag,
} from "@/pages/leikwan/panel/rule-latency";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";

// Plan 270: the user view of 利群汇聚 (screens 06–08). Only for users with an
// active 利群 grant (others are sent back to 主页). Users see their entry, the
// 备线 switch, the 专线 they can use and their own 利群 rules — never the
// 利群转发号, owner, 预热 or the 备线 forward ids.

type UserLine = NonNullable<LeiKwanRuleList["lines"]>[number];

const lineShort = (name: string) => name.replace(/^利群·/, "");

// Plan 272: the action column fits 复制链接 · 诊断 · 换落地 · 删除 in one row.
const RULE_GRID =
  "grid grid-cols-[minmax(0,1.2fr)_minmax(168px,200px)_minmax(0,1fr)_236px] gap-3";

// Plan 276 (F04): MM-DD HH:mm in the viewer's time zone.
function fmtGraceUntil(ms: number) {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");

  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** Plan 276 (F04): amber notice while the old 主入口 is still open after a 换主入口. */
function GraceNotice({ data }: { data: LeiKwanRuleList }) {
  const g = data.grant;
  const gr = g?.grace;

  if (!g || !gr || !data.entryLock || gr.until <= Date.now()) return null;
  const from = gr.prevEntryName || "原入口";
  const to = g.mainEntryName || "新入口";

  return (
    <div
      className="flex items-start gap-2 rounded-xl border border-amber-500/40 bg-amber-500/10 px-3.5 py-2.5 text-sm text-amber-800 dark:border-amber-400/40 dark:bg-amber-400/10 dark:text-amber-200"
      data-leikwan-grace=""
      role="status"
    >
      <AlertTriangle className="mt-0.5 shrink-0" size={16} />
      <span className="min-w-0 break-words">
        主入口已从 <b>{from}</b> 换到 <b>{to}</b>；旧地址最晚{" "}
        <b>{fmtGraceUntil(gr.until)}</b>{" "}
        停用。用订阅的刷新一次即可，用节点链接的请重新复制
      </span>
    </div>
  );
}

function UserLanding({
  rule,
  actions,
}: {
  rule: LeiKwanRule;
  actions: LeiKwanRuleActions;
}) {
  return (
    <div className="min-w-0">
      {rule.landing.kind === "manual" ? (
        <div className="break-all font-mono text-xs">
          {rule.landing.host}:{rule.landing.port}
        </div>
      ) : rule.landing.kind === "external" ? (
        <>
          <div className="break-words text-sm">
            {rule.landing.name || `${rule.landing.host}:${rule.landing.port}`}
          </div>
          <div className="text-xs text-default-500">{externalNote(rule)}</div>
        </>
      ) : (
        <>
          <div className="break-words text-sm">{rule.landing.name}</div>
          <div className="text-xs text-default-500">
            {rule.landing.serverName || "我的落地节点"}
          </div>
        </>
      )}
      <PendingBlock compact rule={rule} onSwap={actions.openSwap} />
    </div>
  );
}

function protocolChip(r: LeiKwanRule, compact = false) {
  return r.landing.kind === "manual" ? (
    <Pill tone="slate">手动</Pill>
  ) : r.landing.kind === "external" ? (
    <Pill title={externalNote(r)} tone="pink">
      {externalProtocol(r.landing.protocol) || "外部节点"}
    </Pill>
  ) : r.landing.protocol ? (
    <Pill tone="pink">{protocolLabel(r.landing.protocol, compact)}</Pill>
  ) : null;
}

export function LeiKwanUserView() {
  const h5 = useH5Mode();
  const navigate = useNavigate();
  const [data, setData] = useState<LeiKwanRuleList | null>(null);
  const [line, setLine] = useState<number | null>(null);
  // Plan 272: 「生成订阅」 dialog.
  const [subOpen, setSubOpen] = useState(false);

  const load = useCallback(async () => {
    const res = await getLeiKwanRules();

    if (res.code !== 0) {
      toast.error(res.msg || "读取利群转发失败");

      return;
    }
    if (res.data.granted === false) {
      // No (active) grant: the page is not for this user.
      void refreshLeiKwanNav();
      toast.error("没有利群汇聚授权");
      navigate("/dashboard", { replace: true });

      return;
    }
    setData(res.data);
  }, [navigate]);

  useEffect(() => {
    markLeiKwanNavSeen();
    void load();
  }, [load]);
  usePullToRefresh(load);

  const actions = useLeiKwanRuleActions({
    admin: false,
    reload: () => void load(),
    entryLock: !!data?.entryLock,
  });
  const subDialog = (
    <LeiKwanSubDialog
      admin={false}
      open={subOpen}
      onClose={() => setSubOpen(false)}
    />
  );
  const subNote = "推荐用订阅：换入口时刷新订阅即可，不用改地址";
  // Plan 280: 利群转发数 — 已用 x / N next to 添加转发, greyed at the limit.
  const ruleLimit = data?.ruleLimit;
  const limitFull = !!ruleLimit?.full;
  const limitText = ruleLimit
    ? ruleLimit.unlimited
      ? `已用 ${ruleLimit.used}（不限）`
      : `已用 ${ruleLimit.used} / ${ruleLimit.limit}`
    : "";
  const rules = useMemo(() => data?.rules || [], [data]);
  const lines = useMemo<UserLine[]>(() => {
    const out = [...(data?.lines || [])];

    // A rule on a line that is no longer listed still gets its group.
    rules.forEach((r) => {
      if (!out.some((l) => l.id === r.lineId))
        out.push({ id: r.lineId, name: r.lineName, routeLabel: r.routeLabel });
    });

    return out;
  }, [data, rules]);
  const mine = (id: number) => rules.filter((r) => r.lineId === id);
  const shown = lines.filter((l) => (line ? l.id === line : true));
  const groups = shown
    .map((l) => ({ line: l, rules: mine(l.id) }))
    .filter((g) => g.rules.length > 0 || line === g.line.id);
  const visibleCount = groups.reduce((n, g) => n + g.rules.length, 0);
  const expand = useExpandOne(rules.map((r) => r.id));
  const g = data?.grant;

  if (!data)
    return (
      <div className="flex justify-center py-16">
        <Spinner />
      </div>
    );

  const grantChips = (
    <>
      {g && (
        <Pill
          title={
            g.mainEntryOnline === false
              ? `${g.mainEntryHost || ""} · ${offlineText(g.mainEntryOfflineSince)}：你的利群转发现在连不上，请联系管理员`
              : g.mainEntryHost
          }
          tone={g.mainEntryOnline === false ? "red" : "teal"}
        >
          主入口 {g.mainEntryName || "—"}
          {g.mainEntryOnline === false ? " · 离线" : ""}
        </Pill>
      )}
      {g && (
        <Pill tone={g.backupLine ? "green" : "slate"}>
          备线 {g.backupLine ? "开" : "关"}
        </Pill>
      )}
      <Pill tone="purple">
        {h5 ? `转发 ${rules.length}` : `利群转发 ${rules.length} 条`}
      </Pill>
      {g && (g.backupFlow || 0) > 0 && (
        <Pill
          title="你的利群转发走备线用掉的流量，从上次重置算起，也算进总流量（共用的备线只算一次）"
          tone="green"
        >
          备线流量 {fmtBytes(g.backupFlow)}
        </Pill>
      )}
    </>
  );
  const emptyLine = (id: number) => (
    <div
      key={`empty-${id}`}
      className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-dashed border-divider px-4 py-3 text-sm text-default-500"
      data-leikwan-empty-line={id}
    >
      这条专线上还没有你的转发
      <Button
        color="success"
        isDisabled={limitFull}
        size="sm"
        startContent={<Plus size={14} />}
        variant="flat"
        onPress={() => actions.openCreate(id)}
      >
        在这条专线添加转发
      </Button>
    </div>
  );
  const noRules = (
    <div className="rounded-xl border border-dashed border-divider px-4 py-5 text-center text-sm text-default-500">
      还没有利群转发，点「添加转发」
    </div>
  );

  if (h5)
    return (
      <div className="space-y-3" data-leikwan-user="h5">
        <LkCard className="space-y-1.5 p-3">
          <div className="flex flex-wrap gap-1.5">{grantChips}</div>
          <p className="text-xs text-default-500">转发数、流量、到期跟账号走</p>
        </LkCard>
        <GraceNotice data={data} />
        <div className="space-y-1">
          <Button
            className="h-11 w-full"
            color="primary"
            startContent={<Link2 size={16} />}
            onPress={() => setSubOpen(true)}
          >
            生成订阅
          </Button>
          <p className="px-1 text-xs text-default-500">{subNote}</p>
        </div>
        <div className="space-y-1">
          <Button
            className="h-11 w-full"
            color="success"
            isDisabled={limitFull}
            startContent={<Plus size={16} />}
            variant="flat"
            onPress={() => actions.openCreate(line || undefined)}
          >
            添加转发
          </Button>
          {ruleLimit && (
            <p
              className={`px-1 text-xs ${limitFull ? "text-warning-600 dark:text-warning-400" : "text-default-500"}`}
              data-leikwan-limit=""
            >
              {limitFull ? ruleLimit.error : `利群转发${limitText}`}
            </p>
          )}
        </div>
        <div className="flex gap-2 overflow-x-auto pb-0.5">
          <LkTab active={line === null} onPress={() => setLine(null)}>
            全部
          </LkTab>
          {lines.map((l) => (
            <LkTab
              key={l.id}
              active={line === l.id}
              onPress={() => setLine(line === l.id ? null : l.id)}
            >
              {lineShort(l.name)}
            </LkTab>
          ))}
        </div>
        {groups.map(({ line: l, rules: list }) => (
          <div key={l.id} className="space-y-2" data-leikwan-line={l.name}>
            <div className="flex flex-wrap items-center gap-2 px-0.5">
              <span className="text-[15px] font-semibold">{l.name}</span>
              <Pill title={l.routeLabel} tone="teal">
                {routeShort(l.routeLabel)}
              </Pill>
            </div>
            {list.map((r) => (
              <LkExpandCard
                key={r.id}
                chip={protocolChip(r, true)}
                data-leikwan-rule={r.name}
                dot={<RuleDot rule={r} />}
                name={r.name}
                open={expand.open === r.id}
                onToggle={() => expand.toggle(r.id)}
              >
                <LkField label="入口">
                  {r.entry ? (
                    <button
                      className="font-mono text-xs"
                      type="button"
                      onClick={() =>
                        void copyText(r.entry!.address, "入口地址")
                      }
                    >
                      {r.entry.address}
                    </button>
                  ) : (
                    "授权没有主入口"
                  )}
                  {r.entry?.online === false && (
                    <OfflinePill
                      inline
                      label="主入口离线"
                      since={r.entry.offlineSince}
                    />
                  )}
                </LkField>
                {/* Plan 310: the row keeps one chip (protocol); 全程 here. */}
                <RuleLatencyField rule={r} />
                <LkField label="落地">
                  {r.landing.kind === "manual"
                    ? `${r.landing.host}:${r.landing.port}`
                    : r.landing.name}
                  {r.landing.kind === "external" && (
                    <span className="text-xs text-default-500">
                      {externalNote(r)}
                    </span>
                  )}
                </LkField>
                <LkField label="备线">
                  {r.backupError ? (
                    <UserBackupChip rule={r} />
                  ) : (
                    <Pill tone={r.backupOn ? "green" : "slate"}>
                      {r.backupOn ? "开 · 断线自动切，地址不变" : "没开"}
                    </Pill>
                  )}
                </LkField>
                {(r.inFlow !== undefined ||
                  r.outFlow !== undefined ||
                  r.vendorFlow) && (
                  <LkField label="流量">
                    <FlowPill rule={r} />
                    <BackupFlowPill rule={r} />
                    <VendorFlowPill rule={r} />
                  </LkField>
                )}
                {r.userExpired && (
                  <LkField label="到期">
                    <Pill tone="red">已到期</Pill>
                  </LkField>
                )}
                {r.blockReason && (
                  <LkField label="状态">
                    <span className="text-red-700 dark:text-red-300">
                      {blockedNote(r)}
                    </span>
                  </LkField>
                )}
                {r.pendingLanding && (
                  <LkField label="待切换">
                    <PendingBlock
                      compact
                      inField
                      rule={r}
                      onSwap={actions.openSwap}
                    />
                  </LkField>
                )}
                <div className="flex flex-wrap gap-2 pt-1">
                  <RuleButtons fill actions={actions} admin={false} rule={r} />
                </div>
              </LkExpandCard>
            ))}
            {list.length === 0 && emptyLine(l.id)}
          </div>
        ))}
        {groups.length === 0 && noRules}
        {actions.modals}
        {subDialog}
      </div>
    );

  return (
    <div className="space-y-5" data-leikwan-user="">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 space-y-2.5">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h1 className="text-2xl font-bold">利群汇聚</h1>
            <span className="text-sm text-default-500">
              利群专线的转发在这里添加，跟「转发」页分开
            </span>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {grantChips}
            <Pill tone="slate">转发数、流量、到期跟账号走</Pill>
          </div>
          <p className="text-xs text-default-500">{subNote}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            color="primary"
            startContent={<Link2 size={15} />}
            title="一条订阅链接带上你全部的利群转发"
            onPress={() => setSubOpen(true)}
          >
            生成订阅
          </Button>
          {ruleLimit && (
            <span
              className="self-center text-xs text-default-500"
              data-leikwan-limit=""
              title="利群转发数：只算利群汇聚里的转发，不占总转发数"
            >
              {limitText}
            </span>
          )}
          <Button
            color="success"
            isDisabled={limitFull}
            startContent={<Plus size={15} />}
            variant="flat"
            onPress={() => actions.openCreate(line || undefined)}
          >
            添加转发
          </Button>
        </div>
      </header>
      {limitFull && (
        <p
          className="-mt-3 text-right text-xs text-warning-600 dark:text-warning-400"
          data-leikwan-limit-full=""
        >
          {ruleLimit?.error}
        </p>
      )}
      <GraceNotice data={data} />
      <LkCard className="space-y-3 p-4" data-leikwan-lines="">
        <LkCardTitle
          chip={<Pill tone="teal">{lines.length} 条</Pill>}
          hint="点一条，只看这条专线的转发"
          title="可用专线"
        />
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {lines.map((l) => {
            const n = mine(l.id).length;
            const active = line === l.id;

            return (
              <button
                key={l.id}
                aria-pressed={active}
                className={`flex min-w-0 flex-col gap-2.5 rounded-xl border p-3.5 text-left transition-colors ${
                  active
                    ? "border-teal-500/60 bg-teal-500/[0.08] dark:border-teal-400/50 dark:bg-teal-400/10"
                    : "border-divider bg-default-50/60 hover:border-teal-500/30 dark:bg-default-50/30"
                }`}
                data-leikwan-line-card={l.name}
                type="button"
                onClick={() => setLine(active ? null : l.id)}
              >
                <span className="flex items-start justify-between gap-2">
                  <span className="min-w-0 break-words text-[15px] font-semibold">
                    {l.name}
                  </span>
                  {n > 0 ? (
                    <Pill tone="purple">我的 {n}</Pill>
                  ) : (
                    <Pill tone="slate">还没建</Pill>
                  )}
                </span>
                <span className="flex flex-wrap gap-1">
                  <Pill title={l.routeLabel} tone="teal">
                    {routeShort(l.routeLabel)}
                  </Pill>
                  {l.transport && <Pill tone="pink">{l.transport}</Pill>}
                </span>
              </button>
            );
          })}
        </div>
      </LkCard>
      <LkCard className="overflow-hidden" data-leikwan-mine="">
        <div className="border-b border-divider px-4 py-3.5">
          <LkCardTitle
            chip={<Pill tone="purple">{visibleCount} 条</Pill>}
            right={
              <span className="text-xs text-default-500">
                入口 = 你的主入口 IP : 利群端口；断线时自动走备线，地址不变
              </span>
            }
            title="我的利群转发"
          />
        </div>
        <div className="space-y-4 p-4">
          {groups.map(({ line: l, rules: list }) => (
            <div key={l.id} className="space-y-2" data-leikwan-line={l.name}>
              {list.length > 0 && (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-[15px] font-semibold">{l.name}</span>
                    <Pill title={l.routeLabel} tone="teal">
                      {routeShort(l.routeLabel)}
                    </Pill>
                    <Pill tone="purple">{list.length} 条</Pill>
                  </div>
                  <div className={`${RULE_GRID} px-3 text-xs text-default-500`}>
                    <span>转发</span>
                    <span>入口地址</span>
                    <span>落地</span>
                    <span className="text-right">操作</span>
                  </div>
                </>
              )}
              {list.map((r) => (
                <div
                  key={r.id}
                  className={`${RULE_GRID} items-center rounded-xl border border-divider bg-default-50/60 px-3 py-2.5 dark:bg-default-50/30`}
                  data-leikwan-rule={r.name}
                >
                  <div className="min-w-0 space-y-1.5">
                    <div className="flex items-start gap-1.5">
                      <RuleDot rule={r} />
                      <span className="min-w-0 break-words text-sm font-semibold leading-5">
                        {r.name}
                      </span>
                    </div>
                    <div className="flex flex-wrap gap-1">
                      {/* Plan 310: 全程 tag (入口机 → 利群专线出口). */}
                      <RuleLatencyTag rule={r} />
                      <UserBackupChip rule={r} />
                      {protocolChip(r)}
                      {r.userExpired && <Pill tone="red">已到期</Pill>}
                      <BlockedPill rule={r} />
                      <FlowPill rule={r} />
                      <BackupFlowPill rule={r} />
                      <VendorFlowPill rule={r} />
                    </div>
                    {r.blockReason && (
                      <div className="text-[11px] text-red-700 dark:text-red-300">
                        {blockedNote(r)}
                      </div>
                    )}
                  </div>
                  <div className="min-w-0">
                    {r.entry ? (
                      <button
                        className="inline-flex max-w-full items-center gap-1.5 rounded-lg bg-default-100 px-2 py-1 hover:bg-default-200 dark:bg-default-100/50"
                        title="点一下复制"
                        type="button"
                        onClick={() =>
                          void copyText(r.entry!.address, "入口地址")
                        }
                      >
                        <span className="break-all font-mono text-xs">
                          {r.entry.address}
                        </span>
                        <Copy className="shrink-0 text-default-400" size={12} />
                      </button>
                    ) : (
                      <span className="text-xs text-default-400">
                        授权没有主入口
                      </span>
                    )}
                    {r.entry?.online === false && (
                      <div className="mt-1">
                        <OfflinePill
                          inline
                          label="主入口离线"
                          since={r.entry.offlineSince}
                        />
                      </div>
                    )}
                  </div>
                  <UserLanding actions={actions} rule={r} />
                  <div className="flex flex-wrap justify-end gap-1.5">
                    <RuleButtons
                      compact
                      actions={actions}
                      admin={false}
                      rule={r}
                    />
                  </div>
                </div>
              ))}
              {list.length === 0 && emptyLine(l.id)}
            </div>
          ))}
          {groups.length === 0 && noRules}
        </div>
      </LkCard>
      {actions.modals}
      {subDialog}
    </div>
  );
}
