import { useLayoutEffect, useMemo, useRef, useState } from "react";
import toast from "react-hot-toast";
import {
  ArrowRight,
  Ban,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  type LucideIcon,
  Rocket,
  Search,
  Server,
  X,
  Zap,
} from "lucide-react";

import {
  SubAdminItem,
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
import { landingToneClass, type LandingTone, Pill } from "@/pages/landing/pill";
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
  /** desktop only: the source's name (a node of an 外部订阅源) */
  detail?: string;
  /** where the node comes from (one small tag in the 走哪里 picker) */
  source?: "rule" | "landing" | "external" | "source";
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

// User 11-15: two kinds of exits, always shown as two groups — 系统策略
// (the four client built-ins, fixed order, one lucide icon each, the same tone
// as their chip) and 订阅里的节点 (subscription order, one small source tag).
const BASE_EXITS: {
  exit: string;
  label: string;
  tone: Tone;
  Icon: LucideIcon;
  hint: string;
}[] = [
  {
    exit: "select",
    label: "节点选择",
    tone: "blue",
    Icon: Rocket,
    hint: "在客户端里手动选",
  },
  {
    exit: "auto",
    label: "自动选择",
    tone: "teal",
    Icon: Zap,
    hint: "自动挑最快的",
  },
  {
    exit: "direct",
    label: "直连",
    tone: "green",
    Icon: ArrowRight,
    hint: "不走代理",
  },
  { exit: "reject", label: "拒绝", tone: "red", Icon: Ban, hint: "直接断开" },
];

const textTone: Record<Tone, string> = {
  blue: "text-blue-700 dark:text-blue-300",
  teal: "text-teal-700 dark:text-teal-300",
  green: "text-green-700 dark:text-green-300",
  red: "text-red-700 dark:text-red-300",
  purple: "text-purple-700 dark:text-purple-300",
  amber: "text-amber-700 dark:text-amber-300",
};

// Selected pill of the desktop 自己写 form.
const solidTone: Record<Tone, string> = {
  blue: "bg-blue-600 text-white",
  teal: "bg-teal-600 text-white",
  green: "bg-green-600 text-white",
  red: "bg-red-600 text-white",
  purple: "bg-purple-600 text-white",
  amber: "bg-amber-600 text-white",
};

// Source tag of a node exit (same words / tones as the 已选 tab).
const sourceTag: Record<
  NonNullable<RouteNodeOption["source"]>,
  { tone: LandingTone; text: string }
> = {
  rule: { tone: "green", text: "规则" },
  landing: { tone: "teal", text: "落地" },
  external: { tone: "purple", text: "外部节点" },
  source: { tone: "amber", text: "订阅源" },
};

type NodeSource = NonNullable<RouteNodeOption["source"]>;

// User 11-15 (v4): the small node icon takes the source's colour, like the
// 系统策略 icons take theirs (规则 green · 落地 teal · 外部节点 purple ·
// 订阅源 orange). A missing node stays amber.
const nodeIconTone: Record<NodeSource, string> = {
  rule: "text-green-600 dark:text-green-400",
  landing: "text-teal-600 dark:text-teal-400",
  external: "text-purple-600 dark:text-purple-400",
  source: "text-orange-600 dark:text-orange-400",
};

function nodeIconClass(source?: NodeSource) {
  return source ? nodeIconTone[source] : "text-default-400";
}

interface ExitView {
  label: string;
  tone: Tone;
  missing: boolean;
  /** 系统策略 icon; a node exit gets the small node marker */
  Icon?: LucideIcon;
  /** a node exit: its icon in the source's colour */
  iconClass?: string;
}

function exitView(exit: string, nodes: Map<string, RouteNodeOption>): ExitView {
  if (exit.startsWith("node:")) {
    const n = nodes.get(exit.slice(5));

    if (n)
      return {
        label: n.name,
        tone: "purple",
        missing: false,
        Icon: Server,
        iconClass: n.source ? nodeIconTone[n.source] : undefined,
      };

    return { label: "节点选择", tone: "amber", missing: true, Icon: Rocket };
  }
  const b = BASE_EXITS.find((x) => x.exit === exit) || BASE_EXITS[0];

  return { label: b.label, tone: b.tone, missing: false, Icon: b.Icon };
}

/** 国内网站 (fixed row): 直连 or 走代理. */
function modeExitView(mode: SubRuleMode): ExitView {
  return mode === "global"
    ? { label: "走代理", tone: "blue", missing: false, Icon: Rocket }
    : { label: "直连", tone: "green", missing: false, Icon: ArrowRight };
}

function ExitChip({ view, open }: { view: ExitView; open?: boolean }) {
  const Icon = view.Icon;

  return (
    <span
      className={`inline-flex max-w-[11rem] items-center gap-1 rounded-lg px-2 py-0.5 text-xs font-semibold ${toneClass[view.tone]}`}
      data-exit-chip={view.tone}
    >
      {Icon && (
        <Icon
          className={`shrink-0 ${view.iconClass || ""}`}
          data-exit-icon={view.iconClass ? "node" : undefined}
          size={12}
          strokeWidth={2.5}
        />
      )}
      <span className="truncate">{view.label}</span>
      {open !== false && <ChevronDown className="-ml-0.5 shrink-0" size={12} />}
    </span>
  );
}

/** H5 list rows: the exit as coloured text with the same small icon. */
function ExitText({ view }: { view: ExitView }) {
  const Icon = view.Icon;

  return (
    <span
      className={`inline-flex min-w-0 items-center gap-1 font-semibold ${textTone[view.tone]}`}
    >
      {Icon && (
        <Icon
          className={`shrink-0 ${view.iconClass || ""}`}
          size={11}
          strokeWidth={2.5}
        />
      )}
      <span className="min-w-0 truncate">{view.label}</span>
    </span>
  );
}

function SourceTag({ node }: { node: RouteNodeOption }) {
  if (!node.source) return null;
  const t = sourceTag[node.source];

  return (
    <span
      className={`inline-flex shrink-0 items-center whitespace-nowrap rounded-full px-1.5 text-[11px] font-semibold leading-[18px] ${landingToneClass[t.tone]}`}
      data-exit-source={node.source}
    >
      {t.text}
    </span>
  );
}

const groupHeadClass =
  "px-3 pb-1 pt-2 text-[11px] font-medium tracking-wide text-default-400";

/** One option row (H5 sheet / desktop menu): icon · label · hint|tag · ✓. */
function ExitOptionRow({
  Icon,
  iconClass,
  label,
  hint,
  tag,
  detail,
  on,
}: {
  Icon: LucideIcon;
  iconClass: string;
  label: string;
  hint?: string;
  tag?: React.ReactNode;
  detail?: string;
  on: boolean;
}) {
  return (
    <span
      className="flex w-full min-w-0 items-center gap-2"
      data-exit-on={on ? "" : undefined}
    >
      <Icon className={`shrink-0 ${iconClass}`} size={15} strokeWidth={2.25} />
      <span className="min-w-0 truncate" title={label}>
        {label}
      </span>
      {hint && (
        <span className="shrink-0 whitespace-nowrap text-xs text-default-400">
          {hint}
        </span>
      )}
      <span className="min-w-0 flex-1" />
      {detail && (
        <span className="max-w-[6rem] shrink-[3] truncate text-xs text-default-400">
          {detail}
        </span>
      )}
      {tag}
      <span className="flex w-4 shrink-0 justify-end">
        {on && <Check className="text-primary" size={15} />}
      </span>
    </span>
  );
}

// User 11-15 (v4): a subscription may hold up to 300 nodes. 系统策略 stays
// fixed; only the 订阅里的节点 group scrolls in its own box, the current
// choice is scrolled into view on open, a 「搜索节点」 box shows when there
// are more than 8 nodes, and rows render in batches (smooth with 300).
const NODE_SEARCH_OVER = 8;
const NODE_BATCH = 60;

function nodeMatches(n: RouteNodeOption, q: string) {
  if (!q) return true;
  const tag = n.source ? sourceTag[n.source].text : "";

  return (
    n.name.toLowerCase().includes(q) ||
    (n.detail || "").toLowerCase().includes(q) ||
    tag.includes(q)
  );
}

function NodeGroup({
  exit,
  options,
  menu,
  fill,
  row,
}: {
  exit: string;
  options: RouteNodeOption[];
  /** desktop dropdown (Radix menu): keys from the search box stay there */
  menu?: boolean;
  /** H5 sheet: the box takes the space left (sheet ≤ 85vh) */
  fill?: boolean;
  row: (n: RouteNodeOption, on: boolean) => React.ReactNode;
}) {
  const searchable = options.length > NODE_SEARCH_OVER;
  const [q, setQ] = useState("");
  const query = q.trim().toLowerCase();
  const list = useMemo(
    () => (query ? options.filter((n) => nodeMatches(n, query)) : options),
    [options, query],
  );
  // Rendered window [start, end) of `list`: opens around the current choice,
  // grows by NODE_BATCH when the box is scrolled near either end.
  const [win, setWin] = useState(() => {
    const i = options.findIndex((n) => exit === `node:${n.key}`);
    const start = i < 0 ? 0 : Math.max(0, i - NODE_BATCH / 3);

    return { start, end: Math.min(options.length, start + NODE_BATCH) };
  });
  const boxRef = useRef<HTMLDivElement>(null);
  const grewTop = useRef<number | null>(null);
  const autoCentre = useRef(true);

  // Open: centre the current choice in the box (the box only, never the
  // page) — again while the menu / sheet settles its height, until the user
  // scrolls or types.
  useLayoutEffect(() => {
    const box = boxRef.current;

    if (!box) return;
    const centre = () => {
      if (!autoCentre.current) return;
      const on = box.querySelector<HTMLElement>("[data-exit-on]");

      if (!on) return;
      const b = box.getBoundingClientRect();
      const r = on.getBoundingClientRect();

      box.scrollTop += r.top - b.top - (box.clientHeight - r.height) / 2;
    };
    const ro = new ResizeObserver(centre);
    const stop = () => ro.disconnect();
    const timer = window.setTimeout(stop, 600);

    centre();
    ro.observe(box);
    box.addEventListener("wheel", stop, { once: true, passive: true });
    box.addEventListener("touchstart", stop, { once: true, passive: true });

    return () => {
      window.clearTimeout(timer);
      stop();
    };
  }, []);

  // After rows were added above, keep the visible rows where they were.
  useLayoutEffect(() => {
    const box = boxRef.current;

    if (box && grewTop.current !== null) {
      box.scrollTop += box.scrollHeight - grewTop.current;
      grewTop.current = null;
    }
  }, [win.start]);

  const more = () => {
    const box = boxRef.current;

    if (!box) return;
    if (
      win.end < list.length &&
      box.scrollTop + box.clientHeight > box.scrollHeight - 240
    )
      setWin((w) => ({ ...w, end: Math.min(list.length, w.end + NODE_BATCH) }));
    else if (win.start > 0 && box.scrollTop < 240) {
      grewTop.current = box.scrollHeight;
      setWin((w) => ({ ...w, start: Math.max(0, w.start - NODE_BATCH) }));
    }
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!menu || e.key === "Escape" || e.key === "Tab") return;
    // Radix would take letters as typeahead and move the focus away.
    e.stopPropagation();
    if (e.key === "ArrowDown") {
      e.preventDefault();
      boxRef.current?.querySelector<HTMLElement>("[role=menuitem]")?.focus();
    }
  };

  return (
    <div
      className={fill ? "flex min-h-0 flex-auto flex-col" : ""}
      data-exit-nodes={options.length}
    >
      <div className="flex items-center justify-between">
        <p className={groupHeadClass} data-exit-group="nodes">
          订阅里的节点
        </p>
        {searchable && (
          <span className="px-3 pb-1 pt-2 text-[11px] text-default-400">
            {query ? `${list.length} / ${options.length}` : options.length} 个
          </span>
        )}
      </div>
      {searchable && (
        <div className={`shrink-0 ${menu ? "px-2 pb-1.5" : "pb-2"}`}>
          <div className="relative">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-default-400"
              size={14}
            />
            <input
              data-exit-search
              aria-label="搜索节点"
              className={`w-full rounded-lg border border-divider bg-default-50 pl-8 pr-2.5 outline-none placeholder:text-default-400 focus:border-primary dark:bg-default-100/60 ${menu ? "h-8 text-sm" : "h-9 text-base"}`}
              placeholder="搜索节点"
              type="search"
              value={q}
              onChange={(e) => {
                autoCentre.current = false;
                setQ(e.target.value);
                setWin({ start: 0, end: NODE_BATCH });
                if (boxRef.current) boxRef.current.scrollTop = 0;
              }}
              onKeyDown={onKeyDown}
            />
          </div>
        </div>
      )}
      <div
        ref={boxRef}
        data-exit-scroll
        className={`overflow-y-auto overscroll-contain ${
          menu
            ? "max-h-[max(8rem,min(55vh,calc(var(--radix-dropdown-menu-content-available-height,100vh)-16rem)))]"
            : `rounded-xl border border-divider ${fill ? "min-h-0 flex-auto" : "max-h-[40vh]"}`
        }`}
        onScroll={more}
      >
        {options.length === 0 && (
          <p
            className={`text-xs text-default-400 ${menu ? "px-3 pb-2 pt-0.5" : "px-3 py-2.5"}`}
          >
            订阅里还没有节点
          </p>
        )}
        {options.length > 0 && list.length === 0 && (
          <p
            data-exit-nomatch
            className="px-3 py-3 text-center text-xs text-default-400"
          >
            没有匹配的节点
          </p>
        )}
        {list
          .slice(win.start, win.end)
          .map((n) => row(n, exit === `node:${n.key}`))}
      </div>
    </div>
  );
}

