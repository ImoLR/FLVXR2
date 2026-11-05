import type {
  TrafficRankItem,
  TrafficRankingView,
  TrafficWindow,
  TrafficWindowKey,
  UserTrafficSplitView,
} from "@/api/traffic-stats";

import { useCallback, useState } from "react";

import { getTrafficRanking, getUserTrafficSplit } from "@/api/traffic-stats";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Pill } from "@/pages/landing/pill";
import {
  Meter,
  RolePill,
  formatBytes,
  shortDay,
  useVisiblePoll,
} from "@/pages/dashboard/components/traffic-shared";

// Plan 212: admin 「流量排行」 — 今日 / 本周 (近 7 天) / 本月 × 用户 / 服务器 from
// the daily rollup (one request for all six lists); a user row opens that
// user's per-server split (one more request).

const POLL_MS = 60000;
const TOP = 10;
const WINDOW_LABEL: Record<TrafficWindowKey, string> = {
  today: "今日",
  week: "本周",
  month: "本月",
};

type View = "users" | "servers";

const total = (it: { in: number; out: number }) => (it.in || 0) + (it.out || 0);

function sinceText(ms: number) {
  const d = new Date(ms + 8 * 3600 * 1000); // UTC+8, like the day rows
  const p = (n: number) => String(n).padStart(2, "0");

  return `${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())}`;
}

function Segmented<K extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { key: K; text: string }[];
  value: K;
  onChange: (key: K) => void;
}) {
  return (
    <div
      aria-label={label}
      className="inline-flex rounded-full bg-default-100 p-0.5 dark:bg-default-50"
      role="tablist"
    >
      {options.map((o) => (
        <button
          key={o.key}
          aria-selected={o.key === value}
          className={`rounded-full px-3 py-1 text-xs font-semibold transition-colors ${
            o.key === value
              ? "bg-teal-500/15 text-teal-700 dark:bg-teal-400/20 dark:text-teal-300"
              : "text-default-500 hover:text-foreground"
          }`}
          role="tab"
          type="button"
          onClick={() => onChange(o.key)}
        >
          {o.text}
        </button>
      ))}
    </div>
  );
}

function RankRow({
  rank,
  item,
  sum,
  onOpen,
}: {
  rank: number;
  item: TrafficRankItem;
  sum: number;
  onOpen?: () => void;
}) {
  const value = total(item);
  const share = sum > 0 ? (value / sum) * 100 : 0;
  const isUser = item.userId !== undefined;
  const title = isUser ? item.user || `#${item.userId}` : item.name;
  const subtitle = isUser ? item.name : "";
  const pills = !isUser && (item.roles || []).length > 0 && (
    <span className="flex shrink-0 items-center gap-1">
      {(item.roles || []).map((role) => (
        <RolePill key={role} kind={role} />
      ))}
    </span>
  );
  const amount = (
    <span className="inline-block shrink-0 text-sm font-semibold tabular-nums text-foreground sm:w-28 sm:text-right">
      {formatBytes(value)}
    </span>
  );
  // Desktop: one line (name | bar | amount | share). H5: name + amount, the
  // role pills, then bar + share.
  const content = (
    <>
      <span
        className={`w-6 shrink-0 self-start text-right text-sm leading-5 tabular-nums sm:self-center ${rank <= 3 ? "font-semibold text-foreground" : "text-default-400"}`}
      >
        {rank}
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-1.5 sm:flex-row sm:items-center sm:gap-4">
        <div className="flex min-w-0 items-center justify-between gap-2 sm:w-72 sm:shrink-0 sm:justify-start">
          <div className="flex min-w-0 items-center gap-1.5">
            <span
              className={`min-w-0 truncate text-sm font-medium leading-5 ${item.deleted ? "text-default-400 line-through" : "text-foreground"}`}
              title={title}
            >
              {item.deleted && !title ? `已删除 #${item.serverRefId}` : title}
            </span>
            {subtitle && (
              <span className="min-w-0 truncate text-xs text-default-400">
                {subtitle}
              </span>
            )}
            {pills && <span className="hidden sm:flex">{pills}</span>}
          </div>
          <span className="sm:hidden">{amount}</span>
        </div>
        {pills && <div className="flex sm:hidden">{pills}</div>}
        <div className="flex min-w-0 items-center gap-3 sm:flex-1">
          <div className="min-w-0 flex-1">
            <Meter label={`${title} 占 ${share.toFixed(1)}%`} percent={share} />
          </div>
          <span className="hidden shrink-0 sm:block">{amount}</span>
          <span className="w-12 shrink-0 text-right text-xs tabular-nums text-default-500">
            {share.toFixed(1)}%
          </span>
        </div>
      </div>
    </>
  );

  if (!onOpen) {
    return <li className="flex items-center gap-3 px-2 py-2">{content}</li>;
  }

  return (
    <li>
      <button
        className="flex w-full items-center gap-3 rounded-lg px-2 py-2 text-left transition-colors hover:bg-default-100 focus-visible:outline-2 focus-visible:outline-teal-500 dark:hover:bg-default-50"
        title="查看该用户各服务器的流量"
        type="button"
        onClick={onOpen}
      >
        {content}
      </button>
    </li>
  );
}

