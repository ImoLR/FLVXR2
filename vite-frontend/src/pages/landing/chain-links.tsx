import { useState } from "react";
import toast from "react-hot-toast";

import { LandingLinkCard, LinkCardLink, copyToClipboard } from "./link-card";

import {
  ForwardChainInfo,
  ForwardExternalInfo,
  getChainLinks,
  getExternalForwardLinks,
} from "@/api/line-chain";
import {
  OnboardingRing,
  useOnboardingHint,
} from "@/components/onboarding-ring";
import { landingLinks } from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";

interface ChainRow {
  id: number;
  name: string;
  chain?: ForwardChainInfo;
  landingNodeId?: number;
  lineNonCompliant?: boolean;
  lineComplianceAck?: boolean;
  /** Plan 284: the forward's target is one of the owner's 外部节点. */
  externalNode?: ForwardExternalInfo;
}

// Plan 284: 「外部节点 · 协议」 tag of a port forward to the user's own node
// (+ 「自动识别」 for an old 手动填写 forward whose target equals the node).
export function ForwardExternalBadges({
  external,
}: {
  external?: ForwardExternalInfo;
}) {
  if (!external) return null;

  return (
    <span className="inline-flex flex-wrap items-center gap-1" data-external-badges>
      <span
        className="inline-flex items-center whitespace-nowrap rounded-full bg-cyan-500/15 px-2 text-[10px] font-semibold leading-[18px] text-cyan-700 dark:bg-cyan-400/15 dark:text-cyan-300"
        title={`端口转发到你的外部节点「${external.name}」（${external.address}），复制链接 / 订阅里的地址是入口地址`}
      >
        外部节点 · {external.protocolLabel}
      </span>
      {external.auto && (
        <span
          className="inline-flex items-center whitespace-nowrap rounded-full bg-slate-500/15 px-2 text-[10px] font-semibold leading-[18px] text-slate-600 dark:bg-slate-400/15 dark:text-slate-300"
          title="手动填写的目标正好是你的外部节点，已自动识别；编辑时选「我的外部节点」可固定下来"
        >
          自动识别
        </span>
      )}
      {external.mismatch && (
        <span
          className="inline-flex items-center whitespace-nowrap rounded-full bg-amber-500/15 px-2 text-[10px] font-semibold leading-[18px] text-amber-700 dark:bg-amber-400/15 dark:text-amber-300"
          title={`转发目标和外部节点地址 ${external.address} 不一致，请编辑转发重新选择外部节点`}
        >
          ⚠ 地址不一致
        </span>
      )}
      {external.blocked && (
        <span
          className="inline-flex items-center whitespace-nowrap rounded-full bg-red-500/15 px-2 text-[10px] font-semibold leading-[18px] text-red-700 dark:bg-red-400/15 dark:text-red-300"
          title={external.blocked}
        >
          ⚠ 隧道屏蔽
        </span>
      )}
    </span>
  );
}

// Badges next to a rule name: 「我的节点」 for chain rules (with a busy or
// error hint), 「不合规」 for rules a 线路机 tunnel no longer allows and (admin
// only, plan 164) a quiet 「已确认合规」 once an admin confirmed the target.
export function ForwardLineBadges({ forward }: { forward: ChainRow }) {
  const external = !forward.chain ? forward.externalNode : undefined;

  if (
    !forward.chain &&
    !forward.lineNonCompliant &&
    !forward.lineComplianceAck &&
    !external
  )
    return null;
  const chain = forward.chain;

  return (
    <div className="mt-1 flex flex-wrap items-center gap-1">
      {external && <ForwardExternalBadges external={external} />}
      {external && (
        <span
          className="min-w-0 truncate text-xs text-default-500"
          data-external-target-name
          title={`目标：外部节点「${external.name}」 ${external.address}`}
        >
          → {external.name}
        </span>
      )}
      {chain && (
        <Chip color="secondary" size="sm" title={chain.summary} variant="flat">
          我的节点
        </Chip>
      )}
      {chain?.inboundProtocol === "anytls" && (
        <span
          className="inline-flex items-center whitespace-nowrap rounded-full bg-pink-500/15 px-2 text-[10px] font-semibold leading-[18px] text-pink-700 dark:bg-pink-400/20 dark:text-pink-300"
          title="连接协议 AnyTLS（Surge 等不支持 VLESS 的客户端可用）"
        >
          AnyTLS
        </span>
      )}
      {chain && chain.opStartedAt > 0 && (
        <span className="text-xs text-default-500">处理中…</span>
      )}
      {chain && chain.opStartedAt === 0 && chain.error && (
        <span
          className="whitespace-normal text-xs font-medium leading-snug text-warning-600 dark:text-warning-400"
          title={chain.error}
        >
          ⚠ {chain.error}
        </span>
      )}
      {forward.lineNonCompliant && (
        <Chip
          color="warning"
          size="sm"
          title="线路机隧道只能转发到 VLESS Reality Vision / AnyTLS 落地或「我的外部节点」；该规则仍在运行，修改目标后即合规"
          variant="flat"
        >
          不合规
        </Chip>
      )}
      {forward.lineComplianceAck && !forward.lineNonCompliant && (
        <span
          className="inline-flex items-center whitespace-nowrap rounded-full bg-green-500/15 px-2 text-[10px] font-semibold leading-[18px] text-green-700 dark:bg-green-400/15 dark:text-green-300"
          title="管理员已确认目标是 VLESS Reality Vision 或 AnyTLS；目标、落地或隧道改了会自动取消"
        >
          已确认合规
        </span>
      )}
    </div>
  );
}

