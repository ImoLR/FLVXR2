import type {
  LineMonitorChart,
  LineMonitorLineInput,
  LineMonitorNodeOption,
  LineMonitorPortHint,
} from "@/api/line-monitor";

import { useEffect, useMemo, useRef, useState } from "react";
import toast from "react-hot-toast";

import { lineColor } from "./format";

import { getLineMonitorPorts, saveLineMonitorChart } from "@/api/line-monitor";
import { landingToneClass } from "@/pages/landing/pill";
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

export const SSH_HINT =
  "请勿使用 SSH 端口（22），频繁连接可能被对方 fail2ban 封禁";

interface DraftLine {
  key: string;
  id?: number;
  color: number;
  sourceNodeId: number;
  targetType: "node" | "custom";
  targetNodeId: number;
  host: string;
  port: string;
  label: string;
  paused: boolean;
}

let draftSeq = 0;
const newKey = () => `n${++draftSeq}`;

interface Props {
  isOpen: boolean;
  chart: LineMonitorChart | null; // null = new
  nodes: LineMonitorNodeOption[];
  totalLines: number;
  maxPerChart: number;
  maxTotal: number;
  dark: boolean;
  onClose: () => void;
  onSaved: (id: number) => void;
}

export function ChartDialog({
  isOpen,
  chart,
  nodes,
  totalLines,
  maxPerChart,
  maxTotal,
  dark,
  onClose,
  onSaved,
}: Props) {
  const [name, setName] = useState("");
  const [lines, setLines] = useState<DraftLine[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [ports, setPorts] = useState<Record<number, LineMonitorPortHint[]>>({});
  const portsLoading = useRef(new Set<number>());

  const localNodes = useMemo(() => nodes.filter((n) => !n.remote), [nodes]);
  const nodeById = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);
  const ownLines = chart?.lines.length ?? 0;
  const othersTotal = totalLines - ownLines;

  useEffect(() => {
    if (!isOpen) return;
    setError("");
    if (chart) {
      setName(chart.name);
      setLines(
        chart.lines.map((l) => ({
          key: `e${l.id}`,
          id: l.id,
          color: l.color,
          sourceNodeId: l.sourceNodeId,
          targetType: l.targetNodeId > 0 ? "node" : "custom",
          targetNodeId: l.targetNodeId,
          host: l.host,
          port: String(l.port),
          label: l.label,
          paused: l.paused === 1,
        })),
      );
    } else {
      setName("");
      setLines([
        {
          key: newKey(),
          color: 0,
          sourceNodeId: 0,
          targetType: "node",
          targetNodeId: 0,
          host: "",
          port: "",
          label: "",
          paused: false,
        },
      ]);
    }
  }, [isOpen, chart]);

  // Port suggestions of every node target in the dialog (one request per node).
  useEffect(() => {
    if (!isOpen) return;
    for (const l of lines) {
      const id = l.targetType === "node" ? l.targetNodeId : 0;

      if (id <= 0 || ports[id] || portsLoading.current.has(id)) continue;
      portsLoading.current.add(id);
      void getLineMonitorPorts(id).then((res) => {
        portsLoading.current.delete(id);
        setPorts((p) => ({
          ...p,
          [id]: res.code === 0 && Array.isArray(res.data) ? res.data : [],
        }));
      });
    }
  }, [isOpen, lines, ports]);

  const update = (key: string, patch: Partial<DraftLine>) =>
    setLines((ls) => ls.map((l) => (l.key === key ? { ...l, ...patch } : l)));

  const nextColor = () => {
    const used = new Set(lines.map((l) => l.color));
    let c = 0;

    while (used.has(c)) c++;

    return c;
  };

  const canAdd =
    lines.length < maxPerChart && othersTotal + lines.length < maxTotal;

  const save = async () => {
    setError("");
    const payload: LineMonitorLineInput[] = lines.map((l) => ({
      id: l.id,
      sourceNodeId: l.sourceNodeId,
      targetType: l.targetType,
      targetNodeId: l.targetType === "node" ? l.targetNodeId : 0,
      host: l.host.trim(),
      port: Number(l.port) || 0,
      label: l.label.trim(),
      paused: l.paused,
    }));

    setSaving(true);
    const res = await saveLineMonitorChart({
      id: chart?.id,
      name: name.trim(),
      lines: payload,
    });

    setSaving(false);
    if (res.code !== 0 || !res.data) {
      setError(res.msg || "保存失败");

      return;
    }
    for (const w of res.data.warnings ?? [])
      toast(w, { icon: "⚠️", duration: 6000 });
    toast.success(
      chart ? "监测图已保存" : "监测图已创建，约 30 秒后出现第一批数据",
    );
    onSaved(res.data.id);
  };

  return (
    <Modal
      isDismissable={false}
      isOpen={isOpen}
      placement="center"
      scrollBehavior="inside"
      size="2xl"
      onClose={onClose}
    >
      <ModalContent>
        <>
          <ModalHeader className="flex flex-col gap-1">
            <h2 className="text-lg font-bold">
              {chart ? "编辑监测图" : "新建监测图"}
            </h2>
            <p className="text-xs font-normal text-default-500">
              每条线路每 30 秒从源节点 TCP
              连接目标一次，所有线路画在同一张图里对比。最多 {maxPerChart} 条 /
              图，全部监测图合计 {maxTotal} 条（已用{" "}
              {othersTotal + lines.length}）。
            </p>
          </ModalHeader>
          <ModalBody className="space-y-3">
            <Input
              data-testid="lm-name"
              label="名称"
              placeholder="例如：AWS HK → Seednet"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            {lines.map((l, i) => {
              const target =
                l.targetType === "node"
                  ? nodeById.get(l.targetNodeId)
                  : undefined;
              const hints =
                l.targetType === "node" && l.targetNodeId > 0
                  ? (ports[l.targetNodeId] ?? null)
                  : null;
              const src = nodeById.get(l.sourceNodeId);

              return (
                <div
                  key={l.key}
                  className="space-y-2 rounded-lg border border-divider p-3"
                  data-testid="lm-line-form"
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2 text-sm font-semibold">
                      <span
                        className="h-2.5 w-2.5 rounded-full"
                        style={{ background: lineColor(l.color, dark) }}
                      />
                      线路 {i + 1}
                    </div>
                    <div className="flex items-center gap-2">
                      {l.id && (
                        <div className="flex items-center gap-1.5 text-xs text-default-600">
                          <Switch
                            aria-label="暂停"
                            isSelected={l.paused}
                            size="sm"
                            onValueChange={(v) => update(l.key, { paused: v })}
                          />
                          暂停
                        </div>
                      )}
                      {lines.length > 1 && (
                        <Button
                          color="danger"
                          size="sm"
                          variant="flat"
                          onPress={() =>
                            setLines((ls) => ls.filter((x) => x.key !== l.key))
                          }
                        >
                          移除
                        </Button>
                      )}
                    </div>
                  </div>
                  <div className="grid gap-2 sm:grid-cols-2">
                    <Select
                      isSearchable
                      label="源节点（由它发起 TCP 连接）"
                      placeholder="选择源节点"
                      searchPlaceholder="搜索节点"
                      selectedKeys={
                        l.sourceNodeId ? [String(l.sourceNodeId)] : []
                      }
                      onSelectionChange={(keys) =>
                        update(l.key, {
                          sourceNodeId: Number(Array.from(keys)[0] || 0),
                        })
                      }
                    >
                      {localNodes.map((n) => (
                        <SelectItem key={String(n.id)} textValue={n.name}>
                          {n.name}
                          {n.online ? "" : "（离线）"}
                        </SelectItem>
                      ))}
                    </Select>
                    <div className="space-y-1">
                      <span className="text-sm font-medium">目标</span>
                      <div className="flex gap-1.5">
                        <Button
                          color={
                            l.targetType === "node" ? "primary" : "default"
                          }
                          size="sm"
                          variant={l.targetType === "node" ? "solid" : "flat"}
                          onPress={() => update(l.key, { targetType: "node" })}
                        >
                          FLVX 节点
                        </Button>
                        <Button
                          color={
                            l.targetType === "custom" ? "primary" : "default"
                          }
                          data-testid="lm-target-custom"
                          size="sm"
                          variant={l.targetType === "custom" ? "solid" : "flat"}
                          onPress={() =>
                            update(l.key, {
                              targetType: "custom",
                              targetNodeId: 0,
                            })
                          }
                        >
                          自定义地址
                        </Button>
                      </div>
                    </div>
                  </div>
                  {l.targetType === "node" ? (
                    <div className="grid gap-2 sm:grid-cols-2">
                      <Select
                        isSearchable
                        label="目标节点"
                        placeholder="选择目标节点"
                        searchPlaceholder="搜索节点"
                        selectedKeys={
                          l.targetNodeId ? [String(l.targetNodeId)] : []
                        }
                        onSelectionChange={(keys) =>
                          update(l.key, {
                            targetNodeId: Number(Array.from(keys)[0] || 0),
                          })
                        }
                      >
                        {nodes
                          .filter((n) => n.id !== l.sourceNodeId)
                          .map((n) => (
                            <SelectItem key={String(n.id)} textValue={n.name}>
                              {n.name}
                            </SelectItem>
                          ))}
                      </Select>
                      <Input
                        description="留空 = 跟随该节点的地址（节点改地址后自动跟着变）"
                        label="目标地址（可改）"
                        placeholder={
                          target
                            ? `跟随节点地址：${target.address}`
                            : "先选择目标节点"
                        }
                        value={l.host}
                        onChange={(e) =>
                          update(l.key, { host: e.target.value })
                        }
                      />
                    </div>
                  ) : (
                    <Input
                      description="不是 FLVX 节点的机器，例如一台 HiNet 家宽。只填 IP 或域名，不含端口。"
                      label="目标地址"
                      placeholder="例如 203.0.113.10 或 hinet.example.com"
                      value={l.host}
                      onChange={(e) => update(l.key, { host: e.target.value })}
                    />
                  )}
                  <div className="grid gap-2 sm:grid-cols-2">
                    <div className="space-y-1">
                      <Input
                        inputMode="numeric"
                        label="端口"
                        placeholder={
                          l.targetType === "node"
                            ? "选下方正在监听的端口，或自己填写"
                            : "对方正在监听的端口"
                        }
                        value={l.port}
                        onChange={(e) =>
                          update(l.key, {
                            port: e.target.value
                              .replace(/[^0-9]/g, "")
                              .slice(0, 5),
                          })
                        }
                      />
                      {l.port.trim() === "22" && (
                        <p
                          className="text-xs font-medium text-amber-600 dark:text-amber-300"
                          data-testid="lm-ssh-hint"
                        >
                          ⚠ {SSH_HINT}
                        </p>
                      )}
                    </div>
                    <Input
                      label="备注（可选）"
                      placeholder="图例里显示，例如：黑五"
                      value={l.label}
                      onChange={(e) => update(l.key, { label: e.target.value })}
                    />
                  </div>
                  {hints && (
                    <div
                      className="flex flex-wrap items-center gap-1.5"
                      data-testid="lm-port-hints"
                    >
                      <span className="text-xs text-default-500">
                        该节点正在监听：
                      </span>
                      {hints.length === 0 && (
                        <span className="text-xs text-default-400">
                          面板里没有该节点的监听端口，请手动填写
                        </span>
                      )}
                      {hints.map((p) => (
                        <button
                          key={p.port}
                          className={`rounded-full px-2 text-xs font-semibold leading-[20px] ${landingToneClass[p.kind === "tunnel" ? "teal" : p.kind === "landing" ? "cyan" : "purple"]} ${String(p.port) === l.port ? "ring-2 ring-primary/60" : ""}`}
                          title={p.label}
                          type="button"
                          onClick={() =>
                            update(l.key, { port: String(p.port) })
                          }
                        >
                          {p.port} · {p.label}
                        </button>
                      ))}
                    </div>
                  )}
                  {src && !src.online && (
                    <p className="text-xs text-default-500">
                      源节点当前离线：离线期间显示为「无数据」，不算断流。
                    </p>
                  )}
                </div>
              );
            })}
            <div className="flex flex-wrap items-center gap-2">
              <Button
                color="success"
                data-testid="lm-add-line"
                isDisabled={!canAdd}
                size="sm"
                variant="flat"
                onPress={() =>
                  setLines((ls) => [
                    ...ls,
                    {
                      key: newKey(),
                      color: nextColor(),
                      sourceNodeId: ls[ls.length - 1]?.sourceNodeId ?? 0,
                      targetType: "node",
                      targetNodeId: 0,
                      host: "",
                      port: "",
                      label: "",
                      paused: false,
                    },
                  ])
                }
              >
                + 添加线路
              </Button>
              {!canAdd && (
                <span className="text-xs text-default-500">
                  {lines.length >= maxPerChart
                    ? `每个监测图最多 ${maxPerChart} 条线路`
                    : `全部监测图合计最多 ${maxTotal} 条线路`}
                </span>
              )}
            </div>
            {error && (
              <p
                className="rounded-md bg-danger-50 px-3 py-2 text-sm text-danger dark:bg-danger-900/20"
                data-testid="lm-save-error"
              >
                {error}
              </p>
            )}
          </ModalBody>
          <ModalFooter>
            <Button color="danger" variant="flat" onPress={onClose}>
              取消
            </Button>
            <Button
              color="primary"
              data-testid="lm-save"
              isLoading={saving}
              onPress={save}
            >
              保存
            </Button>
          </ModalFooter>
        </>
      </ModalContent>
    </Modal>
  );
}
