import type {
  LineMonitorCheckCommand,
  LineMonitorLine,
  LineMonitorTestResult,
} from "@/api/line-monitor";

import { useEffect, useState } from "react";
import toast from "react-hot-toast";
import { Check, Copy, X } from "lucide-react";

import { getLineCheckCommand, testLineCheckPort } from "@/api/line-monitor";
import { landingToneClass, type LandingTone } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Plan 255 part 2 「检测端口」: chips, the custom target 开启命令 and 测试连通.

/** 端口未开启 (grey-blue: neither 断流 red nor 无数据 grey). */
export const CLOSED_PILL =
  "bg-sky-600/15 text-sky-800 dark:bg-sky-300/15 dark:text-sky-200";

export const WINDOW_LABEL: Record<number, string> = {
  1: "1 小时",
  6: "6 小时",
  24: "24 小时（默认）",
  72: "3 天",
};
export const WINDOW_HINT =
  "目标机器若断网超过这个时间，检测端口会自己删除，恢复后需要重新执行命令";
export const LANDING_SCAN_HINT =
  "落地端口被频繁空连接可能被当成扫描，优先用「自动开启检测端口」";
export const OTA_HINT = "需升级节点 agent（OTA）后才可自动开启检测端口";

export const windowText = (h: number) => (h >= 72 ? "3 天" : `${h} 小时`);

export async function copyText(value: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);

      return true;
    }
    const el = document.createElement("textarea");

    el.value = value;
    el.style.position = "fixed";
    el.style.opacity = "0";
    document.body.appendChild(el);
    el.select();
    const ok = document.execCommand("copy");

    el.remove();

    return ok;
  } catch {
    return false;
  }
}

export interface CheckStatus {
  tone?: LandingTone;
  className?: string;
  text: string;
  title?: string;
  action?: "rotate" | "verify" | "command";
  actionLabel?: string;
  warn?: boolean;
}

/**
 * What the line row says about its check port. live = the latest slot status
 * (4 = 端口未开启) or undefined.
 */
export function checkPortStatus(
  l: LineMonitorLine,
  live?: number,
): CheckStatus | null {
  const auto = l.portMode === "auto" && l.targetNodeId > 0;
  const custom = l.targetNodeId <= 0;

  if (live === 4) {
    if (auto)
      return {
        warn: true,
        className: CLOSED_PILL,
        text: "⚠ 检测端口已关闭（可能已自动过期）",
        action: "verify",
        actionLabel: "重新开启",
      };
    if (custom)
      return {
        warn: true,
        className: CLOSED_PILL,
        text: "⚠ 检测端口已关闭（可能已自动过期）",
        action: "command",
        actionLabel: "复制开启命令",
      };

    return {
      warn: true,
      className: CLOSED_PILL,
      text: "⚠ 目标端口未开启（连接被拒绝）",
    };
  }
  if (!auto) return null;
  const p = `:${l.port}`;
  const src = l.sourceName || "源节点";

  switch (l.checkState) {
    case "ok":
      return {
        tone: "green",
        text: `检测端口 ${p} 已开启 ✓`,
        title: l.checkMsg,
      };
    case "unreachable":
      return {
        tone: "amber",
        text: `检测端口 ${p} 已开启，但从 ${src} 连不上（可能商家防火墙 / NAT 未放行该端口）`,
        title: l.checkMsg,
        action: "rotate",
        actionLabel: "换一个端口",
      };
    case "refused":
      return {
        tone: "amber",
        text: `检测端口 ${p}：从 ${src} 连接被拒绝（该地址上没有监听，可能 NAT 转发到了别的机器）`,
        action: "rotate",
        actionLabel: "换一个端口",
      };
    case "busy":
    case "failed":
      return {
        tone: "red",
        text: `检测端口 ${p}：${l.checkMsg || "目标节点没能开启"}`,
        action: "rotate",
        actionLabel: "换一个端口",
      };
    case "pending":
      return {
        tone: "slate",
        text: `检测端口 ${p} 待开启（目标节点离线，上线后自动开启）`,
      };
    case "unsupported":
      return { tone: "slate", text: OTA_HINT };
    case "unverified":
      return {
        tone: "slate",
        text: `检测端口 ${p} 已开启（${l.checkMsg || "源节点离线，未验证"}）`,
        action: "verify",
        actionLabel: "重新检测",
      };
    default:
      return {
        tone: "slate",
        text: `检测端口 ${p} 检测中…`,
        action: "verify",
        actionLabel: "重新检测",
      };
  }
}

function pillClass(s: CheckStatus) {
  return `${s.className ?? landingToneClass[s.tone ?? "slate"]} inline-flex max-w-full items-center rounded-full px-2 text-xs font-semibold leading-[18px] whitespace-normal break-words`;
}

