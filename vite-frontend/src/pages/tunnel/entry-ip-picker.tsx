import { Button } from "@/shadcn-bridge/heroui/button";
import {
  addressKey,
  nodeIpOptions,
  splitEntryAddresses,
  type NodeIpSource,
} from "@/utils/node-ips";

interface EntryIpPickerProps {
  nodes: NodeIpSource[];
  value: string;
  onChange: (value: string) => void;
}

// Chips for every IP of the selected entry nodes. Ticking writes the address
// into the existing 「入口地址」 textarea (one per line); the textarea stays
// the source of truth, so custom entries typed there are kept.
export function EntryIpPicker({ nodes, value, onChange }: EntryIpPickerProps) {
  const groups = nodes
    .map((node) => ({ node, options: nodeIpOptions(node) }))
    .filter((group) => group.options.length > 0);

  if (groups.length === 0) return null;
  const tokens = splitEntryAddresses(value);
  const selected = new Set(tokens.map(addressKey));
  const toggle = (ip: string) => {
    const key = addressKey(ip);
    const next = selected.has(key)
      ? tokens.filter((token) => addressKey(token) !== key)
      : [...tokens, ip];

    onChange(next.join("\n"));
  };

  return (
    <div
      className="rounded-lg border border-default-200 px-3 py-2 space-y-2"
      data-testid="entry-ip-picker"
    >
      <div className="text-xs font-medium text-default-600">
        选择入口 IP（勾选后写入上面的入口地址）
      </div>
      {groups.map(({ node, options }) => (
        <div
          key={node.id ?? node.name}
          className="flex flex-wrap items-center gap-1.5"
        >
          <span className="text-xs text-default-500 mr-1 max-w-[10rem] truncate">
            {node.name || `节点 ${node.id}`}
          </span>
          {options.map((option) => {
            const active = selected.has(addressKey(option.ip));

            return (
              <Button
                key={option.ip}
                aria-pressed={active}
                className="h-7 min-h-0 px-2 text-xs gap-1 rounded-full"
                color={active ? "success" : "default"}
                size="sm"
                variant={active ? "flat" : "bordered"}
                onPress={() => toggle(option.ip)}
              >
                <span aria-hidden="true">{active ? "✓" : "+"}</span>
                <span className="text-[11px] opacity-80">{option.label}</span>
                <span className="font-mono">{option.ip}</span>
              </Button>
            );
          })}
        </div>
      ))}
      <p className="text-[11px] leading-4 text-default-400">
        只决定用户看到和复制的入口地址；规则仍在节点所有地址上监听（单独设置了监听IP的除外），都不勾选时使用默认入口地址
      </p>
    </div>
  );
}