/** Desktop: the coloured 「走哪里 ▾」 chip with a grouped dropdown. */
function ExitDropdown({
  exit,
  nodes,
  options,
  onChange,
  trigger,
  nodesOnly,
}: {
  exit: string;
  nodes: Map<string, RouteNodeOption>;
  options: RouteNodeOption[];
  onChange: (exit: string) => void;
  /** default: the exit chip of the current choice */
  trigger?: React.ReactNode;
  /** only the 订阅里的节点 group (the 自己写 form has the four as pills) */
  nodesOnly?: boolean;
}) {
  return (
    <Dropdown placement={nodesOnly ? "bottom-start" : "bottom-end"}>
      <DropdownTrigger>
        <button data-route-exit aria-label="走哪里" type="button">
          {trigger || <ExitChip view={exitView(exit, nodes)} />}
        </button>
      </DropdownTrigger>
      <DropdownMenu aria-label="走哪里">
        <div data-exit-menu className="w-[22rem] max-w-[calc(100vw-2rem)]">
          {!nodesOnly && (
            <>
              <DropdownMenuLabel
                className={groupHeadClass}
                data-exit-group="sys"
              >
                系统策略
              </DropdownMenuLabel>
              {BASE_EXITS.map((b) => (
                <DropdownItem key={b.exit} onPress={() => onChange(b.exit)}>
                  <ExitOptionRow
                    Icon={b.Icon}
                    hint={b.hint}
                    iconClass={textTone[b.tone]}
                    label={b.label}
                    on={exit === b.exit}
                  />
                </DropdownItem>
              ))}
              <DropdownMenuSeparator />
            </>
          )}
          <NodeGroup
            menu
            exit={exit}
            options={options}
            row={(n, on) => (
              <DropdownItem
                key={n.key}
                onPress={() => onChange(`node:${n.key}`)}
              >
                <ExitOptionRow
                  Icon={Server}
                  detail={n.detail}
                  iconClass={nodeIconClass(n.source)}
                  label={n.name}
                  on={on}
                  tag={<SourceTag node={n} />}
                />
              </DropdownItem>
            )}
          />
        </div>
      </DropdownMenu>
    </Dropdown>
  );
}

