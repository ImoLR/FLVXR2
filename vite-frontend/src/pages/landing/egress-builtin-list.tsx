import { useState } from "react";
import {
  ChevronDown,
  ChevronUp,
  CircleCheck,
  CircleX,
  Lock,
  ShieldCheck,
} from "lucide-react";

import { Pill } from "./pill";
import { ScanGuardCompactRow, ScanGuardSection } from "./scan-guard";

import {
  type AbuseKey,
  type AbuseOffReason,
  type AbuseValues,
  type EgressBuiltin,
  type ScanGuardAdmin,
} from "@/api/landing-egress";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Plan 140: the 内置 layer as a list of its real rules, in the rule-list
// style: the locked safety rule (with its CIDRs), the default anti-abuse
// rules (admin: global switches) and 禁止端口扫描. Plan 219: the source
// whitelist row is gone (FLVX nodes are not exempt from source rules).
// Plan 143: the four 防滥用 rows form one group (SMTP 25, 发信端口,
// Telnet/SMB, BT); whether a row is in force is a green ✓ / red ✗ at the
// far right of the row (with the reason as tooltip / small text).

export const builtinScopeText =
  "作用于所有落地节点：自有、妙妙屋共享和线路机节点，始终最先匹配";

type AbuseRow = {
  key: AbuseKey;
  name: string;
  chips: string[];
  hint: string;
};

const abuseRows: AbuseRow[] = [
  {
    key: "smtp",
    name: "屏蔽 SMTP 25（TCP）",
    chips: ["目标端口 25", "TCP"],
    hint: "防止落地被用来群发垃圾邮件；只影响经代理连 25 端口，不影响落地本机",
  },
  {
    key: "mail",
    name: "屏蔽发信端口 465/587/2525（TCP）",
    chips: ["目标端口 465,587,2525", "TCP"],
    hint: "只影响用邮件客户端通过代理发信；收信（IMAP 993/143、POP3 995/110）和网页邮箱不受影响",
  },
  {
    key: "telnet",
    name: "屏蔽 Telnet/SMB 23、135-139、445（TCP+UDP）",
    chips: ["目标端口 23,135-139,445", "TCP+UDP"],
    hint: "Telnet 和 Windows 文件共享常被用来扫描、传播蠕虫；不封 22（SSH）和 3389（远程桌面）",
  },
  {
    key: "bt",
    name: "屏蔽 BT",
    chips: ["协议 bittorrent"],
    hint: "按流量特征识别 BT 下载并封锁：能识别 TCP 明文 BT，以及 UDP 上的 uTP、DHT 和 UDP tracker（需新版落地 Core），识别不了加密 BT",
  },
];

const offReasonText: Record<AbuseOffReason, string> = {
  global: "未生效：全局已关闭",
  profile: "未生效：所用路由方案已关闭默认防滥用",
  pending: "未生效：落地服务器还不能应用规则",
};

function BlockChip() {
  return <Pill tone="red">→ 封锁</Pill>;
}

function InfoChip({ text }: { text: string }) {
  return <Pill tone="slate">{text}</Pill>;
}

/** Plan 149: short names of the 内置 rows for the collapsed H5 layer. */
const abuseShort: Record<AbuseKey, string> = {
  smtp: "屏蔽 SMTP 25",
  mail: "屏蔽发信端口 465/587/2525",
  telnet: "屏蔽 Telnet/SMB",
  bt: "屏蔽 BT",
};

/**
 * Plan 149: the 内置 layer collapsed to one line per rule (name + action +
 * ✓ / ✗), for the H5 路由规则 dialog; the full list opens on demand.
 */
export function BuiltinCompactRows({
  values,
  offReasons = {},
  scanGuard,
}: {
  values: AbuseValues;
  offReasons?: Partial<Record<AbuseKey, AbuseOffReason>>;
  // Plan 202: the 禁止端口扫描 line (node view).
  scanGuard?: EgressBuiltin["scanGuard"];
}) {
  const rows: { key: string; name: string; on: boolean; label: string }[] = [
    {
      key: "safety",
      name: "禁止访问内网 / 云元数据",
      on: true,
      label: "生效中（锁定，始终生效）",
    },
    ...abuseRows.map((row) => ({
      key: row.key,
      name: abuseShort[row.key],
      on: values[row.key],
      label: values[row.key]
        ? "生效中"
        : offReasonText[offReasons[row.key] || "global"] || "未生效",
    })),
  ];

  return (
    <div className="divide-y divide-default-200 overflow-hidden rounded-lg bg-default-100/60 dark:divide-default-100 dark:bg-default-50/60">
      {rows.map((row) => (
        <div
          key={row.key}
          className={`grid grid-cols-[14px_minmax(0,1fr)_auto_auto] items-center gap-2 px-3 py-2 ${row.on ? "" : "opacity-70"}`}
          data-builtin-compact={row.key}
        >
          <Lock aria-label="锁定" className="text-default-500" size={14} />
          <span className="truncate text-sm font-medium" title={row.name}>
            {row.name}
          </span>
          <BlockChip />
          <StateIcon label={row.label} on={row.on} />
        </div>
      ))}
      {scanGuard && <ScanGuardCompactRow guard={scanGuard} />}
    </div>
  );
}

// In force = green ✓, not in force = red ✗; the label carries the reason.
export function StateIcon({ on, label }: { on: boolean; label: string }) {
  const Icon = on ? CircleCheck : CircleX;

  return (
    <span
      aria-label={label}
      className={`inline-flex shrink-0 ${on ? "text-success" : "text-danger"}`}
      role="img"
      title={label}
    >
      <Icon aria-hidden size={16} strokeWidth={2.25} />
    </span>
  );
}