function SplitModal({
  open,
  win,
  split,
  loading,
  onClose,
}: {
  open: boolean;
  win: TrafficWindow | undefined;
  split: UserTrafficSplitView | null;
  loading: boolean;
  onClose: () => void;
}) {
  const max = split ? Math.max(1, ...split.servers.map(total)) : 1;

  return (
    <Modal isOpen={open} scrollBehavior="inside" size="lg" onClose={onClose}>
      <ModalContent>
        <ModalHeader className="flex flex-col gap-1">
          <span>
            {split ? split.user : "用户"} · 各服务器流量
            {win && (
              <span className="ml-2 text-sm font-normal text-default-500">
                {WINDOW_LABEL[win.key]} {shortDay(win.from)}
                {win.from !== win.to && ` 至 ${shortDay(win.to)}`}
              </span>
            )}
          </span>
          {split && (
            <span className="text-sm font-normal text-default-500">
              合计{" "}
              <span className="font-semibold text-foreground">
                {formatBytes(total(split))}
              </span>
              （上传 {formatBytes(split.in)} · 下载 {formatBytes(split.out)}）
            </span>
          )}
        </ModalHeader>
        <ModalBody>
          {loading && !split ? (
            <p className="py-6 text-center text-sm text-default-500">
              正在加载…
            </p>
          ) : !split || split.servers.length === 0 ? (
            <p className="py-6 text-center text-sm text-default-500">
              这段时间没有按服务器记录的流量
            </p>
          ) : (
            <ul data-traffic-split className="space-y-3">
              {split.servers.map((s) => (
                <li
                  key={`${s.serverKind}:${s.serverRefId}`}
                  className="space-y-1.5"
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex min-w-0 items-center gap-1.5">
                      <span
                        className={`min-w-0 truncate text-sm font-medium ${s.deleted ? "text-default-400 line-through" : "text-foreground"}`}
                      >
                        {s.name || `已删除 #${s.serverRefId}`}
                      </span>
                      {(s.roles || []).map((role) => (
                        <RolePill key={role} kind={role} />
                      ))}
                    </div>
                    <span className="shrink-0 text-sm font-semibold tabular-nums text-foreground">
                      {formatBytes(total(s))}
                    </span>
                  </div>
                  <Meter
                    label={`${s.name} ${formatBytes(total(s))}`}
                    percent={(total(s) / max) * 100}
                  />
                </li>
              ))}
            </ul>
          )}
          <p className="text-xs text-default-500">
            只列入口和落地服务器。经入口到落地的流量在两台上各算一次，所以各服务器相加可能大于合计。
          </p>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" size="sm" variant="flat" onPress={onClose}>
            关闭
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}

export default function TrafficRankingCard() {
  const [data, setData] = useState<TrafficRankingView | null>(null);
  const [failed, setFailed] = useState(false);
  const [windowKey, setWindowKey] = useState<TrafficWindowKey>("today");
  const [view, setView] = useState<View>("users");
  const [expanded, setExpanded] = useState(false);
  const [splitOpen, setSplitOpen] = useState(false);
  const [split, setSplit] = useState<UserTrafficSplitView | null>(null);
  const [splitLoading, setSplitLoading] = useState(false);

  const load = useCallback(() => {
    getTrafficRanking()
      .then((res) => {
        if (res.code === 0 && res.data) {
          setData(res.data);
          setFailed(false);
        } else {
          setFailed(true);
        }
      })
      .catch(() => setFailed(true));
  }, []);

  useVisiblePoll(load, POLL_MS);

  const win = data?.windows.find((w) => w.key === windowKey);
  const items = (view === "users" ? win?.users : win?.servers) || [];
  const sum = items.reduce((acc, it) => acc + total(it), 0);
  const shown = expanded ? items : items.slice(0, TOP);

  const openUser = (userId: number) => {
    setSplit(null);
    setSplitLoading(true);
    setSplitOpen(true);
    getUserTrafficSplit(userId, windowKey)
      .then((res) => {
        if (res.code === 0 && res.data) setSplit(res.data);
      })
      .catch(() => undefined)
      .finally(() => setSplitLoading(false));
  };

  if (!data && !failed) return null;

  return (
    <Card
      data-traffic-ranking-card
      className="mb-6 lg:mb-8 border border-gray-200 dark:border-default-200 shadow-md"
    >
      <CardHeader className="pb-2">
        <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex min-w-0 flex-col gap-1">
            <div className="flex items-center gap-2">
              <svg
                aria-hidden="true"
                className="h-5 w-5 text-teal-600 dark:text-teal-400"
                fill="none"
                stroke="currentColor"
                strokeLinecap="round"
                strokeWidth={2}
                viewBox="0 0 24 24"
              >
                <path d="M4 6h10M4 12h16M4 18h7" />
              </svg>
              <h2 className="text-lg lg:text-xl font-semibold text-foreground">
                流量排行
              </h2>
            </div>
            <p className="text-xs text-default-500">
              计费流量，与「已用流量」同口径；按 UTC+8 自然日统计。
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Segmented
              label="统计范围"
              options={[
                { key: "today", text: "今日" },
                { key: "week", text: "本周" },
                { key: "month", text: "本月" },
              ]}
              value={windowKey}
              onChange={(k) => {
                setWindowKey(k);
                setExpanded(false);
              }}
            />
            <Segmented
              label="排行对象"
              options={[
                { key: "users", text: "用户" },
                { key: "servers", text: "服务器" },
              ]}
              value={view}
              onChange={(k) => {
                setView(k);
                setExpanded(false);
              }}
            />
          </div>
        </div>
      </CardHeader>
      <CardBody className="pt-0">
        {failed && !data ? (
          <p className="py-6 text-center text-sm text-default-500">
            排行加载失败，稍后自动重试
          </p>
        ) : (
          <>
            <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-default-500">
              {win && (
                <span>
                  {shortDay(win.from)}
                  {win.from !== win.to && ` 至 ${shortDay(win.to)}`}
                  {win.key === "week" && "（近 7 天）"}
                </span>
              )}
              <span>
                合计{" "}
                <span className="font-semibold text-foreground">
                  {formatBytes(sum)}
                </span>
              </span>
              <span>
                {items.length}{" "}
                {view === "users" ? "个活跃用户（不含管理员）" : "台服务器"}
              </span>
              {win?.partial && data && data.since > 0 && (
                <Pill
                  title="这项统计从新版本上线时开始，之前的流量没有按天记录"
                  tone="amber"
                >
                  统计从 {sinceText(data.since)} 开始
                </Pill>
              )}
            </div>
            {view === "servers" && items.length > 0 && (
              <p className="mb-1 text-xs text-default-400">
                只列入口和落地服务器，含全部用户（包括管理员）的流量；经入口到落地的流量在两台上各算一次，所以服务器相加可能大于用户合计。
              </p>
            )}
            {items.length === 0 ? (
              <p className="py-6 text-center text-sm text-default-500">
                {WINDOW_LABEL[windowKey]}还没有流量
              </p>
            ) : (
              <ul className="-mx-2" data-traffic-ranking-list={view}>
                {shown.map((it, i) => (
                  <RankRow
                    key={
                      view === "users"
                        ? `u${it.userId}`
                        : `${it.serverKind}:${it.serverRefId}`
                    }
                    item={it}
                    rank={i + 1}
                    sum={sum}
                    onOpen={
                      view === "users" && it.userId
                        ? () => openUser(it.userId as number)
                        : undefined
                    }
                  />
                ))}
              </ul>
            )}
            {items.length > TOP && (
              <div className="mt-2 flex justify-center">
                <Button
                  color="secondary"
                  size="sm"
                  variant="flat"
                  onPress={() => setExpanded((v) => !v)}
                >
                  {expanded ? "收起" : `展开全部（${items.length}）`}
                </Button>
              </div>
            )}
          </>
        )}
      </CardBody>
      <SplitModal
        loading={splitLoading}
        open={splitOpen}
        split={split}
        win={win}
        onClose={() => setSplitOpen(false)}
      />
    </Card>
  );
}
