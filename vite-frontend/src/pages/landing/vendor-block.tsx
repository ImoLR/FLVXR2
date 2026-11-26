import { useEffect, useState } from "react";
import toast from "react-hot-toast";
import {
  Building2,
  CircleCheck,
  CircleX,
  Lock,
  Plus,
  Trash2,
} from "lucide-react";

import { Pill } from "./pill";
import { AttentionIcon } from "./admin-attention";

import {
  type EgressBuiltin,
  type EgressGlobal,
  type LandingVendor,
  type VendorOffReason,
  assignLandingVendor,
  saveLandingVendors,
} from "@/api/landing-egress";
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
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Plan 316 「屏蔽本机厂商官网」: every landing blocks the websites of the VPS
// vendor that hosts it (website, client area, billing, tickets, panels), so a
// rider cannot attack the vendor from the vendor's own IP (DMIT closed a
// whole account for that, 11-26). Per server, own vendor only. The 内置 row
// (admin: switch + 管理厂商; node: read-only ✓/✗), its collapsed H5 line and
// the vendor manager dialog.

type Vendor = NonNullable<EgressBuiltin["vendor"]>;

export const vendorRuleName = "屏蔽本机厂商官网";

const offText: Record<VendorOffReason, string> = {
  global: "未生效：全局已关闭",
  unset: "未生效：这台落地服务器还没设置厂商",
  pending: "未生效：落地服务器还不能应用规则",
};

function vendorState(
  v: Vendor,
  admin: boolean,
): { on: boolean; label: string } {
  if (admin)
    return v.on
      ? {
          on: true,
          label: v.unset
            ? `生效中（${v.unset} 台落地服务器未设置厂商）`
            : "生效中",
        }
      : { on: false, label: "未生效：全局已关闭" };
  if (v.on) return { on: true, label: "生效中" };

  return { on: false, label: offText[v.offReason || "global"] || "未生效" };
}

