import { useState } from "react";
import toast from "react-hot-toast";

import { LandingLinkCard, LinkCardLink, copyToClipboard } from "./link-card";

import { ForwardChainInfo, getChainLinks } from "@/api/line-chain";
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
}

// Badges next to a rule name: 「我的节点」 for chain rules (with a busy or
// error hint), 「不合规」 for rules a 线路机 tunnel no longer allows and (admin
// only, plan 164) a quiet 「已确认合规」 once an admin confirmed the target.
export function ForwardLineBadges({ forward }: { forward: ChainRow }) {
  if (!forward.chain && !forward.lineNonCompliant && !forward.lineComplianceAck)
    return null;
  const chain = forward.chain;

  return (
    <div className="mt-1 flex flex-wrap items-center gap-1">
      {chain && (
        <Chip color="secondary" size="sm" title={chain.summary} variant="flat">
          我的节点
        </Chip>
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
          title="线路机隧道只能转发到 VLESS Reality Vision 落地或「我自己的节点」；该规则仍在运行，修改目标后即合规"
          variant="flat"
        >
          不合规
        </Chip>
      )}
      {forward.lineComplianceAck && !forward.lineNonCompliant && (
        <span
          className="inline-flex items-center whitespace-nowrap rounded-full bg-green-500/15 px-2 text-[10px] font-semibold leading-[18px] text-green-700 dark:bg-green-400/15 dark:text-green-300"
          title="管理员已确认目标是 VLESS Reality Vision；目标、落地或隧道改了会自动取消"
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
    : Boolean(forward.landingNodeId);
  // Plan 146/169: hint until the user has copied a link once (one rule row plays per visit).
  const linkHint = useOnboardingHint("copyLink", { enabled: visible });

  if (!visible) return null;

  const load = async () => {
    setLoading(true);
    try {
      const res = forward.chain
        ? await getChainLinks(forward.id)
        : await landingLinks(forward.landingNodeId!);
      // Plan 175: every link of a rule runs through that rule (经规则转发);
      // chain links carry no kind, landing links keep theirs.
      const entries: LinkCardLink[] = forward.chain
        ? (res.data || []).map((link) => ({
            ...link,
            kind: "forward" as const,
          }))
        : (res.data || []).filter(
            (link) => "forwardId" in link && link.forwardId === forward.id,
          );

      if (res.code !== 0 || !entries.length) {
        toast.error(res.msg || "链接获取失败");

        return;
      }
      setQR("");
      // One plain link copies at once; a link-less entry (Snell: client
      // configs only) opens the dialog instead of copying an empty string.
      if (entries.length === 1 && entries[0].url) {
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