export function BuiltinRuleList({
  safety,
  values,
  admin = false,
  profilesOff = [],
  profileOff,
  offReasons = {},
  onSwitch,
  switching = false,
  scanGuard,
  onScanSaved,
}: {
  safety: string[];
  // admin: the global switches; read-only: whether each applies here.
  values: AbuseValues;
  admin?: boolean;
  // Admin global view: profiles that switched the defaults off.
  profilesOff?: { id: number; name: string }[];
  // Profile editor: this profile's own toggle is off.
  profileOff?: boolean;
  // Read-only node view: why a row is not in force.
  offReasons?: Partial<Record<AbuseKey, AbuseOffReason>>;
  onSwitch?: (next: AbuseValues) => void;
  switching?: boolean;
  // Plan 202: 禁止端口扫描 (admin global view: editable via onScanSaved;
  // node view: read-only with the node's server state).
  scanGuard?: EgressBuiltin["scanGuard"];
  onScanSaved?: (g: ScanGuardAdmin) => void;
}) {
  const [open, setOpen] = useState(false);
  const anyOn = Object.values(values).some(Boolean);
  // State of one row: in force + the reason shown when it is not.
  const rowState = (key: AbuseKey): { on: boolean; label: string } => {
    if (!values[key]) {
      if (admin) return { on: false, label: "未生效：全局已关闭" };

      return {
        on: false,
        label: offReasonText[offReasons[key] || "global"] || "未生效",
      };
    }
    if (profileOff) return { on: false, label: "未生效：本方案已关闭" };
    if (admin && profilesOff.length > 0)
      return {
        on: true,
        label: `生效中（${profilesOff.length} 个路由方案已关闭）`,
      };

    return { on: true, label: "生效中" };
  };

  return (
    <div className="divide-y divide-divider rounded-lg border border-divider">
      <div className="flex items-start gap-3 bg-default-50 p-3">
        <Lock
          aria-label="锁定"
          className="mt-1 shrink-0 text-default-500"
          size={14}
        />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">
              安全：禁止访问内网 / 回环 / 链路本地 / 云元数据 / 组播
            </span>
            <BlockChip />
          </div>
          <p className="text-xs text-default-500">
            保护落地服务器自己的内网和云厂商元数据接口（169.254.169.254
            等）；节点程序本身也强制执行，任何规则都不能放行。
          </p>
          <div className="flex flex-wrap items-center gap-1">
            <InfoChip text={`目标 IP ${safety.length} 条`} />
            <Button
              className="h-6 min-w-0 px-2 text-xs"
              color="default"
              size="sm"
              startContent={
                open ? <ChevronUp size={14} /> : <ChevronDown size={14} />
              }
              variant="flat"
              onPress={() => setOpen((v) => !v)}
            >
              {open ? "收起" : `展开 ${safety.length} 条`}
            </Button>
          </div>
          {open && (
            <div className="flex flex-wrap gap-1 pt-1">
              {safety.map((cidr) => (
                <Pill key={cidr} className="font-mono" tone="slate">
                  {cidr}
                </Pill>
              ))}
            </div>
          )}
        </div>
        <div className="flex shrink-0 items-center self-center">
          <StateIcon on label="生效中（锁定，始终生效）" />
        </div>
      </div>
      <div className="flex items-start gap-2 bg-default-100/70 px-3 py-1.5">
        <ShieldCheck className="mt-0.5 shrink-0 text-default-500" size={14} />
        <span className="shrink-0 whitespace-nowrap text-xs font-medium text-default-700">
          防滥用
        </span>
        <span className="min-w-0 text-xs text-default-500">
          {admin
            ? "全局开关；路由方案里的「默认防滥用规则」也要开着才生效"
            : "4 条，节点规则不能放行"}
        </span>
      </div>
      {abuseRows.map((row) => {
        const state = rowState(row.key);

        return (
          <div
            key={row.key}
            className={`flex items-start gap-3 p-3 ${state.on ? "bg-default-50" : "bg-default-50/40"}`}
          >
            <span className="mt-1 w-[14px] shrink-0" />
            <div
              className={`min-w-0 flex-1 space-y-1 ${state.on ? "" : "opacity-70"}`}
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{row.name}</span>
                <BlockChip />
              </div>
              <div className="flex flex-wrap gap-1">
                {row.chips.map((chip) => (
                  <InfoChip key={chip} text={chip} />
                ))}
              </div>
              <p className="text-xs text-default-500">{row.hint}</p>
              {!state.on && !(admin && onSwitch) && (
                <p className="text-xs text-danger">{state.label}</p>
              )}
            </div>
            <div className="flex shrink-0 items-center gap-2 self-center">
              {admin && onSwitch && (
                <Switch
                  aria-label={`${row.name}全局开关`}
                  isDisabled={switching}
                  isSelected={values[row.key]}
                  onValueChange={(v) => onSwitch({ ...values, [row.key]: v })}
                />
              )}
              <StateIcon label={state.label} on={state.on} />
            </div>
          </div>
        );
      })}
      {admin && anyOn && profilesOff.length > 0 && (
        <p className="bg-warning-50 px-3 py-2 text-xs text-warning dark:bg-warning-500/10">
          {profilesOff.length} 个路由方案关闭了默认防滥用（
          {profilesOff.map((p) => p.name).join("、")}
          ），使用这些方案的节点不受上面的防滥用规则限制
        </p>
      )}
      {scanGuard && (
        <ScanGuardSection
          admin={admin && Boolean(onScanSaved)}
          guard={scanGuard}
          onSaved={onScanSaved}
        />
      )}
    </div>
  );
}
