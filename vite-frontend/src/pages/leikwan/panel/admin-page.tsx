import type { ReactNode } from "react";
import type { LeiKwanAccount } from "@/api/leikwan";
import type {
  LeiKwanEntryCaps,
  LeiKwanGrant,
  LeiKwanHostList,
  LeiKwanHostManage,
  LeiKwanLine,
  LeiKwanLineList,
  LeiKwanRule,
  LeiKwanRuleList,
  LeiKwanUsageView,
} from "@/api/leikwan-line";
import type { LeiKwanHostItem } from "@/pages/leikwan/panel/host-manage";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  AlertTriangle,
  Copy,
  Link2,
  Plus,
  RefreshCw,
  Settings,
} from "lucide-react";
import toast from "react-hot-toast";

import { getAllUsers } from "@/api";
import { deleteLeiKwanPipe, getLeiKwanAccounts } from "@/api/leikwan";
import {
  deleteLeiKwanGrant,
  getLeiKwanGrants,
  getLeiKwanHosts,
  getLeiKwanLines,
  getLeiKwanRules,
  getLeiKwanUsage,
} from "@/api/leikwan-line";
import { useExpandOne } from "@/components/expand-row";
import { useGroupScrollSettle } from "@/hooks/use-group-scroll-settle";
import { useH5Mode } from "@/hooks/useH5Mode";
import { usePullToRefresh } from "@/hooks/usePullToRefresh";
import { AttentionIcon } from "@/pages/landing/admin-attention";
import { Pill } from "@/pages/landing/pill";
import { BackupRebuildButton } from "@/pages/leikwan/panel/backup-tunnel";
import {
  EntryLimitPills,
  OfflinePill,
  hostOnline,
  hostRunning,
  hostStatusText,
  hostStatusTone,
  limitOf,
} from "@/pages/leikwan/panel/entry-state";
import {
  LeiKwanGrantDialog,
  type LeiKwanGrantDialogMode,
} from "@/pages/leikwan/panel/grant-dialog";
import { HostDetails, HostDrawer } from "@/pages/leikwan/panel/host-drawer";
import {
  HostNameMarker,
  hostAttentionLevel,
  hostsAttentionLevel,
} from "@/pages/leikwan/panel/host-manage";
import {
  LkCard,
  LkCardTitle,
  LkChevron,
  LkDot,
  LkExpandCard,
  LkField,
  LkTab,
} from "@/pages/leikwan/panel/layout-parts";
import { LeiKwanLineDialog } from "@/pages/leikwan/panel/line-dialog";
import {
  AdminBackupChip,
  AllEntriesPill,
  BlockedPill,
  BackupFlowPill,
  FlowPill,
  PendingBlock,
  RuleButtons,
  RuleDot,
  blockedNote,
  copyText,
  externalNote,
  externalProtocol,
  protocolLabel,
  routeShort,
  useLeiKwanRuleActions,
  type LeiKwanRuleActions,
} from "@/pages/leikwan/panel/rule-actions";
import { LeiKwanSettingsModal } from "@/pages/leikwan/panel/settings-modal";
import { LeiKwanSubDialog } from "@/pages/leikwan/panel/sub-dialog";
import { OnOff, QuotaPill, fmtBytes } from "@/pages/leikwan/panel/shared";
import {
  DevicesCard,
  DevicesPhoneList,
  PackageCard,
  PackagePhoneBlock,
  VendorFlowPill,
} from "@/pages/leikwan/panel/usage-parts";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import { getSessionName } from "@/utils/session";

// Plan 270: the admin view of 利群汇聚 — everything 利群 on one page (approved
// design 11-17, screens 01–05). Desktop: header chips, the 「专线 · 规则」
// tree (one 专线 open at a time) and a right column with 授权 / 入口机 (tiles
// → host drawer with the plan-269 面板接管 controls) / 旧管道. H5: summary
// chips + tabs 规则 / 授权 / 入口机 with ＋/− rows (one open per list).

type Pipe = LeiKwanLineList["pipes"][number];

interface LineGroup {
  id: number;
  line?: LeiKwanLine;
  name: string;
  route: string;
  rules: LeiKwanRule[];
}

const testNote = (r: LeiKwanRule) =>
  (r.accelOverride?.note || "").includes("测试")
    ? r.accelOverride?.note
    : undefined;

function WarmChip({ rule }: { rule: LeiKwanRule }) {
  if (!rule.accel) return null;
  // Plan 295: the owner's 主入口 has no 预热池 → grey, with the reason.
  const noWarm = limitOf(rule.entryLimits, "warm");

  if (rule.accel.warmMain > 0 && noWarm)
    return (
      <Pill
        title={`设置是预热 ${rule.accel.warmMain}，但${noWarm.why}：这条转发现在不预热`}
        tone="slate"
      >
        预热 · 入口不支持
      </Pill>
    );

  return rule.accel.warmMain > 0 ? (
    <Pill title={rule.accelOverride?.note} tone="amber">
      预热 {rule.accel.warmMain}
    </Pill>
  ) : (
    <Pill title={rule.accelOverride?.note} tone="slate">
      不预热
    </Pill>
  );
}

/** Plan 295 (H5): one line naming what the owner's 主入口 cannot run. */
const limitsNote = (r: LeiKwanRule) => {
  const list = r.entryLimits || [];
  const stable = list.some((l) => l.why.includes("稳定版"));

  return `主入口 ${r.entry?.nodeName || ""} 不支持 ${list.map((l) => l.label).join("、")}${stable ? "（利群客户端是稳定版）" : ""}`;
};

/** The 利群加速 chips of a 专线 (group header line 2). */
function LineChips({
  line,
  compact = false,
  noWarm = [],
}: {
  line: LeiKwanLine;
  compact?: boolean;
  /** Plan 295: 利群入口 without a 预热池 (names) */
  noWarm?: string[];
}) {
  const a = line.accel;
  const backup = line.backupTunnelName;

  if (compact)
    return (
      <>
        {backup ? (
          <Pill title={`备线隧道 · ${backup}`} tone="green">
            备线 {shortTunnel(backup)}
          </Pill>
        ) : (
          <Pill tone="slate">备线 不建</Pill>
        )}
        {a.warm.main > 0 ? (
          <Pill tone="amber">
            预热 {a.warm.main}/{a.warm.backup}/{a.warm.standby}
          </Pill>
        ) : (
          <Pill tone="slate">不预热</Pill>
        )}
        <Pill tone={a.logswitch ? "green" : "slate"}>
          快切 {a.logswitch ? "开" : "关"}
        </Pill>
      </>
    );

  return (
    <>
      <Pill
        wrap
        title={backup || "这条专线不建备线"}
        tone={backup ? "green" : "slate"}
      >
        备线隧道 · {backup || "不建"}
      </Pill>
      {a.warm.main > 0 ? (
        <Pill tone="amber">
          预热 主{a.warm.main} · 备{a.warm.backup} · 待{a.warm.standby}
        </Pill>
      ) : (
        <Pill tone="slate">不预热</Pill>
      )}
      {a.warm.main > 0 && noWarm.length > 0 && (
        <Pill
          title={`${noWarm.join("、")} 没有预热池：主入口是它们的用户，这条专线的转发不预热`}
          tone="slate"
        >
          {noWarm.length} 台入口不支持预热
        </Pill>
      )}
      <Pill tone={a.logswitch ? "green" : "slate"}>
        日志快切 {a.logswitch ? "开" : "关"}
      </Pill>
      <Pill tone={a.logswitch && a.returnCheck ? "green" : "slate"}>
        切回复查 {a.logswitch && a.returnCheck ? "开" : "关"}
      </Pill>
      <Pill tone="slate">
        SS 过滤 {a.ssFilter === "enforce" ? "强制" : "关"}
      </Pill>
      {(line.flowMode !== undefined || line.trafficRatio !== undefined) && (
        <Pill
          title="利群流量计费：单向 (上传+下载)×倍率；双向 再 ×2"
          tone="purple"
        >
          {line.flowMode === 2 ? "双向" : "单向"} ×{line.trafficRatio || 1}
        </Pill>
      )}
    </>
  );
}