/** H5: the grouped list of exits with ✓ (bottom sheet / edit page). */
function ExitList({
  exit,
  options,
  onChange,
  fill,
}: {
  exit: string;
  options: RouteNodeOption[];
  onChange: (exit: string) => void;
  /** in the bottom sheet: the node box takes the height left */
  fill?: boolean;
}) {
  const row = (key: string, value: string, body: React.ReactNode) => (
    <button
      key={key}
      className="flex w-full items-center border-b border-divider px-3 py-2.5 text-left text-sm last:border-b-0 active:bg-default-100"
      data-exit-option={value}
      type="button"
      onClick={() => onChange(value)}
    >
      {body}
    </button>
  );

  return (
    <div
      data-exit-list
      className={fill ? "flex min-h-0 flex-auto flex-col gap-2" : "space-y-2"}
    >
      <div className="shrink-0">
        <p className={`${groupHeadClass} pt-0`} data-exit-group="sys">
          系统策略
        </p>
        <div className="rounded-xl border border-divider">
          {BASE_EXITS.map((b) =>
            row(
              b.exit,
              b.exit,
              <ExitOptionRow
                Icon={b.Icon}
                hint={b.hint}
                iconClass={textTone[b.tone]}
                label={b.label}
                on={exit === b.exit}
              />,
            ),
          )}
        </div>
      </div>
      <NodeGroup
        exit={exit}
        fill={fill}
        options={options}
        row={(n, on) =>
          row(
            n.key,
            `node:${n.key}`,
            <ExitOptionRow
              Icon={Server}
              iconClass={nodeIconClass(n.source)}
              label={n.name}
              on={on}
              tag={<SourceTag node={n} />}
            />,
          )
        }
      />
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
      <ModalContent
        data-exit-sheet
        className="bottom-0 left-0 right-0 top-auto flex max-h-[85vh] w-full max-w-none translate-x-0 translate-y-0 flex-col gap-3 overflow-hidden rounded-b-none rounded-t-2xl p-4 pb-[calc(1.5rem+env(safe-area-inset-bottom))]"
      >
        <div className="mx-auto h-1 w-10 shrink-0 rounded-full bg-default-300" />
        <p className="shrink-0 text-base font-semibold">{title}</p>
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
              // mr-1.5: the name lines up with the other rows (gap-3).
              <span className="mr-1.5 w-6 shrink-0 text-center text-lg">
                {m.icon}
              </span>
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
              <ExitText view={v} />
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
              <ExitText view={v} />
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

  const modeView = modeExitView(mode);
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
            <div className="w-[16rem]" data-exit-menu="cn">
              <DropdownItem onPress={() => onModeChange("cn_direct")}>
                <ExitOptionRow
                  Icon={ArrowRight}
                  hint="国内不走代理"
                  iconClass={textTone.green}
                  label="直连"
                  on={mode !== "global"}
                />
              </DropdownItem>
              <DropdownItem onPress={() => onModeChange("global")}>
                <ExitOptionRow
                  Icon={Rocket}
                  hint="全部走代理"
                  iconClass={textTone.blue}
                  label="走代理"
                  on={mode === "global"}
                />
              </DropdownItem>
            </div>
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
            fill
            exit={sheetEntry.exit}
            options={nodeOptions}
            onChange={(x) => {
              setExit(sheet, x);
              setSheet(null);
            }}
          />
          <Button
            className="w-full shrink-0"
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
  items = [],
}: {
  isH5: boolean;
  routing: SubAdminRoute[];
  mode: SubRuleMode;
  catalog: SubRouteCategory[];
  /** the subscription's items: a node exit's icon takes its source colour */
  items?: SubAdminItem[];
}) {
  const cat = new Map(catalog.map((c) => [c.key, c]));
  const none = new Map<string, RouteNodeOption>();
  // Node exits arrive by name only: find the source by the picked item of
  // that name; any other name is a node of a followed source (整组).
  const srcOf = new Map<string, NodeSource>();

  for (const it of items)
    if (!it.missing && it.group !== "source_all" && !srcOf.has(it.name))
      srcOf.set(it.name, it.group);
  const followsSource = items.some(
    (it) => it.group === "source_all" && !it.missing,
  );
  const view = (e: SubAdminRoute): ExitView => {
    if (e.exit !== "node") return exitView(e.exit, none);
    if (e.exitMissing || !e.exitName)
      return { label: "节点选择", tone: "amber", missing: true, Icon: Rocket };

    const src = srcOf.get(e.exitName) || (followsSource ? "source" : undefined);

    return {
      label: e.exitName,
      tone: "purple",
      missing: false,
      Icon: Server,
      iconClass: src ? nodeIconTone[src] : undefined,
    };
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
              <ExitText view={v} />
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

  const modeView = modeExitView(mode);

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

  const exitNode = exit.startsWith("node:") ? exitView(exit, nodes) : null;
  const exitPicker = isH5 ? (
    <ExitList exit={exit} options={nodeOptions} onChange={setExit} />
  ) : (
    <div data-exit-form className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="w-[5.5rem] shrink-0 text-[11px] text-default-400">
          系统策略
        </span>
        {BASE_EXITS.map((b) => (
          <button
            key={b.exit}
            className={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs font-semibold ${
              exit === b.exit
                ? solidTone[b.tone]
                : "bg-slate-500/10 text-slate-600 hover:bg-slate-500/15 dark:bg-slate-400/15 dark:text-slate-300"
            }`}
            data-exit-option={b.exit}
            title={b.hint}
            type="button"
            onClick={() => setExit(b.exit)}
          >
            <b.Icon
              className={exit === b.exit ? "" : textTone[b.tone]}
              size={12}
              strokeWidth={2.5}
            />
            {b.label}
          </button>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="w-[5.5rem] shrink-0 text-[11px] text-default-400">
          订阅里的节点
        </span>
        {nodeOptions.length === 0 ? (
          <span className="text-xs text-default-400">订阅里还没有节点</span>
        ) : (
          <ExitDropdown
            nodesOnly
            exit={exit}
            nodes={nodes}
            options={nodeOptions}
            trigger={
              <span
                className={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs font-semibold ${
                  exitNode
                    ? solidTone.purple
                    : "bg-slate-500/10 text-slate-600 hover:bg-slate-500/15 dark:bg-slate-400/15 dark:text-slate-300"
                }`}
              >
                <Server size={12} strokeWidth={2.5} />
                <span className="max-w-[12rem] truncate">
                  {exitNode ? exitNode.label : "选一个节点"}
                </span>
                <ChevronDown size={12} />
              </span>
            }
            onChange={setExit}
          />
        )}
      </div>
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
