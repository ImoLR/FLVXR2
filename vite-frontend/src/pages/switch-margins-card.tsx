import type {
  SwitchMarginKey,
  SwitchMarginRow,
  SwitchMarginsView,
} from "@/api/switch-margins";

import { useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";

import { getSwitchMargins, saveSwitchMargins } from "@/api/switch-margins";
import { Pill } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Divider } from "@/shadcn-bridge/heroui/divider";
import { Input } from "@/shadcn-bridge/heroui/input";

// Plan 234 网站配置 → 自动切换门槛: how much faster the new choice must be
// before an automatic switch (最优线路 / Reality 伪装域名 / Reality 目标 IP),
// with 「最近 7 天自动切换 N 次」 next to each so a flapping value shows.

const TITLES: Record<SwitchMarginKey, string> = {
  best: "最优线路",
  realityDomain: "Reality 伪装域名",
  realityIp: "Reality 目标 IP",
};

function explain(key: SwitchMarginKey, ms: number, rounds: number): string {
  const faster = ms > 0 ? `快 ${ms} ms 以上` : "更快（一样快不换）";

  switch (key) {
    case "best":
      return `「最优」隧道的出口 / 中转：新的要${faster}，并且连续 ${rounds} 轮（约 ${rounds} 分钟）都这样才换。「规则最优」按目标选线路也用这个数字。`;
    case "realityDomain":
      return `落地服务器的自动（最快）模式：测速后，新域名要${faster}才换。只影响以后新建的节点。`;
    case "realityIp":
      return `同一个伪装域名有多个 IP 时，落地机每 6 小时测一次，新 IP 要${faster}才换。`;
  }
}

function dayText(ms: number): string {
  const d = new Date(ms);

  return `${d.getMonth() + 1}-${String(d.getDate()).padStart(2, "0")}`;
}

function parseMs(raw: string, max: number): number | null {
  const t = raw.trim();

  if (!/^\d+$/.test(t)) return null;
  const n = Number(t);

  return n >= 0 && n <= max ? n : null;
}

