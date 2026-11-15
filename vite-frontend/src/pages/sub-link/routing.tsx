import { useMemo, useState } from "react";
import toast from "react-hot-toast";
import { Check, ChevronDown, ChevronLeft, ChevronRight, X } from "lucide-react";

import {
  SubAdminRoute,
  SubLinkSet,
  SubRouteCategory,
  SubRouteEntry,
  SubRouteLimits,
  SubRuleMode,
  subAgo,
  subLinkFetch,
  subLinkRefresh,
} from "@/api/sub-link";
import {
  ExpandActions,
  ExpandChip,
  ExpandField,
  ExpandRow,
  useExpandOne,
} from "@/components/expand-row";
import { Pill } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownItem,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";
import { Input, Textarea } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";

// Plan 256 「分流」: one list per subscription. Each row is one group in the
// client (Clash / sing-box / Surge); custom rows always come first, builtin
// rows in the fixed rule order, then the two fixed rows 国内网站 / 其余全部.

export interface RouteNodeOption {
  key: string;
  name: string;
  detail?: string;
}

export const DEFAULT_LIMITS: SubRouteLimits = {
  entries: 30,
  lines: 300,
  lineLen: 253,
  nameLen: 20,
};

const CUSTOM_ICON = "📌";
// Group / policy names of the clients a custom entry may not take.
const RESERVED_NAMES = [
  "节点选择",
  "自动选择",
  "DIRECT",
  "REJECT",
  "REJECT-DROP",
  "REJECT-TINYGIF",
  "REJECT-NO-DROP",
  "PASS",
  "COMPATIBLE",
  "GLOBAL",
  "PROXY",
];
const toastOpts = { duration: 4000 };

// ------------------------------------------------------------ custom lines

type RuleKind = "suffix" | "full" | "keyword" | "ip";

interface ParsedLines {
  lines: string[]; // normalized (what the server stores)
  counts: { domain: number; ip: number; keyword: number };
  error: string;
}

const domainChars = /^[a-z0-9_.-]+$/;
const wordChars = /^[a-z0-9_-]+$/;

function validDomain(d: string) {
  return (
    d.length <= 253 &&
    domainChars.test(d) &&
    !d.startsWith(".") &&
    !d.endsWith(".") &&
    !d.includes("..") &&
    d.split(".").every((l) => l.length <= 63)
  );
}

function parseIPv4(s: string): number[] | null {
  const parts = s.split(".");

  if (parts.length !== 4) return null;
  const out = parts.map((p) => (/^\d{1,3}$/.test(p) ? Number(p) : -1));

  return out.every((n) => n >= 0 && n <= 255) ? out : null;
}

/** Rough IP / CIDR check (the server canonicalizes). */
function parseIP(s: string): string | null {
  const t = s.replace(/^\[|\]$/g, "");
  const [addr, bits, extra] = t.split("/");

  if (extra !== undefined) return null;
  if (parseIPv4(addr)) {
    if (bits !== undefined && !(/^\d+$/.test(bits) && Number(bits) <= 32))
      return null;

    return bits === undefined ? addr : `${addr}/${bits}`;
  }
  if (addr.includes(":") && /^[0-9a-fA-F:.]+$/.test(addr)) {
    if (bits !== undefined && !(/^\d+$/.test(bits) && Number(bits) <= 128))
      return null;
    if ((addr.match(/::/g) || []).length > 1) return null;

    return (bits === undefined ? addr : `${addr}/${bits}`).toLowerCase();
  }

  return null;
}

/** One custom line, as the server's ParseRouteLine. */
export function parseRouteLine(
  raw: string,
  lineLen: number,
): { kind: RuleKind; line: string } | string {
  let s = raw;

  if (s.length > lineLen) return `太长了（最多 ${lineLen} 个字符）`;
  let kind: RuleKind | "" = "";
  const i = s.indexOf(":");

  if (i > 0) {
    const p = s.slice(0, i).toLowerCase();
    const map: Record<string, RuleKind> = {
      full: "full",
      domain: "suffix",
      keyword: "keyword",
      ip: "ip",
    };

    if (map[p]) {
      kind = map[p];
      s = s.slice(i + 1).trim();
    }
  }
  if (!kind && s.includes("://")) {
    try {
      const u = new URL(s);

      if (u.hostname) s = u.hostname.replace(/^\[|\]$/g, "");
    } catch {
      // not a URL
    }
  }
  if (!kind || kind === "ip") {
    const ip = parseIP(s);

    if (ip) return { kind: "ip", line: ip };
    if (kind === "ip") return "不是有效的 IP 或 IP 段";
  }

  if (/[^\x00-\x7f]/.test(s))
    return "里有中文或全角字符（中文域名请写成 xn-- 开头的形式）";
  let d = s.toLowerCase();

  if (kind === "full") {
    return validDomain(d) ? { kind, line: `full:${d}` } : "不是有效的域名";
  }
  if (kind === "keyword") {
    if (!domainChars.test(d) || d.replace(/^[-_.]+|[-_.]+$/g, "").length < 2)
      return "关键词至少 2 个字，只能有字母、数字、- _ .";

    return { kind, line: d.includes(".") ? `keyword:${d}` : d };
  }
  for (const p of ["+.", "*.", "."]) {
    if (d.startsWith(p)) {
      d = d.slice(p.length);
      kind = "suffix";
      break;
    }
  }
  if (!kind && !d.includes(".")) {
    if (!wordChars.test(d) || d.replace(/^[-_]+|[-_]+$/g, "").length < 2)
      return "不认识的写法（一行写一个域名、IP 或关键词）";

    return { kind: "keyword", line: d };
  }
  if (!validDomain(d)) return "不认识的写法（一行写一个域名、IP 或关键词）";

  return { kind: "suffix", line: d };
}

export function parseRouteText(text: string, lineLen: number): ParsedLines {
  const out: ParsedLines = {
    lines: [],
    counts: { domain: 0, ip: 0, keyword: 0 },
    error: "",
  };
  const seen = new Set<string>();

  text.split("\n").forEach((raw, i) => {
    if (out.error) return;
    const s = raw.trim();

    if (!s || s.startsWith("#") || s.startsWith("//")) return;
    const r = parseRouteLine(s, lineLen);

    if (typeof r === "string") {
      out.error = `第 ${i + 1} 行「${s.length > 40 ? `${s.slice(0, 40)}…` : s}」${r}`;

      return;
    }
    if (seen.has(r.line)) return;
    seen.add(r.line);
    out.lines.push(r.line);
    if (r.kind === "ip") out.counts.ip++;
    else if (r.kind === "keyword") out.counts.keyword++;
    else out.counts.domain++;
  });

  return out;
}

