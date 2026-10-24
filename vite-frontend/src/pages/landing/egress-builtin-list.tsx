import { useEffect, useState } from "react";
import toast from "react-hot-toast";
import {
  ChevronDown,
  ChevronUp,
  CircleCheck,
  CircleX,
  Lock,
  ShieldCheck,
} from "lucide-react";

import {
  type AbuseKey,
  type AbuseOffReason,
  type AbuseValues,
  saveEgressWhitelist,
  splitList,
} from "@/api/landing-egress";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Textarea } from "@/shadcn-bridge/heroui/input";
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Plan 140: the 内置 layer as a list of its real rules, in the rule-list
// style: the locked safety rule (with its CIDRs), the default anti-abuse
// rules (admin: global switches) and the source whitelist.
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
    hint: "按流量特征识别 BT 下载并封锁：能识别 TCP 明文 BT 和 UDP 上的 uTP（需新版落地 Core），识别不了加密 BT 和 DHT",
  },
];

const offReasonText: Record<AbuseOffReason, string> = {
  global: "未生效：全局已关闭",
  profile: "未生效：所用路由方案已关闭默认防滥用",
  pending: "未生效：落地服务器还不能应用规则",
};

function BlockChip() {
  return (
    <Chip color="danger" size="sm" variant="flat">
      → 封锁
    </Chip>
  );
}

function InfoChip({ text }: { text: string }) {
  return (
    <Chip className="text-default-600" size="sm" variant="bordered">
      {text}
    </Chip>
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
  exemptNodeCount,
  whitelist,
  onWhitelistSaved,
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
  // Admin: FLVX node addresses + the editable admin whitelist.
  exemptNodeCount?: number;
  whitelist?: string[];
  onWhitelistSaved?: (list: string[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState((whitelist || []).join("\n"));
  const [saving, setSaving] = useState(false);
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

  useEffect(() => {
    if (!editing) setDraft((whitelist || []).join("\n"));
  }, [whitelist, editing]);

  const saveWhitelist = async () => {
    setSaving(true);
    const res = await saveEgressWhitelist(splitList(draft));

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success("来源白名单已保存，正在同步");
    setEditing(false);
    onWhitelistSaved?.(res.data.whitelist);
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
                <Chip
                  key={cidr}
                  className="font-mono text-default-600"
                  size="sm"
                  variant="bordered"
                >
                  {cidr}
                </Chip>
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
      <div className="flex items-start gap-3 p-3">
        <Lock className="mt-1 shrink-0 text-default-400" size={14} />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">来源白名单</span>
            <Chip color="success" size="sm" variant="flat">
              → 不受来源规则影响
            </Chip>
          </div>
          {admin && whitelist ? (
            <>
              <div className="flex flex-wrap gap-1">
                <InfoChip
                  text={`FLVX 节点地址 ${exemptNodeCount ?? 0} 个（自动）`}
                />
                <InfoChip
                  text={`管理员白名单 ${(whitelist || []).length} 条`}
                />
              </div>
              <p className="text-xs text-default-500">
                来自这些地址的连接不受「来源 IP /
                来源端口」规则影响（例如禁止大陆直连），经 FLVX
                节点转发进来的流量自动包含。
              </p>
              {editing ? (
                <div className="space-y-2">
                  <Textarea
                    description="每行一个 IP 或 CIDR；保存后自动同步"
                    label="管理员白名单"
                    minRows={3}
                    placeholder={"203.0.113.10\n198.51.100.0/24"}
                    value={draft}
                    variant="bordered"
                    onChange={(event) => setDraft(event.target.value)}
                  />
                  <div className="flex justify-end gap-2">
                    <Button
                      color="danger"
                      size="sm"
                      variant="flat"
                      onPress={() => {
                        setEditing(false);
                        setDraft((whitelist || []).join("\n"));
                      }}
                    >
                      取消
                    </Button>
                    <Button
                      color="success"
                      isLoading={saving}
                      size="sm"
                      onPress={() => void saveWhitelist()}
                    >
                      保存白名单
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="flex flex-wrap items-center gap-1">
                  {(whitelist || []).slice(0, 6).map((cidr) => (
                    <Chip
                      key={cidr}
                      className="font-mono text-default-600"
                      size="sm"
                      variant="bordered"
                    >
                      {cidr}
                    </Chip>
                  ))}
                  {(whitelist || []).length > 6 && (
                    <InfoChip text={`等 ${(whitelist || []).length} 条`} />
                  )}
                  <Button
                    className="h-6 min-w-0 px-2 text-xs"
                    color="primary"
                    size="sm"
                    variant="flat"
                    onPress={() => setEditing(true)}
                  >
                    编辑白名单
                  </Button>
                </div>
              )}
            </>
          ) : (
            <p className="text-xs text-default-500">
              经 FLVX
              节点转发进来的流量和管理员白名单里的地址，不受来源规则影响。
            </p>
          )}
        </div>
      </div>
    </div>
  );
}
