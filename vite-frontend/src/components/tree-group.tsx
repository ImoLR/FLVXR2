import type { ReactNode } from "react";

import { Button } from "@/shadcn-bridge/heroui/button";

// Plans 163 / 173 / 175 (rules page), shared with the tunnel page (plan 178):
// one look for collapsible groups — collapsed header, the open group's teal
// accent + sticky tinted header + tree line (CSS in styles/globals.css,
// `flvx-rule-group*` / `flvx-rule-tree*`) and the 「收起 · 组名」 footer.

export const treeGroupWrapperClass = (collapsed: boolean): string =>
  `flvx-rule-group overflow-hidden supports-[overflow:clip]:overflow-clip rounded-lg border border-divider bg-content1${collapsed ? "" : " flvx-rule-group-open"}`;

// The open group's header sticks (solid tint); collapsed headers are plain.
export const treeGroupHeaderClass = (collapsed: boolean): string =>
  collapsed
    ? "flex items-center justify-between bg-default-100/50 hover:bg-default-200/50 px-4 py-2.5 max-md:px-2 max-md:gap-1"
    : "flvx-rule-group-head-open sticky z-20 flex items-center justify-between border-b px-4 py-2.5 max-md:px-2 max-md:gap-1";

export const TreeGroupHeader = ({
  collapsed,
  className,
  onToggle,
  children,
}: {
  collapsed: boolean;
  className: string;
  onToggle: () => void;
  children: ReactNode;
}) => (
  <div
    aria-expanded={!collapsed}
    className={`${className} cursor-pointer select-none transition-colors`}
    data-group-header=""
    role="button"
    tabIndex={0}
    onClick={onToggle}
    onKeyDown={(e) => {
      if (e.target !== e.currentTarget) return;
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        onToggle();
      }
    }}
  >
    {children}
  </div>
);

export const TreeGroupChevron = ({ collapsed }: { collapsed: boolean }) => (
  <Button
    isIconOnly
    aria-label={collapsed ? "展开分组" : "折叠分组"}
    className="h-7 w-7 min-w-7 pointer-events-none"
    size="sm"
    variant="flat"
  >
    <svg
      aria-hidden="true"
      className={`h-4 w-4 transition-transform ${collapsed ? "-rotate-90" : "rotate-0"}`}
      fill="none"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="2"
      viewBox="0 0 24 24"
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  </Button>
);

// Plan 173: a clear end of the open group; closes it like the header.
export const TreeGroupFooter = ({
  name,
  onToggle,
}: {
  name: string;
  onToggle: () => void;
}) => (
  <button
    aria-label={`收起 ${name}`}
    className="flvx-rule-group-footer flex w-full items-center justify-center border-t px-3 py-2"
    type="button"
    onClick={onToggle}
  >
    <span className="flvx-rule-group-footer-pill inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium">
      <svg
        aria-hidden="true"
        className="h-3.5 w-3.5 shrink-0"
        fill="none"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="2"
        viewBox="0 0 24 24"
      >
        <path d="m18 15-6-6-6 6" />
      </svg>
      <span className="shrink-0">收起 ·</span>
      <span className="min-w-0 truncate">{name}</span>
    </span>
  </button>
);