// One entry copies at once; several entries open the shared link list.
export function ChainLinkButton({
  forward,
  className,
}: {
  forward: ChainRow;
  className?: string;
}) {
  const [links, setLinks] = useState<LinkCardLink[]>([]);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [qr, setQR] = useState("");
  const visible = forward.chain
    ? forward.chain.state === "active"
    : Boolean(forward.landingNodeId || forward.externalNode);
  // Plan 146/169: hint until the user has copied a link once (one rule row plays per visit).
  const linkHint = useOnboardingHint("copyLink", { enabled: visible });

  if (!visible) return null;

  const load = async () => {
    setLoading(true);
    try {
      let res: { code: number; msg: string };
      let entries: LinkCardLink[] = [];

      if (forward.chain) {
        const r = await getChainLinks(forward.id);

        res = r;
        // Plan 175: every link of a rule runs through that rule (经规则转发);
        // chain links carry no kind, landing links keep theirs.
        entries = (r.data || []).map((link) => ({
          ...link,
          kind: "forward" as const,
        }));
      } else if (!forward.landingNodeId && forward.externalNode) {
        // Plan 284: the external node's own link at each entry address.
        const r = await getExternalForwardLinks(forward.id);

        res = r;
        entries = (r.data?.links || []).map((link) => ({
          name: link.name,
          url: link.url,
          blocked: link.blocked,
          kind: "forward" as const,
        }));
      } else {
        const r = await landingLinks(forward.landingNodeId!);

        res = r;
        entries = (r.data || []).filter(
          (link) => "forwardId" in link && link.forwardId === forward.id,
        );
      }

      if (res.code !== 0 || !entries.length) {
        toast.error(res.msg || "链接获取失败");

        return;
      }
      setQR("");
      // One plain link copies at once; a link-less entry (Snell: client
      // configs only) opens the dialog instead of copying an empty string.
      // Plan 250: an AnyTLS chain link opens it too — the anytls:// link
      // cannot carry the certificate pin, the Clash / Surge configs do.
      if (
        entries.length === 1 &&
        entries[0].url &&
        !(forward.chain && entries[0].surge)
      ) {
        const ok = await copyToClipboard(entries[0].url);

        if (ok) toast.success("链接已复制");
        else {
          setLinks(entries);
          setOpen(true);
        }

        return;
      }
      setLinks(entries);
      setOpen(true);
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
      <Button
        ref={linkHint.ref}
        className={`relative ${className || "min-h-7 px-2"}`}
        color="success"
        isLoading={loading}
        size="sm"
        title="复制链接"
        variant="flat"
        onPress={() => {
          linkHint.complete();
          void load();
        }}
      >
        复制链接
        <OnboardingRing active={linkHint.active} />
      </Button>
      <Modal
        isOpen={open}
        scrollBehavior="inside"
        size="lg"
        onClose={() => setOpen(false)}
      >
        {/* Plan 175: same card as the 落地 page 「节点链接」 dialog. */}
        <ModalContent className="flvx-landing">
          <ModalHeader>节点链接 · {forward.name}</ModalHeader>
          <ModalBody className="space-y-4">
            {links.map((link, index) => (
              <LandingLinkCard
                key={`${link.kind}-${index}`}
                link={link}
                qrOpen={qr === link.url}
                onToggleQR={() => setQR(qr === link.url ? "" : link.url)}
              />
            ))}
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setOpen(false)}
            >
              关闭
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  );
}
