import type { ReactNode } from "react";
import type { EntryLabelMap } from "@/components/entry-label";
import type { ForwardChainInfo, ForwardExternalInfo } from "@/api/line-chain";
import type { TreeExternal } from "@/api/landing-tree";

import { useEffect, useState } from "react";
import { ChevronRight, Link2 } from "lucide-react";

import { ChainLinkButton } from "./chain-links";
import { LandingTone, Pill } from "./pill";

import { formatInAddress } from "@/pages/forward/address";
import { Button } from "@/shadcn-bridge/heroui/button";

// Plan 327 — 我的节点 tree. Under a node card hang its children (every
// forward whose target is the node, and 「链式出口 → 外部节点」 when 链式代理
// is on); under an 外部节点 card the 线路机加速 (plan 108 chain) and 端口转发
// (plan 284) forwards that use it. A thin violet line with ├ ticks and a └ on
// the last row (CSS `flvx-mytree-*` in globals.css, mmwx relay-row style);
// up to 3 rows show, the rest fold behind 「再显示 N 条」.

/** The forward-list fields the tree reads (GET /forward/list items). */
export interface TreeForward {
  id: number;
  name: string;
  userId?: number;
  userName?: string;
  tunnelName?: string;
  inIp?: string;
  inPort?: number;
  status: number;
  mode?: string;
  landingNodeId?: number;
  chain?: ForwardChainInfo;
  externalNode?: ForwardExternalInfo;
  entryLabels?: EntryLabelMap;
}

export type TreeKidKind = "forward" | "chain-exit" | "line" | "port";

export interface TreeKid {
  /** forward:<id> or exit:<node id> — also the focus target key. */
  key: string;
  kind: TreeKidKind;
  forward?: TreeForward;
  /** chain-exit: the 外部节点 the node exits through. */
  exit?: { id: number; name: string; protocolLabel?: string; state?: string };
}

/** 外部节点 card of the tree; virtual = a pasted 线路机 link not in the list. */
export interface TreeExternalCard extends TreeExternal {
  key: string;
  virtual?: boolean;
}

export const KID_LIMIT = 3;

const KID_META: Record<TreeKidKind, { label: string; tone: LandingTone }> = {
  forward: { label: "转发", tone: "purple" },
  "chain-exit": { label: "链式出口", tone: "teal" },
  line: { label: "线路机加速", tone: "cyan" },
  port: { label: "端口转发", tone: "purple" },
};

/** 入口 text like the forward row (专属域名 / IP in inIp, + N more). */
export function forwardEntryText(forward: TreeForward): string {
  const ip = (forward.inIp || "").replace(/\s/g, "");

  if (!ip) return "";

  return formatInAddress(ip, forward.inPort || 0);
}

function KidStatus({ forward }: { forward: TreeForward }) {
  if (forward.chain && forward.chain.state !== "active") {
    return forward.chain.state === "failed" ? (
      <Pill title={forward.chain.error} tone="red">
        未生效
      </Pill>
    ) : (
      <Pill tone="amber">准备中</Pill>
    );
  }
  if (forward.status === 1) return <Pill tone="green">正常</Pill>;
  if (forward.status === 0) return <Pill tone="amber">暂停</Pill>;

  return <Pill tone="red">异常</Pill>;
}

