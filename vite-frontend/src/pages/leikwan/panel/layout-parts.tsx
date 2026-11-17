import type { ReactNode } from "react";

import { ChevronRight, Minus, Plus } from "lucide-react";

// Plan 270: small layout pieces of the 利群汇聚 page (approved design
// /leikwan-redesign/): the white card, the H5 tab pills and the H5 ＋/− card
// row (blue ＋ closed, red − open, details under a dashed line; one open per
// list via useExpandOne).

export function LkCard({
  children,
  className = "",
  ...rest
}: {
  children: ReactNode;
  className?: string;
} & Record<`data-${string}`, unknown>) {
  return (
    <section
      className={`rounded-2xl border border-divider bg-content1 ${className}`}
      {...rest}
    >
      {children}
    </section>
  );
}

export function LkCardTitle({
  title,
  chip,
  hint,
  right,
}: {
  title: string;
  chip?: ReactNode;
  hint?: ReactNode;
  right?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <h2 className="text-base font-semibold">{title}</h2>
        {chip}
        {hint && <span className="text-xs text-default-500">{hint}</span>}
      </div>
      {right}
    </div>
  );
}

/** H5 tab pill: selected = teal tint, others = plain card pill. */
export function LkTab({
  active,
  children,
  onPress,
}: {
  active: boolean;
  children: ReactNode;
  onPress: () => void;
}) {
  return (
    <button
      aria-pressed={active}
      className={`h-9 shrink-0 whitespace-nowrap rounded-full px-4 text-sm transition-colors ${
        active
          ? "bg-teal-500/20 font-semibold text-teal-800 dark:bg-teal-400/20 dark:text-teal-200"
          : "border border-divider bg-content1 text-default-600"
      }`}
      type="button"
      onClick={onPress}
    >
      {children}
    </button>
  );
}

export function LkChevron({ open }: { open: boolean }) {
  return (
    <ChevronRight
      aria-hidden="true"
      className={`shrink-0 text-default-500 transition-transform ${open ? "rotate-90" : ""}`}
      size={16}
    />
  );
}

/** Round ＋/− toggle (blue when closed, red when open). */
export function LkToggle({
  open,
  onToggle,
}: {
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      data-expand-toggle
      aria-expanded={open}
      aria-label={open ? "收起详情" : "展开详情"}
      className={`flex h-7 w-8 shrink-0 items-center justify-center rounded-lg ${
        open
          ? "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300"
          : "bg-blue-500/15 text-blue-700 dark:bg-blue-400/20 dark:text-blue-300"
      }`}
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        onToggle();
      }}
    >
      {open ? <Minus size={16} /> : <Plus size={16} />}
    </button>
  );
}

/** H5 card row: dot + name (wraps) + chip + ＋/−; details below. */
export function LkExpandCard({
  name,
  dot,
  chip,
  leading,
  open,
  onToggle,
  children,
  className = "",
  ...rest
}: {
  name: ReactNode;
  dot?: ReactNode;
  chip?: ReactNode;
  leading?: ReactNode;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
  className?: string;
} & Record<`data-${string}`, unknown>) {
  return (
    <div
      className={`rounded-xl border border-divider bg-content1 ${className}`}
      data-expand-open={open ? "" : undefined}
      data-expand-row=""
      {...rest}
    >
      {/* The whole row toggles; the ＋ button is the accessible control. */}
      {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions */}
      <div
        className="flex min-h-[48px] cursor-pointer select-none items-center gap-2 py-1.5 pl-3 pr-2"
        onClick={onToggle}
      >
        {leading}
        {dot}
        <span className="min-w-0 flex-1 break-words text-sm font-semibold leading-5">
          {name}
        </span>
        {chip}
        <LkToggle open={open} onToggle={onToggle} />
      </div>
      {open && (
        <div
          data-expand-body
          className="mx-2.5 space-y-2 border-t border-dashed border-divider px-1 pb-3 pt-2.5"
        >
          {children}
        </div>
      )}
    </div>
  );
}

/** One label / value line of an H5 card's details. */
export function LkField({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="flex gap-3 text-[13px] leading-6">
      <span className="w-12 shrink-0 text-default-500">{label}</span>
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1 break-all">
        {children}
      </div>
    </div>
  );
}

export function LkDot({ ok = true, title }: { ok?: boolean; title?: string }) {
  return (
    <span
      aria-label={title}
      className={`h-2 w-2 shrink-0 rounded-full ${ok ? "bg-green-500" : "bg-slate-400"}`}
      role="img"
      title={title}
    />
  );
}
