// Node 「附加 IP」 helpers. The panel stores extra IPs as a JSON array
// (["2001:db8::2"]); legacy rows may hold a comma/space separated list.

export const MAX_NODE_EXTRA_IPS = 16;

export interface NodeIpSource {
  id?: number;
  name?: string;
  serverIp?: string;
  serverIpV4?: string;
  serverIpV6?: string;
  extraIPs?: string;
}

export interface NodeIpOption {
  ip: string;
  label: string;
  kind: "v4" | "v6" | "server" | "extra";
}

const IPV4 =
  /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

const stripBrackets = (value: string) =>
  value.trim().replace(/^\[(.*)\]$/, "$1");

/** Canonical IP literal (lowercase, compressed IPv6) or null. */
export const canonicalIp = (value: string): string | null => {
  const host = stripBrackets(value);

  if (!host || host.length > 45) return null;
  if (IPV4.test(host)) return host;
  if (!host.includes(":") || /[^0-9a-fA-F:.]/.test(host)) return null;
  try {
    return new URL(`http://[${host}]/`).hostname.replace(/^\[|\]$/g, "");
  } catch {
    return null;
  }
};

const HOSTNAME =
  /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]*[a-z][a-z0-9-]*$/;

/** A DNS name used as an entry address (DDNS domains, plan 142) or null. */
export const canonicalHost = (value: string): string | null => {
  const host = value.trim().toLowerCase().replace(/\.$/, "");

  return HOSTNAME.test(host) && !host.startsWith("-") ? host : null;
};

/** Comparison key for an entry address: canonical IP or lowercased host. */
export const addressKey = (value: string): string =>
  canonicalIp(value) ?? stripBrackets(value).toLowerCase();

export const isIpv6 = (value: string) =>
  (canonicalIp(value) || "").includes(":");

const splitList = (raw: string): string[] =>
  raw
    .split(/[\s,，;；、]+/)
    .map((token) => token.trim())
    .filter(Boolean);

/** Reads the stored value (JSON array or legacy list) into canonical IPs. */
export const parseExtraIPs = (raw?: string | null): string[] => {
  const value = (raw || "").trim();

  if (!value) return [];
  let tokens: string[] | null = null;

  if (value.startsWith("[")) {
    try {
      const parsed = JSON.parse(value);

      if (Array.isArray(parsed)) tokens = parsed.map((item) => String(item));
    } catch {
      tokens = null;
    }
  }
  const out: string[] = [];

  for (const token of tokens ?? splitList(value)) {
    const ip = canonicalIp(token) ?? canonicalHost(token);

    if (ip && !out.includes(ip)) out.push(ip);
  }

  return out;
};

/** Text shown in the node form: one IP per line. */
export const extraIPsToText = (raw?: string | null): string =>
  parseExtraIPs(raw).join("\n");

/** Mirrors the backend checks so the form can flag problems inline. */
export const validateExtraIPsText = (
  text: string,
  primaries: Array<string | undefined>,
): string => {
  const primary = new Set(
    primaries.filter((item) => item?.trim()).map((item) => addressKey(item!)),
  );
  const seen = new Set<string>();

  for (const token of splitList(text)) {
    const ip = canonicalIp(token) ?? canonicalHost(token);

    if (!ip) return `「${token}」不是有效的 IPv4、IPv6 地址或域名`;
    if (primary.has(ip)) return `「${token}」与上面的主 IP 重复`;
    seen.add(ip);
  }
  if (seen.size > MAX_NODE_EXTRA_IPS) {
    return `附加 IP 最多 ${MAX_NODE_EXTRA_IPS} 个`;
  }

  return "";
};

/** Every IP of a node: primary IPv4, primary IPv6, legacy server IP, extras. */
export const nodeIpOptions = (node?: NodeIpSource | null): NodeIpOption[] => {
  if (!node) return [];
  const options: NodeIpOption[] = [];
  const seen = new Set<string>();
  const add = (value: string | undefined, kind: NodeIpOption["kind"]) => {
    const ip = (value || "").trim();

    if (!ip || seen.has(addressKey(ip))) return;
    seen.add(addressKey(ip));
    const v6 = isIpv6(ip);
    const host = !canonicalIp(ip);
    const label =
      kind === "extra"
        ? `附加 ${host ? "域名" : v6 ? "IPv6" : "IPv4"}`
        : kind === "server"
          ? "主地址"
          : host
            ? `${kind === "v6" ? "IPv6" : "IPv4"} 域名`
            : v6
              ? "IPv6"
              : "IPv4";

    options.push({ ip, label, kind });
  };

  add(node.serverIpV4, "v4");
  add(node.serverIpV6, "v6");
  add(node.serverIp, "server");
  for (const ip of parseExtraIPs(node.extraIPs)) add(ip, "extra");

  return options;
};

/** Splits the 入口地址 textarea / stored list into trimmed tokens. */
export const splitEntryAddresses = (value: string): string[] =>
  value
    .split(/[,\r\n]/)
    .map((token) => token.trim())
    .filter(Boolean);
