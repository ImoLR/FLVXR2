import type { ForwardChainInfo, ForwardExternalInfo } from "@/api/line-chain";

import { ChevronRight } from "lucide-react";
import { useNavigate } from "react-router-dom";

import {
  OnboardingRing,
  useOnboardingHint,
} from "@/components/onboarding-ring";
import { Button } from "@/shadcn-bridge/heroui/button";

// Plan 327: one link action per forward row, by target type.
// - 我的面板节点 / 我的外部节点 (plain or 线路机 chain): 「复制链接 ›」 opens
//   我的节点 at that forward (/landing?focus=forward:<id>): the group opens,
//   the forward's child row glows and its 复制链接 rings (15 s or a click).
// - 手动填写 (纯转发): 「复制地址」 = the entry host:port (专属域名 when the
//   forward has one, else IP — what inIp carries, like the 入口地址 cell);
//   several entries open the page's address list.

interface LinkActionForward {
  id: number;
  inIp?: string;
  inPort?: number;
  mode?: string;
  landingNodeId?: number;
  chain?: ForwardChainInfo;
  externalNode?: ForwardExternalInfo;
}

/** host:port of one inIp entry (bare IPv6 gets brackets). */
export function entryAddress(raw: string, port: number): string {
  const host = raw.trim();

  if (host.startsWith("["))
    return /\]:\d+$/.test(host) ? host : `${host}:${port}`;
  const colons = (host.match(/:/g) || []).length;

  if (colons > 1) return `[${host}]:${port}`;
  if (colons === 1) return host;

  return `${host}:${port}`;
}

export type ForwardLinkKind = "link" | "address" | null;

export function forwardLinkKind(forward: LinkActionForward): ForwardLinkKind {
  if (forward.chain) return forward.chain.state === "active" ? "link" : null;
  if (forward.landingNodeId || forward.externalNode) return "link";
  if (forward.mode === "wg_path") return null;

  return (forward.inIp || "").trim() && forward.inPort ? "address" : null;
}

export function ForwardLinkAction({
  forward,
  className,
  showAddressModal,
}: {
  forward: LinkActionForward;
  className?: string;
  /** The forward page's 复制 / address-list helper. */
  showAddressModal: (
    addressString: string,
    port: number | null,
    title: string,
  ) => void;
}) {
  const navigate = useNavigate();
  const kind = forwardLinkKind(forward);
  // Plan 146/169 hint (was on the row's 复制链接): until it was used once.
  const linkHint = useOnboardingHint("copyLink", { enabled: kind === "link" });

  if (kind === "link")
    return (
      <Button
        ref={linkHint.ref}
        className={`relative ${className || "min-h-7 px-2"}`}
        color="success"
        data-forward-link-go={forward.id}
        endContent={<ChevronRight className="-mr-1" size={14} />}
        size="sm"
        title="到「我的节点」里这条转发复制链接"
        variant="flat"
        onPress={() => {
          linkHint.complete();
          navigate(`/landing?focus=forward:${forward.id}`);
        }}
      >
        复制链接
        <OnboardingRing active={linkHint.active} />
      </Button>
    );
  if (kind === "address")
    return (
      <Button
        className={className || "min-h-7 px-2"}
        color="success"
        data-forward-copy-address={forward.id}
        size="sm"
        title="复制入口地址（地址:端口）"
        variant="flat"
        onPress={() =>
          showAddressModal(
            (forward.inIp || "")
              .split(",")
              .filter((item) => item.trim())
              .map((item) => entryAddress(item, forward.inPort || 0))
              .join(","),
            null,
            "入口地址",
          )
        }
      >
        复制地址
      </Button>
    );

  return null;
}