/** The 检测端口 block under a chart's legend (one row per line with news). */
export function CheckPortRows({
  lines,
  colors,
  live,
  busyId,
  onAction,
}: {
  lines: LineMonitorLine[];
  colors: string[];
  live: (l: LineMonitorLine) => number | undefined;
  busyId: number | null;
  onAction: (
    l: LineMonitorLine,
    action: "rotate" | "verify" | "command",
  ) => void;
}) {
  const rows = lines
    .map((l, i) => ({ l, i, s: checkPortStatus(l, live(l)) }))
    .filter((r) => r.s);

  if (rows.length === 0) return null;

  return (
    <div className="space-y-1" data-testid="lm-check-rows">
      {rows.map(({ l, i, s }) => (
        <div key={l.id} className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span
            className="h-2 w-2 shrink-0 rounded-full"
            style={{ background: colors[i] }}
          />
          <span className="shrink-0 text-xs text-default-500">
            {l.label || l.targetName || l.effectiveHost}
          </span>
          <span
            className={pillClass(s!)}
            data-testid="lm-check-chip"
            title={s!.title}
          >
            {s!.text}
          </span>
          {s!.action && (
            <button
              className={`shrink-0 rounded-full px-2 text-xs font-semibold leading-[18px] ${landingToneClass[s!.action === "rotate" ? "purple" : "teal"]} disabled:opacity-60`}
              data-testid={`lm-check-${s!.action}`}
              disabled={busyId === l.id}
              type="button"
              onClick={() => onAction(l, s!.action!)}
            >
              {busyId === l.id ? "处理中…" : s!.actionLabel}
            </button>
          )}
        </div>
      ))}
    </div>
  );
}

function CodeBlock({
  label,
  value,
  testId,
}: {
  label: string;
  value: string;
  testId: string;
}) {
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-semibold text-default-600">{label}</span>
        <Button
          color="primary"
          data-testid={`${testId}-copy`}
          size="sm"
          startContent={<Copy className="h-3.5 w-3.5" />}
          variant="flat"
          onPress={async () => {
            if (await copyText(value)) toast.success("已复制");
            else toast.error("复制失败，请手动选择复制");
          }}
        >
          复制
        </Button>
      </div>
      <pre
        className="max-h-56 overflow-auto whitespace-pre rounded-md bg-default-100 p-2 font-mono text-[11px] leading-[15px] text-default-700 dark:bg-default-50/10 dark:text-default-300"
        data-testid={testId}
      >
        {value}
      </pre>
    </div>
  );
}

