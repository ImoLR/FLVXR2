import type { LandingConnMode, LandingConnSumMode } from "@/api/landing";

import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// Plan 141: a landing connection limit counts either 仅入站 (client
// connections, the same meaning as a rule's 连接数) or 入站+出站 (every TCP
// socket of the node's port, today's behaviour).
export const CONN_MODE_NAME: Record<LandingConnMode, string> = {
  inbound: "仅入站",
  total: "入站+出站",
};

export const CONN_MODE_HINT =
  "仅入站 = 客户端连进来的连接，与规则连接数一致；入站+出站 = 该端口进出全部 TCP（含 TIME_WAIT/CLOSE_WAIT），通常约为入站的 2 倍以上";

/** 「(入站)」 / 「(入站+出站)」 / 「(按各节点方式)」 after a displayed number. */
export const connModeTag = (mode?: LandingConnSumMode | null) =>
  mode === "inbound"
    ? "(入站)"
    : mode === "mixed"
      ? "(按各节点方式)"
      : "(入站+出站)";

/** Why a 仅入站 setting does not apply yet (old agent / 妙妙屋 Helper). */
export const connModePendingText = (source?: string) =>
  source === "shared"
    ? "仅入站需要妙妙屋侧升级后生效，目前仍按入站+出站计算"
    : "仅入站需要升级节点后生效，目前仍按入站+出站计算";

export function ConnModeSelect({
  label = "连接数计算方式",
  value,
  inheritMode,
  description,
  isDisabled,
  onChange,
}: {
  label?: string;
  /** null = follow the server (only offered when inheritMode is set). */
  value: LandingConnMode | null;
  inheritMode?: LandingConnMode;
  description?: React.ReactNode;
  isDisabled?: boolean;
  onChange: (mode: LandingConnMode | null) => void;
}) {
  const key = value ?? "inherit";

  return (
    <Select
      description={description}
      isDisabled={isDisabled}
      label={label}
      selectedKeys={[key]}
      variant="bordered"
      onSelectionChange={(keys) => {
        const next = String(Array.from(keys)[0] || key);

        onChange(next === "inherit" ? null : (next as LandingConnMode));
      }}
    >
      {[
        ...(inheritMode
          ? [
              <SelectItem key="inherit">
                {`跟随服务器（${CONN_MODE_NAME[inheritMode]}）`}
              </SelectItem>,
            ]
          : []),
        <SelectItem key="inbound">仅入站</SelectItem>,
        <SelectItem key="total">入站+出站</SelectItem>,
      ]}
    </Select>
  );
}
