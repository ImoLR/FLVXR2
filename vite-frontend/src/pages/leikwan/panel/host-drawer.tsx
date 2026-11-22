import type { LeiKwanHostManage } from "@/api/leikwan-line";
import type { LeiKwanHostItem } from "@/pages/leikwan/panel/host-manage";

import { Pill } from "@/pages/landing/pill";
import {
  CapPills,
  OfflinePill,
  hostOnline,
  hostRunning,
  hostStatusText,
  hostStatusTone,
  offlineText,
  twShort,
} from "@/pages/leikwan/panel/entry-state";
import {
  HostManageBlock,
  HostNameMarker,
} from "@/pages/leikwan/panel/host-manage";
import { KV, OnOff } from "@/pages/leikwan/panel/shared";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";

// Plan 270: one 利群入口 host — the read-only host snapshot (厂商版本, 更新锁,
// 前端通道, 日志快切, 首包合并, 手机备线 …) and every plan-269 面板接管 control
// (接管 switch, diff view, drift ⚠, 流量统计, host reports). Desktop opens it
// from the 2×2 tile in a modal; H5 shows it inside the ＋/− row.

// Plan 295: the state helpers live in entry-state (offline wins).
export { hostRunning, hostStatusText };

export function HostDetails({
  host,
  nightOnly,
  onView,
  compact = false,
}: {
  host: LeiKwanHostItem;
  nightOnly: boolean;
  onView: (nodeId: number, view: LeiKwanHostManage) => void;
  /** H5: narrow label column */
  compact?: boolean;
}) {
  const s = host.snapshot;
  const kvw = compact ? "w-[5.25rem]" : "w-28";

  return (
    <div className="space-y-2" data-leikwan-host={host.node.id}>
      <div className="flex flex-wrap items-center gap-1.5 text-xs text-default-500">
        <span className="font-mono">{host.node.host}</span>
        <Pill
          title={
            host.node.online ? undefined : offlineText(host.node.offlineSince)
          }
          tone={host.node.online ? "green" : "red"}
        >
          {host.node.online ? "节点在线" : "节点离线"}
        </Pill>
        {!host.node.online && host.node.offlineSince && (
          <span>自 {twShort(host.node.offlineSince)} 台湾</span>
        )}
        <Pill tone="purple">利群转发 {host.node.leikwanForwards} 条</Pill>
        {s ? (
          <span>
            快照{" "}
            {new Date(s.capturedAt || host.capturedAt || 0).toLocaleString(
              "zh-CN",
              { timeZone: "Asia/Taipei" },
            )}{" "}
            台湾
          </span>
        ) : (
          <Pill tone="amber">还没有快照</Pill>
        )}
      </div>
      {s && (
        <div className="divide-y divide-divider/60">
          <KV label="利群客户端" labelClassName={kvw}>
            {hostOnline(host) ? (
              <Pill tone={hostStatusTone(host)}>{hostStatusText(host)}</Pill>
            ) : (
              <Pill title="节点离线：这是离线前最后一次快照的状态" tone="slate">
                快照时{" "}
                {host.node.leikwanStatus === "running"
                  ? "运行中"
                  : host.node.leikwanStatus || "未检测到"}
              </Pill>
            )}
            <OnOff label="更新锁" on={s.updateLock} />
            <OnOff label="原生 UDP" on={s.nativeUdp} />
            {s.selectionMode && (
              <Pill tone="slate">选路 {s.selectionMode}</Pill>
            )}
          </KV>
          <KV label="厂商版本" labelClassName={kvw}>
            <Pill
              tone={s.agentVersion.startsWith("2026.11") ? "green" : "slate"}
            >
              agent {s.agentVersion}
            </Pill>
            <Pill
              tone={s.coreVersion.startsWith("2026.11") ? "green" : "slate"}
            >
              核心 {s.coreVersion}
            </Pill>
          </KV>
          <KV label="前端通道" labelClassName={kvw}>
            <Pill tone={s.frontChannel === "test" ? "amber" : "slate"}>
              {s.frontChannel === "test"
                ? "测试版"
                : s.frontChannel === "stable"
                  ? "稳定版"
                  : s.frontChannel || "—"}
            </Pill>
            <Pill tone={s.fastFailover === "test" ? "amber" : "slate"}>
              快切 v4{" "}
              {s.fastFailover === "test"
                ? "测试版"
                : s.fastFailover === "stable"
                  ? "稳定版"
                  : "关"}
            </Pill>
            {s.frontBuild && (
              <span className="text-xs text-default-500">{s.frontBuild}</span>
            )}
          </KV>
          <KV label="日志快切" labelClassName={kvw}>
            <Pill tone={s.logswitch ? "green" : "slate"}>
              {s.logswitch ? `${s.logswitch} 运行中` : "没装"}
            </Pill>
            {s.logswitchRecordOnly && s.logswitchRecordOnly.length > 0 && (
              <Pill title={s.logswitchRecordOnly.join(" ")} tone="amber">
                全部触发只记录
              </Pill>
            )}
            <span className="text-xs text-default-500">
              接线 {s.lines?.length || 0} 条
            </span>
          </KV>
          {host.caps && (
            <KV label="支持功能" labelClassName={kvw}>
              <CapPills caps={host.caps} />
            </KV>
          )}
          <KV label="首包合并" labelClassName={kvw}>
            <Pill tone={s.coalesce.startsWith("on") ? "green" : "slate"}>
              {s.coalesce || "—"}
            </Pill>
          </KV>
          <KV label="手机备线" labelClassName={kvw}>
            {s.phoneBackup && s.phoneBackup.length > 0 ? (
              <Pill
                wrap
                title={s.phoneBackup.map((p) => p.note).join(" · ")}
                tone="slate"
              >
                {s.phoneBackup.map((p) => p.port).join(" · ")}
              </Pill>
            ) : (
              "—"
            )}
          </KV>
          <KV label="转发" labelClassName={kvw}>
            <span className="text-xs">{s.forwardsReady || "—"}</span>
          </KV>
          <KV label="在线设备名额" labelClassName={kvw}>
            <span className="text-xs text-default-500">
              {s.deviceQuota || "主机上读不到（11-16 记录 5 / 5）"}
            </span>
          </KV>
          {s.extra && s.extra.length > 0 && (
            <KV label="其他（清单外）" labelClassName={kvw}>
              <ul className="list-disc pl-4 text-xs text-default-600">
                {s.extra.map((x) => (
                  <li key={x}>{x}</li>
                ))}
              </ul>
            </KV>
          )}
        </div>
      )}
      <HostManageBlock host={host} nightOnly={nightOnly} onView={onView} />
    </div>
  );
}