function StateIcon({ on, label }: { on: boolean; label: string }) {
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

/** The 内置 group + row (inside BuiltinRuleList). */
export function VendorBlockSection({
  vendor,
  admin,
  onSaved,
}: {
  vendor: Vendor;
  // admin = the global 内置 card (switch, servers, 管理厂商).
  admin?: boolean;
  onSaved?: (g: EgressGlobal) => void;
}) {
  const editable = Boolean(admin && onSaved);
  const [saving, setSaving] = useState(false);
  const [managerOpen, setManagerOpen] = useState(false);
  const state = vendorState(vendor, Boolean(admin));
  const servers = vendor.servers || [];
  const unset = servers.filter((s) => !s.vendor);
  const toggle = async (on: boolean) => {
    setSaving(true);
    const res = await saveLandingVendors({ on });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success("屏蔽本机厂商官网已保存，正在同步到落地服务器");
    onSaved?.(res.data);
  };
  const hint = admin
    ? "每台落地只屏蔽托管它的那家 VPS 厂商自己的官网、客户中心、账单、工单和面板（含子域名），防止有人借这台机器的 IP 攻击厂商、导致整个账号被封。在每台落地服务器的「设置」里选厂商；不影响其它网站，也不影响落地本机。"
    : "这台落地不能访问它所在 VPS 厂商自己的官网、客户中心和面板（含子域名），其它网站不受影响。";

  return (
    <>
      <div
        data-vendor-header
        className="flex items-start gap-2 bg-default-100/70 px-3 py-1.5"
      >
        <Building2 className="mt-0.5 shrink-0 text-default-500" size={14} />
        <span className="shrink-0 whitespace-nowrap text-xs font-medium text-default-700">
          本机厂商
        </span>
        <span className="min-w-0 text-xs text-default-500">
          {admin
            ? "每台落地按自己的厂商生效，排在所有规则最前，用户规则不能放行"
            : "1 条，节点规则不能放行"}
        </span>
      </div>
      <div
        data-vendor-row
        className={`flex items-start gap-3 p-3 ${state.on ? "bg-default-50" : "bg-default-50/40"}`}
      >
        {admin ? (
          <span className="mt-1 w-[14px] shrink-0" />
        ) : (
          <Lock
            aria-label="锁定"
            className="mt-1 shrink-0 text-default-500"
            size={14}
          />
        )}
        <div
          className={`min-w-0 flex-1 space-y-1 ${state.on ? "" : "opacity-80"}`}
        >
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{vendorRuleName}</span>
            <Pill tone="red">→ 封锁</Pill>
          </div>
          {admin ? (
            <div className="flex flex-wrap gap-1">
              <Pill tone="slate">按目标域名 + TLS SNI / HTTP Host</Pill>
              <Pill tone="slate">厂商 {(vendor.vendors || []).length} 家</Pill>
            </div>
          ) : (
            vendor.name && (
              <div className="flex flex-wrap gap-1">
                <Pill tone="slate">厂商 {vendor.name}</Pill>
                {(vendor.domains || []).map((d) => (
                  <Pill key={d} className="font-mono" tone="slate">
                    {d}
                  </Pill>
                ))}
              </div>
            )
          )}
          <p className="text-xs text-default-500">{hint}</p>
          {!admin && vendor.on && !vendor.sni && (
            <p className="text-xs text-default-400">
              当前按访问的域名匹配；节点程序升级后也按 TLS SNI / HTTP Host
              匹配。
            </p>
          )}
          {!state.on && !editable && (
            <p className="text-xs text-danger">{state.label}</p>
          )}
          {admin && (
            <div className="space-y-1 pt-1">
              <div className="flex flex-wrap items-center gap-1">
                {servers.map((s) => (
                  <Pill
                    key={s.id}
                    title={
                      s.vendor
                        ? s.sni
                          ? "按域名 + SNI / Host 匹配"
                          : s.source === "shared"
                            ? "妙妙屋共享：按访问的域名匹配"
                            : "按访问的域名匹配（节点程序升级后也按 SNI / Host）"
                        : s.suggest
                          ? `未设置（建议：${nameOf(vendor, s.suggest)}）`
                          : "未设置厂商"
                    }
                    tone={s.vendor ? "teal" : "amber"}
                  >
                    {!s.vendor && (
                      <AttentionIcon
                        className="mr-0.5 inline-block h-3 w-3 align-[-1px]"
                        level="warn"
                      />
                    )}
                    {s.name}：{s.vendor ? s.vendorName : "未设置"}
                  </Pill>
                ))}
              </div>
              <div className="flex flex-wrap items-center gap-2 text-xs text-default-500">
                <span>
                  {unset.length > 0
                    ? `${unset.length} 台未设置厂商（不屏蔽，卡片上有 ⚠）`
                    : "所有落地服务器都已设置厂商"}
                </span>
                {editable && (
                  <Button
                    className="h-6 min-w-0 px-2 text-xs"
                    color="primary"
                    size="sm"
                    variant="flat"
                    onPress={() => setManagerOpen(true)}
                  >
                    管理厂商
                  </Button>
                )}
              </div>
            </div>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2 self-center">
          {editable && (
            <Switch
              aria-label="屏蔽本机厂商官网全局开关"
              isDisabled={saving}
              isSelected={vendor.on}
              onValueChange={(v) => void toggle(v)}
            />
          )}
          <StateIcon label={state.label} on={state.on} />
        </div>
      </div>
      {editable && managerOpen && (
        <VendorManager
          vendor={vendor}
          onClose={() => setManagerOpen(false)}
          onSaved={(g) => onSaved?.(g)}
        />
      )}
    </>
  );
}

const nameOf = (v: Vendor, key: string) =>
  (v.vendors || []).find((x) => x.key === key)?.name || key;

/** Plan 316: the collapsed H5 line of the 内置 layer. */
export function VendorCompactRow({ vendor }: { vendor: Vendor }) {
  const state = vendorState(vendor, false);
  const name = vendor.name
    ? `${vendorRuleName}（${vendor.name}）`
    : vendorRuleName;

  return (
    <div
      className={`grid grid-cols-[14px_minmax(0,1fr)_auto_auto] items-center gap-2 px-3 py-2 ${state.on ? "" : "opacity-70"}`}
      data-builtin-compact="vendor"
    >
      <Lock aria-label="锁定" className="text-default-500" size={14} />
      <span className="truncate text-sm font-medium" title={name}>
        {name}
      </span>
      <Pill tone="red">→ 封锁</Pill>
      <StateIcon label={state.label} on={state.on} />
    </div>
  );
}

type Draft = {
  key: string;
  name: string;
  domains: string;
  remark: string;
  aliases?: string[];
};

const toDraft = (v: LandingVendor): Draft => ({
  key: v.key,
  name: v.name,
  domains: v.domains.join("\n"),
  remark: v.remark || "",
  aliases: v.aliases,
});

/** 管理厂商: the vendor list (name + domains) and each server's vendor. */
function VendorManager({
  vendor,
  onClose,
  onSaved,
}: {
  vendor: Vendor;
  onClose: () => void;
  onSaved: (g: EgressGlobal) => void;
}) {
  const [rows, setRows] = useState<Draft[]>(() =>
    (vendor.vendors || []).map(toDraft),
  );
  const [saving, setSaving] = useState(false);
  const [assigning, setAssigning] = useState(0);
  const [servers, setServers] = useState(vendor.servers || []);

  useEffect(() => setServers(vendor.servers || []), [vendor.servers]);
  const used = new Set(servers.map((s) => s.vendor).filter(Boolean));
  const setRow = (i: number, patch: Partial<Draft>) =>
    setRows((cur) => cur.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const saveList = async () => {
    setSaving(true);
    const res = await saveLandingVendors({
      vendors: rows.map((r) => ({
        key: r.key,
        name: r.name,
        domains: r.domains
          .split(/[\s,，]+/)
          .map((d) => d.trim())
          .filter(Boolean),
        remark: r.remark,
        aliases: r.aliases,
      })),
    });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success("厂商列表已保存，正在同步到落地服务器");
    onSaved(res.data);
    const next = res.data.builtin?.vendor;

    if (next?.vendors) setRows(next.vendors.map(toDraft));
  };
  const assign = async (serverId: number, key: string) => {
    setAssigning(serverId);
    const res = await assignLandingVendor(serverId, key);

    setAssigning(0);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success("已保存，正在同步这台落地服务器");
    onSaved(res.data);
    if (res.data.builtin?.vendor?.servers)
      setServers(res.data.builtin.vendor.servers);
  };
  const saved = vendor.vendors || [];

  return (
    <Modal isOpen scrollBehavior="inside" size="2xl" onClose={onClose}>
      <ModalContent className="flvx-landing">
        <ModalHeader>屏蔽本机厂商官网 · 管理厂商</ModalHeader>
        <ModalBody data-vendor-manager className="space-y-4">
          <section className="space-y-2">
            <h3 className="text-sm font-medium">每台落地服务器的厂商</h3>
            <p className="text-xs text-default-500">
              只屏蔽这台服务器自己的厂商；未设置的不屏蔽（卡片上显示
              ⚠）。也可以在服务器的「设置 → 出站与路由」里改。
            </p>
            <div className="divide-y divide-divider rounded-lg border border-divider">
              {servers.map((s) => (
                <div
                  key={s.id}
                  className="grid grid-cols-1 items-center gap-2 p-2 sm:grid-cols-[minmax(0,1fr)_220px]"
                  data-vendor-server={s.id}
                >
                  <div className="min-w-0">
                    <div className="truncate text-sm font-medium">
                      {!s.vendor && (
                        <AttentionIcon
                          className="mr-1 inline-block h-3.5 w-3.5 align-[-2px]"
                          level="warn"
                        />
                      )}
                      {s.name}
                    </div>
                    <div className="text-xs text-default-500">
                      {s.source === "shared" ? "妙妙屋共享" : "自有落地"} ·{" "}
                      {s.nodes} 个节点
                      {!s.vendor &&
                        s.suggest &&
                        ` · 建议：${nameOf(vendor, s.suggest)}`}
                    </div>
                  </div>
                  <Select
                    aria-label={`${s.name} 的厂商`}
                    isDisabled={assigning === s.id}
                    selectedKeys={[s.vendor || "-"]}
                    size="sm"
                    variant="bordered"
                    onSelectionChange={(keys) => {
                      const k = String(Array.from(keys)[0] ?? "");

                      if (!k) return;
                      void assign(s.id, k === "-" ? "" : k);
                    }}
                  >
                    {[
                      <SelectItem key="-">未设置（不屏蔽）</SelectItem>,
                      ...saved.map((v) => (
                        <SelectItem key={v.key}>{v.name}</SelectItem>
                      )),
                    ]}
                  </Select>
                </div>
              ))}
            </div>
          </section>
          <section className="space-y-2">
            <h3 className="text-sm font-medium">厂商和它的域名</h3>
            <p className="text-xs text-default-500">
              只填厂商自己的官网、客户中心、账单、工单、面板域名，每行一个，子域名自动包含；不要填别的网站也在用的公共服务（如
              amazonaws.com、cloudfront.net、aliyuncs.com、cloudflare.com）。
            </p>
            {rows.map((r, i) => (
              <div
                key={r.key || `new-${i}`}
                className="space-y-2 rounded-lg border border-divider p-2"
                data-vendor-edit={r.key || "new"}
              >
                <div className="flex items-start gap-2">
                  <Input
                    className="flex-1"
                    label="名称"
                    size="sm"
                    value={r.name}
                    variant="bordered"
                    onChange={(e) => setRow(i, { name: e.target.value })}
                  />
                  <Button
                    isIconOnly
                    aria-label={`删除 ${r.name}`}
                    color="danger"
                    isDisabled={used.has(r.key)}
                    size="sm"
                    title={used.has(r.key) ? "还有落地服务器在用" : "删除"}
                    variant="flat"
                    onPress={() =>
                      setRows((cur) => cur.filter((_, j) => j !== i))
                    }
                  >
                    <Trash2 size={14} />
                  </Button>
                </div>
                <textarea
                  aria-label={`${r.name} 的域名`}
                  className="min-h-[56px] w-full rounded-md border border-input bg-transparent px-2 py-1 font-mono text-xs"
                  placeholder={"dmit.io\ndmithost.net"}
                  value={r.domains}
                  onChange={(e) => setRow(i, { domains: e.target.value })}
                />
                <Input
                  label="备注"
                  size="sm"
                  value={r.remark}
                  variant="bordered"
                  onChange={(e) => setRow(i, { remark: e.target.value })}
                />
              </div>
            ))}
            <Button
              size="sm"
              startContent={<Plus size={14} />}
              variant="flat"
              onPress={() =>
                setRows((cur) => [
                  ...cur,
                  { key: "", name: "", domains: "", remark: "" },
                ])
              }
            >
              添加厂商
            </Button>
          </section>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            关闭
          </Button>
          <Button
            color="success"
            isLoading={saving}
            onPress={() => void saveList()}
          >
            保存厂商列表
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
