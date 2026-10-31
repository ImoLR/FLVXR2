import type { ReactNode } from "react";

import { Copy, QrCode } from "lucide-react";
import toast from "react-hot-toast";

import { Pill } from "./pill";

import { Button } from "@/shadcn-bridge/heroui/button";
import { OfflineQRCode } from "@/pages/node/node-deploy-modal";

// Plan 175: one link card for the 落地 page 「节点链接」 dialog and the rules
// page 「复制链接」 dialog (tinted 直连 / 经规则转发 pill, name, mono address,
// link, copy + QR, optional client configs).

export async function copyToClipboard(value: string): Promise<boolean> {
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

// Plan 149: `host:port` of a share link (`scheme://[user@]host:port…`), or ""
// when the link does not carry it in that form (e.g. legacy base64 ss://).
export function linkAddress(url?: string) {
  const m =
    /^[a-z][a-z0-9+.-]*:\/\/(?:[^@/?#]*@)?(\[[0-9a-f:.]+\]|[a-z0-9.-]+):(\d{1,5})(?:[/?#]|$)/i.exec(
      url || "",
    );

  return m ? `${m[1]}:${m[2]}` : "";
}

// Plan 121: client config block (Clash / mihomo entry, Surge line) shown only
// for AnyTLS / Snell / Mieru links.
export function LandingClientConfig({
  label,
  value,
}: {
  label: string;
  value?: string;
}) {
  if (!value) return null;

  return (
    <div className="space-y-2">
      <p className="text-xs font-medium text-default-600">{label}</p>
      <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded bg-default-100 p-2 text-xs">
        {value}
      </pre>
      <Button
        color="success"
        size="sm"
        startContent={<Copy size={15} />}
        variant="flat"
        onPress={() =>
          void copyToClipboard(value).then((ok) =>
            ok ? toast.success(`${label} 配置已复制`) : toast.error("复制失败"),
          )
        }
      >
        复制 {label} 配置
      </Button>
    </div>
  );
}

export interface LinkCardLink {
  name: string;
  url: string;
  /** "direct" = straight to the landing node; anything else runs through a rule. */
  kind?: "direct" | "forward";
  clash?: string;
  surge?: string;
}

export function LandingLinkCard({
  link,
  qrOpen,
  onToggleQR,
  children,
}: {
  link: LinkCardLink;
  qrOpen: boolean;
  onToggleQR: () => void;
  /** Protocol notes shown between the QR code and the client configs. */
  children?: ReactNode;
}) {
  const direct = link.kind === "direct";
  const address = linkAddress(link.url);

  return (
    <div
      className="space-y-2 rounded-lg border border-divider p-3"
      data-link-card={direct ? "direct" : "forward"}
    >
      <h3 className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 font-medium">
        <Pill tone={direct ? "green" : "purple"}>
          {direct ? "直连" : "经规则转发"}
        </Pill>
        {!(direct && link.name === "直连") && (
          <span className="min-w-0 break-all">{link.name}</span>
        )}
        {address && (
          <span className="break-all font-mono text-xs font-normal text-default-500">
            {address}
          </span>
        )}
      </h3>
      {link.url && (
        <p className="break-all rounded bg-default-100 p-2 text-xs">
          {link.url}
        </p>
      )}
      {link.url && (
        <div className="flex gap-2">
          <Button
            color="success"
            size="sm"
            startContent={<Copy size={15} />}
            variant="flat"
            onPress={() =>
              void copyToClipboard(link.url).then((ok) =>
                ok ? toast.success("链接已复制") : toast.error("复制失败"),
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
            onPress={onToggleQR}
          >
            二维码
          </Button>
        </div>
      )}
      {link.url && qrOpen && (
        <div className="flex justify-center py-2">
          <OfflineQRCode value={link.url} />
        </div>
      )}
      {children}
      <LandingClientConfig label="Clash / mihomo" value={link.clash} />
      <LandingClientConfig label="Surge" value={link.surge} />
    </div>
  );
}
