import type {
  LineMonitorChart,
  LineMonitorLine,
  LineMonitorList,
  LineMonitorNodeOption,
} from "@/api/line-monitor";

import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import toast from "react-hot-toast";
import { Activity, ArrowLeft } from "lucide-react";

import { ChartCard } from "./node-monitor/chart-card";
import { ChartDialog } from "./node-monitor/chart-dialog";
import { CheckCommandModal, windowText } from "./node-monitor/check-port";

import {
  deleteLineMonitorChart,
  getLineMonitorList,
  getLineMonitorOptions,
  pauseLineMonitorLine,
  reorderLineMonitorCharts,
  rotateLineCheckPort,
  verifyLineCheckPort,
} from "@/api/line-monitor";
import { AnimatedPage } from "@/components/animated-page";
import { useMobileBreakpoint } from "@/hooks/useMobileBreakpoint";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody } from "@/shadcn-bridge/heroui/card";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { useThemeContext } from "@/themes/context";
import { isAdmin } from "@/utils/auth";

// Plan 255 「线路监测」: admin-built comparison charts (source node → target TCP
// latency, 丢包, 断流) — reached from the 节点 page toolbar only.
export default function NodeMonitorPage() {
  const admin = isAdmin();
  const navigate = useNavigate();
  const { effectiveMode } = useThemeContext();
  const dark = effectiveMode === "dark";
  const isMobile = useMobileBreakpoint();
  const [list, setList] = useState<LineMonitorList | null>(null);
  const [nodes, setNodes] = useState<LineMonitorNodeOption[]>([]);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<{
    open: boolean;
    chart: LineMonitorChart | null;
  }>({ open: false, chart: null });
  const [deleting, setDeleting] = useState<LineMonitorChart | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  // plan 255 part 2 检测端口
  const [checkBusyId, setCheckBusyId] = useState<number | null>(null);
  const [commandLine, setCommandLine] = useState<LineMonitorLine | null>(null);
  const [pausing, setPausing] = useState<LineMonitorLine | null>(null);

  const reload = useCallback(async () => {
    const res = await getLineMonitorList();

    if (res.code === 0 && res.data) {
      setList(res.data);
      setError("");
    } else {
      setError(res.msg || "加载失败");
    }
  }, []);

  useEffect(() => {
    if (!admin) return;
    void reload();
    void getLineMonitorOptions().then((res) => {
      if (res.code === 0 && res.data) setNodes(res.data.nodes ?? []);
    });
  }, [admin, reload]);

  // Line live states / source online flags refresh with the list (60 s, visible tab only).
  useEffect(() => {
    if (!admin) return;
    const timer = window.setInterval(() => {
      if (!document.hidden) void reload();
    }, 60_000);

    return () => window.clearInterval(timer);
  }, [admin, reload]);

  const openNew = () => setEditing({ open: true, chart: null });
  const onEdit = useCallback(
    (chart: LineMonitorChart) => setEditing({ open: true, chart }),
    [],
  );
  const onDelete = useCallback(
    (chart: LineMonitorChart) => setDeleting(chart),
    [],
  );

  const onMove = useCallback(
    async (chart: LineMonitorChart, dir: -1 | 1) => {
      if (!list) return;
      const ids = list.charts.map((c) => c.id);
      const i = ids.indexOf(chart.id);
      const j = i + dir;

      if (i < 0 || j < 0 || j >= ids.length) return;
      [ids[i], ids[j]] = [ids[j], ids[i]];
      setList({
        ...list,
        charts: ids.map((id) => list.charts.find((c) => c.id === id)!),
      });
      const res = await reorderLineMonitorCharts(ids);

      if (res.code !== 0) toast.error(res.msg || "排序失败");
      void reload();
    },
    [list, reload],
  );

  const doPause = useCallback(
    async (line: LineMonitorLine, paused: boolean) => {
      const res = await pauseLineMonitorLine(line.id, paused);

      if (res.code !== 0) {
        toast.error(res.msg || "操作失败");

        return;
      }
      toast.success(paused ? "已暂停该线路" : "已恢复该线路");
      void reload();
    },
    [reload],
  );

  // Pausing a line with a check port asks first: the port expires on its own.
  const onPause = useCallback(
    (line: LineMonitorLine, paused: boolean) => {
      const auto = line.portMode === "auto" && line.targetNodeId > 0;
      const custom = line.targetNodeId <= 0 && (line.checkWindow ?? 0) > 0;

      if (paused && (auto || custom)) {
        setPausing(line);

        return;
      }
      void doPause(line, paused);
    },
    [doPause],
  );

  const onCheckPort = useCallback(
    async (line: LineMonitorLine, action: "rotate" | "verify" | "command") => {
      if (action === "command") {
        setCommandLine(line);

        return;
      }
      setCheckBusyId(line.id);
      if (action === "verify") {
        const res = await verifyLineCheckPort(line.id);

        setCheckBusyId(null);
        if (res.code !== 0) toast.error(res.msg || "操作失败");
        else if (res.data?.checks?.[0]?.state === "ok")
          toast.success(`检测端口 :${res.data.checks[0].port} 已开启 ✓`);
        else toast(res.data?.checks?.[0]?.msg || "已重新发送", { icon: "⚠️" });
      } else {
        const res = await rotateLineCheckPort(line.id);

        setCheckBusyId(null);
        if (res.code !== 0 || !res.data) toast.error(res.msg || "换端口失败");
        else if (res.data.ok)
          toast.success(
            `已换到检测端口 :${res.data.tries[res.data.tries.length - 1]?.port}，从源节点验证通过 ✓`,
          );
        else
          toast(res.data.msg || "换端口没有成功", {
            icon: "⚠️",
            duration: 9000,
          });
      }
      void reload();
    },
    [reload],
  );

  const confirmDelete = async () => {
    if (!deleting) return;
    setDeleteBusy(true);
    const res = await deleteLineMonitorChart(deleting.id);

    setDeleteBusy(false);
    if (res.code !== 0) {
      toast.error(res.msg || "删除失败");

      return;
    }
    toast.success("监测图已删除");
    setDeleting(null);
    void reload();
  };

  if (!admin) {
    return (
      <AnimatedPage className="px-3 lg:px-6 py-8">
        <Card>
          <CardBody className="py-12 text-center text-sm text-default-500">
            权限不足，仅管理员可用
          </CardBody>
        </Card>
      </AnimatedPage>
    );
  }

  const charts = list?.charts ?? [];

  return (
    <AnimatedPage className="px-3 lg:px-6 py-8">
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl font-bold">线路监测</h1>
          <p className="mt-0.5 text-sm text-default-500">
            把想对比的线路画进同一张图：每 30 秒测一次 TCP
            延迟，看抖动、丢包和断流，数据保留 30 天。
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {list && (
            <span className="text-xs text-default-500" data-testid="lm-total">
              线路 {list.totalLines}/{list.maxTotal}
            </span>
          )}
          <Button size="sm" variant="flat" onPress={() => navigate("/node")}>
            <ArrowLeft className="h-4 w-4" />
            节点
          </Button>
          {charts.length > 0 && (
            <Button
              color="success"
              data-testid="lm-new"
              size="sm"
              variant="flat"
              onPress={openNew}
            >
              新建监测图
            </Button>
          )}
        </div>
      </div>

      {error && !list ? (
        <Card>
          <CardBody className="py-10 text-center text-sm text-danger">
            {error}
          </CardBody>
        </Card>
      ) : !list ? (
        <div className="h-72 animate-pulse rounded-xl bg-default-100 dark:bg-default-50/10" />
      ) : charts.length === 0 ? (
        <Card data-testid="lm-empty">
          <CardBody className="flex flex-col items-center gap-3 py-14 text-center">
            <span className="flex h-12 w-12 items-center justify-center rounded-full bg-cyan-500/15 text-cyan-700 dark:bg-cyan-400/15 dark:text-cyan-300">
              <Activity className="h-6 w-6" />
            </span>
            <div className="text-base font-semibold">还没有监测图</div>
            <p className="max-w-md text-sm text-default-500">
              选一个源节点和要对比的目标（FLVX 节点或任意 IP:端口），面板每 30
              秒测一次，几条线画在同一张图里。
            </p>
            <Button
              color="success"
              data-testid="lm-new"
              variant="flat"
              onPress={openNew}
            >
              新建监测图
            </Button>
          </CardBody>
        </Card>
      ) : (
        <div className="space-y-4">
          {charts.map((c, i) => (
            <ChartCard
              key={c.id}
              chart={c}
              checkBusyId={checkBusyId}
              dark={dark}
              index={i}
              isMobile={isMobile}
              total={charts.length}
              onCheckPort={onCheckPort}
              onDelete={onDelete}
              onEdit={onEdit}
              onMove={onMove}
              onPause={onPause}
            />
          ))}
        </div>
      )}

      <ChartDialog
        chart={editing.chart}
        dark={dark}
        isOpen={editing.open}
        maxPerChart={list?.maxPerChart ?? 8}
        maxTotal={list?.maxTotal ?? 40}
        nodes={nodes}
        totalLines={list?.totalLines ?? 0}
        onClose={() => setEditing({ open: false, chart: null })}
        onSaved={() => {
          setEditing({ open: false, chart: null });
          void reload();
        }}
      />

      <Modal
        isOpen={deleting != null}
        placement="center"
        size="md"
        onClose={() => setDeleting(null)}
      >
        <ModalContent>
          <>
            <ModalHeader>
              <h2 className="text-lg font-bold">删除监测图</h2>
            </ModalHeader>
            <ModalBody>
              <p>
                确定删除 <strong>「{deleting?.name}」</strong> 吗？
              </p>
              <p className="text-sm text-default-500">
                它的 {deleting?.lines.length ?? 0} 条线路和全部测量数据（最多 30
                天）会一起删除，不可恢复。
              </p>
            </ModalBody>
            <ModalFooter>
              <Button
                color="danger"
                variant="flat"
                onPress={() => setDeleting(null)}
              >
                取消
              </Button>
              <Button
                color="danger"
                data-testid="lm-delete-confirm"
                isLoading={deleteBusy}
                onPress={confirmDelete}
              >
                删除
              </Button>
            </ModalFooter>
          </>
        </ModalContent>
      </Modal>
      <CheckCommandModal
        line={commandLine}
        sourceIds={
          commandLine
            ? Array.from(
                new Set(
                  charts
                    .find((c) => c.id === commandLine.chartId)
                    ?.lines.filter(
                      (x) =>
                        x.targetNodeId <= 0 &&
                        x.effectiveHost.toLowerCase() ===
                          commandLine.effectiveHost.toLowerCase() &&
                        x.port === commandLine.port,
                    )
                    .map((x) => x.sourceNodeId) ?? [commandLine.sourceNodeId],
                ),
              )
            : []
        }
        onClose={() => setCommandLine(null)}
      />

      <Modal
        isOpen={pausing != null}
        placement="center"
        size="md"
        onClose={() => setPausing(null)}
      >
        <ModalContent>
          <>
            <ModalHeader>
              <h2 className="text-lg font-bold">暂停线路</h2>
            </ModalHeader>
            <ModalBody>
              {pausing && pausing.targetNodeId <= 0 ? (
                <p data-testid="lm-pause-warning">
                  暂停超过 {windowText(pausing.checkWindow || 24)}
                  ，对方检测端口会自动删除，恢复后需要在目标机器上重新执行开启命令。
                </p>
              ) : (
                <p data-testid="lm-pause-warning">
                  暂停超过 24
                  小时，目标节点会自动关闭检测端口；恢复时面板会自动重新开启。
                </p>
              )}
            </ModalBody>
            <ModalFooter>
              <Button
                color="danger"
                variant="flat"
                onPress={() => setPausing(null)}
              >
                取消
              </Button>
              <Button
                color="primary"
                data-testid="lm-pause-confirm"
                onPress={() => {
                  const l = pausing;

                  setPausing(null);
                  if (l) void doPause(l, true);
                }}
              >
                暂停
              </Button>
            </ModalFooter>
          </>
        </ModalContent>
      </Modal>
    </AnimatedPage>
  );
}