/** 「mk深港ixp1 -> HK(端口转发…)」 → 「mk深港ixp1 -> HK」 for the H5 chip. */
const shortTunnel = (name: string) =>
  name
    .replace(/[（(].*$/, "")
    .replace(/\s*->\s*/g, "→")
    .trim() || name;

function LandingCell({
  rule,
  actions,
}: {
  rule: LeiKwanRule;
  actions: LeiKwanRuleActions;
}) {
  const manual = rule.landing.kind === "manual";

  // Plan 272: a 外部节点 landing = its name + muted 「外部节点 · 协议」.
  if (rule.landing.kind === "external")
    return (
      <div className="min-w-0 space-y-1">
        <div
          className="break-words text-sm"
          title={`${rule.landing.host}:${rule.landing.port}`}
        >
          {rule.landing.name || `${rule.landing.host}:${rule.landing.port}`}
        </div>
        <div className="text-xs text-default-500">{externalNote(rule)}</div>
        <PendingBlock compact rule={rule} onSwap={actions.openSwap} />
        {rule.landingHint && !rule.pendingLanding && (
          <div className="text-[11px] text-amber-600 dark:text-amber-300">
            {rule.landingHint}
          </div>
        )}
      </div>
    );

  return (
    <div className="min-w-0 space-y-1">
      {manual ? (
        <div className="break-all font-mono text-xs text-default-700">
          {rule.landing.host}:{rule.landing.port}
        </div>
      ) : (
        <div
          className="break-words text-sm"
          title={`${rule.landing.host}:${rule.landing.port}`}
        >
          {rule.landing.serverName || "落地"} · {rule.landing.name}
        </div>
      )}
      <div className="flex flex-wrap gap-1">
        {manual ? (
          <Pill title={rule.landingHint} tone="slate">
            手动
          </Pill>
        ) : (
          <Pill tone="pink">{protocolLabel(rule.landing.protocol)}</Pill>
        )}
      </div>
      <PendingBlock compact rule={rule} onSwap={actions.openSwap} />
      {rule.landingHint && !rule.pendingLanding && (
        <div className="text-[11px] text-amber-600 dark:text-amber-300">
          {rule.landingHint}
        </div>
      )}
    </div>
  );
}

function EntryCopy({ rule }: { rule: LeiKwanRule }) {
  if (!rule.entry)
    return <span className="text-xs text-default-400">授权没有主入口</span>;

  return (
    <div className="flex min-w-0 flex-col items-start gap-1">
      <EntryCopyButton rule={rule} />
      {rule.entry.online === false && (
        <OfflinePill
          inline
          label={`${rule.entry.nodeName} 离线`}
          since={rule.entry.offlineSince}
        />
      )}
    </div>
  );
}

function EntryCopyButton({ rule }: { rule: LeiKwanRule }) {
  if (!rule.entry) return null;

  return (
    <button
      className="inline-flex max-w-full items-center gap-1.5 rounded-lg bg-default-100 px-2 py-1 text-foreground hover:bg-default-200 dark:bg-default-100/50"
      title={`入口 ${rule.entry.nodeName} · 点一下复制`}
      type="button"
      onClick={() => void copyText(rule.entry!.address, "入口地址")}
    >
      <span className="whitespace-nowrap font-mono text-xs">
        {rule.entry.address}
      </span>
      <Copy className="shrink-0 text-default-400" size={12} />
    </button>
  );
}

// Plan 276 (F13): the 入口地址 column fits a 15-character IPv4 + port unbroken.
const RULE_GRID =
  "grid grid-cols-[minmax(0,1.2fr)_184px_minmax(0,1fr)_184px] gap-2.5";

function AdminRuleRow({
  rule: r,
  actions,
}: {
  rule: LeiKwanRule;
  actions: LeiKwanRuleActions;
}) {
  const test = testNote(r);

  return (
    <div
      className={`${RULE_GRID} items-center rounded-xl border border-divider bg-default-50/60 px-2.5 py-2.5 dark:bg-default-50/30`}
      data-leikwan-rule={r.name}
    >
      <div className="min-w-0 space-y-1.5">
        <div className="flex items-start gap-1.5">
          <RuleDot rule={r} />
          {r.backupError && (
            <AlertTriangle
              aria-label="备线未建好"
              className="mt-0.5 shrink-0 text-red-700"
              size={15}
            />
          )}
          <span className="min-w-0 break-words text-sm font-semibold leading-5">
            {r.name}
          </span>
        </div>
        <div className="flex flex-wrap gap-1">
          <Pill
            title={r.source === "import" ? "导入时登记的利群转发" : undefined}
            tone="purple"
          >
            利群 #{r.forwardId}
          </Pill>
          <AdminBackupChip rule={r} />
          <WarmChip rule={r} />
          <EntryLimitPills limits={r.entryLimits} />
          {r.userName && <Pill tone="slate">{r.userName}</Pill>}
          {test && (
            <Pill title={test} tone="amber">
              测试
            </Pill>
          )}
          {r.userExpired && (
            <Pill title="用户已到期：不能添加利群转发" tone="red">
              已到期
            </Pill>
          )}
          <BlockedPill rule={r} />
          <FlowPill rule={r} />
          <BackupFlowPill rule={r} />
          <VendorFlowPill rule={r} />
          <AllEntriesPill actions={actions} rule={r} />
        </div>
        {r.blockReason && (
          <div className="text-[11px] text-red-700 dark:text-red-300">
            {blockedNote(r)}
          </div>
        )}
      </div>
      <div className="min-w-0">
        <EntryCopy rule={r} />
      </div>
      <LandingCell actions={actions} rule={r} />
      <div className="flex flex-wrap justify-end gap-1.5">
        <RuleButtons admin compact actions={actions} rule={r} />
      </div>
    </div>
  );
}

// Plan 287: the 转发 / 隧道 page position correction on a 专线 header tap.
const lineGroupKey = (id: number) => `lk-line:${id}`;

function LineGroupBlock({
  group: g,
  open,
  onToggle,
  onEdit,
  onChanged,
  actions,
  noWarm,
}: {
  group: LineGroup;
  open: boolean;
  onToggle: () => void;
  onEdit: (l: LeiKwanLine) => void;
  onChanged: () => void;
  actions: LeiKwanRuleActions;
  noWarm?: string[];
}) {
  const line = g.line;
  const attention = g.rules.some((r) => r.backupError);

  return (
    <div
      className="flvx-group-anchor border-b border-divider last:border-b-0"
      data-group-key={lineGroupKey(g.id)}
      data-leikwan-line={g.name}
      data-open={open ? "" : undefined}
    >
      {}
      <div
        aria-expanded={open}
        className={`flex cursor-pointer flex-col gap-2 px-4 py-3.5 ${open ? "bg-teal-500/[0.06] dark:bg-teal-400/[0.07]" : "hover:bg-default-50 dark:hover:bg-default-50/30"}`}
        data-group-header=""
        role="button"
        tabIndex={0}
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onToggle();
          }
        }}
      >
        <div className="flex items-center justify-between gap-3">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <LkChevron open={open} />
            <span className="text-[15px] font-semibold">{g.name}</span>
            <Pill title={g.route} tone="teal">
              {routeShort(g.route)}
            </Pill>
            {line && <Pill tone="pink">{line.transport}</Pill>}
            <Pill tone="purple">{g.rules.length} 条</Pill>
            {line && line.status !== 1 && <Pill tone="slate">已停用</Pill>}
            {attention && (
              <AlertTriangle
                aria-label="有备线未建好"
                className="text-red-700"
                size={15}
              />
            )}
          </div>
          {line && (
            // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions
            <span
              className="flex shrink-0 flex-wrap items-center justify-end gap-1.5"
              onClick={(e) => e.stopPropagation()}
            >
              {(line.backupStats?.stale || 0) > 0 && (
                <BackupRebuildButton line={line} onDone={onChanged} />
              )}
              <Button
                color="primary"
                size="sm"
                variant="flat"
                onPress={() => onEdit(line)}
              >
                编辑专线
              </Button>
            </span>
          )}
        </div>
        {line && (
          <div className="flex flex-wrap gap-1.5 pl-6">
            <LineChips line={line} noWarm={noWarm} />
          </div>
        )}
      </div>
      {!open && (
        <div className="px-4 pb-3 pl-10 text-xs leading-5 text-default-500">
          {g.rules.length
            ? g.rules.map((r) => r.name).join(" · ")
            : "还没有转发"}
        </div>
      )}
      {open && (
        <div className="space-y-2 pb-3 pl-8 pr-3 pt-1">
          {g.rules.length > 0 && (
            <div className={`${RULE_GRID} px-2.5 text-xs text-default-500`}>
              <span>转发</span>
              <span>入口地址</span>
              <span>落地</span>
              <span className="text-right">操作</span>
            </div>
          )}
          {g.rules.map((r) => (
            <AdminRuleRow key={r.id} actions={actions} rule={r} />
          ))}
          {g.rules.length === 0 && (
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-dashed border-divider px-4 py-3 text-sm text-default-500">
              这条专线还没有转发
              <Button
                color="success"
                size="sm"
                startContent={<Plus size={14} />}
                variant="flat"
                onPress={() => actions.openCreate(g.id)}
              >
                在这条专线添加转发
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function HostTile({
  host,
  onOpen,
}: {
  host: LeiKwanHostItem;
  onOpen: () => void;
}) {
  return (
    <button
      className="flex min-w-0 flex-col gap-1.5 rounded-xl border border-divider bg-default-50/60 p-2.5 text-left transition-colors hover:border-teal-500/40 hover:bg-teal-500/5 dark:bg-default-50/30"
      data-leikwan-host-tile={host.node.id}
      type="button"
      onClick={onOpen}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <LkDot off={!hostOnline(host)} ok={hostRunning(host)} />
        <span className="min-w-0 break-words text-[13px] font-semibold">
          {host.node.name}
        </span>
        <HostNameMarker manage={host.manage} />
      </span>
      <span className="flex flex-wrap gap-1">
        {hostOnline(host) ? (
          <Pill tone={hostStatusTone(host)}>{hostStatusText(host)}</Pill>
        ) : (
          <OfflinePill since={host.node.offlineSince} />
        )}
        <Pill tone={host.mainEntryUsers > 0 ? "teal" : "slate"}>
          主入口 {host.mainEntryUsers} 人
        </Pill>
      </span>
    </button>
  );
}

const initial = (name?: string) => (name || "?").slice(0, 1);

/** Plan 295: the 主入口 chip — red when offline, click = 换主入口. */
function EntryChip({
  g,
  onEntry,
  prefix = "主入口 ",
}: {
  g: LeiKwanGrant;
  onEntry?: (g: LeiKwanGrant) => void;
  prefix?: string;
}) {
  const off = g.mainEntryOnline === false;
  const pill = (
    <Pill
      title={`${g.mainEntryHost || ""}${off ? " · 离线" : ""}${onEntry ? " · 点一下换主入口" : ""}`}
      tone={off ? "red" : g.mainEntryOutsideGroup ? "amber" : "teal"}
    >
      {prefix}
      {g.mainEntryName || "—"}
      {g.mainEntryOutsideGroup ? " · 不在入口组" : ""}
      {off ? " · 离线" : ""}
    </Pill>
  );

  if (!onEntry) return pill;

  return (
    <button
      className="inline-flex max-w-full rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
      data-leikwan-entry-chip={g.userName}
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        onEntry(g);
      }}
    >
      {pill}
    </button>
  );
}

function GrantChips({
  g,
  onEntry,
}: {
  g: LeiKwanGrant;
  onEntry?: (g: LeiKwanGrant) => void;
}) {
  return (
    <>
      <EntryChip g={g} onEntry={onEntry} />
      <OnOff label="备线" on={g.backupLine} />
      <Pill tone="purple">转发 {g.ruleCount}</Pill>
      {g.status !== 1 && <Pill tone="slate">已停用</Pill>}
      {(g.backupFlow || 0) > 0 && (
        <Pill
          title="这个用户的利群转发走备线用掉的流量，从上次重置算起（共用的备线只算一次）"
          tone="green"
        >
          备线流量 {fmtBytes(g.backupFlow)}
        </Pill>
      )}
    </>
  );
}

/** 「MM-DD HH:mm」 of a timestamp (ms, or s from older payloads). */
function shortTime(t: number) {
  const d = new Date(t < 1e12 ? t * 1000 : t);
  const p = (n: number) => String(n).padStart(2, "0");

  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** Plan 272: the old 主入口 kept open after a change (grace). */
function GracePill({ g }: { g: LeiKwanGrant }) {
  if (!g.grace) return null;

  return (
    <Pill
      wrap
      title={`换主入口后旧入口「${g.grace.prevEntryName}」暂时保留：这个用户刷新任一订阅后就停用，最晚到 ${shortTime(g.grace.until)}`}
      tone="amber"
    >
      旧入口 {g.grace.prevEntryName || `#${g.grace.prevEntryNodeId}`} 保留中 ·
      最晚 {shortTime(g.grace.until)}
    </Pill>
  );
}

export function LeiKwanAdminView() {
  const h5 = useH5Mode();
  const navigate = useNavigate();
  const [data, setData] = useState<LeiKwanLineList | null>(null);
  const [hosts, setHosts] = useState<LeiKwanHostList | null>(null);
  const [grants, setGrants] = useState<LeiKwanGrant[]>([]);
  const [rules, setRules] = useState<LeiKwanRuleList | null>(null);
  const [accounts, setAccounts] = useState<LeiKwanAccount[]>([]);
  const [users, setUsers] = useState<{ id: number; name: string }[]>([]);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  // Plan 286 r2: every 专线 starts collapsed (no first-line auto-open).
  const [openLine, setOpenLine] = useState<number | null>(null);
  const [lineOpen, setLineOpen] = useState(false);
  const [editLine, setEditLine] = useState<LeiKwanLine | null>(null);
  const [grantOpen, setGrantOpen] = useState(false);
  const [editGrant, setEditGrant] = useState<LeiKwanGrant | null>(null);
  // Plan 295: 换主入口 (grant row / 主入口 chip / host drawer 「设为主入口…」).
  const [grantMode, setGrantMode] = useState<LeiKwanGrantDialogMode>("grant");
  const [presetEntry, setPresetEntry] = useState<number | undefined>();
  const [revoke, setRevoke] = useState<number | null>(null);
  const [drawerHost, setDrawerHost] = useState<number | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [tab, setTab] = useState<"rules" | "grants" | "hosts">("rules");
  // Plan 273: 套餐流量 + 利群在线设备 (the panel's snapshot of the vendor).
  const [usage, setUsage] = useState<LeiKwanUsageView | null>(null);
  const [pipeBusy, setPipeBusy] = useState<number | null>(null);
  // Plan 272: 「生成订阅」 dialog.
  const [subOpen, setSubOpen] = useState(false);
  const me = getSessionName();

  const loadAccounts = useCallback(async () => {
    const res = await getLeiKwanAccounts();

    if (res.code === 0) setAccounts(res.data || []);
  }, []);
  const load = useCallback(async () => {
    setLoading(true);
    const [l, hst, g, r, u] = await Promise.all([
      getLeiKwanLines(),
      getLeiKwanHosts(),
      getLeiKwanGrants(),
      getLeiKwanRules(),
      getLeiKwanUsage(),
      loadAccounts(),
    ]);

    setLoading(false);
    setLoaded(true);
    if (l.code === 0) setData(l.data);
    else toast.error(l.msg || "读取利群专线失败");
    if (hst.code === 0) setHosts(hst.data);
    if (g.code === 0) setGrants(g.data.grants || []);
    if (r.code === 0) setRules(r.data);
    if (u.code === 0) setUsage(u.data);
  }, [loadAccounts]);
  const reloadRules = useCallback(() => {
    void load();
  }, [load]);

  useEffect(() => {
    void load();
  }, [load]);
  usePullToRefresh(load);
  // Plan 273: the card follows the 5 min vendor reads while the page is open
  // (a panel-memory read, never a vendor call).
  useEffect(() => {
    const t = window.setInterval(() => {
      if (document.visibilityState !== "visible") return;
      void getLeiKwanUsage().then((u) => {
        if (u.code === 0) setUsage(u.data);
      });
    }, 120_000);

    return () => window.clearInterval(t);
  }, []);
  useEffect(() => {
    if (!grantOpen || users.length) return;
    void getAllUsers({}).then((res) => {
      if (res.code === 0 && Array.isArray(res.data))
        setUsers(res.data.map((u) => ({ id: u.id, name: u.user })));
    });
  }, [grantOpen, users.length]);

  // Plan 272: global 「只开主入口」 (rule list; the setting as a fallback).
  const entryLock = !!(rules?.entryLock ?? data?.setting.global.entryLock);
  const actions = useLeiKwanRuleActions({
    admin: true,
    reload: reloadRules,
    entryLock,
  });

  const groups = useMemo<LineGroup[]>(() => {
    const byLine = new Map<number, LeiKwanRule[]>();

    (rules?.rules || []).forEach((r) => {
      byLine.set(r.lineId, [...(byLine.get(r.lineId) || []), r]);
    });
    const out: LineGroup[] = (data?.lines || []).map((line) => ({
      id: line.id,
      line,
      name: line.name,
      route: line.routeLabel,
      rules: byLine.get(line.id) || [],
    }));

    byLine.forEach((list, id) => {
      if (!out.some((g) => g.id === id))
        out.push({
          id,
          name: list[0].lineName,
          route: list[0].routeLabel,
          rules: list,
        });
    });

    return out;
  }, [data, rules]);
  const currentOpen = openLine;
  const queueGroupScroll = useGroupScrollSettle();
  const toggleLine = (id: number) => {
    queueGroupScroll(lineGroupKey(id), currentOpen !== id);
    setOpenLine(currentOpen === id ? null : id);
  };

  // Plan 269: one host's new 面板接管 view (from a host/* action reply).
  const patchHost = (nodeId: number, view: LeiKwanHostManage) =>
    setHosts((cur) => {
      if (!cur) return cur;
      const list = cur.hosts.map((x) =>
        x.node.id === nodeId ? { ...x, manage: view } : x,
      );

      return {
        ...cur,
        hosts: list,
        attention: list.filter((x) => hostAttentionLevel(x.manage)).length,
      };
    });
  const hostList = hosts?.hosts || [];
  const running = hostList.filter(hostRunning).length;
  // Plan 295: what each entry can run (grant dialog) + entries without 预热池.
  const capsById = useMemo(() => {
    const out: Record<number, LeiKwanEntryCaps> = {};

    (hosts?.hosts || []).forEach((h) => {
      if (h.caps) out[h.node.id] = h.caps;
    });

    return out;
  }, [hosts]);
  const noWarm = hostList
    .filter((h) => h.caps?.known && !h.caps.warm)
    .map((h) => h.node.name);
  const entryLevel = hostsAttentionLevel(hosts);
  const entries = data?.entryNodes || [];
  const account =
    accounts.find((a) => a.id === data?.setting.accountId) || accounts[0];
  const ruleCount = rules?.rules.length || 0;
  const lineCount = data?.lines.length || 0;
  const pipes = data?.pipes || [];
  const nightOnly = !!hosts?.global?.nightOnly;
  const drawer = hostList.find((h) => h.node.id === drawerHost) || null;

  const openLineDialog = (l: LeiKwanLine | null) => {
    setEditLine(l);
    setLineOpen(true);
  };
  const openGrantDialog = (
    g: LeiKwanGrant | null,
    mode: LeiKwanGrantDialogMode = "grant",
  ) => {
    setEditGrant(g);
    setGrantMode(mode);
    setPresetEntry(undefined);
    setGrantOpen(true);
  };
  const openEntryDialog = (g: LeiKwanGrant) => openGrantDialog(g, "entry");
  // Host drawer 「设为主入口…」: pick a granted user, this entry preselected.
  const openSetMain = (nodeId: number) => {
    setDrawerHost(null);
    setEditGrant(null);
    setGrantMode("entry");
    setPresetEntry(nodeId);
    setGrantOpen(true);
  };
  const doRevoke = async (g: LeiKwanGrant) => {
    if (revoke !== g.userId) {
      setRevoke(g.userId);

      return;
    }
    setRevoke(null);
    const res = await deleteLeiKwanGrant(g.userId);

    if (res.code !== 0) toast.error(res.msg || "取消失败");
    else {
      toast.success("已取消授权");
      void load();
    }
  };
  const removePipe = async (p: Pipe) => {
    if (p.tunnelId) {
      // The regular 隧道 delete dialog (plan 094 pipe handling there).
      navigate("/tunnel", {
        state: {
          leikwanPipe: { id: p.id, forwardId: p.forwardId },
          leikwanDeleteTunnelId: p.tunnelId,
        },
      });

      return;
    }
    if (!p.id || !p.etag) return;
    if (pipeBusy !== -p.id) {
      setPipeBusy(-p.id); // first click = arm

      return;
    }
    setPipeBusy(p.id);
    const res = await deleteLeiKwanPipe({
      pipeId: p.id,
      etag: p.etag,
      confirmed: true,
    });

    setPipeBusy(null);
    if (res.code !== 0) toast.error(res.msg || "删除失败");
    else {
      toast.success("管道已删除，名额已腾出");
      void load();
    }
  };

  const headerChips = (
    <>
      {!h5 &&
        (account ? (
          <Pill title={account.name} tone="slate">
            账号 服务 {account.serviceId}
          </Pill>
        ) : (
          <Pill tone="amber">还没有利群账号</Pill>
        ))}
      {h5 && data?.quota && !data.quota.error ? (
        <Pill
          tone={
            data.quota.limit > 0 && data.quota.active >= data.quota.limit
              ? "amber"
              : "purple"
          }
        >
          名额 {data.quota.active} / {data.quota.limit}
          {data.quota.limit > 0 && data.quota.active >= data.quota.limit
            ? " · 已满"
            : ""}
        </Pill>
      ) : (
        <QuotaPill quota={data?.quota} />
      )}
      <Pill tone="teal">专线 {lineCount}</Pill>
      <Pill tone="purple">转发 {ruleCount}</Pill>
      {!h5 && <Pill tone="purple">授权 {grants.length} 人</Pill>}
      <Pill
        icon={entryLevel ? <AttentionIcon level={entryLevel} /> : undefined}
        tone={hostList.length && running < hostList.length ? "amber" : "green"}
      >
        入口机 {running} / {hostList.length}
        {h5 ? "" : " 运行中"}
      </Pill>
    </>
  );

  const pipeCard = pipes.length > 0 && (
    <LkCard className="space-y-2.5 p-4" data-leikwan-pipes="">
      <LkCardTitle
        chip={
          <Pill tone="amber">
            {pipes.length} 条 · 占 {pipes.length} 个名额
          </Pill>
        }
        title="旧管道"
      />
      {pipes.map((p) => (
        <div key={p.forwardId} className="space-y-2">
          <div className="flex flex-wrap items-center gap-1">
            <Pill tone="purple">利群 #{p.forwardId}</Pill>
            <Pill title={p.routeLabel} tone="teal">
              {routeShort(p.routeLabel)}
            </Pill>
            <Pill wrap tone="slate">
              {p.tunnelId
                ? `隧道「${p.tunnelName || `#${p.tunnelId}`}」${p.tunnelRules ?? 0} 条转发`
                : "管道没绑隧道"}
            </Pill>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              color="danger"
              isDisabled={!p.tunnelId && (!p.id || !p.etag)}
              isLoading={pipeBusy === p.id}
              size="sm"
              variant="flat"
              onPress={() => void removePipe(p)}
            >
              {!p.tunnelId && p.id && pipeBusy === -p.id
                ? "确认删除（利群转发一起删）"
                : "删掉，腾出名额"}
            </Button>
            {p.tunnelId && (
              <span className="text-xs text-default-500">
                在隧道页确认删除（可选连利群转发一起删）
              </span>
            )}
          </div>
        </div>
      ))}
    </LkCard>
  );

  const grantRows = grants.map((g) => (
    <div
      key={g.id}
      className="space-y-2.5 rounded-xl border border-divider bg-default-50/60 p-3 dark:bg-default-50/30"
      data-leikwan-grant-row={g.userName}
    >
      <div className="flex min-w-0 items-center gap-2.5">
        <span className="flex h-[30px] w-[30px] shrink-0 items-center justify-center rounded-full bg-purple-500/15 text-[13px] font-semibold text-purple-700 dark:bg-purple-400/20 dark:text-purple-300">
          {initial(g.userName)}
        </span>
        <span className="min-w-0 break-words font-semibold">{g.userName}</span>
        {g.userName && g.userName === me && <Pill tone="purple">管理员</Pill>}
      </div>
      <div className="flex flex-wrap gap-1">
        <GrantChips g={g} onEntry={openEntryDialog} />
        <GracePill g={g} />
      </div>
      <div className="flex flex-wrap gap-1.5">
        <Button
          color="primary"
          size="sm"
          variant="flat"
          onPress={() => openGrantDialog(g)}
        >
          修改
        </Button>
        <Button
          color="secondary"
          data-leikwan-switch-entry={g.userName}
          size="sm"
          variant="flat"
          onPress={() => openEntryDialog(g)}
        >
          换主入口
        </Button>
        <Button
          color="danger"
          isDisabled={g.ruleCount > 0}
          size="sm"
          title={g.ruleCount > 0 ? "先删掉这个用户的利群转发" : undefined}
          variant="flat"
          onPress={() => void doRevoke(g)}
        >
          {revoke === g.userId ? "确认取消授权" : "取消授权"}
        </Button>
      </div>
      {revoke === g.userId && (
        <p className="mt-1.5 text-xs text-amber-700 dark:text-amber-300">
          取消后这个用户看不到利群汇聚，利群汇聚订阅里的利群节点会消失
        </p>
      )}
    </div>
  ));

  const dialogs = (
    <>
      {actions.modals}
      <LeiKwanLineDialog
        data={data}
        isOpen={lineOpen}
        line={editLine}
        onClose={() => setLineOpen(false)}
        onSaved={() => void load()}
      />
      <LeiKwanGrantDialog
        caps={capsById}
        entries={entries}
        entryLock={entryLock}
        grant={editGrant}
        grants={presetEntry !== undefined ? grants : undefined}
        isOpen={grantOpen}
        lineNames={(data?.lines || []).map((l) => l.name)}
        mode={grantMode}
        presetEntry={presetEntry}
        quota={data?.quota}
        users={users}
        onClose={() => setGrantOpen(false)}
        onSaved={() => void load()}
      />
      <HostDrawer
        host={drawer}
        nightOnly={nightOnly}
        onClose={() => setDrawerHost(null)}
        onSetMain={grants.length > 0 ? openSetMain : undefined}
        onView={patchHost}
      />
      <LeiKwanSettingsModal
        accounts={accounts}
        data={data}
        open={settingsOpen}
        onAccountsChanged={() => void load()}
        onClose={() => setSettingsOpen(false)}
      />
      <LeiKwanSubDialog
        admin
        grants={grants}
        me={me}
        open={subOpen}
        onClose={() => setSubOpen(false)}
      />
    </>
  );

  if (!loaded)
    return (
      <div className="flex justify-center py-16">
        <Spinner />
      </div>
    );

  if (h5)
    return (
      <AdminPhone
        actions={actions}
        dialogs={dialogs}
        grants={grants}
        groups={groups}
        headerChips={headerChips}
        hostList={hostList}
        hosts={hosts}
        me={me}
        nightOnly={nightOnly}
        pipeCard={pipeCard}
        revoke={revoke}
        setTab={setTab}
        tab={tab}
        usage={usage}
        onEditGrant={openGrantDialog}
        onEditLine={openLineDialog}
        onEntryGrant={openEntryDialog}
        onHostView={patchHost}
        onNewGrant={() => openGrantDialog(null)}
        onRevoke={(g) => void doRevoke(g)}
        onSettings={() => setSettingsOpen(true)}
        onSub={() => setSubOpen(true)}
      />
    );

  return (
    <div className="space-y-5" data-leikwan-admin="">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 space-y-2.5">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h1 className="text-2xl font-bold">利群汇聚</h1>
            <span className="text-sm text-default-500">
              利群专线的转发、授权、入口机都在这一页，不进隧道页和转发页
            </span>
          </div>
          <div className="flex flex-wrap gap-1.5">{headerChips}</div>
        </div>
        <div className="flex gap-2">
          <Button
            color="primary"
            isDisabled={grants.length === 0}
            startContent={<Link2 size={15} />}
            title={
              grants.length === 0
                ? "还没有授权用户"
                : "一条订阅链接带上全部利群转发（可选用户）"
            }
            onPress={() => setSubOpen(true)}
          >
            生成订阅
          </Button>
          <Button
            color="primary"
            isLoading={loading}
            startContent={!loading && <RefreshCw size={15} />}
            variant="flat"
            onPress={() => void load()}
          >
            刷新
          </Button>
          <Button
            color="warning"
            startContent={<Settings size={15} />}
            variant="flat"
            onPress={() => setSettingsOpen(true)}
          >
            设置
          </Button>
        </div>
      </header>
      <div className="grid grid-cols-1 items-start gap-5 xl:grid-cols-[minmax(0,1fr)_288px]">
        <LkCard className="min-w-0 overflow-hidden" data-leikwan-tree="">
          <div className="border-b border-divider px-4 py-3.5">
            <LkCardTitle
              chip={
                <Pill tone="purple">
                  {lineCount} 条专线 · {ruleCount} 条转发
                </Pill>
              }
              right={
                <div className="flex flex-wrap gap-2">
                  <Button
                    color="success"
                    isDisabled={!data?.accountReady}
                    size="sm"
                    startContent={<Plus size={14} />}
                    variant="flat"
                    onPress={() => openLineDialog(null)}
                  >
                    新建专线
                  </Button>
                  <Button
                    color="success"
                    isDisabled={lineCount === 0}
                    size="sm"
                    startContent={<Plus size={14} />}
                    variant="flat"
                    onPress={() => actions.openCreate()}
                  >
                    添加转发
                  </Button>
                </div>
              }
              title="专线 · 转发"
            />
          </div>
          {groups.map((g) => (
            <LineGroupBlock
              key={g.id}
              actions={actions}
              group={g}
              noWarm={noWarm}
              open={currentOpen === g.id}
              onChanged={() => void load()}
              onEdit={openLineDialog}
              onToggle={() => toggleLine(g.id)}
            />
          ))}
          {groups.length === 0 && (
            <div className="px-4 py-8 text-center text-sm text-default-500">
              {data?.accountReady
                ? "还没有利群专线：点「新建专线」"
                : "先在「设置」里添加利群账号和 API Token"}
            </div>
          )}
        </LkCard>
        <div className="flex min-w-0 flex-col gap-4">
          <PackageCard data={usage} />
          <LkCard className="space-y-3 p-4" data-leikwan-grants="">
            <LkCardTitle
              chip={<Pill tone="purple">{grants.length} 人</Pill>}
              right={
                <Button
                  color="success"
                  isDisabled={!entries.length}
                  size="sm"
                  startContent={<Plus size={14} />}
                  variant="flat"
                  onPress={() => openGrantDialog(null)}
                >
                  授权用户
                </Button>
              }
              title="授权"
            />
            {grantRows}
            {grants.length === 0 && (
              <p className="rounded-xl border border-dashed border-divider px-3 py-4 text-center text-sm text-default-500">
                还没有授权
              </p>
            )}
            <p className="text-xs leading-relaxed text-default-500">
              转发数、流量、到期跟用户自己的走；授权只选主入口和备线开关。
            </p>
          </LkCard>
          <LkCard className="space-y-3 p-4" data-leikwan-hosts="">
            <LkCardTitle
              chip={
                <>
                  <Pill tone="teal">
                    分组「{hosts?.entryGroupName || "利群入口"}」
                  </Pill>
                  {entryLevel && <AttentionIcon level={entryLevel} />}
                </>
              }
              title="入口机"
            />
            {(hosts?.attention || 0) > 0 && (
              <div
                data-attention-notice
                className={`flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${
                  entryLevel === "error"
                    ? "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300"
                    : "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300"
                }`}
              >
                <AttentionIcon level={entryLevel || "warn"} />
                <span className="min-w-0 flex-1">
                  {hosts?.attention} 台需要处理：
                  {hostList
                    .filter((h) => hostAttentionLevel(h.manage))
                    .map((h) => h.node.name)
                    .join("、")}
                </span>
              </div>
            )}
            <div className="grid grid-cols-2 gap-2">
              {hostList.map((h) => (
                <HostTile
                  key={h.node.id}
                  host={h}
                  onOpen={() => setDrawerHost(h.node.id)}
                />
              ))}
            </div>
            {hostList.length === 0 && (
              <p className="text-sm text-default-500">
                分组里还没有节点：在节点页把节点拉进这个组
              </p>
            )}
            <p className="text-xs leading-relaxed text-default-500">
              点一台看主机快照：厂商版本、更新锁、日志快切、首包合并、手机备线端口，还有面板接管。
            </p>
          </LkCard>
          <DevicesCard data={usage} />
          {pipeCard}
        </div>
      </div>
      {dialogs}
    </div>
  );
}

/** H5 (screens 03–05): chips, tabs 规则 / 授权 / 入口机 + 设置. */
function AdminPhone({
  headerChips,
  tab,
  setTab,
  groups,
  actions,
  grants,
  hosts,
  hostList,
  nightOnly,
  pipeCard,
  me,
  revoke,
  dialogs,
  onEditLine,
  onEditGrant,
  onEntryGrant,
  onNewGrant,
  onRevoke,
  onHostView,
  onSettings,
  onSub,
  usage,
}: {
  headerChips: ReactNode;
  tab: "rules" | "grants" | "hosts";
  setTab: (t: "rules" | "grants" | "hosts") => void;
  groups: LineGroup[];
  actions: LeiKwanRuleActions;
  grants: LeiKwanGrant[];
  hosts: LeiKwanHostList | null;
  hostList: LeiKwanHostItem[];
  nightOnly: boolean;
  pipeCard: ReactNode;
  me: string | null;
  revoke: number | null;
  dialogs: ReactNode;
  onEditLine: (l: LeiKwanLine | null) => void;
  onEditGrant: (g: LeiKwanGrant) => void;
  /** Plan 295: 换主入口 */
  onEntryGrant: (g: LeiKwanGrant) => void;
  onNewGrant: () => void;
  onRevoke: (g: LeiKwanGrant) => void;
  onHostView: (nodeId: number, view: LeiKwanHostManage) => void;
  onSettings: () => void;
  /** Plan 272: 「生成订阅」 */
  onSub: () => void;
  /** Plan 273: 套餐流量 block + 利群在线设备 under the 入口机 tab */
  usage: LeiKwanUsageView | null;
}) {
  const [usageOpen, setUsageOpen] = useState(false);
  // Plan 286 r2: every 专线 starts collapsed (no first-line auto-open).
  const [openLine, setOpenLine] = useState<number | null>(null);
  const currentLine = openLine;
  const queueGroupScroll = useGroupScrollSettle();
  const ruleExpand = useExpandOne(
    groups.flatMap((g) => g.rules.map((r) => r.id)),
  );
  const grantExpand = useExpandOne(grants.map((g) => g.userId));
  const hostExpand = useExpandOne(hostList.map((h) => h.node.id));
  const ruleCount = groups.reduce((n, g) => n + g.rules.length, 0);
  const entryLevel = hostsAttentionLevel(hosts);

  return (
    <div className="space-y-3" data-leikwan-admin="h5">
      <div className="flex flex-wrap gap-1.5">{headerChips}</div>
      <PackagePhoneBlock
        data={usage}
        open={usageOpen}
        onToggle={() => setUsageOpen((o) => !o)}
      />
      <Button
        className="h-10 w-full"
        color="primary"
        isDisabled={grants.length === 0}
        startContent={<Link2 size={16} />}
        onPress={onSub}
      >
        生成订阅
      </Button>
      <div className="flex items-center gap-2">
        <div className="flex min-w-0 flex-1 gap-2 overflow-x-auto">
          <LkTab active={tab === "rules"} onPress={() => setTab("rules")}>
            转发 {ruleCount}
          </LkTab>
          <LkTab active={tab === "grants"} onPress={() => setTab("grants")}>
            授权 {grants.length}
          </LkTab>
          <LkTab active={tab === "hosts"} onPress={() => setTab("hosts")}>
            <span className="inline-flex items-center gap-1">
              入口机 {hostList.length}
              {entryLevel && <AttentionIcon level={entryLevel} />}
            </span>
          </LkTab>
        </div>
        <button
          aria-label="利群设置"
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-amber-500/20 text-amber-700 dark:bg-amber-400/20 dark:text-amber-300"
          type="button"
          onClick={onSettings}
        >
          <Settings size={18} />
        </button>
      </div>

      {tab === "rules" && (
        <div className="space-y-3">
          <div className="flex gap-2">
            <Button
              className="h-11 flex-1"
              color="success"
              startContent={<Plus size={16} />}
              variant="flat"
              onPress={() => actions.openCreate()}
            >
              添加转发
            </Button>
            <Button
              className="h-11"
              color="success"
              startContent={<Plus size={16} />}
              variant="flat"
              onPress={() => onEditLine(null)}
            >
              专线
            </Button>
          </div>
          {groups.map((g) => {
            const open = currentLine === g.id;

            return (
              <div
                key={g.id}
                className="flvx-group-anchor rounded-2xl border border-divider bg-content1"
                data-group-key={lineGroupKey(g.id)}
                data-leikwan-line={g.name}
              >
                {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions */}
                <div
                  className="flex min-h-[52px] cursor-pointer items-center gap-2 px-3 py-2"
                  data-group-header=""
                  onClick={() => {
                    queueGroupScroll(lineGroupKey(g.id), !open);
                    setOpenLine(open ? null : g.id);
                  }}
                >
                  <LkChevron open={open} />
                  <span className="min-w-0 break-words text-[15px] font-semibold">
                    {g.name}
                  </span>
                  <Pill title={g.route} tone="teal">
                    {routeShort(g.route)}
                  </Pill>
                  {g.rules.some((r) => r.backupError) && (
                    <AlertTriangle
                      className="shrink-0 text-red-700"
                      size={14}
                    />
                  )}
                  <span className="flex-1" />
                  <span className="flex h-7 min-w-7 shrink-0 items-center justify-center rounded-full bg-purple-500/15 px-1.5 text-xs font-semibold text-purple-700 dark:bg-purple-400/20 dark:text-purple-300">
                    {g.rules.length}
                  </span>
                </div>
                {open && (
                  <div className="space-y-2 px-2.5 pb-2.5">
                    {g.line && (
                      <div className="flex flex-wrap items-center gap-1 px-0.5">
                        <LineChips compact line={g.line} />
                        <button
                          className="ml-auto text-sm font-medium text-primary"
                          type="button"
                          onClick={() => onEditLine(g.line!)}
                        >
                          编辑专线
                        </button>
                      </div>
                    )}
                    {g.rules.map((r) => (
                      <LkExpandCard
                        key={r.id}
                        chip={<Pill tone="purple">#{r.forwardId}</Pill>}
                        data-leikwan-rule={r.name}
                        dot={<RuleDot rule={r} />}
                        name={
                          <>
                            {r.backupError && (
                              <AlertTriangle
                                className="mr-1 inline text-red-700"
                                size={13}
                              />
                            )}
                            {r.name}
                          </>
                        }
                        open={ruleExpand.open === r.id}
                        onToggle={() => ruleExpand.toggle(r.id)}
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
                              label={`主入口 ${r.entry.nodeName} 离线`}
                              since={r.entry.offlineSince}
                            />
                          )}
                        </LkField>
                        <LkField label="落地">
                          <span
                            className={
                              r.landing.kind === "manual"
                                ? "font-mono text-xs"
                                : ""
                            }
                          >
                            {r.landing.kind === "manual"
                              ? `${r.landing.host}:${r.landing.port}`
                              : r.landing.kind === "external"
                                ? r.landing.name ||
                                  `${r.landing.host}:${r.landing.port}`
                                : `${r.landing.serverName || "落地"} · ${r.landing.name}`}
                          </span>
                          {r.landing.kind === "external" && (
                            <span className="text-xs text-default-500">
                              {externalNote(r)}
                            </span>
                          )}
                        </LkField>
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
                        {r.blockReason && (
                          <LkField label="状态">
                            <span className="text-red-700 dark:text-red-300">
                              {blockedNote(r)}
                            </span>
                          </LkField>
                        )}
                        {(r.entryLimits || []).length > 0 && (
                          <LkField label="说明">
                            <span className="text-xs text-default-500">
                              {limitsNote(r)}
                            </span>
                          </LkField>
                        )}
                        <LkField label="标签">
                          <AdminBackupChip rule={r} />
                          <WarmChip rule={r} />
                          <EntryLimitPills limits={r.entryLimits} />
                          <Pill
                            tone={
                              r.landing.kind === "manual" ? "slate" : "pink"
                            }
                          >
                            {r.landing.kind === "manual"
                              ? "手动落地"
                              : r.landing.kind === "external"
                                ? externalProtocol(r.landing.protocol) ||
                                  "外部节点"
                                : protocolLabel(r.landing.protocol)}
                          </Pill>
                          {r.userName && <Pill tone="slate">{r.userName}</Pill>}
                          {testNote(r) && <Pill tone="amber">测试</Pill>}
                          {r.userExpired && <Pill tone="red">已到期</Pill>}
                          <FlowPill rule={r} />
                          <BackupFlowPill rule={r} />
                          <VendorFlowPill rule={r} />
                          <AllEntriesPill actions={actions} rule={r} />
                        </LkField>
                        <div className="flex flex-wrap gap-2 pt-1">
                          <RuleButtons admin fill actions={actions} rule={r} />
                        </div>
                      </LkExpandCard>
                    ))}
                    {g.rules.length === 0 && (
                      <div className="flex items-center justify-between gap-2 rounded-xl border border-dashed border-divider px-3 py-2.5 text-sm text-default-500">
                        还没有转发
                        <Button
                          color="success"
                          size="sm"
                          variant="flat"
                          onPress={() => actions.openCreate(g.id)}
                        >
                          ＋ 添加转发
                        </Button>
                      </div>
                    )}
                  </div>
                )}
              </div>
            );
          })}
          {pipeCard}
        </div>
      )}

      {tab === "grants" && (
        <div className="space-y-3">
          <Button
            className="h-11 w-full"
            color="success"
            startContent={<Plus size={16} />}
            variant="flat"
            onPress={onNewGrant}
          >
            授权用户
          </Button>
          {grants.map((g) => (
            <LkExpandCard
              key={g.id}
              chip={<EntryChip g={g} prefix="" />}
              data-leikwan-grant-row={g.userName}
              leading={
                <span className="flex h-[30px] w-[30px] shrink-0 items-center justify-center rounded-full bg-purple-500/15 text-[13px] font-semibold text-purple-700 dark:bg-purple-400/20 dark:text-purple-300">
                  {initial(g.userName)}
                </span>
              }
              name={g.userName}
              open={grantExpand.open === g.userId}
              onToggle={() => grantExpand.toggle(g.userId)}
            >
              <LkField label="主入口">
                <EntryChip g={g} prefix="" onEntry={onEntryGrant} />
                <span className="font-mono text-xs">{g.mainEntryHost}</span>
              </LkField>
              <LkField label="备线">
                <Pill tone={g.backupLine ? "green" : "slate"}>
                  {g.backupLine ? "开 · 每条转发自动带备线" : "关"}
                </Pill>
              </LkField>
              <LkField label="转发">
                <Pill tone="purple">{g.ruleCount} 条</Pill>
                <Pill tone="slate">数量 / 到期跟账号</Pill>
                {g.userName === me && <Pill tone="purple">管理员</Pill>}
                {g.status !== 1 && <Pill tone="slate">已停用</Pill>}
              </LkField>
              {g.grace && (
                <LkField label="旧入口">
                  <GracePill g={g} />
                </LkField>
              )}
              {(g.backupFlow || 0) > 0 && (
                <LkField label="备线流量">
                  <Pill
                    title="这个用户的利群转发走备线用掉的流量，从上次重置算起（共用的备线只算一次）"
                    tone="green"
                  >
                    {fmtBytes(g.backupFlow)}
                  </Pill>
                </LkField>
              )}
              <div className="flex gap-2 pt-1">
                <Button
                  className="min-w-0 flex-1"
                  color="primary"
                  size="sm"
                  variant="flat"
                  onPress={() => onEditGrant(g)}
                >
                  修改
                </Button>
                <Button
                  className="min-w-0 flex-1"
                  color="secondary"
                  data-leikwan-switch-entry={g.userName}
                  size="sm"
                  variant="flat"
                  onPress={() => onEntryGrant(g)}
                >
                  换主入口
                </Button>
                <Button
                  className="min-w-0 flex-1"
                  color="danger"
                  isDisabled={g.ruleCount > 0}
                  size="sm"
                  variant="flat"
                  onPress={() => onRevoke(g)}
                >
                  {revoke === g.userId ? "确认取消" : "取消授权"}
                </Button>
              </div>
              {revoke === g.userId && (
                <p className="text-xs text-amber-700 dark:text-amber-300">
                  取消后利群汇聚订阅里的利群节点会消失
                </p>
              )}
            </LkExpandCard>
          ))}
          {grants.length === 0 && (
            <p className="rounded-xl border border-dashed border-divider px-3 py-4 text-center text-sm text-default-500">
              还没有授权
            </p>
          )}
          <p className="px-1 text-xs text-default-500">
            授权后，用户侧边栏才出现「利群汇聚」。
          </p>
        </div>
      )}

      {tab === "hosts" && (
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-2 px-0.5">
            <Pill tone="teal">
              分组「{hosts?.entryGroupName || "利群入口"}」
            </Pill>
            <span className="text-xs text-default-500">
              加入口机 = 在节点页把节点拉进这个组
            </span>
          </div>
          {hostList.map((h) => (
            <LkExpandCard
              key={h.node.id}
              chip={
                <span className="inline-flex shrink-0 items-center gap-1">
                  {!hostOnline(h) && (
                    <OfflinePill since={h.node.offlineSince} />
                  )}
                  <Pill tone={h.mainEntryUsers > 0 ? "teal" : "slate"}>
                    主入口 {h.mainEntryUsers}
                  </Pill>
                </span>
              }
              data-leikwan-host-row={h.node.id}
              dot={
                <LkDot
                  off={!hostOnline(h)}
                  ok={hostRunning(h)}
                  title={hostStatusText(h)}
                />
              }
              name={
                <span className="inline-flex items-center gap-1">
                  {h.node.name}
                  <HostNameMarker manage={h.manage} />
                </span>
              }
              open={hostExpand.open === h.node.id}
              onToggle={() => hostExpand.toggle(h.node.id)}
            >
              <HostDetails
                compact
                host={h}
                nightOnly={nightOnly}
                onView={onHostView}
              />
            </LkExpandCard>
          ))}
          <DevicesPhoneList data={usage} />
        </div>
      )}
      {dialogs}
    </div>
  );
}
