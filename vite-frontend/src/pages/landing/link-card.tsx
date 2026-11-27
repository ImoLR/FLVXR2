import type { ReactNode } from "react";

import { Copy, QrCode } from "lucide-react";
import toast from "react-hot-toast";

import { Pill } from "./pill";

import { Button } from "@/shadcn-bridge/heroui/button";
import { OfflineQRCode } from "@/components/qr-code";
import {
  type EntryLabel,
  type EntryLabelMap,
  EntryLabelPills,
  entryLabelOf,
} from "@/components/entry-label";

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
  /**
   * "direct" = straight to the landing node; anything else runs through a
   * rule ("leikwan" = a 利群汇聚 rule, plan 272).
   */
  kind?: "direct" | "forward" | "leikwan";
  /** plan 272 (leikwan): the rule's name and its entry address */
  ruleName?: string;
  address?: string;
  /** Plan 321: 电信 / 联通 / IPv6 of the link's entry address. */
  entryLabel?: EntryLabel;
  clash?: string;
  surge?: string;
  /** plan 193: why a Snell node has no Surge line */
  surgeNote?: string;
  /** plan 224: the rule's tunnel blocks a protocol feature of the node */
  blocked?: string;
}

export function LandingLinkCard({
  link,
  qrOpen,
  onToggleQR,
  children,
  entryLabels,
}: {
  link: LinkCardLink;
  qrOpen: boolean;
  onToggleQR: () => void;
  /** Protocol notes shown between the QR code and the client configs. */
  children?: ReactNode;
  /** Plan 321: host → 电信 / 联通 / IPv6 (the forward row's entryLabels). */
  entryLabels?: EntryLabelMap;
}) {
  const direct = link.kind === "direct";
  // Plan 272: a 利群汇聚 rule's link (violet pill + its entry address).
  const leikwan = link.kind === "leikwan";
  const address = (leikwan && link.address) || linkAddress(link.url);
  // Plan 321: several entries of one node look alike — tag each one.
  const entryLabel =
    link.entryLabel ||
    (address ? entryLabelOf(entryLabels, address) : undefined);

  return (
    <div
      className="space-y-2 rounded-lg border border-divider p-3"
      data-link-card={leikwan ? "leikwan" : direct ? "direct" : "forward"}
    >
      <h3 className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 font-medium">
        <Pill tone={leikwan ? "violet" : direct ? "green" : "purple"}>
          {leikwan ? "利群汇聚" : direct ? "直连" : "经转发"}
        </Pill>
        {!(direct && link.name === "直连") && (
          <span className="min-w-0 break-all">{link.name}</span>
        )}
        {address && (
          <span className="break-all font-mono text-xs font-normal text-default-500">
            {address}
          </span>
        )}
        <EntryLabelPills label={entryLabel} />
      </h3>
      {leikwan && link.ruleName && (
        <p className="text-xs text-default-500">利群转发「{link.ruleName}」</p>
      )}
      {link.blocked && (
        <p className="text-xs font-medium text-warning-600 dark:text-warning-400">
          ⚠ {link.blocked}，此链接无法连接
        </p>
      )}
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
      {link.surgeNote && (
        <p className="text-xs text-default-500">{link.surgeNote}</p>
      )}
    </div>
  );
}
