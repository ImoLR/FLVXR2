import type { ReactNode } from "react";

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { TriangleAlert } from "lucide-react";

import { LandingServer } from "@/api/landing";
import { cn } from "@/lib/utils";

export type AttentionLevel = "warn" | "error";
/** 设置 dialog sections that can carry the marker (data-attention-section). */
export type AttentionSection = "egress" | "guard" | "defaults";

export interface AttentionReason {
  key: string;
  level: AttentionLevel;
  text: string;
  /** Sections of the 设置 dialog where the admin acts (first = scroll target). */
  sections: AttentionSection[];
}

export interface AdminAttention {
  level: AttentionLevel;
  reasons: AttentionReason[];
}

/**
 * Plan 218 — THE single place that decides whether a 落地 server needs its
 * admin: the card ⚠ (on 「设置」 and next to the name), the page header
 * 「N 台服务器需要处理」 and the 设置 dialog's banner / section markers all
 * read this function and nothing else.
 *
 * New admin-action states (需重启 / 需确认 / 需升级 / 失败) MUST be added here,
 * from data already in the server list (no extra request per card).
 *
 * Not here on purpose: states only 妙妙屋 can fix on a shared server (its
 * Helper 需升级, its Core restart/upgrade, its guard or scan read errors) —
 * the FLVX admin cannot act on them (the 「共享详情 · 异常」 button already
 * shows 妙妙屋 sync problems). Waiting states (already confirmed, pending
 * sync) are not actions either. Exception (plan 243): a share held by the
 * mass-vanish guard — FLVX refused to auto-revoke, its admin must look.
 */
export function serverAdminAttention(
  server: LandingServer,
): AdminAttention | null {
  const egress = server.egress;

  if (!egress) return null; // non-admin view
  const native = server.source !== "shared";
  const reasons: AttentionReason[] = [];
  const add = (
    key: string,
    level: AttentionLevel,
    text: string,
    sections: AttentionSection[] = ["egress"],
  ) => reasons.push({ key, level, text, sections });

  // Failures (red). A shared server's 出站与路由 error is often FLVX's own
  // rules (panel-side compile), so it counts for both kinds.
  if (egress.state === "error")
    add(
      "egress-error",
      "error",
      egress.error
        ? `出站与路由应用失败：${egress.error}`
        : "出站与路由应用失败",
    );
  if ((egress.withheldNodes ?? 0) > 0)
    add(
      "withheld",
      "error",
      `${egress.withheldNodes} 个落地节点因出站与路由未生效已暂停`,
    );
  // Plan 243: 妙妙屋 dropped many shares at once; FLVX kept them instead of
  // revoking, the admin checks 妙妙屋 (the text says why).
  if (!native && server.sharedHold)
    add("shared-hold", "error", server.sharedHold, ["defaults"]);
  if (!native) return pack(reasons);

  const guard = egress.realityGuard;
  const guardOn = Boolean(guard?.enabled);
  const scan = server.scanGuard?.state;

  if (guardOn && guard?.state === "error")
    add("guard-error", "error", "Reality 防盗未生效：路由规则应用失败", [
      "guard",
    ]);
  if (scan === "error")
    add(
      "scan-error",
      "error",
      "禁止端口扫描检测异常：读取落地 Core 的扫描记录失败",
    );

  // Confirm the Core upgrade (plan 129): 「保存时重启落地 Core 以升级」.
  // 禁止端口扫描 「core」 = the running Core lacks the guard; the same restart
  // fixes it.
  const upgradeConfirmed = Boolean(egress.coreUpgradeConfirmed);

  if (egress.coreUpgradeRequired && !upgradeConfirmed)
    add(
      "core-upgrade",
      "warn",
      scan === "core"
        ? "落地 Core 待升级，禁止端口扫描升级后才生效：勾选「保存时重启落地 Core 以升级」"
        : "落地 Core 待升级：勾选「保存时重启落地 Core 以升级」",
    );
  else if (scan === "core" && !upgradeConfirmed)
    add(
      "scan-core",
      "warn",
      "禁止端口扫描未生效：落地 Core 需要重启一次（新版 Core 下载完成后在这里确认）",
    );

  // Confirm the one restart that enables routing (plan 127):
  // 「保存时重启落地 Core 以启用」. Not before the agent's first report (a
  // fresh server reports ready within a minute), not once confirmed.
  if (!egress.restartConfirmed) {
    const waiting: string[] = [];

    if (egress.abuseDefaults === "restart" && egress.checkedAt > 0)
      waiting.push("内置防滥用规则");
    if (guardOn && guard?.state === "restart") waiting.push("Reality 防盗");
    if (egress.state === "restart_required") waiting.push("出站与路由");
    if (waiting.length)
      add(
        "core-restart",
        "warn",
        `落地 Core 需要重启一次，${waiting.join("、")}才会生效`,
        guardOn && guard?.state === "restart"
          ? ["egress", "guard"]
          : ["egress"],
      );
  }

  // Upgrade the node program (节点页 → 升级).
  const old: [string, AttentionSection][] = [];

  if (!egress.supported) old.push(["出站与路由", "egress"]);
  if (
    guardOn &&
    guard?.state === "agent_old" &&
    server.protocols?.includes("vless_reality_vision")
  )
    old.push(["Reality 防盗", "guard"]);
  if (scan === "agent") old.push(["禁止端口扫描", "egress"]);
  if (server.connModeSupported === false)
    old.push(["节点连接数按入站计算", "defaults"]);
  if (old.length)
    add(
      "agent",
      "warn",
      `节点程序版本过旧，${old.map(([name]) => name).join("、")}未生效：请在「节点」页升级这台服务器的节点程序`,
      Array.from(new Set(old.map(([, section]) => section))),
    );

  return pack(reasons);
}