function checkName(name: string, nameLen: number): string {
  const n = name.trim();

  if (!n) return "请给自己写的分流起个名字";
  if ([...n].length > nameLen) return `名字最多 ${nameLen} 个字`;

  if (/[,，"\\\u0000-\u001f\u007f\u2028\u2029]/.test(n))
    return "名字里不能有逗号、引号、反斜杠或换行";

  return "";
}

// ------------------------------------------------------------ exits

type Tone = "blue" | "teal" | "green" | "red" | "purple" | "amber";

const toneClass: Record<Tone, string> = {
  blue: "bg-blue-500/15 text-blue-700 dark:bg-blue-400/20 dark:text-blue-300",
  teal: "bg-teal-500/15 text-teal-700 dark:bg-teal-400/15 dark:text-teal-300",
  green:
    "bg-green-500/15 text-green-700 dark:bg-green-400/15 dark:text-green-300",
  red: "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300",
  purple:
    "bg-purple-500/15 text-purple-700 dark:bg-purple-400/20 dark:text-purple-300",
  amber:
    "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300",
};

const BASE_EXITS: { exit: string; label: string; tone: Tone }[] = [
  { exit: "select", label: "🚀 节点选择", tone: "blue" },
  { exit: "auto", label: "⚡ 自动选择", tone: "teal" },
  { exit: "direct", label: "直连", tone: "green" },
  { exit: "reject", label: "拒绝", tone: "red" },
];

interface ExitView {
  label: string;
  tone: Tone;
  missing: boolean;
}

function exitView(exit: string, nodes: Map<string, RouteNodeOption>): ExitView {
  if (exit.startsWith("node:")) {
    const n = nodes.get(exit.slice(5));

    if (n) return { label: n.name, tone: "purple", missing: false };

    return { label: "节点选择", tone: "amber", missing: true };
  }
  const b = BASE_EXITS.find((x) => x.exit === exit) || BASE_EXITS[0];

  return {
    label: b.label.replace(/^\S+ /, ""),
    tone: b.tone,
    missing: false,
  };
}

function ExitChip({ view, open }: { view: ExitView; open?: boolean }) {
  return (
    <span
      className={`inline-flex max-w-[11rem] items-center gap-0.5 rounded-lg px-2 py-0.5 text-xs font-semibold ${toneClass[view.tone]}`}
    >
      <span className="truncate">{view.label}</span>
      {open !== false && <ChevronDown className="shrink-0" size={12} />}
    </span>
  );
}

/** Desktop: the coloured 「走哪里 ▾」 chip with a dropdown. */
function ExitDropdown({
  exit,
  nodes,
  options,
  onChange,
}: {
  exit: string;
  nodes: Map<string, RouteNodeOption>;
  options: RouteNodeOption[];
  onChange: (exit: string) => void;
}) {
  return (
    <Dropdown placement="bottom-end">
      <DropdownTrigger>
        <button data-route-exit aria-label="走哪里" type="button">
          <ExitChip view={exitView(exit, nodes)} />
        </button>
      </DropdownTrigger>
      <DropdownMenu aria-label="走哪里">
        {[
          ...BASE_EXITS.map((b) => (
            <DropdownItem key={b.exit} onPress={() => onChange(b.exit)}>
              <span className="flex w-full items-center gap-2">
                <span className="flex-1">{b.label}</span>
                {exit === b.exit && <Check size={14} />}
              </span>
            </DropdownItem>
          )),
          ...(options.length
            ? [
                <DropdownMenuSeparator key="sep" />,
                <DropdownMenuLabel
                  key="label"
                  className="text-xs font-normal text-default-500"
                >
                  订阅里的节点
                </DropdownMenuLabel>,
              ]
            : []),
          ...options.map((n) => (
            <DropdownItem key={n.key} onPress={() => onChange(`node:${n.key}`)}>
              <span className="flex w-full max-w-[18rem] items-center gap-2">
                <span className="min-w-0 flex-1 truncate">{n.name}</span>
                {exit === `node:${n.key}` && <Check size={14} />}
              </span>
            </DropdownItem>
          )),
        ]}
      </DropdownMenu>
    </Dropdown>
  );
}

/** H5: a list of exits with ✓ (inside the bottom sheet / edit page). */
function ExitList({
  exit,
  options,
  onChange,
}: {
  exit: string;
  options: RouteNodeOption[];
  onChange: (exit: string) => void;
}) {
  const row = (key: string, value: string, label: string, detail?: string) => (
    <button
      key={key}
      className="flex w-full items-center gap-2 border-b border-divider px-3 py-2.5 text-left text-sm last:border-b-0"
      type="button"
      onClick={() => onChange(value)}
    >
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {detail && (
        <span className="max-w-[45%] shrink-0 truncate text-xs text-default-400">
          {detail}
        </span>
      )}
      {exit === value && <Check className="shrink-0 text-primary" size={16} />}
    </button>
  );

  return (
    <div className="max-h-[45vh] overflow-y-auto rounded-xl border border-divider">
      {BASE_EXITS.slice(0, 2).map((b) => row(b.exit, b.exit, b.label))}
      {options.map((n) => row(n.key, `node:${n.key}`, n.name, n.detail))}
      {BASE_EXITS.slice(2).map((b) => row(b.exit, b.exit, b.label))}
    </div>
  );
}

function BottomSheet({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <Modal isOpen onClose={onClose}>
      <ModalContent className="bottom-0 left-0 right-0 top-auto max-h-[85vh] w-full max-w-none translate-x-0 translate-y-0 gap-3 overflow-y-auto rounded-b-none rounded-t-2xl p-4 pb-6">
        <div className="mx-auto h-1 w-10 rounded-full bg-default-300" />
        <p className="text-base font-semibold">{title}</p>
        {children}
      </ModalContent>
    </Modal>
  );
}

// ------------------------------------------------------------ list

/** Custom entries first (in their order), builtins in the catalog order. */
export function normalizeRouting(
  list: SubRouteEntry[],
  catalog: SubRouteCategory[],
): SubRouteEntry[] {
  const order = new Map(catalog.map((c, i) => [c.key, i]));
  // Plan 261: link entries are 自己写 too (same place in the order).
  const custom = list.filter((e) => e.type === "custom" || e.type === "link");
  const seen = new Set<string>();
  const builtin = list
    .filter((e) => e.type === "builtin" && order.has(e.key || ""))
    .filter((e) => (seen.has(e.key!) ? false : (seen.add(e.key!), true)))
    .sort((a, b) => order.get(a.key!)! - order.get(b.key!)!);

  return [...custom, ...builtin];
}

export function defaultRouting(
  catalog: SubRouteCategory[],
  defaults: string[],
): SubRouteEntry[] {
  return normalizeRouting(
    defaults
      .map((k) => catalog.find((c) => c.key === k))
      .filter((c): c is SubRouteCategory => !!c)
      .map((c) => ({ type: "builtin" as const, key: c.key, exit: c.exit })),
    catalog,
  );
}

function customSummary(lines: string[] = []) {
  const shown = lines.slice(0, 3).join(" · ");

  return lines.length > 3 ? `${shown} 等 ${lines.length} 条` : shown;
}

// ------------------------------------------------------------ links (plan 261)

const LINK_KIND: Record<string, string> = {
  full: "域名",
  suffix: "后缀",
  keyword: "关键词",
  ip: "IP 段",
};

/** 「3 小时前更新」 / 「昨天 22:14 更新」. */
function linkAgo(ms: number) {
  const a = subAgo(ms);

  if (!a) return "";

  return /(前|刚刚)$/.test(a) ? `${a}更新` : `${a} 更新`;
}

/** The muted status line of a link entry. */
export function linkStatus(set?: SubLinkSet): {
  text: string;
  bad: boolean;
  reason?: string;
} {
  if (!set) return { text: "规则集已失效，请修改后换个链接", bad: true };
  if (set.failCount > 0 && set.lastError)
    return {
      text: "更新失败，正在使用上次的内容",
      bad: true,
      reason: set.lastError,
    };

  return {
    text: `${set.ruleCount} 条 · ${linkAgo(set.fetchedAt)}`,
    bad: false,
  };
}

function LinkChip() {
  return (
    <Pill className="shrink-0" title="规则集链接（面板代为下载）" tone="cyan">
      🔗 链接
    </Pill>
  );
}

/** 立即更新 with the toast wording of both places (list H5 / edit dialog). */
async function refreshLink(
  id: number,
  onLinks: (sets: SubLinkSet[]) => void,
): Promise<void> {
  const res = await subLinkRefresh(id);

  if (res.code !== 0) {
    toast.error(res.msg || "更新失败", toastOpts);

    return;
  }
  onLinks([res.data]);
  if (res.data.error)
    toast.error(`更新失败：${res.data.error}（正在使用上次的内容）`, toastOpts);
  else toast.success(`已更新，共 ${res.data.ruleCount} 条`, toastOpts);
}

/** 获取 result: counts line + the first rules. */
function LinkPreview({ set }: { set: SubLinkSet }) {
  const c = set.counts;

  return (
    <div
      data-route-link-preview
      className="space-y-1.5 rounded-xl border border-divider bg-default-100/50 px-3 py-2 dark:bg-default-100/30"
    >
      <p className="text-xs text-default-600">
        共 <b className="text-foreground">{set.ruleCount}</b> 条：域名{" "}
        {c.domain} · 后缀 {c.suffix} · 关键词 {c.keyword} · IP 段 {c.ip}
        {c.ignored > 0 && (
          <span className="text-default-400">
            （忽略 {c.ignored} 行不支持的规则）
          </span>
        )}
      </p>
      {set.sample && set.sample.length > 0 && (
        <ul className="space-y-0.5 font-mono text-[12px] text-default-600">
          {set.sample.map((r) => (
            <li key={`${r.kind}:${r.value}`} className="flex min-w-0 gap-2">
              <span className="w-12 shrink-0 text-default-400">
                {LINK_KIND[r.kind] || r.kind}
              </span>
              <span className="min-w-0 truncate">{r.value}</span>
            </li>
          ))}
          {set.ruleCount > set.sample.length && (
            <li className="text-default-400">…</li>
          )}
        </ul>
      )}
    </div>
  );
}

type Editing = { index: number | null; tab: "pick" | "custom" } | null;

export function RoutingPanel({
  isH5,
  routing,
  onChange,
  mode,
  onModeChange,
  catalog,
  defaults,
  limits,
  nodeOptions,
  nodeNames,
  links = {},
  onLinks = () => {},
}: {
  isH5: boolean;
  routing: SubRouteEntry[];
  onChange: (next: SubRouteEntry[]) => void;
  mode: SubRuleMode;
  onModeChange: (m: SubRuleMode) => void;
  catalog: SubRouteCategory[];
  defaults: string[];
  limits: SubRouteLimits;
  nodeOptions: RouteNodeOption[];
  nodeNames: Set<string>;
  /** plan 261: stored sets of the link entries (by id) */
  links?: Record<string, SubLinkSet>;
  onLinks?: (sets: SubLinkSet[]) => void;
}) {
  const [editing, setEditing] = useState<Editing>(null);
  const [sheet, setSheet] = useState<number | null>(null);
  const [refreshing, setRefreshing] = useState<number | null>(null);
  const linkRows = useExpandOne(
    routing.filter((e) => e.type === "link").map((e) => e.ref || 0),
  );
  const nodes = useMemo(
    () => new Map(nodeOptions.map((n) => [n.key, n])),
    [nodeOptions],
  );
  const cat = useMemo(() => new Map(catalog.map((c) => [c.key, c])), [catalog]);
  const lineTotal = routing.reduce(
    (n, e) => n + (e.type === "custom" ? e.lines?.length || 0 : 0),
    0,
  );
  const set = (next: SubRouteEntry[]) =>
    onChange(normalizeRouting(next, catalog));
  const setExit = (i: number, exit: string) =>
    set(routing.map((e, j) => (j === i ? { ...e, exit } : e)));
  const remove = (i: number) => set(routing.filter((_, j) => j !== i));
  const openAdd = () => {
    if (routing.length >= limits.entries) {
      toast.error(`每条订阅最多 ${limits.entries} 个分流`, toastOpts);

      return;
    }
    setEditing({ index: null, tab: "pick" });
  };
  const rowMeta = (e: SubRouteEntry) => {
    if (e.type === "custom")
      return {
        icon: CUSTOM_ICON,
        name: e.name || "",
        desc: customSummary(e.lines),
        custom: true,
      };
    if (e.type === "link")
      return {
        icon: CUSTOM_ICON,
        name: e.name || "",
        desc: "",
        custom: true,
      };
    const c = cat.get(e.key || "");

    return {
      icon: c?.icon || "",
      name: c?.name || e.key || "",
      desc: c?.desc || "",
      custom: false,
    };
  };

  const rows = routing.map((e, i) => {
    const m = rowMeta(e);
    const v = exitView(e.exit, nodes);
    const missingNote = v.missing && (
      <span className="block text-xs text-amber-700 dark:text-amber-300">
        指定的节点已不在订阅里，已改走节点选择
      </span>
    );

    if (e.type === "link") {
      const set = links[String(e.ref)];
      const st = linkStatus(set);
      const statusLine = (
        <span
          className={`block truncate text-xs ${st.bad ? "text-red-600/80 dark:text-red-300/80" : "text-default-400"}`}
          data-route-link-status={st.bad ? "bad" : "ok"}
          title={st.reason}
        >
          {st.text}
        </span>
      );

      if (isH5) {
        const open = linkRows.open === (e.ref || 0);

        return (
          <ExpandRow
            key={`link:${e.ref}`}
            chip={
              <ExpandChip className="bg-cyan-500/15 text-cyan-700 dark:bg-cyan-400/20 dark:text-cyan-300">
                🔗 链接
              </ExpandChip>
            }
            className={`border-b border-divider ${v.missing ? "bg-amber-500/10" : ""}`}
            leading={
              <span className="w-6 shrink-0 text-center text-lg">{m.icon}</span>
            }
            name={m.name}
            nameTitle={m.name}
            open={open}
            rowProps={{ "data-route-row": e.name }}
            status={st.bad ? "bad" : undefined}
            statusTitle={st.bad ? st.text : undefined}
            onToggle={() => linkRows.toggle(e.ref || 0)}
          >
            <ExpandField label="走哪里">
              <span
                className={`font-semibold ${toneClass[v.tone]
                  .split(" ")
                  .filter(
                    (c) => c.startsWith("text-") || c.startsWith("dark:text-"),
                  )
                  .join(" ")}`}
              >
                {v.label}
              </span>
              {missingNote}
            </ExpandField>
            <ExpandField label="规则">
              <span className={st.bad ? "text-red-600 dark:text-red-300" : ""}>
                {st.text}
              </span>
              {st.reason && (
                <span className="block text-default-500">
                  原因：{st.reason}
                </span>
              )}
            </ExpandField>
            {set?.urlHint && (
              <ExpandField label="地址">
                <span className="font-mono">{set.urlHint}</span>
              </ExpandField>
            )}
            <ExpandActions>
              <Button
                color="primary"
                size="sm"
                variant="flat"
                onPress={() => setEditing({ index: i, tab: "custom" })}
              >
                修改
              </Button>
              {set && (
                <Button
                  color="success"
                  isLoading={refreshing === e.ref}
                  size="sm"
                  variant="flat"
                  onPress={async () => {
                    setRefreshing(e.ref || 0);
                    await refreshLink(set.id, onLinks);
                    setRefreshing(null);
                  }}
                >
                  立即更新
                </Button>
              )}
              <Button
                color="danger"
                size="sm"
                variant="flat"
                onPress={() => remove(i)}
              >
                删除
              </Button>
            </ExpandActions>
          </ExpandRow>
        );
      }

      return (
        <div
          key={`link:${e.ref}`}
          className={`flex items-center gap-3 border-b border-divider px-3 py-2 ${v.missing ? "bg-amber-500/10" : ""}`}
          data-route-row={e.name}
        >
          <span className="w-6 shrink-0 text-center text-lg">{m.icon}</span>
          <div
            className="min-w-0 flex-1 cursor-pointer"
            role="button"
            tabIndex={0}
            title="点一下修改"
            onClick={() => setEditing({ index: i, tab: "custom" })}
            onKeyDown={(ev) =>
              ev.key === "Enter" && setEditing({ index: i, tab: "custom" })
            }
          >
            <div className="flex items-center gap-1.5 text-sm font-medium">
              <span className="truncate">{m.name}</span>
              <Pill tone="slate">自己写的</Pill>
              <LinkChip />
            </div>
            {statusLine}
            {missingNote}
          </div>
          <ExitDropdown
            exit={e.exit}
            nodes={nodes}
            options={nodeOptions}
            onChange={(x) => setExit(i, x)}
          />
          <button
            aria-label="删除这个分流"
            className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-red-500/10 text-red-600 hover:bg-red-500/20 dark:bg-red-400/15 dark:text-red-300"
            title="删除这个分流"
            type="button"
            onClick={() => remove(i)}
          >
            <X size={14} />
          </button>
        </div>
      );
    }

    if (isH5) {
      return (
        <button
          key={`${e.type}:${e.key || e.name}`}
          className={`flex w-full items-center gap-3 border-b border-divider px-3 py-2.5 text-left ${v.missing ? "bg-amber-500/10" : ""}`}
          data-route-row={e.key || e.name}
          type="button"
          onClick={() =>
            m.custom ? setEditing({ index: i, tab: "custom" }) : setSheet(i)
          }
        >
          <span className="w-6 shrink-0 text-center text-lg">{m.icon}</span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium">{m.name}</span>
            <span className="flex min-w-0 items-baseline text-xs">
              <span
                className={`min-w-0 truncate font-semibold ${toneClass[v.tone]
                  .split(" ")
                  .filter(
                    (c) => c.startsWith("text-") || c.startsWith("dark:text-"),
                  )
                  .join(" ")}`}
              >
                {v.label}
              </span>
              {m.custom && (
                <span className="shrink-0 whitespace-pre text-default-400">
                  {" "}
                  · {e.lines?.length || 0} 条
                </span>
              )}
            </span>
            {missingNote}
          </span>
          <ChevronRight className="shrink-0 text-default-400" size={16} />
        </button>
      );
    }

    return (
      <div
        key={`${e.type}:${e.key || e.name}`}
        className={`flex items-center gap-3 border-b border-divider px-3 py-2 ${v.missing ? "bg-amber-500/10" : ""}`}
        data-route-row={e.key || e.name}
      >
        <span className="w-6 shrink-0 text-center text-lg">{m.icon}</span>
        <div
          className={`min-w-0 flex-1 ${m.custom ? "cursor-pointer" : ""}`}
          role={m.custom ? "button" : undefined}
          tabIndex={m.custom ? 0 : undefined}
          title={m.custom ? "点一下修改" : undefined}
          onClick={() => m.custom && setEditing({ index: i, tab: "custom" })}
          onKeyDown={(ev) =>
            m.custom &&
            ev.key === "Enter" &&
            setEditing({ index: i, tab: "custom" })
          }
        >
          <div className="flex items-center gap-1.5 text-sm font-medium">
            <span className="truncate">{m.name}</span>
            {m.custom && <Pill tone="slate">自己写的</Pill>}
          </div>
          {m.desc && (
            <div
              className={`truncate text-xs text-default-500 ${m.custom ? "font-mono" : ""}`}
            >
              {m.desc}
            </div>
          )}
          {missingNote}
        </div>
        <ExitDropdown
          exit={e.exit}
          nodes={nodes}
          options={nodeOptions}
          onChange={(x) => setExit(i, x)}
        />
        <button
          aria-label="删除这个分流"
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-red-500/10 text-red-600 hover:bg-red-500/20 dark:bg-red-400/15 dark:text-red-300"
          title="删除这个分流"
          type="button"
          onClick={() => remove(i)}
        >
          <X size={14} />
        </button>
      </div>
    );
  });

  const modeView: ExitView =
    mode === "global"
      ? { label: "走代理", tone: "blue", missing: false }
      : { label: "直连", tone: "green", missing: false };
  const fixed = (
    <>
      <div
        className="flex items-center gap-3 border-b border-divider bg-slate-500/5 px-3 py-2.5 dark:bg-slate-400/5"
        data-route-fixed="cn"
      >
        <span className="w-6 shrink-0 text-center text-lg">🇨🇳</span>
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium">国内网站</div>
          {!isH5 && (
            <div className="text-xs text-default-400">固定在倒数第二行</div>
          )}
        </div>
        <Dropdown placement="bottom-end">
          <DropdownTrigger>
            <button aria-label="国内网站走哪里" type="button">
              <ExitChip view={modeView} />
            </button>
          </DropdownTrigger>
          <DropdownMenu aria-label="国内网站走哪里">
            <DropdownItem onPress={() => onModeChange("cn_direct")}>
              <span className="flex w-full items-center gap-2">
                <span className="flex-1">直连</span>
                {mode !== "global" && <Check size={14} />}
              </span>
            </DropdownItem>
            <DropdownItem onPress={() => onModeChange("global")}>
              <span className="flex w-full items-center gap-2">
                <span className="flex-1">走代理（全部走代理）</span>
                {mode === "global" && <Check size={14} />}
              </span>
            </DropdownItem>
          </DropdownMenu>
        </Dropdown>
        {!isH5 && <span className="w-7 shrink-0" />}
      </div>
      <div
        className="flex items-center gap-3 bg-slate-500/5 px-3 py-2.5 dark:bg-slate-400/5"
        data-route-fixed="final"
      >
        <span className="w-6 shrink-0 text-center text-lg">🌐</span>
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium">其余全部</div>
          {!isH5 && (
            <div className="text-xs text-default-400">固定在最后一行</div>
          )}
        </div>
        <ExitChip open={false} view={exitView("select", nodes)} />
        {!isH5 && <span className="w-7 shrink-0" />}
      </div>
    </>
  );

  const editingEntry =
    editing && editing.index !== null ? routing[editing.index] : null;
  const sheetEntry = sheet !== null ? routing[sheet] : null;

  return (
    <div
      data-routing-panel
      className={isH5 ? "flex min-h-0 flex-1 flex-col gap-2" : "space-y-2"}
    >
      {!isH5 && (
        <p className="text-xs text-default-500">
          从上往下匹配，列表里每一行就是客户端里的一个分组。不需要的就删掉。
        </p>
      )}
      <div
        className={`overflow-y-auto rounded-2xl border border-divider ${isH5 ? "min-h-0 flex-1" : "max-h-[42vh]"}`}
      >
        {rows}
        {routing.length === 0 && (
          <p className="border-b border-divider px-3 py-4 text-center text-sm text-default-400">
            现在没有分流，客户端里只有「节点选择 / 自动选择」两个分组。
          </p>
        )}
        <div className="border-b border-divider px-3 py-2">
          <Button
            data-route-add
            className={isH5 ? "w-full" : ""}
            color="success"
            size="sm"
            variant="flat"
            onPress={openAdd}
          >
            ＋ 添加分流
          </Button>
        </div>
        {fixed}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1 text-xs text-default-400">
          {isH5
            ? `最多 ${limits.entries} 个 · 自己写的排最前`
            : `「自己写的」永远排在最前，内置分流的顺序由系统排好，不用拖。最多 ${limits.entries} 个分流，自己写的合计 ${lineTotal} / ${limits.lines} 行。`}
        </span>
        <Button
          size="sm"
          variant="flat"
          onPress={() => set(defaultRouting(catalog, defaults))}
        >
          恢复默认
        </Button>
        <Button
          color="danger"
          isDisabled={routing.length === 0}
          size="sm"
          variant="flat"
          onPress={() => set([])}
        >
          全部删除
        </Button>
      </div>
      <p data-route-note className="text-xs text-default-400">
        分流只对 Clash / Stash / sing-box / Surge 生效；Loon、Quantumult
        X、Shadowrocket、v2rayN 只拿到节点，规则用 App 自己的。
      </p>

      {sheetEntry && sheet !== null && (
        <BottomSheet
          title={`${rowMeta(sheetEntry).icon} ${rowMeta(sheetEntry).name} 走哪里`}
          onClose={() => setSheet(null)}
        >
          <ExitList
            exit={sheetEntry.exit}
            options={nodeOptions}
            onChange={(x) => {
              setExit(sheet, x);
              setSheet(null);
            }}
          />
          <Button
            className="w-full"
            color="danger"
            variant="flat"
            onPress={() => {
              remove(sheet);
              setSheet(null);
            }}
          >
            删除这个分流
          </Button>
        </BottomSheet>
      )}
      {editing && (
        <AddRouteDialog
          catalog={catalog}
          entry={editingEntry}
          initialTab={editing.tab}
          isH5={isH5}
          limitLinks={
            routing.filter((e) => e.type === "link").length -
            (editingEntry?.type === "link" ? 1 : 0)
          }
          limits={limits}
          lineTotal={lineTotal - (editingEntry?.lines?.length || 0)}
          links={links}
          nodeNames={nodeNames}
          nodeOptions={nodeOptions}
          routing={routing}
          onClose={() => setEditing(null)}
          onDelete={
            editing.index !== null
              ? () => {
                  remove(editing.index!);
                  setEditing(null);
                }
              : undefined
          }
          onLinks={onLinks}
          onSave={(added, sets) => {
            if (sets?.length) onLinks(sets);
            if (editing.index !== null) {
              set(routing.map((e, j) => (j === editing.index ? added[0] : e)));
            } else {
              if (routing.length + added.length > limits.entries) {
                toast.error(`每条订阅最多 ${limits.entries} 个分流`, toastOpts);

                return;
              }
              set([...routing, ...added]);
            }
            setEditing(null);
          }}
        />
      )}
    </div>
  );
}

// ------------------------------------------------------------ read-only

/**
 * Plan 260: the same list read-only, for the admin's 「全部用户」 view — no ✕,
 * no ＋ 添加分流, no 恢复默认 / 全部删除; exits are plain chips. A node exit
 * arrives as exit "node" + the node's name (ref keys never reach the admin).
 */
export function RoutingReadOnly({
  isH5,
  routing,
  mode,
  catalog,
}: {
  isH5: boolean;
  routing: SubAdminRoute[];
  mode: SubRuleMode;
  catalog: SubRouteCategory[];
}) {
  const cat = new Map(catalog.map((c) => [c.key, c]));
  const none = new Map<string, RouteNodeOption>();
  const view = (e: SubAdminRoute): ExitView => {
    if (e.exit !== "node") return exitView(e.exit, none);
    if (e.exitMissing || !e.exitName)
      return { label: "节点选择", tone: "amber", missing: true };

    return { label: e.exitName, tone: "purple", missing: false };
  };

  const rows = routing.map((e) => {
    const link = e.type === "link";
    const custom = e.type === "custom" || link;
    const c = cat.get(e.key || "");
    const icon = custom ? CUSTOM_ICON : c?.icon || "";
    const name = custom ? e.name || "" : c?.name || e.key || "";
    const desc = link ? "" : custom ? customSummary(e.lines) : c?.desc || "";
    // Plan 261: a link entry = count + status, never its URL.
    const st = link
      ? linkStatus(
          e.linkMissing
            ? undefined
            : ({
                ruleCount: e.ruleCount || 0,
                fetchedAt: e.fetchedAt || 0,
                failCount: e.linkFailed ? 1 : 0,
                lastError: e.linkFailed ? "x" : "",
              } as SubLinkSet),
        )
      : null;
    const linkLine = st && (
      <span
        className={`block truncate text-xs ${st.bad ? "text-red-600/80 dark:text-red-300/80" : "text-default-400"}`}
        data-route-link-status={st.bad ? "bad" : "ok"}
      >
        {st.text}
      </span>
    );
    const v = view(e);
    const missingNote = v.missing && (
      <span className="block text-xs text-amber-700 dark:text-amber-300">
        指定的节点已不在订阅里，已改走节点选择
      </span>
    );
    const key = `${e.type}:${e.key || e.name}`;

    if (isH5) {
      return (
        <div
          key={key}
          className={`flex w-full items-center gap-3 border-b border-divider px-3 py-2.5 ${v.missing ? "bg-amber-500/10" : ""}`}
          data-route-row={e.key || e.name}
        >
          <span className="w-6 shrink-0 text-center text-lg">{icon}</span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium">{name}</span>
            <span className="flex min-w-0 items-baseline text-xs">
              <span
                className={`min-w-0 truncate font-semibold ${toneClass[v.tone]
                  .split(" ")
                  .filter(
                    (x) => x.startsWith("text-") || x.startsWith("dark:text-"),
                  )
                  .join(" ")}`}
              >
                {v.label}
              </span>
              {custom && !link && (
                <span className="shrink-0 whitespace-pre text-default-400">
                  {" "}
                  · {e.lines?.length || 0} 条
                </span>
              )}
            </span>
            {linkLine}
            {missingNote}
          </span>
          {link && <LinkChip />}
        </div>
      );
    }

    return (
      <div
        key={key}
        className={`flex items-center gap-3 border-b border-divider px-3 py-2 ${v.missing ? "bg-amber-500/10" : ""}`}
        data-route-row={e.key || e.name}
      >
        <span className="w-6 shrink-0 text-center text-lg">{icon}</span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5 text-sm font-medium">
            <span className="truncate">{name}</span>
            {custom && <Pill tone="slate">自己写的</Pill>}
            {link && <LinkChip />}
          </div>
          {desc && (
            <div
              className={`truncate text-xs text-default-500 ${custom ? "font-mono" : ""}`}
              title={custom ? e.lines?.join("\n") : undefined}
            >
              {desc}
            </div>
          )}
          {linkLine}
          {missingNote}
        </div>
        <ExitChip open={false} view={v} />
      </div>
    );
  });

  const modeView: ExitView =
    mode === "global"
      ? { label: "走代理", tone: "blue", missing: false }
      : { label: "直连", tone: "green", missing: false };

  return (
    <div
      data-routing-readonly
      className={isH5 ? "flex min-h-0 flex-1 flex-col gap-2" : "space-y-2"}
    >
      {!isH5 && (
        <p className="text-xs text-default-500">
          从上往下匹配，列表里每一行就是客户端里的一个分组。
        </p>
      )}
      <div
        className={`overflow-y-auto rounded-2xl border border-divider ${isH5 ? "min-h-0 flex-1" : "max-h-[52vh]"}`}
      >
        {rows}
        {routing.length === 0 && (
          <p className="border-b border-divider px-3 py-4 text-center text-sm text-default-400">
            现在没有分流，客户端里只有「节点选择 / 自动选择」两个分组。
          </p>
        )}
        <div
          className="flex items-center gap-3 border-b border-divider bg-slate-500/5 px-3 py-2.5 dark:bg-slate-400/5"
          data-route-fixed="cn"
        >
          <span className="w-6 shrink-0 text-center text-lg">🇨🇳</span>
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium">国内网站</div>
            {!isH5 && (
              <div className="text-xs text-default-400">固定在倒数第二行</div>
            )}
          </div>
          <ExitChip open={false} view={modeView} />
        </div>
        <div
          className="flex items-center gap-3 bg-slate-500/5 px-3 py-2.5 dark:bg-slate-400/5"
          data-route-fixed="final"
        >
          <span className="w-6 shrink-0 text-center text-lg">🌐</span>
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium">其余全部</div>
            {!isH5 && (
              <div className="text-xs text-default-400">固定在最后一行</div>
            )}
          </div>
          <ExitChip open={false} view={exitView("select", none)} />
        </div>
      </div>
      <p className="text-xs text-default-400">
        分流只对 Clash / Stash / sing-box / Surge 生效；Loon、Quantumult
        X、Shadowrocket、v2rayN 只拿到节点，规则用 App 自己的。
      </p>
    </div>
  );
}

// ------------------------------------------------------------ add / edit

function AddRouteDialog({
  isH5,
  catalog,
  routing,
  entry,
  initialTab,
  limits,
  lineTotal,
  nodeOptions,
  nodeNames,
  links,
  limitLinks,
  onLinks,
  onSave,
  onDelete,
  onClose,
}: {
  isH5: boolean;
  catalog: SubRouteCategory[];
  routing: SubRouteEntry[];
  entry: SubRouteEntry | null;
  initialTab: "pick" | "custom";
  limits: SubRouteLimits;
  lineTotal: number;
  nodeOptions: RouteNodeOption[];
  nodeNames: Set<string>;
  links: Record<string, SubLinkSet>;
  /** link entries of the list besides this one */
  limitLinks: number;
  onLinks: (sets: SubLinkSet[]) => void;
  onSave: (entries: SubRouteEntry[], sets?: SubLinkSet[]) => void;
  onDelete?: () => void;
  onClose: () => void;
}) {
  const editing = !!entry;
  const [tab, setTab] = useState<"pick" | "custom">(initialTab);
  // Plan 261: 自己写 = 手动输入 (the form of plan 256) or 规则集链接.
  const [source, setSource] = useState<"manual" | "link">(
    entry?.type === "link" ? "link" : "manual",
  );
  const [url, setUrl] = useState("");
  const [fetching, setFetching] = useState(false);
  const [fetched, setFetched] = useState<SubLinkSet | null>(null);
  const [fetchError, setFetchError] = useState("");
  const [changing, setChanging] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const current = entry?.type === "link" ? links[String(entry.ref)] : undefined;
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [name, setName] = useState(entry?.name || "");
  const [exit, setExit] = useState(entry?.exit || "direct");
  const [text, setText] = useState((entry?.lines || []).join("\n"));
  const have = new Set(
    routing.filter((e) => e.type === "builtin").map((e) => e.key),
  );
  const parsed = useMemo(
    () => parseRouteText(text, limits.lineLen),
    [text, limits.lineLen],
  );
  const nodes = useMemo(
    () => new Map(nodeOptions.map((n) => [n.key, n])),
    [nodeOptions],
  );

  const nameError = (n: string) => {
    const bad = checkName(n, limits.nameLen);

    if (bad) return bad;
    if (
      catalog.some((c) => c.name === n || `${c.icon} ${c.name}` === n) ||
      RESERVED_NAMES.includes(n.toUpperCase())
    )
      return `「${n}」是内置的名字，请换一个`;
    if (nodeNames.has(n) || nodeNames.has(`${CUSTOM_ICON} ${n}`))
      return `「${n}」和订阅里的节点同名，请换一个名字`;
    if (
      routing.some(
        (e) =>
          (e.type === "custom" || e.type === "link") &&
          e.name === n &&
          e !== entry,
      )
    )
      return `「${n}」这个名字已经用过了`;

    return "";
  };
  const doFetch = async () => {
    const u = url.trim();

    setFetchError("");
    if (!/^https?:\/\/\S+$/i.test(u)) {
      setFetchError("地址无效，请填写 http:// 或 https:// 开头的完整地址");

      return;
    }
    setFetching(true);
    try {
      const res = await subLinkFetch(u);

      if (res.code !== 0) {
        setFetched(null);
        setFetchError(res.msg || "获取失败");

        return;
      }
      setFetched(res.data);
    } finally {
      setFetching(false);
    }
  };
  const saveLink = () => {
    const n = name.trim();
    const bad = nameError(n);

    if (bad) return toast.error(bad, toastOpts);
    const ref = fetched?.id ?? (entry?.type === "link" ? entry.ref : undefined);

    if (!ref) return toast.error("请先点「获取」", toastOpts);
    if (routing.some((e) => e.type === "link" && e.ref === ref && e !== entry))
      return toast.error("这个规则集链接已经在列表里了", toastOpts);
    const max = limits.links || 5;

    if (entry?.type !== "link" && limitLinks + 1 > max)
      return toast.error(`每条订阅最多 ${max} 个规则集链接`, toastOpts);
    onSave(
      [{ type: "link", name: n, ref, exit }],
      fetched ? [fetched] : undefined,
    );
  };
  const saveCustom = () => {
    const n = name.trim();
    const bad = nameError(n);

    if (bad) return toast.error(bad, toastOpts);
    if (parsed.error) return toast.error(parsed.error, toastOpts);
    if (!parsed.lines.length)
      return toast.error("还没有写网站或 IP", toastOpts);
    if (lineTotal + parsed.lines.length > limits.lines)
      return toast.error(
        `自己写的分流合计最多 ${limits.lines} 行，现在是 ${lineTotal + parsed.lines.length} 行`,
        toastOpts,
      );
    onSave([{ type: "custom", name: n, lines: parsed.lines, exit }]);
  };
  const savePicked = () => {
    const add = catalog
      .filter((c) => picked.has(c.key))
      .map((c) => ({ type: "builtin" as const, key: c.key, exit: c.exit }));

    if (add.length) onSave(add);
  };

  const tabs = !editing && (
    <div className="flex rounded-xl bg-slate-500/10 p-0.5 dark:bg-slate-400/15">
      {(
        [
          ["pick", "常用"],
          ["custom", "自己写"],
        ] as const
      ).map(([k, t]) => (
        <button
          key={k}
          className={`flex-1 rounded-lg py-1.5 text-sm ${
            tab === k
              ? "bg-white font-semibold shadow-sm dark:bg-slate-700"
              : "text-default-600"
          }`}
          data-route-add-tab={k}
          type="button"
          onClick={() => setTab(k)}
        >
          {t}
        </button>
      ))}
    </div>
  );

  const grid = (
    <div
      data-route-pick
      className={`grid gap-2 ${isH5 ? "grid-cols-2" : "grid-cols-3"}`}
    >
      {catalog.map((c) => {
        const added = have.has(c.key);
        const on = picked.has(c.key);

        return (
          <button
            key={c.key}
            className={`flex items-center gap-2 rounded-xl border px-3 py-2 text-left text-sm ${
              added
                ? "cursor-default border-divider opacity-50"
                : on
                  ? "border-blue-500 bg-blue-500/10 dark:border-blue-400"
                  : "border-divider hover:bg-default-100"
            }`}
            disabled={added}
            type="button"
            onClick={() => {
              const next = new Set(picked);

              if (on) next.delete(c.key);
              else next.add(c.key);
              setPicked(next);
            }}
          >
            <span className="text-lg">{c.icon}</span>
            <span className="min-w-0 flex-1">
              <span className="block truncate font-medium">{c.name}</span>
              {c.desc && !isH5 && (
                <span className="block truncate text-[11px] text-default-400">
                  {c.desc}
                </span>
              )}
            </span>
            {added ? (
              <span className="shrink-0 text-xs text-default-400">
                {isH5 ? "✓" : "已添加"}
              </span>
            ) : (
              on && <Check className="shrink-0 text-blue-600" size={16} />
            )}
          </button>
        );
      })}
    </div>
  );

  const exitPicker = isH5 ? (
    <ExitList exit={exit} options={nodeOptions} onChange={setExit} />
  ) : (
    <div className="flex flex-wrap items-center gap-1.5">
      {(
        [
          ["direct", "直连"],
          ["select", "节点选择"],
          ["auto", "自动选择"],
          ["reject", "拒绝"],
        ] as const
      ).map(([v, t]) => (
        <button
          key={v}
          className={`rounded-lg px-2.5 py-1 text-xs font-semibold ${
            exit === v
              ? "bg-green-600 text-white"
              : "bg-slate-500/10 text-slate-600 dark:bg-slate-400/15 dark:text-slate-300"
          }`}
          type="button"
          onClick={() => setExit(v)}
        >
          {t}
        </button>
      ))}
      {nodeOptions.length > 0 && (
        <Dropdown>
          <DropdownTrigger>
            <button
              className={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs font-semibold ${
                exit.startsWith("node:")
                  ? "bg-purple-600 text-white"
                  : "bg-slate-500/10 text-slate-600 dark:bg-slate-400/15 dark:text-slate-300"
              }`}
              type="button"
            >
              <span className="max-w-[10rem] truncate">
                {exit.startsWith("node:")
                  ? exitView(exit, nodes).label
                  : "某个节点"}
              </span>
              <ChevronDown size={12} />
            </button>
          </DropdownTrigger>
          <DropdownMenu aria-label="某个节点">
            {nodeOptions.map((n) => (
              <DropdownItem
                key={n.key}
                onPress={() => setExit(`node:${n.key}`)}
              >
                <span className="block max-w-[18rem] truncate">{n.name}</span>
              </DropdownItem>
            ))}
          </DropdownMenu>
        </Dropdown>
      )}
    </div>
  );

  const counts = parsed.counts;
  const customForm = (
    <div data-route-custom className="space-y-3">
      <div className="space-y-3">
        <Input
          classNames={isH5 ? undefined : { base: "max-w-xs" }}
          label="名字"
          maxLength={limits.nameLen}
          placeholder="例如：公司"
          size="sm"
          value={name}
          variant="bordered"
          onChange={(e) => setName(e.target.value)}
        />
        <div className="space-y-1">
          <span className="text-xs text-default-500">走哪里</span>
          {exitPicker}
        </div>
      </div>
      <Textarea
        className="font-mono text-[13px]"
        label="网站 / IP，一行一个（直接粘贴就行）"
        maxRows={isH5 ? 10 : 8}
        minRows={isH5 ? 5 : 6}
        placeholder={"mycompany.com\ncorp.example.net\n10.8.0.0/16"}
        value={text}
        variant="bordered"
        onChange={(e) => setText(e.target.value)}
      />
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        {parsed.error ? (
          <span className="text-danger">{parsed.error}</span>
        ) : (
          <>
            <span className="text-default-500">认出来：</span>
            <Pill tone="green">域名 {counts.domain}</Pill>
            <Pill tone="teal">IP 段 {counts.ip}</Pill>
            {counts.keyword > 0 && (
              <Pill tone="purple">关键词 {counts.keyword}</Pill>
            )}
            <span className="text-default-400">
              · 写 example.com 会连同所有子域名一起匹配；没有点的词按关键词匹配
            </span>
          </>
        )}
      </div>
    </div>
  );

  const sourceSwitch = !editing && (
    <div
      data-route-source
      className="inline-flex rounded-lg bg-slate-500/10 p-0.5 text-xs dark:bg-slate-400/15"
    >
      {(
        [
          ["manual", "手动输入"],
          ["link", "规则集链接"],
        ] as const
      ).map(([k, t]) => (
        <button
          key={k}
          className={`rounded-md px-2.5 py-1 ${
            source === k
              ? "bg-white font-semibold shadow-sm dark:bg-slate-700"
              : "text-default-600"
          }`}
          data-route-source-tab={k}
          type="button"
          onClick={() => setSource(k)}
        >
          {t}
        </button>
      ))}
    </div>
  );
  const urlForm = (
    <div className="space-y-1.5">
      <div className="flex items-end gap-2">
        <Input
          data-route-link-url
          autoComplete="off"
          classNames={{ base: "min-w-0 flex-1" }}
          label="规则集地址"
          placeholder="https://raw.githubusercontent.com/…/Netflix.list"
          size="sm"
          value={url}
          variant="bordered"
          onChange={(e) => {
            setUrl(e.target.value);
            setFetchError("");
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") void doFetch();
          }}
        />
        <Button
          data-route-link-fetch
          color="secondary"
          isDisabled={!url.trim()}
          isLoading={fetching}
          size="sm"
          variant="flat"
          onPress={() => void doFetch()}
        >
          获取
        </Button>
      </div>
      {fetchError && (
        <p data-route-link-error className="text-xs text-danger">
          {fetchError}
        </p>
      )}
    </div>
  );
  const currentStatus = linkStatus(current);
  const linkForm = (
    <div data-route-link className="space-y-3">
      <div className="space-y-3">
        <Input
          classNames={isH5 ? undefined : { base: "max-w-xs" }}
          label="名字"
          maxLength={limits.nameLen}
          placeholder="例如：奈飞"
          size="sm"
          value={name}
          variant="bordered"
          onChange={(e) => setName(e.target.value)}
        />
        <div className="space-y-1">
          <span className="text-xs text-default-500">走哪里</span>
          {exitPicker}
        </div>
      </div>
      {editing && !changing ? (
        <div
          data-route-link-current
          className="space-y-2 rounded-xl border border-divider px-3 py-2"
        >
          <div className="flex min-w-0 items-center gap-2 text-sm">
            <LinkChip />
            <span className="min-w-0 truncate font-mono text-xs text-default-600">
              {current?.urlHint || "（地址已隐藏）"}
            </span>
          </div>
          <p
            className={`text-xs ${currentStatus.bad ? "text-red-600 dark:text-red-300" : "text-default-500"}`}
          >
            {currentStatus.text}
            {currentStatus.reason && (
              <span className="block text-default-500">
                原因：{currentStatus.reason}
              </span>
            )}
          </p>
          <div className="flex flex-wrap gap-2">
            {current && (
              <Button
                color="success"
                isLoading={refreshing}
                size="sm"
                variant="flat"
                onPress={async () => {
                  setRefreshing(true);
                  await refreshLink(current.id, onLinks);
                  setRefreshing(false);
                }}
              >
                立即更新
              </Button>
            )}
            <Button
              color="primary"
              size="sm"
              variant="flat"
              onPress={() => setChanging(true)}
            >
              换链接
            </Button>
          </div>
        </div>
      ) : (
        urlForm
      )}
      {fetched && <LinkPreview set={fetched} />}
      <p className="text-xs leading-5 text-default-400">
        支持 Clash / Surge / Loon / Quantumult X
        的文本规则集（.list、.txt、.yaml）。面板代为下载并每天更新，客户端从面板下载，不用连
        GitHub。只保留域名、后缀、关键词和 IP
        段，规则集里自带的策略不生效，走哪里由你选。
      </p>
    </div>
  );
  // Editing keeps the entry's own form (a manual entry renders exactly as
  // before plan 261); only 「＋ 添加分流」 gets the switch.
  const custom = editing ? (
    source === "link" ? (
      linkForm
    ) : (
      customForm
    )
  ) : (
    <div className="space-y-3">
      {sourceSwitch}
      {source === "link" ? linkForm : customForm}
    </div>
  );
  const body = tab === "pick" && !editing ? grid : custom;
  const pickHint = (
    <span className="min-w-0 flex-1 text-xs text-default-500">
      {isH5
        ? "灰色的是已经在列表里的"
        : "点选可以多选。默认列表只放了 8 个，其余的都在这里。"}
    </span>
  );
  const footer = (
    <>
      {tab === "pick" && !editing && !isH5 && pickHint}
      {editing && onDelete && (
        <Button color="danger" variant="flat" onPress={onDelete}>
          删除这个分流
        </Button>
      )}
      <Button
        className={isH5 ? "flex-1" : ""}
        color="danger"
        variant="flat"
        onPress={onClose}
      >
        取消
      </Button>
      {tab === "pick" && !editing ? (
        <Button
          className={isH5 ? "flex-[2]" : ""}
          color="success"
          isDisabled={!picked.size}
          onPress={savePicked}
        >
          添加{picked.size ? ` ${picked.size} 个` : ""}
        </Button>
      ) : (
        <Button
          className={isH5 ? "flex-[2]" : ""}
          color="success"
          isDisabled={source === "link" && !fetched && (!editing || changing)}
          onPress={source === "link" ? saveLink : saveCustom}
        >
          {editing ? "保存" : "添加"}
        </Button>
      )}
    </>
  );
  const title = editing
    ? `修改分流 · ${entry?.name}`
    : tab === "custom"
      ? "添加分流 · 自己写"
      : "添加分流";

  if (isH5) {
    return (
      <Modal isOpen size="full" onClose={onClose}>
        <ModalContent className="flex flex-col gap-0 overflow-hidden p-0">
          <div className="flex items-center gap-1 px-3 pb-2 pt-3">
            <button
              aria-label="返回"
              className="p-1"
              type="button"
              onClick={onClose}
            >
              <ChevronLeft size={20} />
            </button>
            <span className="truncate text-base font-semibold">{title}</span>
          </div>
          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-3 pb-3">
            {tabs}
            {body}
            {tab === "pick" && !editing && pickHint}
          </div>
          <div className="flex gap-2 border-t border-divider bg-content1 px-3 py-2.5">
            {footer}
          </div>
        </ModalContent>
      </Modal>
    );
  }

  return (
    <Modal isOpen scrollBehavior="inside" size="xl" onClose={onClose}>
      <ModalContent>
        <ModalHeader>{title}</ModalHeader>
        <ModalBody className="space-y-3">
          {tabs}
          {body}
        </ModalBody>
        <ModalFooter className="items-center gap-2">{footer}</ModalFooter>
      </ModalContent>
    </Modal>
  );
}
