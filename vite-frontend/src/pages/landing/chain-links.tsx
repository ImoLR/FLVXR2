import { useState } from "react";
import { Copy, QrCode } from "lucide-react";
import toast from "react-hot-toast";

import { ChainLink, ForwardChainInfo, getChainLinks } from "@/api/line-chain";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { OfflineQRCode } from "@/pages/node/node-deploy-modal";

async function copyText(value: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);

      return true;
    }
    const input = document.createElement("textarea");

    input.value = value;
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.appendChild(input);
    input.select();
    const copied = document.execCommand("copy");

    input.remove();

    return copied;
  } catch {
    return false;
  }
}

interface ChainRow {
  id: number;
  name: string;
  chain?: ForwardChainInfo;
  lineNonCompliant?: boolean;
}

// Badges next to a rule name: 「我的节点」 for chain rules (with a busy or
// error hint) and 「不合规」 for rules a 线路机 tunnel no longer allows.
export function ForwardLineBadges({ forward }: { forward: ChainRow }) {
  if (!forward.chain && !forward.lineNonCompliant) return null;
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
    </div>
  );
}

// 「复制链接」 for a chain rule: one entry copies at once; several entries
// open a list (one link per entry, like the landing 转发版 links).
export function ChainLinkButton({
  forward,
  className,
}: {
  forward: ChainRow;
  className?: string;
}) {
  const [links, setLinks] = useState<ChainLink[]>([]);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [qr, setQR] = useState("");

  if (!forward.chain || forward.chain.state !== "active") return null;

  const load = async () => {
    setLoading(true);
    try {
      const res = await getChainLinks(forward.id);

      if (res.code !== 0 || !res.data?.length) {
        toast.error(res.msg || "链接获取失败");

        return;
      }
      if (res.data.length === 1) {
        const ok = await copyText(res.data[0].url);

        if (ok) toast.success("链接已复制");
        else {
          setLinks(res.data);
          setOpen(true);
        }

        return;
      }
      setLinks(res.data);
      setOpen(true);
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
      <Button
        className={className || "min-h-7 px-2"}
        color="success"
        isLoading={loading}
        size="sm"
        title="复制链接"
        variant="flat"
        onPress={() => void load()}
      >
        复制链接
      </Button>
      <Modal
        isOpen={open}
        scrollBehavior="inside"
        size="lg"
        onClose={() => setOpen(false)}
      >
        <ModalContent className="flvx-landing-text">
          <ModalHeader>节点链接 · {forward.name}</ModalHeader>
          <ModalBody className="space-y-4">
            {links.map((link) => (
              <div
                key={link.url}
                className="space-y-2 rounded-lg border border-divider p-3"
              >
                <h3 className="font-medium">经规则转发 · {link.name}</h3>
                <p className="break-all rounded bg-default-100 p-2 text-xs">
                  {link.url}
                </p>
                <div className="flex gap-2">
                  <Button
                    color="success"
                    size="sm"
                    startContent={<Copy size={15} />}
                    variant="flat"
                    onPress={() =>
                      void copyText(link.url).then((ok) =>
                        ok
                          ? toast.success("链接已复制")
                          : toast.error("复制失败"),
                      )
                    }
                  >
                    复制链接
                  </Button>
                  <Button
                    color="secondary"
                    size="sm"
                    startContent={<QrCode size={15} />}
                    variant="flat"
                    onPress={() => setQR(qr === link.url ? "" : link.url)}
                  >
                    二维码
                  </Button>
                </div>
                {qr === link.url && (
                  <div className="flex justify-center py-2">
                    <OfflineQRCode value={link.url} />
                  </div>
                )}
              </div>
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
