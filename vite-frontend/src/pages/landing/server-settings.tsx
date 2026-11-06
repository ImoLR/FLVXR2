import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import { SharedCaps } from "./shared-caps";

import { getUserGroupList } from "@/api";
import { LandingServer, saveLandingServer } from "@/api/landing";
import { EgressProfile, egressOverview } from "@/api/landing-egress";
import { UserGroupApiItem } from "@/api/types";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
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
import { ExtraProtocolSettings } from "@/pages/landing/extra-protocols";
import {
  AttentionIcon,
  AttentionList,
  AttentionSection,
  SectionTitle,
  sectionLevel,
  serverAdminAttention,
} from "@/pages/landing/admin-attention";
import {
  connModePendingText,
  MachineCapHint,
  NODE_TOTAL_NAME,
  NODE_TOTAL_UNIT,
  nodeLimitUnit,
} from "@/pages/landing/conn-mode";

export const dateInput = (value?: number | null) =>
  value
    ? new Date(value - new Date(value).getTimezoneOffset() * 60000)
        .toISOString()
        .slice(0, 16)
    : "";
export const dateValue = (value: string) =>
  value ? new Date(value).getTime() : null;

export function ServerSettings({
  server,
  onClose,
  onSaved,
}: {
  server: LandingServer;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState(server);
  const [groups, setGroups] = useState<UserGroupApiItem[]>([]);
  const [saving, setSaving] = useState(false);
  const [realityNamesInput, setRealityNamesInput] = useState(
    (server.realityServerNames || []).join(","),
  );
  const [profiles, setProfiles] = useState<EgressProfile[]>([]);
  const [restartConfirmed, setRestartConfirmed] = useState(false);
  // Plan 129: one restart of an outdated landing Core onto the pinned one.
  const [coreUpgradeConfirmed, setCoreUpgradeConfirmed] = useState(false);
  // 「同时应用到现有节点」: off by default (existing nodes keep their number).
  const [applyConnMode, setApplyConnMode] = useState(false);
  // Plan 211: same pattern for the 节点总连接数 default.
  const [applyNodeTotal, setApplyNodeTotal] = useState(false);
  // Plan 158: the 计算方式 selectors are retired — the server total counts
  // 入站+出站, every node limit 入站 (a shared server's nodes once 妙妙屋
  // applies user limits). A native agent that cannot count 入站 yet keeps
  // 入站+出站 until it is upgraded.
  const connModeFixed = Boolean(
    server.connModeFixed || server.source === "shared",
  );
  const nodeUnit = nodeLimitUnit(server);
  const connModePending =
    server.source !== "shared" && server.connModeSupported === false;
  const egress = server.egress;
  const profileId = form.routeProfileId ?? egress?.profileId ?? 0;
  const profileChanged = profileId !== (egress?.profileId ?? 0);
  const needsRestart = Boolean(
    egress?.restartRequired && profileChanged && profileId !== 0,
  );
  // Plan 127: the default anti-abuse rules (and the Reality guard) wait for
  // the routing API; the admin may confirm the one-time restart here.
  const abuseRestart = egress?.abuseDefaults === "restart" && !profileChanged;
  const guard = egress?.realityGuard;
  // Plan 129: a shared server's guard runs in the 妙妙屋 Helper.
  const isShared = server.source === "shared";
  const [guardEnabled, setGuardEnabled] = useState(guard?.enabled ?? true);
  // Plan 218: what this server waits for (same helper as the card's ⚠); the
  // banner lists it, the sections and warning blocks carry the same ⚠.
  const attention = serverAdminAttention(server);
  const has = (key: string) =>
    Boolean(attention?.reasons.some((reason) => reason.key === key));
  const mark = (key: string, level: "warn" | "error" = "warn") =>
    has(key) ? (
      <AttentionIcon
        className="mr-1 inline-block h-3.5 w-3.5 align-[-2px]"
        level={level}
      />
    ) : null;
  const sectionProps = (section: AttentionSection) =>
    sectionLevel(attention, section)
      ? { "data-attention-section": section }
      : {};
  const scrollToSection = (section: AttentionSection) =>
    document
      .querySelector(`[data-attention-section="${section}"]`)
      ?.scrollIntoView({ behavior: "smooth", block: "start" });
  const scan = !isShared ? server.scanGuard?.state : undefined;
  const set = <K extends keyof LandingServer>(
    key: K,
    value: LandingServer[K],
  ) => setForm((prev) => ({ ...prev, [key]: value }));

  useEffect(() => {
    void getUserGroupList().then((res) => {
      if (res.code === 0) setGroups(res.data || []);
      else toast.error(res.msg);
    });
    void egressOverview().then((res) => {
      if (res.code === 0) setProfiles(res.data.profiles || []);
    });
  }, []);
  const numberField = (
    label: string,
    key: keyof LandingServer,
    min = 0,
    step = "1",
    scale = 1,
  ) => (
    <Input
      label={label}
      min={String(min)}
      step={step}
      type="number"
      value={String(Number(form[key] ?? 0) / scale)}
      variant="bordered"
      onChange={(event) =>
        set(key, (Number(event.target.value) * scale) as never)
      }
    />
  );

  const save = async () => {
    if (
      !form.name.trim() ||
      !form.publicHost.trim() ||
      form.portMin < 1024 ||
      form.portMax > 65535 ||
      form.portMin > form.portMax ||
      !form.protocols.length ||
      form.trafficRatio <= 0 ||
      // Plan 223: shared servers' Core limits are 0 / 0 = 不限制 (由共享方决定).
      (!isShared && (form.coreMemoryMb < 64 || form.coreCpuPercent <= 0))
    ) {
      toast.error("请检查名称、地址、端口段、协议、倍率和资源上限");

      return;
    }
    if (needsRestart && !restartConfirmed) {
      toast.error("请先勾选确认重启落地 Core");

      return;
    }
    setSaving(true);
    const response = await saveLandingServer(
      {
        ...form,
        routeProfileId: profileId,
        realityServerNames: realityNamesInput
          .split(",")
          .map((name) => name.trim())
          .filter(Boolean),
      },
      restartConfirmed,
      guard && guard.state !== "shared" && guardEnabled !== guard.enabled
        ? guardEnabled
        : undefined,
      applyConnMode,
      coreUpgradeConfirmed,
      applyNodeTotal,
    );

    setSaving(false);
    if (response.code !== 0) {
      toast.error(response.msg || "保存失败");

      return;
    }
    toast.success("落地服务器设置已保存");
    (response.data?.egressWarnings || []).forEach((w) => toast(w));
    onSaved();
    onClose();
  };

  return (
    <Modal
      isOpen
      classNames={{
        base: "sm:left-auto sm:right-0 sm:translate-x-0 sm:rounded-r-none",
      }}
      scrollBehavior="inside"
      size="lg"
      onClose={onClose}
    >
      <ModalContent className="flvx-landing">
        <ModalHeader>落地服务器设置 · {server.name}</ModalHeader>
        <ModalBody className="space-y-6">
          {attention && (
            <div
              className={`rounded-lg p-3 text-xs ${
                attention.level === "error"
                  ? "bg-red-500/10 dark:bg-red-400/15"
                  : "bg-amber-500/10 dark:bg-amber-400/15"
              }`}
              data-attention-banner={attention.level}
            >
              <AttentionList
                attention={attention}
                onPick={(reason) => scrollToSection(reason.sections[0])}
              />
            </div>
          )}
          {server.source === "shared" && <SharedCaps server={server} />}
          <section className="space-y-3">
            <h3 className="font-medium">基本设置</h3>
            <Input
              label="名称"
              value={form.name}
              variant="bordered"
              onChange={(event) => set("name", event.target.value)}
            />
            <Input
              description="用于直连链接，不含协议和端口"
              label="公网地址"
              value={form.publicHost}
              variant="bordered"
              onChange={(event) => set("publicHost", event.target.value)}
            />
            <div className="grid grid-cols-2 gap-3">
              {numberField("起始端口", "portMin", 1024)}
              {numberField("结束端口", "portMax", 1024)}
            </div>
            <p className="text-xs text-default-500">
              端口由系统自动分配，不能与规则端口段或现有监听冲突。
            </p>
            <Select
              label="状态"
              selectedKeys={[form.status]}
              onSelectionChange={(keys) =>
                set("status", String(Array.from(keys)[0]))
              }
            >
              <SelectItem key="active">启用</SelectItem>
              <SelectItem key="disabled">停用</SelectItem>
            </Select>
          </section>
          <section className="space-y-3">
            <h3 className="font-medium">计费与限制</h3>
            <div className="grid grid-cols-2 gap-3">
              <Select
                label="流量计算"
                selectedKeys={[String(form.flowType)]}
                onSelectionChange={(keys) =>
                  set("flowType", Number(Array.from(keys)[0]))
                }
              >
                <SelectItem key="1">单向</SelectItem>
                <SelectItem key="2">双向</SelectItem>
              </Select>
              {numberField("流量倍率", "trafficRatio", 0.01, "0.01")}
              {/* Plan 211: 服务器总连接数 is hidden (not used, not enforced);
                  each node has its own 节点总连接数 below. */}
              {numberField("每人在本服务器最多节点数", "maxNodesPerUser", 1)}
            </div>
            <p className="text-xs text-default-500">
              落地计入用户同一个总流量。通过本人规则访问时按隧道与落地较贵的费率计费。
            </p>
          </section>
          <section className="space-y-3" {...sectionProps("defaults")}>
            <SectionTitle attention={sectionLevel(attention, "defaults")}>
              新节点默认值
            </SectionTitle>
            {server.source === "shared" &&
              numberField(
                "每个用户默认服务器限额 (GB，计费流量)",
                "defaultUserQuotaBytes",
                0,
                "0.01",
                1073741824,
              )}
            <div className="grid grid-cols-2 gap-3">
              {numberField(
                `节点连接数（${nodeUnit}，0 不限）`,
                "defaultMaxConnections",
              )}
              {numberField("在线 IP 数（0 不限）", "defaultMaxOnlineIps")}
              {server.connMode !== undefined &&
                !connModeFixed &&
                (connModePending || (server.nodeCount ?? 0) > 0) && (
                  <div className="col-span-2 space-y-2">
                    {connModePending && (
                      <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10">
                        {mark("agent")}
                        {connModePendingText()}
                      </p>
                    )}
                    {(server.nodeCount ?? 0) > 0 && (
                      <Checkbox
                        isSelected={applyConnMode}
                        onValueChange={setApplyConnMode}
                      >
                        <span className="text-xs">
                          同时应用到现有 {server.nodeCount}{" "}
                          个节点（节点连接数改为{" "}
                          {form.defaultMaxConnections || "不限"}）
                        </span>
                      </Checkbox>
                    )}
                  </div>
                )}
              {/* Plan 211: per-node 入站+出站 cap (incl. TIME_WAIT/CLOSE_WAIT). */}
              {server.defaultMaxTotalConnections !== undefined && (
                <div data-node-total-default className="col-span-2 space-y-2">
                  {numberField(
                    `${NODE_TOTAL_NAME}（${NODE_TOTAL_UNIT}，0 不限）`,
                    "defaultMaxTotalConnections",
                  )}
                  {(server.nodeCount ?? 0) > 0 && (
                    <Checkbox
                      isSelected={applyNodeTotal}
                      onValueChange={setApplyNodeTotal}
                    >
                      <span className="text-xs">
                        同时应用到现有 {server.nodeCount} 个节点（
                        {NODE_TOTAL_NAME}
                        改为 {form.defaultMaxTotalConnections || "不限"}）
                      </span>
                    </Checkbox>
                  )}
                  <MachineCapHint
                    nodeTotal={Number(form.defaultMaxTotalConnections ?? 0)}
                    server={server}
                  />
                </div>
              )}
              {numberField(
                "流量上限 (GB，计费流量，0 不限)",
                "defaultTrafficLimitBytes",
                0,
                "0.01",
                1073741824,
              )}
            </div>
            <p className="text-xs text-default-500">
              限额和流量上限按计费流量：双向 (上传+下载)×倍率×2，单向
              (上传+下载)×倍率。
            </p>
            <Select
              label="默认有效期"
              selectedKeys={[form.defaultExpiryMode]}
              onSelectionChange={(keys) =>
                set("defaultExpiryMode", String(Array.from(keys)[0]))
              }
            >
              <SelectItem key="follow_user">跟随用户</SelectItem>
              <SelectItem key="days">创建后 N 天</SelectItem>
              <SelectItem key="fixed">固定日期</SelectItem>
              <SelectItem key="permanent">
                永久（仍受用户及服务器期限限制）
              </SelectItem>
            </Select>
            {form.defaultExpiryMode === "days" &&
              numberField("有效天数", "defaultExpiryDays", 1)}
            {form.defaultExpiryMode === "fixed" && (
              <Input
                label="默认到期日期"
                type="datetime-local"
                value={dateInput(form.defaultExpiresAt)}
                onChange={(event) =>
                  set("defaultExpiresAt", dateValue(event.target.value))
                }
              />
            )}
          </section>
          <section className="space-y-3">
            <h3 className="font-medium">协议模板</h3>
            <div className="flex flex-wrap gap-4">
              {(["ss2022", "vless_reality_vision"] as const).map((protocol) => (
                <Checkbox
                  key={protocol}
                  isDisabled={Boolean(
                    server.sharedCaps &&
                      !server.sharedCaps.protocols.includes(protocol),
                  )}
                  isSelected={form.protocols.includes(protocol)}
                  onValueChange={(checked) =>
                    set(
                      "protocols",
                      checked
                        ? [...form.protocols, protocol]
                        : form.protocols.filter((item) => item !== protocol),
                    )
                  }
                >
                  {protocol === "ss2022" ? "SS2022" : "VLESS Reality Vision"}
                </Checkbox>
              ))}
            </div>
            {form.protocols.includes("vless_reality_vision") && (
              <>
                <Input
                  label="Reality 目标 (host:port)"
                  value={form.realityDest}
                  variant="bordered"
                  onChange={(event) => set("realityDest", event.target.value)}
                />
                <Input
                  description="多个域名用逗号分隔"
                  label="Reality Server Names"
                  value={realityNamesInput}
                  variant="bordered"
                  onChange={(event) => setRealityNamesInput(event.target.value)}
                />
              </>
            )}
            <ExtraProtocolSettings form={form} server={server} set={set} />
          </section>
          <section className="space-y-3" {...sectionProps("egress")}>
            <SectionTitle attention={sectionLevel(attention, "egress")}>
              出站与路由
            </SectionTitle>
            {egress && !egress.supported && (
              <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning">
                {!isShared && mark("agent")}
                {egress.unsupportedReason}
              </p>
            )}
            <Select
              description="默认 = 跟随全局默认出站（未设置时直连）；方案在「出站与路由」标签页管理"
              isDisabled={!egress?.supported}
              label="路由方案"
              selectedKeys={[String(profileId)]}
              onSelectionChange={(keys) => {
                setRestartConfirmed(false);
                set("routeProfileId", Number(Array.from(keys)[0]));
              }}
            >
              {[
                <SelectItem key="0">默认（跟随全局默认出站）</SelectItem>,
                ...profiles.map((p) => (
                  <SelectItem key={String(p.id)}>{p.name}</SelectItem>
                )),
              ]}
            </Select>
            {needsRestart && (
              <div className="space-y-2 rounded-lg bg-warning-50 p-3 text-xs text-warning">
                <p>
                  保存后该服务器的落地连接会短暂中断：落地 Core
                  需要重启一次才能启用出站与路由（约 1–3
                  秒），之后修改方案不再中断。
                </p>
                <Checkbox
                  isSelected={restartConfirmed}
                  onValueChange={setRestartConfirmed}
                >
                  我已了解，保存时重启落地 Core
                </Checkbox>
              </div>
            )}
            {egress?.state === "error" && egress.error && (
              <p className="rounded-lg bg-danger-50 p-2 text-xs text-danger">
                {mark("egress-error", "error")}
                应用失败：{egress.error}
              </p>
            )}
            {egress?.state === "restart_required" && !needsRestart && (
              <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning">
                {mark("core-restart")}
                等待落地 Core 重启后生效
              </p>
            )}
            {(egress?.warnings || []).map((warning) => (
              <p key={warning} className="text-xs text-warning">
                {warning.includes("因出站与路由未生效已暂停") &&
                  mark("withheld", "error")}
                {warning}
              </p>
            ))}
            {egress?.state === "applied" && !profileChanged && (
              <p className="text-xs text-default-500">
                当前方案已在落地 Core 生效：{egress.label}
              </p>
            )}
            {egress?.abuseDefaults === "active" && !profileChanged && (
              <p className="text-xs text-default-500">
                内置防滥用规则已生效：屏蔽{" "}
                {(egress.abuseRules || ["SMTP 25", "BT"]).join("、")}
              </p>
            )}
            {egress?.abuseDefaults === "global_off" && !profileChanged && (
              <p className="text-xs text-default-500">
                内置防滥用规则（SMTP
                25、发信端口、Telnet/SMB、BT）已在「全局规则 → 内置」里全部关闭
              </p>
            )}
            {egress?.abuseDefaults === "off" && !profileChanged && (
              <p className="text-xs text-default-500">
                所选路由方案关闭了内置防滥用规则（SMTP
                25、发信端口、Telnet/SMB、BT）
              </p>
            )}
            {abuseRestart && (
              <div className="space-y-2 rounded-lg bg-warning-50 p-3 text-xs text-warning">
                <p>
                  {mark("core-restart")}
                  内置防滥用规则（SMTP 25、发信端口、Telnet/SMB、BT）和 Reality
                  防盗还没在这台服务器生效：落地 Core
                  是旧配置启动的，需要重启一次（约 1–3
                  秒，落地连接短暂中断），之后不再中断。
                </p>
                <Checkbox
                  isSelected={restartConfirmed}
                  onValueChange={setRestartConfirmed}
                >
                  保存时重启落地 Core 以启用
                </Checkbox>
              </div>
            )}
            {egress?.coreUpgradeRequired && (
              <div className="space-y-2 rounded-lg bg-warning-50 p-3 text-xs text-warning">
                {egress.coreUpgradeConfirmed ? (
                  <p>已确认升级：等待节点程序重启落地 Core（约 1 分钟内）。</p>
                ) : (
                  <>
                    <p>
                      {mark("core-upgrade")}
                      新版落地 Core 已下载（可识别并拦截 UDP 上的 BT
                      等），但正在运行的仍是旧版：需要重启一次落地 Core（约 1–3
                      秒，落地连接短暂中断，客户端会自动重连）才会换成新版。不重启时旧版照常工作。
                    </p>
                    <Checkbox
                      isSelected={coreUpgradeConfirmed}
                      onValueChange={setCoreUpgradeConfirmed}
                    >
                      保存时重启落地 Core 以升级
                    </Checkbox>
                  </>
                )}
              </div>
            )}
            {/* Plan 218: 禁止端口扫描 on this server when the admin must act
                (same state as 内置; only shown then). */}
            {(scan === "core" || scan === "agent" || scan === "error") && (
              <p
                className={`rounded-lg p-2 text-xs ${
                  scan === "error"
                    ? "bg-danger-50 text-danger"
                    : "bg-warning-50 text-warning"
                }`}
                data-attention-scan={scan}
              >
                {scan === "error" && mark("scan-error", "error")}
                {scan === "agent" && mark("agent")}
                {scan === "core" &&
                  mark(has("core-upgrade") ? "core-upgrade" : "scan-core")}
                {scan === "agent" &&
                  "禁止端口扫描未生效：需要先在「节点」页升级这台服务器的节点程序。"}
                {scan === "error" &&
                  "禁止端口扫描检测异常：读取落地 Core 的扫描记录失败。"}
                {scan === "core" &&
                  (egress?.coreUpgradeConfirmed
                    ? "禁止端口扫描未生效：已确认升级，等待节点程序重启落地 Core。"
                    : egress?.coreUpgradeRequired
                      ? "禁止端口扫描未生效：运行中的落地 Core 还不支持，勾选上方「保存时重启落地 Core 以升级」并保存后生效。"
                      : "禁止端口扫描未生效：落地 Core 需要重启一次，新版 Core 下载完成后这里会出现确认勾选。")}
              </p>
            )}
          </section>
          {guard && (
            <section className="space-y-3" {...sectionProps("guard")}>
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0 space-y-1">
                  <SectionTitle attention={sectionLevel(attention, "guard")}>
                    Reality 防盗
                  </SectionTitle>
                  <p className="text-xs text-default-500">
                    Reality
                    节点只把带本节点伪装域名（serverNames）的探测转给伪装网站，其它域名一律断开，防止落地被当成免费中转偷流量。已认证用户的数据不经过防盗，用户链接不变。
                  </p>
                </div>
                <Switch
                  aria-label="Reality 防盗"
                  isDisabled={guard.state === "shared"}
                  isSelected={guard.state !== "shared" && guardEnabled}
                  onValueChange={setGuardEnabled}
                />
              </div>
              {guard.state === "shared" && (
                <p className="text-xs text-default-500">
                  共享服务器的 Core 由妙妙屋运行，需妙妙屋支持后才能开启。
                </p>
              )}
              {isShared && guard.state !== "shared" && (
                <p className="text-xs text-default-500">
                  这台共享服务器的防盗由妙妙屋 Helper 执行，只作用于 FLVX
                  的节点，妙妙屋自己的节点不受影响。
                </p>
              )}
              {guard.state !== "shared" && guardEnabled === guard.enabled && (
                <p
                  className={`text-xs ${
                    guard.state === "on" ||
                    guard.state === "idle" ||
                    guard.state === "off"
                      ? "text-default-500"
                      : guard.state === "error"
                        ? "text-danger"
                        : "text-warning"
                  }`}
                >
                  {guard.state === "on" &&
                    `已生效：${guard.nodes} 个 Reality 节点`}
                  {guard.state === "idle" && "已开启：暂无 Reality 节点"}
                  {guard.state === "pending" && "已开启：等待妙妙屋应用"}
                  {guard.state === "off" && "已关闭"}
                  {guard.state === "agent_old" && mark("agent")}
                  {guard.state === "agent_old" &&
                    "节点程序版本过旧，升级后生效"}
                  {guard.state === "restart" &&
                    !isShared &&
                    mark("core-restart")}
                  {guard.state === "restart" &&
                    (isShared
                      ? "待生效：妙妙屋端的 FLVX Core 需重启一次，目前不拦截"
                      : "待生效：落地 Core 需重启一次（见上方勾选）")}
                  {guard.state === "error" &&
                    !isShared &&
                    mark("guard-error", "error")}
                  {guard.state === "error" &&
                    (isShared
                      ? "未生效：妙妙屋端应用失败，Reality 节点暂按未开启处理"
                      : "未生效：路由规则应用失败，Reality 节点暂按未开启处理")}
                </p>
              )}
              {guard.state !== "shared" && guardEnabled !== guard.enabled && (
                <p className="text-xs text-warning">
                  {isShared
                    ? guardEnabled
                      ? "保存后开启：妙妙屋会重新加载这台服务器的 Reality 节点一次，正在使用的连接断开一次，客户端几秒内自动重连"
                      : "保存后关闭：妙妙屋会重新加载 Reality 节点一次，未经认证的探测会照旧转给伪装网站"
                    : guardEnabled
                      ? "保存后开启：Reality 节点会重新加载一次（已有连接不断）"
                      : "保存后关闭：未经认证的探测会照旧转给伪装网站"}
                </p>
              )}
            </section>
          )}
          <section className="space-y-3">
            <h3 className="font-medium">可见用户组</h3>
            <p className="text-xs text-default-500">
              只有选中用户组的成员才能查看此服务器和创建节点。
            </p>
            <p className="text-xs text-default-500">
              不选任何用户组时，仅管理员和直接授权的用户可见（在「用户」编辑弹窗的「落地
              → 可用落地服务器」中授权
              {form.directUserCount
                ? `，当前直接授权 ${form.directUserCount} 人`
                : ""}
              ）。
            </p>
            <div className="flex flex-wrap gap-4">
              {groups.map((group) => (
                <Checkbox
                  key={group.id}
                  isSelected={(form.userGroupIds || []).includes(group.id)}
                  onValueChange={(checked) =>
                    set(
                      "userGroupIds",
                      checked
                        ? [...(form.userGroupIds || []), group.id]
                        : (form.userGroupIds || []).filter(
                            (id) => id !== group.id,
                          ),
                    )
                  }
                >
                  {group.name}
                </Checkbox>
              ))}
            </div>
            {groups.length === 0 && (
              <p className="text-sm text-default-500">
                暂无用户组，请先创建用户组。
              </p>
            )}
          </section>
          <section className="space-y-3">
            <h3 className="font-medium">服务器期限与资源</h3>
            <Input
              description="留空表示不单独限制；到期的节点停用，保留 7 天"
              label="服务器可用期限"
              type="datetime-local"
              value={dateInput(form.availableUntil)}
              onChange={(event) =>
                set("availableUntil", dateValue(event.target.value))
              }
            />
            {isShared ? (
              <div className="grid grid-cols-2 gap-3">
                <Input
                  readOnly
                  description="由共享方决定"
                  label="内存上限"
                  value="不限制"
                  variant="bordered"
                />
                <Input
                  readOnly
                  description="由共享方决定"
                  label="CPU 上限"
                  value="不限制"
                  variant="bordered"
                />
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-3">
                {numberField("内存上限 (MiB)", "coreMemoryMb", 64)}
                {numberField("CPU 上限 (%)", "coreCpuPercent", 1)}
              </div>
            )}
          </section>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button color="primary" isLoading={saving} onPress={save}>
            保存设置
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
