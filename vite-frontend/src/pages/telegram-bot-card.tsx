import type { TgBotBindCode, TgBotMachine, TgBotView } from "@/api/tgbot";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import toast from "react-hot-toast";

import {
  clearTgBotToken,
  getTgBot,
  getTgBotStatus,
  newTgBotBindCode,
  saveTgBotSettings,
  saveTgBotToken,
  sendTgBotTest,
  unbindTgBotChat,
} from "@/api/tgbot";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Divider } from "@/shadcn-bridge/heroui/divider";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Plan 196 设置 → Telegram 通知: the panel's own bot reminds the bound admin
// chats before a machine's (rolling) due date, with 「✅ 已续费」 /
// 「🔕 不再提醒这台」 buttons. The token is write-only (only @username is
// shown after saving).

const PILL =
  "inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-[11px] font-semibold leading-4";
const SOURCE_CHIP =
  "inline-flex shrink-0 items-center rounded-full px-1.5 py-px align-middle text-[10px] font-semibold leading-4";

const CYCLE_LABEL: Record<string, string> = {
  month: "月付",
  quarter: "季付",
  halfyear: "半年付",
  year: "年付",
};

const KIND_LABEL: Record<string, string> = {
  private: "私聊",
  group: "群组",
  supergroup: "群组",
};

const twDate = (ms: number, withTime = false) => {
  if (!ms) return "";
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Taipei",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(new Date(ms));
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "";
  const nowYear = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Taipei",
    year: "numeric",
  }).format(new Date());
  const date =
    get("year") === nowYear
      ? `${get("month")}-${get("day")}`
      : `${get("year")}-${get("month")}-${get("day")}`;

  return withTime ? `${date} ${get("hour")}:${get("minute")}` : date;
};

const daysLabel = (d: number) =>
  d <= 0 ? "今天到期" : d === 1 ? "明天到期" : `剩 ${d} 天`;

const daysTone = (d: number) =>
  d <= 0
    ? "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300"
    : d <= 7
      ? "bg-amber-500/15 text-amber-700 dark:bg-amber-400/20 dark:text-amber-300"
      : "bg-slate-500/10 text-slate-600 dark:bg-slate-400/15 dark:text-slate-300";

const previewText = (m?: TgBotMachine) => {
  if (!m) return "⏰ Bestvm US 月付 明天到期（11-03）";
  const cycle = CYCLE_LABEL[m.cycle] ? ` ${CYCLE_LABEL[m.cycle]}` : "";
  const when =
    m.days <= 0 ? "今天到期" : m.days === 1 ? "明天到期" : `${m.days} 天后到期`;

  return `⏰ ${m.name}${cycle} ${when}（${twDate(m.nextDue)}）${m.source === "old" ? " · 救命版" : ""}`;
};