export function HostDrawer({
  host,
  nightOnly,
  onView,
  onClose,
  onSetMain,
}: {
  host: LeiKwanHostItem | null;
  nightOnly: boolean;
  onView: (nodeId: number, view: LeiKwanHostManage) => void;
  onClose: () => void;
  /** Plan 295: 「设为主入口…」 (pick a granted user in the grant dialog) */
  onSetMain?: (nodeId: number) => void;
}) {
  return (
    <Modal
      classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-2xl rounded-2xl" }}
      isOpen={!!host}
      placement="center"
      scrollBehavior="inside"
      size="2xl"
      onClose={onClose}
    >
      <ModalContent>
        <ModalHeader>
          <span className="flex items-center gap-2">
            <span
              className={`h-2 w-2 rounded-full ${host && !hostOnline(host) ? "bg-red-500" : host && hostRunning(host) ? "bg-green-500" : "bg-slate-400"}`}
            />
            {host?.node.name}
            {host && <HostNameMarker manage={host.manage} />}
            {host && !hostOnline(host) && (
              <OfflinePill since={host.node.offlineSince} />
            )}
            {host && host.mainEntryUsers > 0 && (
              <Pill tone="teal">主入口 {host.mainEntryUsers} 人</Pill>
            )}
          </span>
        </ModalHeader>
        <ModalBody>
          {host && (
            <HostDetails host={host} nightOnly={nightOnly} onView={onView} />
          )}
        </ModalBody>
        <ModalFooter>
          {host && onSetMain && (
            <Button
              color="primary"
              isDisabled={!hostOnline(host)}
              title={
                hostOnline(host)
                  ? "选一个已授权用户，把主入口换到这台"
                  : "离线的入口不能设为主入口"
              }
              variant="flat"
              onPress={() => onSetMain(host.node.id)}
            >
              设为主入口…
            </Button>
          )}
          <Button color="danger" variant="flat" onPress={onClose}>
            关闭
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
