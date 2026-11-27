import { landingToneClass } from "@/pages/landing/pill";

// Plan 321 (user 11-28): an entry address carries a small tag — the carrier
// (电信 / 联通 / 移动) of its DDNS record and 「IPv6」 when it only resolves to
// IPv6. IPv4 shows nothing. The backend sends `{carrier?, ipv6?}` per host
// (forward list `entryLabels`, tunnel list `inIpLabels`, 订阅 pool
// `entryLabel`, 专属域名 items, 利群入口 `entryLabel`).

export interface EntryLabel {
  carrier?: string;
  ipv6?: boolean;
}

export type EntryLabelMap = Record<string, EntryLabel> | null | undefined;

const carrierTone: Record<string, keyof typeof landingToneClass> = {
  电信: "cyan",
  联通: "violet",
  移动: "teal",
};

/** host:port / [v6]:port / bare host → the map key (lower case, no port). */
export function entryHostKey(address: string): string {
  let h = address.trim().toLowerCase();

  if (h.startsWith("[")) {
    const end = h.indexOf("]");

    return end > 0 ? h.slice(1, end) : h.slice(1);
  }
  const colons = (h.match(/:/g) || []).length;

  if (colons === 1) h = h.slice(0, h.indexOf(":"));

  return h.replace(/\.$/, "");
}

export function entryLabelOf(
  labels: EntryLabelMap,
  address: string,
): EntryLabel | undefined {
  if (!labels) return undefined;
  const l = labels[entryHostKey(address)];

  return l && (l.carrier || l.ipv6) ? l : undefined;
}

const pill =
  "inline-flex shrink-0 items-center rounded-full px-1.5 text-[10px] font-semibold leading-4";

/** The tags of one entry address (nothing for IPv4 without a carrier). */
export function EntryLabelPills({
  label,
  className = "",
}: {
  label?: EntryLabel | null;
  className?: string;
}) {
  if (!label || (!label.carrier && !label.ipv6)) return null;

  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 align-middle ${className}`}
      data-entry-label={`${label.carrier || ""}${label.ipv6 ? " IPv6" : ""}`.trim()}
    >
      {label.carrier && (
        <span
          className={`${pill} ${landingToneClass[carrierTone[label.carrier] || "slate"]}`}
        >
          {label.carrier}
        </span>
      )}
      {label.ipv6 && (
        <span className={`${pill} ${landingToneClass.slate}`}>IPv6</span>
      )}
    </span>
  );
}

/** Text form (selects, titles): 「电信 · IPv6」. */
export function entryLabelText(label?: EntryLabel | null): string {
  if (!label) return "";

  return [label.carrier, label.ipv6 ? "IPv6" : ""].filter(Boolean).join(" · ");
}

/** Under a forward's entry address: the tag of each tagged address — with
 * the host's first label when there are several (「k3ct9xa 电信 IPv6 ·
 * k3cu9xb 联通 IPv6」); untagged (IPv4) addresses are left out. */
export function EntryLabelLine({
  addresses,
  labels,
  className = "",
}: {
  /** "host,host" or "host:port,host:port" */
  addresses: string;
  labels: EntryLabelMap;
  className?: string;
}) {
  if (!labels || !addresses) return null;
  const list = addresses
    .split(",")
    .map((a) => a.trim())
    .filter(Boolean);
  const tagged = list
    .map((a) => ({ host: entryHostKey(a), label: entryLabelOf(labels, a) }))
    .filter((x) => x.label);

  if (tagged.length === 0) return null;
  const many = list.length > 1;

  return (
    <div
      className={`flex flex-wrap items-center gap-x-2 gap-y-0.5 ${className}`}
      data-entry-label-line
      title={list
        .map((a) => {
          const t = entryLabelText(entryLabelOf(labels, a));

          return t ? `${a}：${t}` : a;
        })
        .join("\n")}
    >
      {tagged.map(({ host, label }) => (
        <span key={host} className="inline-flex min-w-0 items-center gap-1">
          {many && (
            <span className="max-w-[72px] truncate text-[10px] text-default-500">
              {host.includes(":") ? host : host.split(".")[0]}
            </span>
          )}
          <EntryLabelPills label={label} />
        </span>
      ))}
    </div>
  );
}