export function TelegramBotCard() {
  const [view, setView] = useState<TgBotView | null>(null);
  const [tokenInput, setTokenInput] = useState("");
  const [editingToken, setEditingToken] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const [busy, setBusy] = useState("");
  const [code, setCode] = useState<TgBotBindCode | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const [enabled, setEnabled] = useState(true);
  const [daysText, setDaysText] = useState("7,3,1,0");
  const [time, setTime] = useState("10:00");
  const [muted, setMuted] = useState<Set<string>>(new Set());
  const [initial, setInitial] = useState("");
  const [query, setQuery] = useState("");
  const chatCount = useRef(0);

  const snapshot = (e: boolean, d: string, t: string, m: Set<string>) =>
    JSON.stringify([e, d.replace(/\s+/g, ""), t, [...m].sort()]);

  const apply = useCallback((v: TgBotView) => {
    setView(v);
    chatCount.current = v.chats.length;
    const days = v.settings.days.join(",");
    const m = new Set(v.settings.muted);

    setEnabled(v.settings.enabled);
    setDaysText(days);
    setTime(v.settings.time);
    setMuted(m);
    setInitial(snapshot(v.settings.enabled, days, v.settings.time, m));
    if (v.bindCode && v.bindExpiresAt) {
      setCode((c) =>
        c && c.code === v.bindCode
          ? c
          : {
              code: v.bindCode!,
              expiresAt: v.bindExpiresAt!,
              command: `/start ${v.bindCode}`,
              link: v.profile.username
                ? `https://t.me/${v.profile.username}?start=${v.bindCode}`
                : undefined,
            },
      );
    }
  }, []);

  const reload = useCallback(async () => {
    const res = await getTgBot();

    if (res.code === 0 && res.data) apply(res.data);
  }, [apply]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // While a bind code waits: count down and watch for the new chat.
  useEffect(() => {
    if (!code) return;
    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    const poll = window.setInterval(() => {
      void getTgBotStatus().then((res) => {
        if (res.code !== 0 || !res.data) return;
        if (res.data.chats.length > chatCount.current) {
          const added = res.data.chats[res.data.chats.length - 1];

          toast.success(`已绑定：${added?.title || "新聊天"}`);
          setCode(null);
          void reload();
        } else if (!res.data.bindCode) {
          setCode(null);
        }
      });
    }, 3000);

    return () => {
      window.clearInterval(tick);
      window.clearInterval(poll);
    };
  }, [code, reload]);

  const run = async (
    name: string,
    fn: () => Promise<{ code: number; msg?: string; data?: any }>,
    ok?: (data: any) => void,
  ) => {
    setBusy(name);
    try {
      const res = await fn();

      if (res.code !== 0) {
        toast.error(res.msg || "操作失败");

        return;
      }
      ok?.(res.data);
    } catch {
      toast.error("网络错误，请重试");
    } finally {
      setBusy("");
    }
  };

  const saveToken = () =>
    run(
      "token",
      () => saveTgBotToken(tokenInput.trim()),
      (d: TgBotView) => {
        apply(d);
        setTokenInput("");
        setEditingToken(false);
        toast.success(`已连接 @${d.profile.username}`);
      },
    );
  const clearToken = () =>
    run("clear", clearTgBotToken, (d: TgBotView) => {
      apply(d);
      setCode(null);
      setConfirmClear(false);
      toast.success("已删除 bot token，绑定的聊天也已解绑");
    });
  const bind = () =>
    run("bind", newTgBotBindCode, (d: TgBotBindCode) => {
      setCode(d);
      setNow(Date.now());
    });
  const test = () =>
    run("test", sendTgBotTest, (d: { sent: number }) =>
      toast.success(`测试消息已发出（${d.sent} 个聊天）`),
    );
  const unbind = (chatId: number) =>
    run(
      `unbind-${chatId}`,
      () => unbindTgBotChat(chatId),
      () => {
        toast.success("已解绑");
        void reload();
      },
    );
  const saveSettings = () =>
    run(
      "settings",
      () =>
        saveTgBotSettings({ enabled, days: daysText, time, muted: [...muted] }),
      (d: TgBotView) => {
        apply(d);
        toast.success("提醒设置已保存");
      },
    );

  const dirty =
    view !== null && snapshot(enabled, daysText, time, muted) !== initial;
  const machines = view?.machines ?? [];
  const q = query.trim().toLowerCase();
  const visible = useMemo(
    () =>
      q
        ? machines.filter(
            (m) =>
              m.name.toLowerCase().includes(q) ||
              (m.source === "old" && "救命版".includes(q)),
          )
        : machines,
    [machines, q],
  );
  const remindCount = machines.filter((m) => !muted.has(m.key)).length;
  const tokenSet = view?.tokenSet ?? false;
  const chats = view?.chats ?? [];
  const maxChats = view?.maxChats ?? 5;
  const left = code ? Math.max(0, code.expiresAt - now) : 0;

  const status = !view
    ? null
    : !tokenSet
      ? {
          text: "未设置",
          cls: "bg-slate-500/10 text-slate-600 dark:bg-slate-400/15 dark:text-slate-300",
        }
      : view.lastError && view.running
        ? {
            text: "连接异常",
            cls: "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300",
          }
        : chats.length === 0
          ? {
              text: "待绑定",
              cls: "bg-amber-500/15 text-amber-700 dark:bg-amber-400/20 dark:text-amber-300",
            }
          : {
              text: "运行中",
              cls: "bg-green-500/15 text-green-700 dark:bg-green-400/20 dark:text-green-300",
            };

  return (
    <Card className="shadow-md dark:bg-content1" data-tgbot-card="true">
      <CardHeader className="pb-4">
        <div className="flex w-full items-start justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold">Telegram 通知</h2>
            <p className="text-xs text-gray-500 dark:text-gray-400">
              机器快到期时，bot 在 Telegram
              里提醒绑定的管理员；点「已续费」后这一期不再提醒（节点页的到期提醒也一起消掉），过了到期日自动进入下一期。
            </p>
          </div>
          {status && (
            <span data-tgbot-status className={`${PILL} ${status.cls}`}>
              {status.text}
            </span>
          )}
        </div>
      </CardHeader>
      <Divider />
      <CardBody className="space-y-5 py-4">
        {/* ── Bot ───────────────────────────────────────────── */}
        <section className="space-y-2">
          <div className="text-sm font-semibold text-gray-700 dark:text-gray-300">
            Bot
          </div>
          {tokenSet && !editingToken ? (
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm">
                🤖{" "}
                <span className="font-mono font-semibold">
                  @{view?.profile.username}
                </span>
                {view?.profile.name && (
                  <span className="ml-1 text-xs text-default-500">
                    （{view.profile.name}）
                  </span>
                )}
              </span>
              <span className="flex-1" />
              <Button
                color="warning"
                size="sm"
                variant="flat"
                onPress={() => {
                  setEditingToken(true);
                  setConfirmClear(false);
                }}
              >
                更换
              </Button>
              <Button
                color="danger"
                isLoading={busy === "clear"}
                size="sm"
                variant="flat"
                onPress={() =>
                  confirmClear ? void clearToken() : setConfirmClear(true)
                }
              >
                {confirmClear ? "确认删除？" : "删除"}
              </Button>
            </div>
          ) : (
            <div className="space-y-2">
              <Input
                autoComplete="off"
                description="在 Telegram 里找 @BotFather 创建一个 bot，把它给的 token 粘贴到这里。保存时会先向 Telegram 验证；保存后只显示 bot 的用户名，token 不会再显示。"
                label="Bot token"
                placeholder="123456789:AA…"
                type="password"
                value={tokenInput}
                onChange={(e) => setTokenInput(e.target.value)}
              />
              <div className="flex justify-end gap-2">
                {editingToken && (
                  <Button
                    color="danger"
                    size="sm"
                    variant="flat"
                    onPress={() => {
                      setEditingToken(false);
                      setTokenInput("");
                    }}
                  >
                    取消
                  </Button>
                )}
                <Button
                  color="success"
                  isDisabled={!tokenInput.trim()}
                  isLoading={busy === "token"}
                  size="sm"
                  variant="flat"
                  onPress={() => void saveToken()}
                >
                  保存 token
                </Button>
              </div>
            </div>
          )}
          {tokenSet && view?.lastError && (
            <p className="text-xs text-red-600 dark:text-red-300">
              最近一次出错：{view.lastError}（会自动重试，30 秒到 10 分钟一次）
            </p>
          )}
        </section>

        {/* ── 绑定的聊天 ─────────────────────────────────────── */}
        <section className="space-y-2">
          <div className="flex items-center justify-between text-sm">
            <span className="font-semibold text-gray-700 dark:text-gray-300">
              接收通知的聊天
            </span>
            <span className="text-xs text-gray-400">
              {chats.length} / {maxChats}
            </span>
          </div>
          {chats.length > 0 && (
            <div className="rounded-lg border border-divider">
              {chats.map((c) => (
                <div
                  key={c.chatId}
                  className="flex items-center gap-2 border-b border-divider px-3 py-2 last:border-b-0"
                  data-tgbot-chat={c.title}
                >
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm">
                      {c.title || c.chatId}
                      <span
                        className={`${SOURCE_CHIP} ml-1.5 bg-sky-500/15 text-sky-700 dark:bg-sky-400/20 dark:text-sky-300`}
                      >
                        {KIND_LABEL[c.kind] || c.kind || "聊天"}
                      </span>
                    </div>
                    <div className="text-[11px] text-default-400">
                      {c.createdTime ? `${twDate(c.createdTime)} 绑定` : ""}
                      {c.lastError && (
                        <span className="ml-1 text-red-600 dark:text-red-300">
                          · 发不出去：{c.lastError}
                        </span>
                      )}
                    </div>
                  </div>
                  <Button
                    color="danger"
                    isLoading={busy === `unbind-${c.chatId}`}
                    size="sm"
                    variant="flat"
                    onPress={() => void unbind(c.chatId)}
                  >
                    解绑
                  </Button>
                </div>
              ))}
            </div>
          )}
          {code && left > 0 ? (
            <div
              data-tgbot-code
              className="space-y-1.5 rounded-lg border border-teal-500/30 bg-teal-500/5 p-3 text-sm"
            >
              <div>
                在 Telegram 里给{" "}
                <span className="font-mono font-semibold">
                  @{view?.profile.username}
                </span>{" "}
                发送：
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <code className="rounded-md bg-background px-2 py-1 font-mono text-base font-semibold tracking-wider">
                  {code.command}
                </code>
                <Button
                  color="secondary"
                  size="sm"
                  variant="flat"
                  onPress={() =>
                    void navigator.clipboard
                      ?.writeText(code.command)
                      .then(() => toast.success("已复制"))
                      .catch(() => toast.error("复制失败，请手动输入"))
                  }
                >
                  复制
                </Button>
                {code.link && (
                  <a
                    className="rounded-full bg-sky-500/15 px-3 py-1 text-xs font-semibold text-sky-700 hover:bg-sky-500/25 dark:bg-sky-400/20 dark:text-sky-300"
                    href={code.link}
                    rel="noreferrer"
                    target="_blank"
                  >
                    在 Telegram 打开
                  </a>
                )}
              </div>
              <div className="text-xs text-default-500">
                私聊 bot 或在群里发都可以（群里发
                <span className="font-mono">
                  {" "}
                  /start@{view?.profile.username} {code.code}
                </span>
                ）。绑定码只能用一次，还剩 {Math.floor(left / 60000)}:
                {String(Math.floor((left % 60000) / 1000)).padStart(2, "0")}
                。绑定成功后这里会自动刷新。
              </div>
            </div>
          ) : (
            !tokenSet && (
              <p className="text-xs text-default-400">
                先保存 bot token，再生成绑定码。
              </p>
            )
          )}
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              color="secondary"
              isDisabled={chats.length === 0}
              isLoading={busy === "test"}
              size="sm"
              variant="flat"
              onPress={() => void test()}
            >
              测试发送
            </Button>
            <Button
              color="success"
              isDisabled={!tokenSet || chats.length >= maxChats}
              isLoading={busy === "bind"}
              size="sm"
              variant="flat"
              onPress={() => void bind()}
            >
              {code && left > 0 ? "重新生成绑定码" : "生成绑定码"}
            </Button>
          </div>
        </section>

        {/* ── 到期提醒 ───────────────────────────────────────── */}
        <section className="space-y-3">
          <div className="flex items-center justify-between gap-4">
            <div className="flex flex-col">
              <span className="text-sm font-semibold text-gray-700 dark:text-gray-300">
                到期提醒
              </span>
              <span className="text-xs text-gray-400 dark:text-gray-500">
                每天到点检查一次，到期前第 N
                天各发一条（每台机器一条，带「已续费」「不再提醒这台」按钮）
                {view?.nextRunAt && chats.length > 0 && enabled
                  ? `；下次检查 ${twDate(view.nextRunAt, true)}`
                  : ""}
              </span>
            </div>
            <Switch
              aria-label="到期提醒"
              color="primary"
              isSelected={enabled}
              size="sm"
              onValueChange={setEnabled}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <Input
              description={`到期前几天提醒，逗号隔开（0 = 当天，最多 ${view?.maxDays ?? 60}）`}
              isDisabled={!enabled}
              label="提前天数"
              placeholder="7,3,1,0"
              value={daysText}
              onChange={(e) => setDaysText(e.target.value)}
            />
            <Input
              description="台湾时间"
              isDisabled={!enabled}
              label="发送时间"
              type="time"
              value={time}
              onChange={(e) => setTime(e.target.value)}
            />
          </div>
          <div>
            <div className="mb-1.5 text-xs text-default-500">消息样子：</div>
            <div className="max-w-sm rounded-xl rounded-tl-sm bg-sky-500/10 px-3 py-2 text-sm dark:bg-sky-400/10">
              {previewText(machines.find((m) => !muted.has(m.key)))}
              <div className="mt-2 grid grid-cols-2 gap-1.5">
                <span className="rounded-md bg-background/80 py-1 text-center text-xs font-semibold">
                  ✅ 已续费
                </span>
                <span className="rounded-md bg-background/80 py-1 text-center text-xs font-semibold">
                  🔕 不再提醒这台
                </span>
              </div>
            </div>
          </div>
          <div>
            <div className="mb-2 flex items-center justify-between text-sm">
              <span className="font-semibold text-gray-700 dark:text-gray-300">
                提醒哪些机器
              </span>
              <span className="text-xs text-gray-400">
                提醒 {remindCount} / {machines.length}
              </span>
            </div>
            <p className="mb-2 text-xs text-gray-400 dark:text-gray-500">
              列出所有设了到期时间的机器，按下次到期排序；关掉就不再提醒这台（在
              Telegram 里点「不再提醒这台」也会关掉这里的开关）。
              {view && view.noExpiry > 0 && (
                <>
                  另外 {view.noExpiry}{" "}
                  台没有设置到期时间，不会提醒（在节点页设置到期时间和续费周期）。
                </>
              )}
              {view?.oldPanel === "ok" &&
                " 只在救命版里的机器也在里面（只读救命版的数据），两边都有的机器只算一台。"}
              {view?.oldPanel === "error" && (
                <span className="text-amber-600 dark:text-amber-300">
                  {" "}
                  救命版的数据暂时读不到，它的机器先不列出，读到后会自动补发。
                </span>
              )}
            </p>
            {machines.length > 12 && (
              <input
                aria-label="搜索机器"
                className="mb-2 h-8 w-full rounded-md border border-divider bg-transparent px-2 text-sm outline-none placeholder:text-default-400 focus:border-teal-500 sm:max-w-xs"
                placeholder="搜索名称或「救命版」"
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            )}
            <div className="max-h-80 overflow-y-auto rounded-lg border border-divider">
              {visible.map((m) => {
                const on = !muted.has(m.key);

                return (
                  <div
                    key={m.key}
                    className="flex items-center gap-3 border-b border-divider px-3 py-2 last:border-b-0"
                    data-tgbot-machine={m.name}
                  >
                    <Switch
                      aria-label={`提醒 ${m.name}`}
                      color="success"
                      isDisabled={!enabled}
                      isSelected={on}
                      size="sm"
                      onValueChange={(v) =>
                        setMuted((s) => {
                          const next = new Set(s);

                          if (v) next.delete(m.key);
                          else next.add(m.key);

                          return next;
                        })
                      }
                    />
                    <div className="min-w-0 flex-1">
                      <div
                        className={`truncate text-sm ${on ? "text-foreground" : "text-default-400 line-through"}`}
                      >
                        {m.name}
                        {m.source === "old" ? (
                          <span
                            className={`${SOURCE_CHIP} ml-1.5 bg-amber-500/15 text-amber-700 dark:bg-amber-400/20 dark:text-amber-300`}
                          >
                            救命版
                          </span>
                        ) : (
                          <span
                            className={`${SOURCE_CHIP} ml-1.5 bg-teal-500/15 text-teal-700 dark:bg-teal-400/20 dark:text-teal-300`}
                            title={
                              m.oldMatch
                                ? `救命版里也有这台（叫「${m.oldMatch}」），只提醒一次`
                                : undefined
                            }
                          >
                            转发面板{m.oldMatch ? " · 救命版同机" : ""}
                          </span>
                        )}
                      </div>
                      <div className="text-[11px] text-default-400">
                        {CYCLE_LABEL[m.cycle] ?? "无续费周期"} · 下次到期{" "}
                        {twDate(m.nextDue)}
                      </div>
                    </div>
                    <div className="flex shrink-0 flex-col items-end gap-1">
                      <span className={`${PILL} ${daysTone(m.days)}`}>
                        {daysLabel(m.days)}
                      </span>
                      {m.renewed && (
                        <span
                          className={`${PILL} bg-green-500/15 text-green-700 dark:bg-green-400/20 dark:text-green-300`}
                        >
                          本期已续费
                        </span>
                      )}
                    </div>
                  </div>
                );
              })}
              {view && machines.length === 0 && (
                <div className="px-3 py-4 text-center text-xs text-default-400">
                  还没有设置了到期时间的机器
                </div>
              )}
              {machines.length > 0 && visible.length === 0 && (
                <div className="px-3 py-4 text-center text-xs text-default-400">
                  没有找到「{query.trim()}」
                </div>
              )}
            </div>
          </div>
          <div className="flex justify-end">
            <Button
              color="success"
              isDisabled={!dirty}
              isLoading={busy === "settings"}
              size="sm"
              variant="flat"
              onPress={() => void saveSettings()}
            >
              保存提醒设置
            </Button>
          </div>
        </section>
      </CardBody>
    </Card>
  );
}
