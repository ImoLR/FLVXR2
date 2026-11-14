import { useMemo, useState } from "react";
import toast from "react-hot-toast";
import { Check, ChevronDown, ChevronLeft, ChevronRight, X } from "lucide-react";

import {
  SubRouteCategory,
  SubRouteEntry,
  SubRouteLimits,
  SubRuleMode,
} from "@/api/sub-link";
import { Pill } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownItem,
  DropdownMenu,
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
        <span className="shrink-0 text-xs text-default-400">{detail}</span>
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
  const custom = list.filter((e) => e.type === "custom");
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
}) {
  const [editing, setEditing] = useState<Editing>(null);
  const [sheet, setSheet] = useState<number | null>(null);
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
            <span
              className={`block truncate text-xs font-semibold ${toneClass[
                v.tone
              ]
                .split(" ")
                .filter(
                  (c) => c.startsWith("text-") || c.startsWith("dark:text-"),
                )
                .join(" ")}`}
            >
              {v.label}
              {m.custom && (
                <span className="font-normal text-default-400">
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
        <Button
          isIconOnly
          aria-label="删除这个分流"
          className="min-h-7 min-w-7"
          color="danger"
          size="sm"
          variant="flat"
          onPress={() => remove(i)}
        >
          <X size={14} />
        </Button>
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
    <div data-routing-panel className="flex min-h-0 flex-1 flex-col gap-2">
      {!isH5 && (
        <p className="text-xs text-default-500">
          从上往下匹配，列表里每一行就是客户端里的一个分组。不需要的就删掉。
        </p>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto rounded-2xl border border-divider">
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
          limits={limits}
          lineTotal={lineTotal - (editingEntry?.lines?.length || 0)}
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
          onSave={(added) => {
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
  onSave: (entries: SubRouteEntry[]) => void;
  onDelete?: () => void;
  onClose: () => void;
}) {
  const editing = !!entry;
  const [tab, setTab] = useState<"pick" | "custom">(initialTab);
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

  const saveCustom = () => {
    const n = name.trim();
    const bad = checkName(n, limits.nameLen);

    if (bad) return toast.error(bad, toastOpts);
    if (
      catalog.some((c) => c.name === n || `${c.icon} ${c.name}` === n) ||
      RESERVED_NAMES.includes(n.toUpperCase())
    )
      return toast.error(`「${n}」是内置的名字，请换一个`, toastOpts);
    if (nodeNames.has(n) || nodeNames.has(`${CUSTOM_ICON} ${n}`))
      return toast.error(`「${n}」和订阅里的节点同名，请换一个名字`, toastOpts);
    if (routing.some((e) => e.type === "custom" && e.name === n && e !== entry))
      return toast.error(`「${n}」这个名字已经用过了`, toastOpts);
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
      <div className={isH5 ? "space-y-3" : "flex items-start gap-3"}>
        <Input
          className={isH5 ? "" : "w-52 shrink-0"}
          label="名字"
          maxLength={limits.nameLen}
          placeholder="例如：公司"
          size="sm"
          value={name}
          variant="bordered"
          onChange={(e) => setName(e.target.value)}
        />
        <div className="min-w-0 flex-1 space-y-1">
          <span className="text-xs text-default-500">走哪里</span>
          {exitPicker}
        </div>
      </div>
      <Textarea
        className="font-mono text-[13px]"
        label="网站 / IP，一行一个（直接粘贴就行）"
        maxRows={isH5 ? 10 : 8}
        minRows={4}
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

  const body = tab === "pick" && !editing ? grid : customForm;
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
          onPress={saveCustom}
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