export function SwitchMarginsCard() {
  const [view, setView] = useState<SwitchMarginsView | null>(null);
  const [error, setError] = useState("");
  const [values, setValues] = useState<Record<SwitchMarginKey, string>>({
    best: "",
    realityDomain: "",
    realityIp: "",
  });
  const [saving, setSaving] = useState(false);

  const applyView = (v: SwitchMarginsView) => {
    setView(v);
    const next = { best: "", realityDomain: "", realityIp: "" };

    for (const row of v.rows) next[row.key] = String(row.valueMs);
    setValues(next);
  };

  useEffect(() => {
    let disposed = false;

    void getSwitchMargins().then((res) => {
      if (disposed) return;
      if (res.code !== 0) {
        setError(res.msg || "读取失败");

        return;
      }
      applyView(res.data);
    });

    return () => {
      disposed = true;
    };
  }, []);

  const maxMs = view?.maxMs ?? 200;
  const parsed = useMemo(() => {
    const out: Partial<Record<SwitchMarginKey, number | null>> = {};

    for (const key of Object.keys(values) as SwitchMarginKey[]) {
      out[key] = parseMs(values[key], maxMs);
    }

    return out as Record<SwitchMarginKey, number | null>;
  }, [values, maxMs]);

  const valid = Object.values(parsed).every((v) => v !== null);
  const dirty =
    !!view && view.rows.some((row) => parsed[row.key] !== row.valueMs);

  const save = async () => {
    if (!view || !valid) return;
    setSaving(true);
    try {
      const res = await saveSwitchMargins({
        bestMs: parsed.best as number,
        realityDomainMs: parsed.realityDomain as number,
        realityIpMs: parsed.realityIp as number,
      });

      if (res.code !== 0) {
        toast.error(res.msg || "保存失败");

        return;
      }
      applyView(res.data);
      toast.success("自动切换门槛已保存，约 1 分钟内生效");
    } finally {
      setSaving(false);
    }
  };

  const undo = () => {
    if (view) applyView(view);
  };

  const ipNote = (row: SwitchMarginRow) => {
    const s = view?.ipSupport;
    const value = parsed.realityIp;

    if (!s || value === null || value === row.defaultMs) return null;
    const parts: string[] = [];

    if (s.nativeOld > 0) {
      parts.push(`${s.nativeOld} 台落地机还是旧版本，升级后才按新数字`);
    }
    if (s.sharedOld > 0) {
      parts.push(`${s.sharedOld} 台妙妙屋共享服务器要等妙妙屋升级`);
    }
    if (parts.length === 0) return null;

    return (
      <p className="text-xs text-amber-700 dark:text-amber-300">
        ⚠ {parts.join("；")}，在那之前仍按 {row.defaultMs} ms。
      </p>
    );
  };

  const renderRow = (row: SwitchMarginRow) => {
    const raw = values[row.key];
    const value = parsed[row.key];
    const invalid = raw.trim() !== "" && value === null;
    const c = row.counts;
    const busy = (c.busyCount ?? 0) > 0;
    const windowDays = view?.windowDays ?? 7;
    const countLabel = c.partial
      ? `${dayText(c.since)} 起自动切换 ${c.total} 次`
      : `最近 ${windowDays} 天自动切换 ${c.total} 次`;
    const unit = row.key === "best" ? "隧道" : "落地服务器";
    const busyWho = row.busyName
      ? `${unit}「${row.busyName}」`
      : `有一个${unit}`;

    return (
      <div
        key={row.key}
        className="space-y-2 rounded-xl border border-divider p-3"
        data-switch-margin={row.key}
      >
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0 text-sm font-semibold text-gray-700 dark:text-gray-200">
            {TITLES[row.key]}
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <Input
              aria-label={`${TITLES[row.key]}切换门槛（毫秒）`}
              className="w-16 text-right tabular-nums"
              inputMode="numeric"
              isInvalid={invalid}
              maxLength={3}
              size="sm"
              type="text"
              value={raw}
              onChange={(e) =>
                setValues((prev) => ({ ...prev, [row.key]: e.target.value }))
              }
            />
            <span className="text-xs text-default-500">ms</span>
          </div>
        </div>
        <p className="text-xs leading-5 text-default-500">
          {explain(row.key, value ?? row.valueMs, view?.confirmRounds ?? 2)}
        </p>
        {invalid && (
          <p className="text-xs text-danger">请输入 0–{maxMs} 的整数（毫秒）</p>
        )}
        <div className="flex flex-wrap items-center gap-1.5">
          <Pill
            title="面板自动切换的次数（不含手动修改）"
            tone={busy ? "amber" : "green"}
          >
            {countLabel}
          </Pill>
          {c.failover > 0 && (
            <Pill title="当前的连不上或不能用而换的，不受门槛影响" tone="slate">
              其中故障切换 {c.failover} 次
            </Pill>
          )}
          <span className="text-xs text-default-400">
            推荐 {row.defaultMs} ms
          </span>
          {value !== row.defaultMs && (
            <Button
              className="h-6 min-w-0 px-2 text-xs"
              color="secondary"
              size="sm"
              variant="flat"
              onPress={() =>
                setValues((prev) => ({
                  ...prev,
                  [row.key]: String(row.defaultMs),
                }))
              }
            >
              恢复推荐值
            </Button>
          )}
        </div>
        {busy && (
          <p className="text-xs text-amber-700 dark:text-amber-300">
            切换较频繁：{busyWho}
            {c.partial ? "" : ` ${windowDays} 天里`}因为更快换了 {c.busyCount}{" "}
            次，可以把这个数字调大一些。
          </p>
        )}
        {row.key === "realityIp" && ipNote(row)}
      </div>
    );
  };

  return (
    <Card data-switch-margins-card className="mt-6 shadow-md dark:bg-content1">
      <CardHeader className="pb-4">
        <div>
          <h2 className="text-lg font-semibold">自动切换门槛</h2>
          <p className="text-xs text-gray-500 dark:text-gray-400">
            自动选最快时，新的要比当前的快多少才换。数字大：更稳、少来回换；数字小：更贴近最快。0
            =
            只要更快就换。当前的连不上、被屏蔽或不能用时立刻换，不看这些数字。保存后约
            1 分钟内生效。
          </p>
        </div>
      </CardHeader>
      <Divider />
      <CardBody className="space-y-3 pt-4 md:pt-4">
        {error && <p className="text-sm text-danger">{error}</p>}
        {!view && !error && (
          <p className="text-sm text-default-400">正在读取…</p>
        )}
        {view?.rows.map(renderRow)}
        {view && (
          <div className="flex justify-end gap-2">
            {dirty && (
              <Button color="danger" size="sm" variant="flat" onPress={undo}>
                撤销修改
              </Button>
            )}
            <Button
              color="success"
              isDisabled={!dirty || !valid}
              isLoading={saving}
              size="sm"
              variant="flat"
              onPress={() => void save()}
            >
              保存门槛
            </Button>
          </div>
        )}
      </CardBody>
    </Card>
  );
}