/** Per-source ✓ / ✗ of 测试连通. */
export function TestResults({ results }: { results: LineMonitorTestResult[] }) {
  const kindText: Record<string, string> = {
    refused: "连接被拒绝（端口没有监听）",
    timeout: "超时（被防火墙拦截或网络不通）",
    unreach: "网络不可达",
    dns: "域名解析失败",
    offline: "源节点离线",
    noanswer: "源节点未响应",
    failed: "连接失败",
  };

  return (
    <div className="space-y-1" data-testid="lm-test-results">
      {results.map((r) => (
        <div
          key={r.nodeId}
          className="flex flex-wrap items-center gap-1.5 text-xs"
        >
          <span
            className={`inline-flex h-4 w-4 items-center justify-center rounded-full ${r.ok ? "bg-green-500/20 text-green-700 dark:text-green-300" : "bg-red-500/20 text-red-700 dark:text-red-300"}`}
          >
            {r.ok ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
          </span>
          <span className="font-medium">{r.name || `节点 ${r.nodeId}`}</span>
          <span className="text-default-500">
            {r.ok
              ? `${(r.ms ?? 0).toFixed(1)} ms`
              : kindText[r.kind] || r.msg || "连接失败"}
          </span>
        </div>
      ))}
    </div>
  );
}

/**
 * The custom target 开启命令 panel: idle window, 仅允许来源节点连接, the
 * command + undo, 测试连通. Used in the line dialog and in the line row's
 * 「复制开启命令」.
 */
export function CheckCommandPanel({
  host,
  port,
  sourceIds,
  window: win,
  allow,
  onChange,
}: {
  host: string;
  port: number;
  sourceIds: number[];
  window: number;
  allow: boolean | null;
  onChange?: (patch: { window?: number; allow?: boolean }) => void;
}) {
  const [cmd, setCmd] = useState<LineMonitorCheckCommand | null>(null);
  const [error, setError] = useState("");
  const [testing, setTesting] = useState(false);
  const [results, setResults] = useState<LineMonitorTestResult[] | null>(null);
  const key = `${host}|${port}|${win}|${allow}|${sourceIds.join(",")}`;

  useEffect(() => {
    let live = true;

    setResults(null);
    if (!port) {
      setCmd(null);

      return;
    }
    void getLineCheckCommand({
      host,
      port,
      window: win,
      allow,
      sourceIds,
    }).then((res) => {
      if (!live) return;
      if (res.code !== 0 || !res.data) {
        setCmd(null);
        setError(res.msg || "生成命令失败");

        return;
      }
      setError("");
      setCmd(res.data);
    });

    return () => {
      live = false;
    };
  }, [key]);

  const test = async () => {
    setTesting(true);
    const res = await testLineCheckPort({ host, port, sourceIds });

    setTesting(false);
    if (res.code !== 0 || !res.data) {
      toast.error(res.msg || "测试失败");

      return;
    }
    setResults(res.data.results);
  };

  return (
    <div
      className="space-y-2 rounded-lg border border-dashed border-divider p-2.5"
      data-testid="lm-check-command"
    >
      <div className="text-sm font-semibold">检测端口（在目标机器上开启）</div>
      <p className="text-xs text-default-500">
        面板不能在别人的机器上开端口。用 root 登录目标机器（Debian /
        Ubuntu），粘贴下面的命令执行：只开一个只接受连接、立刻关闭的端口，不读写任何数据，没有连接超过设定时间会自己删除。
      </p>
      <div className="grid gap-2 sm:grid-cols-2">
        <Select
          label="多久没有连接就自动删除"
          selectedKeys={[String(win)]}
          size="sm"
          onSelectionChange={(keys) => {
            const v = Number(Array.from(keys)[0] || 24);

            onChange?.({ window: v });
          }}
        >
          {[1, 6, 24, 72].map((h) => (
            <SelectItem key={String(h)} textValue={WINDOW_LABEL[h]}>
              {WINDOW_LABEL[h]}
            </SelectItem>
          ))}
        </Select>
        <div className="space-y-1">
          <div className="flex items-center gap-2 text-sm">
            <Switch
              aria-label="仅允许来源节点连接"
              data-testid="lm-check-allow"
              isDisabled={!cmd?.allowAvailable}
              isSelected={cmd ? cmd.allow : false}
              size="sm"
              onValueChange={(v) => onChange?.({ allow: v })}
            />
            仅允许来源节点连接
          </div>
          {cmd && (
            <p className="text-xs text-default-500" data-testid="lm-allow-note">
              {cmd.allowNote}
            </p>
          )}
        </div>
      </div>
      <p className="text-xs text-default-500">{WINDOW_HINT}</p>
      {error && <p className="text-xs text-danger">{error}</p>}
      {cmd && (
        <>
          <CodeBlock
            label={`开启命令（端口 ${cmd.port}，${windowText(cmd.window)}没有连接自动删除）`}
            testId="lm-install"
            value={cmd.install}
          />
          <CodeBlock
            label="立即删除（不想等自动删除时）"
            testId="lm-undo"
            value={cmd.undo}
          />
        </>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          color="success"
          data-testid="lm-test"
          isDisabled={!host || !port || sourceIds.length === 0}
          isLoading={testing}
          size="sm"
          variant="flat"
          onPress={test}
        >
          测试连通
        </Button>
        <span className="text-xs text-default-500">
          执行命令后，从每个源节点各连一次
        </span>
      </div>
      {results && <TestResults results={results} />}
    </div>
  );
}

/** 「复制开启命令」 from a line row (custom target). */
export function CheckCommandModal({
  line,
  sourceIds,
  onClose,
}: {
  line: LineMonitorLine | null;
  sourceIds: number[];
  onClose: () => void;
}) {
  const [win, setWin] = useState(24);
  const [allow, setAllow] = useState<boolean | null>(null);

  useEffect(() => {
    if (!line) return;
    setWin(line.checkWindow || 24);
    setAllow(line.checkAllow ? true : null);
  }, [line]);

  return (
    <Modal
      isOpen={!!line}
      placement="center"
      scrollBehavior="inside"
      size="2xl"
      onClose={onClose}
    >
      <ModalContent>
        <ModalHeader className="flex flex-col gap-1">
          <h2 className="text-lg font-bold">复制开启命令</h2>
          <p className="text-xs font-normal text-default-500">
            {line?.effectiveHost}:{line?.port}
            的检测端口没有监听（连接被拒绝），可能已经自动删除。重新执行开启命令即可恢复。
          </p>
        </ModalHeader>
        <ModalBody>
          {line && (
            <CheckCommandPanel
              allow={allow}
              host={line.effectiveHost}
              port={line.port}
              sourceIds={sourceIds}
              window={win}
              onChange={(p) => {
                if (p.window) setWin(p.window);
                if (p.allow !== undefined) setAllow(p.allow);
              }}
            />
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            关闭
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
