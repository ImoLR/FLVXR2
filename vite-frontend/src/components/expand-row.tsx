import type { CSSProperties, HTMLAttributes, ReactNode, Ref } from "react";

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Minus, Plus } from "lucide-react";

// Plan 256 follow-up (user 11-14): the one H5 list row. A phone row shows only
// the name + one small protocol chip (+ a tiny status dot) and a round ＋ at
// the far right; address, latency, usage tags, long text and the action pills
// live in the area that opens under the row. One row open per list. Desktop
// lists keep their own layout — use this only when the page is in H5 mode.

/** Accordion state: one open key per list; forgotten once the item is gone. */
export function useExpandOne<K extends string | number>(keys: readonly K[]) {
  const [open, setOpen] = useState<K | null>(null);
  const present = open !== null && keys.includes(open);

  useEffect(() => {
    if (open !== null && !present) setOpen(null);
  }, [open, present]);

  return {
    open: present ? open : null,
    toggle: (key: K) => setOpen((cur) => (cur === key ? null : key)),
    close: () => setOpen(null),
  };
}

/**
 * Short protocol chip text: 「VLESS Reality」, 「Trojan」, 「SS2022」, 「SOCKS5」,
 * 「Hy2」… (the full label goes into the expanded area).
 */
export function shortProtocol(label?: string) {
  const text = (label || "").trim();

  if (!text) return "";

  return text
    .replace(/^VLESS Encryption/, "VLESS Enc")
    .replace(/^Hysteria\s*2/i, "Hy2")
    .replace(/^Shadowsocks 2022/, "SS2022")
    .replace(/^Shadowsocks/, "SS")
    .replace(/（链式）$/, "");
}

/** Small tinted chip used in the collapsed H5 row. */
export function ExpandChip({
  children,
  className = "bg-pink-500/15 text-pink-700 dark:bg-pink-400/20 dark:text-pink-300",
  title,
}: {
  children: ReactNode;
  className?: string;
  title?: string;
}) {
  return (
    <span
      data-expand-chip
      className={`inline-flex max-w-[42%] shrink-0 items-center whitespace-nowrap rounded-full px-1.5 text-[11px] font-semibold leading-[18px] ${className}`}
      title={title}
    >
      <span className="min-w-0 truncate">{children}</span>
    </span>
  );
}

/** One label / value line of the expanded area (values may wrap). */
export function ExpandField({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="flex gap-3 text-xs leading-5">
      <span className="w-14 shrink-0 text-default-500">{label}</span>
      <div className="min-w-0 flex-1 break-all text-foreground">{children}</div>
    </div>
  );
}

/** The action pills at the bottom of the expanded area. */
export function ExpandActions({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2 pt-1">{children}</div>
  );
}

export type ExpandStatus = "ok" | "bad" | undefined;

export function ExpandRow({
  name,
  nameTitle,
  nameClassName = "",
  chip,
  status,
  statusTitle,
  leading,
  open,
  onToggle,
  children,
  className = "",
  rowRef,
  style,
  rowProps,
}: {
  name: ReactNode;
  nameTitle?: string;
  nameClassName?: string;
  /** one small chip (protocol / 「36 个节点」 / type) */
  chip?: ReactNode;
  /** tiny dot without text: green = OK, red = 超时 / 更新失败 / 失效 */
  status?: ExpandStatus;
  statusTitle?: string;
  /** drag handle / checkbox / icon in front of the name (taps don't toggle) */
  leading?: ReactNode;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
  className?: string;
  rowRef?: Ref<HTMLDivElement>;
  style?: CSSProperties;
  rowProps?: HTMLAttributes<HTMLDivElement> & Record<`data-${string}`, unknown>;
}) {
  const nameRef = useRef<HTMLSpanElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const [indent, setIndent] = useState(12);
  // Mount the details on first open only (long lists stay light).
  const [mounted, setMounted] = useState(open);

  if (open && !mounted) setMounted(true);

  useLayoutEffect(() => {
    if (open && nameRef.current) setIndent(nameRef.current.offsetLeft);
  }, [open]);
  useEffect(() => {
    const el = bodyRef.current as (HTMLDivElement & { inert: boolean }) | null;

    if (el) el.inert = !open;
  }, [open, mounted]);

  return (
    <div
      ref={rowRef}
      data-expand-row
      className={`relative ${className}`}
      data-expand-open={open ? "" : undefined}
      style={style}
      {...rowProps}
    >
      {/* The whole row toggles; the ＋ button is the accessible control. */}
      {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions */}
      <div
        className="flex min-h-[52px] cursor-pointer select-none items-center gap-1.5 py-1 pl-3 pr-0.5"
        onClick={onToggle}
      >
        {leading && (
          // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions
          <div
            className="flex shrink-0 items-center"
            onClick={(e) => e.stopPropagation()}
          >
            {leading}
          </div>
        )}
        <span
          ref={nameRef}
          data-expand-name
          className={`min-w-0 flex-1 truncate text-sm font-medium ${nameClassName}`}
          title={nameTitle}
        >
          {name}
        </span>
        {chip}
        {status && (
          <span
            aria-label={statusTitle}
            className={`h-2 w-2 shrink-0 rounded-full ${status === "ok" ? "bg-green-500" : "bg-red-500"}`}
            data-expand-status={status}
            role="img"
            title={statusTitle}
          />
        )}
        <button
          data-expand-toggle
          aria-expanded={open}
          aria-label={open ? "收起详情" : "展开详情"}
          className="-ml-1 flex h-11 w-10 shrink-0 items-center justify-center rounded-full focus-visible:outline-2 focus-visible:outline-slate-400"
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onToggle();
          }}
        >
          <span className="flex h-7 w-7 items-center justify-center rounded-full bg-slate-500/15 text-slate-600 dark:bg-slate-400/20 dark:text-slate-300">
            {open ? <Minus size={16} /> : <Plus size={16} />}
          </span>
        </button>
      </div>
      <div
        className={`grid transition-[grid-template-rows] duration-150 ease-out motion-reduce:transition-none ${open ? "grid-rows-[1fr]" : "grid-rows-[0fr]"}`}
      >
        <div ref={bodyRef} className="min-h-0 overflow-hidden">
          {mounted && (
            <div
              data-expand-body
              className="space-y-1.5 bg-default-100/60 py-2.5 pr-3 dark:bg-default-100/30"
              style={{ paddingLeft: indent }}
            >
              {children}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
