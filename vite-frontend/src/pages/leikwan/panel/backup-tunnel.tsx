import type {
  LeiKwanBackupTunnel,
  LeiKwanLine,
  LeiKwanLineList,
} from "@/api/leikwan-line";

import { useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";

import { rebuildLeiKwanLineBackups } from "@/api/leikwan-line";
import { MsTag } from "@/components/latency-tag";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import {
  Select,
  SelectItem,
  SelectSection,
} from "@/shadcn-bridge/heroui/select";

// Plan 268 (user 11-17): one 备线隧道 per 利群专线, picked from ANY tunnel,
// listed by 线路分组 like the admin's rule picker. The backup always goes via
// the 前置 (the user's 主入口 machine): its 日志快切 redirects to the backup
// tunnel's entry, so the user's address never changes. The path is shown in
// one line; the 前置 → backup-entry latency only when the panel already has it.

const OTHER = "other";
const NONE = "none";

const typeLabel = (t: LeiKwanBackupTunnel) =>
  t.type === 1 ? "端口转发" : "隧道转发";

type GroupPill = {
  key: string;
  label: string;
  color: string;
  tunnels: LeiKwanBackupTunnel[];
};

function groupPills(tunnels: LeiKwanBackupTunnel[]): GroupPill[] {
  const groups = new Map<string, GroupPill & { order: number }>();
  const other: LeiKwanBackupTunnel[] = [];

  tunnels.forEach((t) => {
    if (!t.groups?.length) {
      other.push(t);

      return;
    }
    t.groups.forEach((g) => {
      const key = `g${g.id}`;
      const entry = groups.get(key) || {
        key,
        label: g.name,
        color: g.color,
        order: g.order,
        tunnels: [],
      };

      entry.tunnels.push(t);
      groups.set(key, entry);
    });
  });
  const pills: GroupPill[] = Array.from(groups.values()).sort(
    (a, b) => a.order - b.order,
  );

  if (other.length)
    pills.push({ key: OTHER, label: "其他", color: "", tunnels: other });

  return pills;
}

// Sections inside a group: by entry machine, like the rule picker's
// 「入口 → …」 sections.
function entrySections(tunnels: LeiKwanBackupTunnel[]) {
  const map = new Map<string, LeiKwanBackupTunnel[]>();

  tunnels.forEach((t) => {
    const key = t.entryNames || "未设置入口";

    map.set(key, [...(map.get(key) || []), t]);
  });

  return Array.from(map.entries()).sort(([a], [b]) =>
    a.localeCompare(b, "zh-CN"),
  );
}

/** One-line path: 用户 → 前置 → backup entry → hops → exits → 落地. */
export function BackupPathLines({
  data,
  tunnel,
}: {
  data: LeiKwanLineList | null;
  tunnel?: LeiKwanBackupTunnel;
}) {
  if (!tunnel) return null;
  const fronts = data?.fronts?.length
    ? data.fronts
    : [{ id: 0, name: "主入口", grants: 0 }];
  const far = data?.frontFarMs || 30;
  const entry = tunnel.entryNames
    ? tunnel.entryNames.replace(/、/g, " / ")
    : "备线入口";
  const middle = [...(tunnel.hops || []), ...(tunnel.exits || [])];

  return (
    <div className="space-y-1" data-leikwan-backup-path="">
      {fronts.map((f) => {
        const lat = (tunnel.frontLatency || []).filter(
          (l) => l.frontId === f.id,
        );
        const best = lat
          .filter((l) => !l.same && l.ok)
          .sort((a, b) => a.ms - b.ms)[0];
        const same = lat.some((l) => l.same);
        const isFar =
          !same && lat.length > 0 && (!best || best.ms > far || !!best.far);

        return (
          <div
            key={f.id}
            className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs text-default-600"
          >
            <span>
              用户 → {f.name}（前置）→ {entry}
              {middle.length ? ` → ${middle.join(" → ")}` : ""} → 落地
            </span>
            {same && <MsTag size="row" text="前置就是备线入口" tone="green" />}
            {!same && best && (
              <MsTag
                size="row"
                text={`前置→备线入口 ${Math.round(best.ms)} ms`}
                tone={isFar ? "amber" : "green"}
              />
            )}
            {!same && lat.length > 0 && !best && (
              <MsTag size="row" text="前置→备线入口 不通" tone="red" />
            )}
            {isFar && (
              <span className="text-amber-600 dark:text-amber-400">
                备线入口离前置较远，切到备线时多绕这一段
              </span>
            )}
          </div>
        );
      })}
    </div>
  );
}

export function BackupTunnelPicker({
  data,
  value,
  onChange,
}: {
  data: LeiKwanLineList | null;
  value: string;
  onChange: (v: string) => void;
}) {
  const tunnels = useMemo(() => data?.backupTunnels || [], [data]);
  const pills = useMemo(() => groupPills(tunnels), [tunnels]);
  const [active, setActive] = useState("");
  const selected = tunnels.find((t) => String(t.id) === value);

  useEffect(() => {
    // Follow the selected tunnel: keep the open 线路分组 if it lists it,
    // else open its (first) group; nothing selected → keep / first pill.
    setActive((cur) => {
      const has = (key: string) =>
        pills.some(
          (p) => p.key === key && p.tunnels.some((t) => String(t.id) === value),
        );

      if (value && has(cur)) return cur;
      const owner = pills.find((p) =>
        p.tunnels.some((t) => String(t.id) === value),
      );

      if (owner) return owner.key;

      return pills.some((p) => p.key === cur) ? cur : pills[0]?.key || "";
    });
  }, [pills, value]);

  const pill = pills.find((p) => p.key === active) || pills[0];
  const sections = entrySections(pill?.tunnels || []);
  // Keys are 「pill:tunnel」 so a tunnel in two groups stays selectable.
  const keyOf = (t: LeiKwanBackupTunnel) => `${pill?.key}:${t.id}`;
  const selectedKeys = value
    ? pill?.tunnels.some((t) => String(t.id) === value)
      ? [`${pill.key}:${value}`]
      : []
    : [NONE];

  return (
    <div className="space-y-2" data-leikwan-backup-picker="">
      {pills.length > 1 && (
        <div>
          <div className="mb-1.5 text-small font-medium text-foreground">
            线路分组
          </div>
          <div
            aria-label="线路分组"
            className="flex flex-wrap gap-2"
            role="radiogroup"
          >
            {pills.map((p) => {
              const on = p.key === pill?.key;

              return (
                <button
                  key={p.key}
                  aria-checked={on}
                  className={`inline-flex h-8 items-center gap-1.5 rounded-full border px-3 text-sm transition-colors ${
                    on
                      ? "border-primary-300 bg-primary-500/10 font-medium text-primary-600 dark:border-primary-500/40 dark:text-primary-400"
                      : "border-default-200 bg-transparent text-default-600 hover:bg-default-100 dark:border-default-100"
                  }`}
                  role="radio"
                  type="button"
                  onClick={() => setActive(p.key)}
                >
                  {p.color && (
                    <span
                      className="h-2 w-2 shrink-0 rounded-full"
                      style={{ backgroundColor: p.color }}
                    />
                  )}
                  <span>{p.label}</span>
                  <span
                    className={on ? "text-primary-500/80" : "text-default-400"}
                  >
                    · {p.tunnels.length}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      )}
      <Select
        description={
          selected && !pill?.tunnels.some((t) => t.id === selected.id)
            ? `当前：${selected.name}（在别的线路分组里）`
            : "任意隧道都可以；备线出口 = 规则的落地"
        }
        label="备线隧道"
        listboxVariant="grouped"
        placeholder="不建备线"
        selectedKeys={selectedKeys}
        variant="bordered"
        onSelectionChange={(keys) => {
          const k = String(Array.from(keys)[0] || "");

          if (!k) return;
          onChange(k === NONE ? "" : k.split(":")[1] || "");
        }}
      >
        {[
          ...sections.map(([entry, list]) => (
            <SelectSection key={entry} title={`入口 ${entry}`}>
              {list.map((t) => (
                <SelectItem
                  key={keyOf(t)}
                  endContent={
                    <span className="rounded bg-default-100 px-1.5 py-0.5 text-xs font-normal text-default-500">
                      {typeLabel(t)}
                    </span>
                  }
                  textValue={t.name}
                >
                  {t.name}
                </SelectItem>
              ))}
            </SelectSection>
          )),
          <SelectSection key={NONE} title="不建">
            <SelectItem key={NONE} textValue="不建备线">
              不建备线
            </SelectItem>
          </SelectSection>,
        ]}
      </Select>
      <BackupPathLines data={data} tunnel={selected} />
      <p className="text-xs text-default-500">
        备线经主入口转发，用户地址不变。给用户开了备线时，面板在这条隧道上自动建一条到同一落地的备线（规则「備線·转发号」，归管理员）。
      </p>
    </div>
  );
}

/** 「按新备线重建」: auto backups not on the line's current tunnel (admin). */
export function BackupRebuildButton({
  line,
  onDone,
}: {
  line: LeiKwanLine;
  onDone: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const stale = line.backupStats?.stale || 0;
  const linked = line.backupStats?.linked || 0;

  if (stale <= 0) return null;

  const run = async () => {
    setBusy(true);
    const res = await rebuildLeiKwanLineBackups(line.id);

    setBusy(false);
    if (res.code !== 0) {
      toast.error(res.msg || "重建失败");

      return;
    }
    const r = res.data?.result;
    const parts = [
      r?.rebuilt ? `重建 ${r.rebuilt} 条` : "",
      r?.created ? `新建 ${r.created} 条` : "",
      r?.removed ? `删除 ${r.removed} 条` : "",
    ].filter(Boolean);

    toast.success(
      parts.length ? `备线已更新：${parts.join("，")}` : "没有要改的",
    );
    (res.data?.warnings || []).forEach((w) => toast.error(w));
    setOpen(false);
    onDone();
  };

  return (
    <>
      <Button
        color="warning"
        size="sm"
        variant="flat"
        onPress={() => setOpen(true)}
      >
        按新备线重建（{stale}）
      </Button>
      <Modal
        classNames={{ base: "!w-[calc(100%-24px)] sm:max-w-md rounded-2xl" }}
        isOpen={open}
        placement="center"
        onClose={() => setOpen(false)}
      >
        <ModalContent>
          <ModalHeader>按新备线重建 · {line.name}</ModalHeader>
          <ModalBody className="space-y-2 text-sm">
            <p>
              {line.backupTunnelId
                ? `把 ${stale} 条自动备线改建到「${line.backupTunnelName}」：先建新的，建好再删旧的；新的建不好就保留旧的。`
                : `这条专线不建备线了：删掉 ${stale} 条自动备线。`}
            </p>
            {linked > 0 && (
              <p className="text-xs text-default-500">
                导入时链接的 {linked} 条现有备线不动。
              </p>
            )}
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setOpen(false)}
            >
              取消
            </Button>
            <Button
              color="warning"
              isLoading={busy}
              variant="flat"
              onPress={() => void run()}
            >
              确认重建
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  );
}
