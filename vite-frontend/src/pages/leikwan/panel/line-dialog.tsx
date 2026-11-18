import type {
  LeiKwanAccel,
  LeiKwanLine,
  LeiKwanLineList,
} from "@/api/leikwan-line";

import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import { saveLeiKwanLine } from "@/api/leikwan-line";
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
import { Pill } from "@/pages/landing/pill";
import {
  HostValuesTable,
  LabeledSwitch,
  LineChip,
  SectionTitle,
  TriggerList,
} from "@/pages/leikwan/panel/shared";
import { BackupTunnelPicker } from "@/pages/leikwan/panel/backup-tunnel";

// Plan 266: new / edit 「利群专线」 tunnel. Only name + LeiKwan route + the
// 利群加速 defaults — there is no entry field: a user's entry comes from the
// grant of the tunnel group 「利群汇聚」.

const defaultAccel: LeiKwanAccel = {
  warm: {
    main: 4,
    backup: 4,
    standby: 2,
    paceMs: 100,
    flush: true,
    burst: "测试中",
  },
  ssFilter: "enforce",
  logswitch: true,
  returnCheck: true,
};

export function LeiKwanLineDialog({
  data,
  line,
  isOpen,
  onClose,
  onSaved,
}: {
  data: LeiKwanLineList | null;
  line: LeiKwanLine | null;
  isOpen: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState("");
  const [routeId, setRouteId] = useState("");
  const [backupTunnelId, setBackupTunnelId] = useState("");
  const [accel, setAccel] = useState<LeiKwanAccel>(defaultAccel);
  const [saving, setSaving] = useState(false);
  const [showTriggers, setShowTriggers] = useState(false);
  // Plan 269: 利群流量 billing (same meaning as tunnels).
  const [flowMode, setFlowMode] = useState("1");
  const [ratio, setRatio] = useState("1");

  useEffect(() => {
    if (!isOpen) return;
    setFlowMode(String(line?.flowMode === 2 ? 2 : 1));
    setRatio(
      String(
        line?.trafficRatio && line.trafficRatio > 0 ? line.trafficRatio : 1,
      ),
    );
    setName(line?.name || "");
    setRouteId(line ? String(line.routeId) : "");
    setAccel(line?.accel || defaultAccel);
    setShowTriggers(false);
    if (line) setBackupTunnelId(String(line.backupTunnelId || ""));
    else {
      // Default 备线隧道 = the 深港ixp1 port-forward tunnel.
      const pick =
        data?.backupTunnels.find(
          (t) =>
            t.type === 1 &&
            /深港ixp ?1/.test(t.entryNames) &&
            !/测试/.test(t.name),
        ) || data?.backupTunnels.find((t) => t.type === 1);

      setBackupTunnelId(pick ? String(pick.id) : "");
    }
  }, [isOpen, line, data]);

  const used = new Set(
    (data?.lines || []).filter((l) => l.id !== line?.id).map((l) => l.routeId),
  );
  const routes = (data?.routes || []).filter((r) => !used.has(r.id));
  const warm = accel.warm;
  const setWarm = (patch: Partial<LeiKwanAccel["warm"]>) =>
    setAccel((a) => ({ ...a, warm: { ...a.warm, ...patch } }));
  const numField = (
    label: string,
    value: number,
    onChange: (v: number) => void,
    suffix?: string,
  ) => (
    <Input
      endContent={
        suffix ? (
          <span className="text-xs text-default-400">{suffix}</span>
        ) : undefined
      }
      inputMode="numeric"
      label={label}
      size="sm"
      value={String(value)}
      variant="bordered"
      onChange={(e) =>
        onChange(Math.max(0, parseInt(e.target.value || "0", 10) || 0))
      }
    />
  );

  const save = async () => {
    const rid = parseInt(routeId, 10);

    if (!name.trim() || !rid) {
      toast.error("请填写隧道名称并选择利群路线");

      return;
    }
    const trafficRatio = parseFloat(ratio);

    if (!(trafficRatio > 0 && trafficRatio <= 100)) {
      toast.error("流量倍率要在 0.01 到 100 之间");

      return;
    }
    setSaving(true);
    const res = await saveLeiKwanLine({
      id: line?.id,
      name: name.trim(),
      routeId: rid,
      transport: line?.transport || "tcp+udp",
      backupTunnelId: parseInt(backupTunnelId || "0", 10) || 0,
      accel,
      inx: line?.inx ?? data?.lines.length ?? 0,
      flowMode: flowMode === "2" ? 2 : 1,
      trafficRatio,
    });

    setSaving(false);
    if (res.code !== 0) {
      toast.error(res.msg || "保存失败");

      return;
    }
    toast.success(line ? "已保存" : "利群专线已建好");
    onSaved();
    onClose();
  };

  return (
    <Modal
      classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-2xl rounded-2xl" }}
      isOpen={isOpen}
      placement="center"
      scrollBehavior="inside"
      size="2xl"
      onClose={onClose}
    >
      <ModalContent>
        <ModalHeader className="flex items-center gap-2">
          {line ? `编辑隧道 · ${line.name}` : "新建隧道"}
          <LineChip />
        </ModalHeader>
        <ModalBody className="space-y-4">
          <Input
            label="隧道名称"
            placeholder="利群·广港"
            value={name}
            variant="bordered"
            onChange={(e) => setName(e.target.value)}
          />
          <div className="space-y-1.5">
            <span className="text-xs text-default-500">隧道类型</span>
            <div className="flex flex-wrap items-center gap-2">
              <Pill tone="slate">端口转发</Pill>
              <Pill tone="slate">隧道转发</Pill>
              <Pill className="ring-1 ring-teal-500/60" tone="teal">
                ✓ 利群专线
              </Pill>
              <span className="text-xs text-default-400">
                端口转发 / 隧道转发 请用隧道页的「新增」
              </span>
            </div>
          </div>
          <Select
            isDisabled={!!line && line.ruleCount > 0}
            label="利群路线"
            placeholder="选择利群路线"
            selectedKeys={routeId ? [routeId] : []}
            variant="bordered"
            onSelectionChange={(keys) =>
              setRouteId(String(Array.from(keys)[0] || ""))
            }
          >
            {[
              ...(line
                ? [
                    {
                      id: line.routeId,
                      label: line.routeLabel,
                      available: true,
                    },
                  ]
                : []),
              ...routes.filter((r) => r.id !== line?.routeId),
            ].map((r) => (
              <SelectItem key={String(r.id)}>
                {`${r.label} · #${r.id}${r.available ? "" : "（暂不可用）"}`}
              </SelectItem>
            ))}
          </Select>
          <div className="rounded-xl bg-teal-500/10 px-3 py-2 text-sm dark:bg-teal-400/10">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">入口</span>
              <Pill tone="teal">
                节点分组「{data?.setting.entryGroupName || "利群入口"}」
              </Pill>
              <span className="text-default-600">用户的入口在授权时指定</span>
            </div>
            <p className="mt-1 text-xs text-default-500">
              这里没有入口可选，建隧道时不用管入口。所在隧道组：
              {data?.setting.groupName || "利群汇聚"}
            </p>
          </div>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <Select
              description="单向：(上传+下载)×倍率；双向：(上传+下载)×倍率×2"
              label="流量计算"
              selectedKeys={[flowMode]}
              variant="bordered"
              onSelectionChange={(keys) => {
                const k = String(Array.from(keys)[0] || "");

                if (k) setFlowMode(k);
              }}
            >
              <SelectItem key="1">单向计算（上传+下载）</SelectItem>
              <SelectItem key="2">双向计算（上传+下载）×2</SelectItem>
            </Select>
            <Input
              description="利群流量统计开了的入口机，计进用户总流量时乘这个倍率"
              label="流量倍率"
              max={100}
              min={0.01}
              placeholder="例如：0.5 或 1 或 2"
              step="any"
              type="number"
              value={ratio}
              variant="bordered"
              onChange={(e) => setRatio(e.target.value)}
            />
          </div>

          <div className="space-y-3 rounded-xl border border-divider p-3">
            <SectionTitle
              hint="只有管理员可见；第一步只保存和显示，主机文件第二步由 agent 生成"
              right={<Pill tone="amber">默认值</Pill>}
              title="利群加速"
            />
            <div className="space-y-2">
              <div className="flex items-center gap-2 text-sm font-medium">
                预热池
                <span className="text-xs font-normal text-default-500">
                  只在这条专线的转发用到的主入口上跑，不是组里每台都开
                </span>
              </div>
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                {numField(
                  "主线",
                  warm.main,
                  (v) => setWarm({ main: Math.min(16, v) }),
                  "条",
                )}
                {numField(
                  "备线",
                  warm.backup,
                  (v) => setWarm({ backup: Math.min(16, v) }),
                  "条",
                )}
                {numField(
                  "备线待命",
                  warm.standby,
                  (v) => setWarm({ standby: Math.min(16, v) }),
                  "条",
                )}
                {numField(
                  "补充间隔",
                  warm.paceMs,
                  (v) => setWarm({ paceMs: Math.min(1000, v) }),
                  "ms",
                )}
              </div>
              <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
                <LabeledSwitch
                  isSelected={warm.flush}
                  onValueChange={(v) => setWarm({ flush: v })}
                >
                  切换和切回时清空重补
                </LabeledSwitch>
                <span className="flex items-center gap-1.5">
                  突发余量 <Pill tone="amber">{warm.burst || "测试中"}</Pill>
                  <span className="text-xs text-default-400">
                    百度云广州 A/B 中，结果出来再定
                  </span>
                </span>
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
              <LabeledSwitch
                isSelected={accel.ssFilter === "enforce"}
                onValueChange={(v) =>
                  setAccel((a) => ({ ...a, ssFilter: v ? "enforce" : "off" }))
                }
              >
                首包过滤（SS2022）
              </LabeledSwitch>
              <span className="text-xs text-default-500">
                密钥从转发的落地自动取
              </span>
            </div>
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
              <LabeledSwitch
                isSelected={accel.logswitch}
                onValueChange={(v) => setAccel((a) => ({ ...a, logswitch: v }))}
              >
                日志快切
              </LabeledSwitch>
              <LabeledSwitch
                isDisabled={!accel.logswitch}
                isSelected={accel.returnCheck}
                onValueChange={(v) =>
                  setAccel((a) => ({ ...a, returnCheck: v }))
                }
              >
                切回前复查
              </LabeledSwitch>
              <Button
                color="secondary"
                size="sm"
                variant="flat"
                onPress={() => setShowTriggers((v) => !v)}
              >
                {showTriggers ? "收起触发条件" : "触发条件（全局一套）"}
              </Button>
            </div>
            {showTriggers && data && (
              <div className="rounded-lg bg-default-100/60 p-2 dark:bg-default-50/40">
                <TriggerList triggers={data.triggers} />
                <p className="mt-2 text-xs text-default-500">
                  保持 {data.setting.global.holdS} s（再次触发翻倍）· 新租约稳定{" "}
                  {data.setting.global.leaseSettleS} s · 宽限{" "}
                  {data.setting.global.grace} · io_error{" "}
                  {data.setting.global.ioError}
                </p>
              </div>
            )}
            <BackupTunnelPicker
              data={data}
              value={backupTunnelId}
              onChange={setBackupTunnelId}
            />
            {line &&
              String(line.backupTunnelId || "") !== backupTunnelId &&
              (line.backupStats?.auto || 0) > 0 && (
                <p className="rounded-lg bg-amber-500/15 px-3 py-2 text-xs text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
                  新转发用新备线。已有的 {line.backupStats?.auto}{" "}
                  条自动备线不会自己搬：保存后在列表点「按新备线重建」。
                  {(line.backupStats?.linked || 0) > 0 &&
                    `导入时链接的 ${line.backupStats?.linked} 条备线不动。`}
                </p>
              )}
          </div>

          {line && line.hostValues.length > 0 && (
            <div className="space-y-2">
              <SectionTitle
                hint="只读：从入口机读到的，第一步不改主机"
                title="主机现值"
              />
              {line.hostValues.map((h) => (
                <div key={h.forwardId} className="space-y-1">
                  <div className="text-xs font-medium">
                    #{h.forwardId} · {h.ruleName}
                  </div>
                  <HostValuesTable values={h.values} />
                </div>
              ))}
            </div>
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="success"
            isLoading={saving}
            variant="flat"
            onPress={() => void save()}
          >
            {line ? "保存" : "建好隧道"}
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