function pack(reasons: AttentionReason[]): AdminAttention | null {
  if (!reasons.length) return null;

  return {
    level: reasons.some((r) => r.level === "error") ? "error" : "warn",
    reasons,
  };
}

/** Hover title: the reasons, one per line. */
export const attentionTitle = (attention: AdminAttention) =>
  `需要处理：\n${attention.reasons.map((r) => `• ${r.text}`).join("\n")}`;

export const attentionSections = (attention: AdminAttention | null) =>
  new Set(attention?.reasons.flatMap((r) => r.sections) ?? []);

/**
 * The ⚠ sign: amber with a dark 「!」, red with a white one for errors. The
 * red triangle's own outline is red-700 (not the 「!」 colour): a white
 * outline melts into a light card and the sign read a size smaller than the
 * amber one (measured 11 vs 14 px); with the dark outline both read the same
 * on light cards and dark ones, and the layout box is unchanged.
 */
export function AttentionIcon({
  level,
  className,
}: {
  level: AttentionLevel;
  className?: string;
}) {
  return (
    <TriangleAlert
      aria-hidden
      className={cn(
        "h-4 w-4 shrink-0",
        level === "error"
          ? "fill-red-500 text-white dark:text-red-950 [&>path:first-child]:stroke-red-700"
          : "fill-amber-400 text-amber-950",
        className,
      )}
      data-attention-icon={level}
      strokeWidth={2.25}
    />
  );
}

const LONG_PRESS_MS = 450;

/**
 * Long-press (touch/pen) on a marker shows the reasons in a small bubble
 * instead of following the tap; mouse users get the native hover title.
 */
export function useAttentionPeek(attention: AdminAttention | null) {
  const [peek, setPeek] = useState<DOMRect | null>(null);
  const timer = useRef<number>();
  const fired = useRef(false);
  const stop = () => window.clearTimeout(timer.current);

  useEffect(() => {
    if (!peek) return;
    const close = () => setPeek(null);
    const auto = window.setTimeout(close, 6000);

    // Any next touch or scroll closes it (not the release of this press).
    const arm = window.setTimeout(() => {
      window.addEventListener("pointerdown", close, true);
      window.addEventListener("scroll", close, true);
    }, 0);

    return () => {
      window.clearTimeout(auto);
      window.clearTimeout(arm);
      window.removeEventListener("pointerdown", close, true);
      window.removeEventListener("scroll", close, true);
    };
  }, [peek]);
  useEffect(() => stop, []);

  if (!attention) return { handlers: {}, bubble: null };

  return {
    handlers: {
      onPointerDown: (event: React.PointerEvent<HTMLElement>) => {
        fired.current = false;
        if (event.pointerType === "mouse") return;
        const target = event.currentTarget;

        stop();
        timer.current = window.setTimeout(() => {
          fired.current = true;
          setPeek(target.getBoundingClientRect());
        }, LONG_PRESS_MS);
      },
      onPointerUp: stop,
      onPointerCancel: stop,
      onPointerLeave: stop,
      // The tap that ends a long-press does not open 设置.
      onClickCapture: (event: React.MouseEvent<HTMLElement>) => {
        if (!fired.current) return;
        fired.current = false;
        event.preventDefault();
        event.stopPropagation();
      },
      // No system menu over the bubble (Android long-press).
      onContextMenu: (event: React.MouseEvent<HTMLElement>) => {
        if (fired.current) event.preventDefault();
      },
    },
    bubble: peek
      ? createPortal(
          <AttentionBubble anchor={peek} attention={attention} />,
          document.body,
        )
      : null,
  };
}