function KidRow({
  kid,
  glow,
  compact,
  onGlowEnd,
  onLocateExit,
}: {
  kid: TreeKid;
  glow: boolean;
  compact: boolean;
  onGlowEnd: () => void;
  onLocateExit?: (externalId: number) => void;
}) {
  const meta = KID_META[kid.kind];
  const forward = kid.forward;
  const tag = <Pill tone={meta.tone}>{meta.label}</Pill>;

  if (kid.kind === "chain-exit" && kid.exit) {
    const exit = kid.exit;

    return (
      <div className="flvx-mytree-kid" data-mytree-kid={kid.key}>
        <div
          className={`flvx-mytree-kid-box flex min-h-9 items-center gap-2 rounded-md px-2.5 py-1.5${glow ? " flvx-mytree-glow" : ""}`}
        >
          {tag}
          <span
            className="min-w-0 flex-1 truncate text-sm"
            title={`→ 我的外部节点「${exit.name}」`}
          >
            <span className="text-default-500">
              {compact ? "→ " : "→ 我的外部节点"}
            </span>
            「<span className="font-medium">{exit.name}</span>」
            {exit.protocolLabel && (
              <span className="ml-1 text-xs text-default-500">
                {exit.protocolLabel}
              </span>
            )}
          </span>
          {exit.state === "syncing" && <Pill tone="amber">同步中</Pill>}
          {exit.state === "error" && <Pill tone="red">未生效</Pill>}
          {onLocateExit && (
            <Button
              className="h-7 min-h-7 shrink-0 px-2"
              color="secondary"
              endContent={<ChevronRight size={14} />}
              size="sm"
              variant="flat"
              onPress={() => onLocateExit(exit.id)}
            >
              {compact ? "查看" : "查看节点"}
            </Button>
          )}
        </div>
      </div>
    );
  }
  if (!forward) return null;
  const entry = forwardEntryText(forward);
  const label =
    kid.kind === "line" ? forward.tunnelName || forward.name : forward.name;
  const sub =
    kid.kind === "line"
      ? forward.name !== label
        ? forward.name
        : ""
      : forward.tunnelName || "";

  return (
    <div className="flvx-mytree-kid" data-mytree-kid={kid.key}>
      <div
        className={`flvx-mytree-kid-box rounded-md px-2.5 py-1.5${glow ? " flvx-mytree-glow" : ""}`}
      >
        <div className="flex min-h-6 items-center gap-2">
          {tag}
          <span
            className="min-w-0 flex-1 truncate text-sm font-medium"
            title={label}
          >
            {label}
            {!compact && sub && (
              <span className="ml-1.5 text-xs font-normal text-default-500">
                · {sub}
              </span>
            )}
          </span>
          {!compact && entry && (
            <span
              className="hidden max-w-[40%] shrink truncate text-xs tabular-nums text-default-600 md:inline"
              title={`入口 ${entry}`}
            >
              入口 {entry}
            </span>
          )}
          {!compact && <KidStatus forward={forward} />}
          <span className="shrink-0">
            <ChainLinkButton
              className="h-7 min-h-7 px-2"
              forward={forward}
              ring={glow}
              onPressed={onGlowEnd}
            />
          </span>
        </div>
        {compact && (
          <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-default-500">
            <span className="min-w-0 flex-1 truncate tabular-nums">
              {entry ? `入口 ${entry}` : "入口准备中"}
              {sub ? ` · ${sub}` : ""}
            </span>
            <KidStatus forward={forward} />
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * The child rows under one card. `forceOpen` = a focus target is folded
 * away (the fold opens by itself on arrival).
 */
export function TreeKids({
  kids,
  glowKey,
  compact,
  forceOpen = false,
  onGlowEnd,
  onLocateExit,
  tone = "forward",
}: {
  kids: TreeKid[];
  glowKey: string;
  compact: boolean;
  forceOpen?: boolean;
  onGlowEnd: () => void;
  onLocateExit?: (externalId: number) => void;
  tone?: "forward" | "external";
}) {
  const [all, setAll] = useState(false);

  // An arrival on a folded row opens the fold (and it stays open).
  useEffect(() => {
    if (forceOpen) setAll(true);
  }, [forceOpen]);
  if (!kids.length) return null;
  const shown = all ? kids : kids.slice(0, KID_LIMIT);
  const rest = kids.length - KID_LIMIT;

  return (
    <div
      className={`flvx-mytree-kids${tone === "external" ? " flvx-mytree-kids-ext" : ""}`}
      data-mytree-kids=""
    >
      {shown.map((kid) => (
        <KidRow
          key={kid.key}
          compact={compact}
          glow={glowKey === kid.key}
          kid={kid}
          onGlowEnd={onGlowEnd}
          onLocateExit={onLocateExit}
        />
      ))}
      {rest > 0 && (
        <div className="flvx-mytree-kid flvx-mytree-kid-fold">
          <button
            className="flvx-mytree-fold inline-flex h-7 items-center gap-1 rounded-full px-3 text-xs font-semibold"
            data-mytree-fold={all ? "less" : "more"}
            type="button"
            onClick={() => setAll(!all)}
          >
            {all ? "收起" : `再显示 ${rest} 条`}
          </button>
        </div>
      )}
    </div>
  );
}

/** 外部节点 card (name, protocol, address, owner, uses). */
export function ExternalNodeCard({
  card,
  owner,
  chainUsers,
  glow,
  compact,
  onLocateNode,
}: {
  card: TreeExternalCard;
  owner?: ReactNode;
  /** names of the panel nodes exiting through it (链式代理). */
  chainUsers: { id: number; name: string }[];
  glow: boolean;
  compact: boolean;
  onLocateNode: (nodeId: number) => void;
}) {
  const address =
    card.host && card.port
      ? `${card.host.includes(":") ? `[${card.host}]` : card.host}:${card.port}`
      : "";

  return (
    <div
      className={`rounded-xl border border-divider bg-content1 px-3 py-2.5 shadow-sm${glow ? " flvx-mytree-glow" : ""}`}
      data-mytree-external={card.key}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        <Link2
          aria-hidden
          className="h-4 w-4 shrink-0 text-indigo-500 dark:text-indigo-300"
        />
        <span
          className="min-w-0 max-w-full truncate text-sm font-bold"
          title={card.name}
        >
          {card.name}
        </span>
        {card.protocolLabel && <Pill tone="pink">{card.protocolLabel}</Pill>}
        {owner}
        {card.virtual && (
          <Pill
            title="线路机转发里直接粘贴的节点链接，不在「外部节点」列表里"
            tone="slate"
          >
            未加入外部节点列表
          </Pill>
        )}
        {!compact && address && (
          <span className="min-w-0 truncate text-xs tabular-nums text-default-500">
            {address}
          </span>
        )}
        {chainUsers.length > 0 && (
          <button
            className="inline-flex max-w-full shrink-0 items-center gap-1 whitespace-nowrap rounded-full bg-teal-500/15 px-2 text-xs font-semibold leading-[18px] text-teal-700 hover:brightness-95 dark:bg-teal-400/15 dark:text-teal-300"
            data-mytree-chain-users=""
            title={`链式出口：${chainUsers.map((n) => n.name).join("、")}`}
            type="button"
            onClick={() => onLocateNode(chainUsers[0].id)}
          >
            被 {chainUsers.length} 个面板节点用作链式出口
            <ChevronRight aria-hidden className="h-3 w-3" />
          </button>
        )}
      </div>
      {compact && address && (
        <p className="mt-1 truncate text-xs tabular-nums text-default-500">
          {address}
        </p>
      )}
    </div>
  );
}