function AttentionBubble({
  anchor,
  attention,
}: {
  anchor: DOMRect;
  attention: AdminAttention;
}) {
  const width = Math.min(320, window.innerWidth - 16);
  const left = Math.min(
    Math.max(8, anchor.left + anchor.width / 2 - width / 2),
    window.innerWidth - width - 8,
  );
  const below = anchor.bottom + 8 + 160 < window.innerHeight;

  return (
    <div
      data-attention-bubble
      className="flvx-landing fixed z-[100] rounded-lg border border-default-200 bg-white p-3 text-xs shadow-lg dark:bg-zinc-900"
      role="tooltip"
      style={{
        left,
        width,
        ...(below
          ? { top: anchor.bottom + 8 }
          : { bottom: window.innerHeight - anchor.top + 8 }),
      }}
    >
      <AttentionList attention={attention} />
      <p className="mt-2 text-default-500">点「设置」处理</p>
    </div>
  );
}

/** The reasons as a list (bubble and 设置 banner). */
export function AttentionList({
  attention,
  onPick,
}: {
  attention: AdminAttention;
  onPick?: (reason: AttentionReason) => void;
}) {
  return (
    <>
      <p
        className={cn(
          "flex items-center gap-1.5 font-semibold",
          attention.level === "error"
            ? "text-red-700 dark:text-red-300"
            : "text-amber-700 dark:text-amber-300",
        )}
      >
        <AttentionIcon level={attention.level} />
        需要处理 {attention.reasons.length} 项
      </p>
      <ul className="mt-1.5 space-y-1">
        {attention.reasons.map((reason) => (
          <li key={reason.key} className="flex gap-1.5">
            <span
              aria-hidden
              className={cn(
                "mt-[5px] h-1.5 w-1.5 shrink-0 rounded-full",
                reason.level === "error" ? "bg-red-500" : "bg-amber-500",
              )}
            />
            {onPick ? (
              <button
                className="text-left text-foreground underline decoration-dotted underline-offset-2 hover:decoration-solid"
                type="button"
                onClick={() => onPick(reason)}
              >
                {reason.text}
              </button>
            ) : (
              <span className="text-foreground">{reason.text}</span>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}

/** Card: small ⚠ next to the server name; tap opens 设置, long-press peeks. */
export function AttentionNameMarker({
  attention,
  onOpen,
}: {
  attention: AdminAttention;
  onOpen: () => void;
}) {
  const { handlers, bubble } = useAttentionPeek(attention);

  return (
    <>
      <button
        aria-label={attentionTitle(attention)}
        className="-m-1 shrink-0 select-none p-1 [-webkit-touch-callout:none]"
        data-attention-name={attention.level}
        title={attentionTitle(attention)}
        type="button"
        onClick={onOpen}
        {...handlers}
      >
        <AttentionIcon level={attention.level} />
      </button>
      {bubble}
    </>
  );
}

/** Card: the corner ⚠ drawn on the 「设置」 button (part of the button). */
export function AttentionCorner({ level }: { level: AttentionLevel }) {
  return (
    <span
      aria-hidden
      className="pointer-events-none absolute -right-1.5 -top-2 drop-shadow-sm"
      data-attention-corner={level}
    >
      <AttentionIcon level={level} />
    </span>
  );
}

/** 设置 dialog: ⚠ after a section title / at the start of its warning block. */
export function SectionTitle({
  children,
  attention,
}: {
  children: ReactNode;
  attention?: AttentionLevel | null;
}) {
  if (!attention) return <h3 className="font-medium">{children}</h3>;

  return (
    <h3
      className="flex items-center gap-1.5 font-medium"
      data-attention-title={attention}
    >
      {children}
      <AttentionIcon level={attention} />
    </h3>
  );
}

/** The level of the reasons that point at one section (null = none). */
export const sectionLevel = (
  attention: AdminAttention | null,
  section: AttentionSection,
): AttentionLevel | null => {
  const hits = attention?.reasons.filter((r) => r.sections.includes(section));

  if (!hits?.length) return null;

  return hits.some((r) => r.level === "error") ? "error" : "warn";
};
