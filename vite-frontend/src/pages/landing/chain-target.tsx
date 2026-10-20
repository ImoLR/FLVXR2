import { useEffect, useRef, useState } from "react";

import { ChainParseResult, parseChainLink } from "@/api/line-chain";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Textarea } from "@/shadcn-bridge/heroui/input";

// 「我自己的节点」: the user pastes their own node link; the server parses and
// validates it and only the protocol and address are shown back.
export function ChainTargetInput({
  value,
  currentSummary,
  error,
  onChange,
}: {
  value: string;
  currentSummary?: string;
  error?: string;
  onChange: (value: string) => void;
}) {
  const [parsed, setParsed] = useState<ChainParseResult | null>(null);
  const [parseError, setParseError] = useState("");
  const [checking, setChecking] = useState(false);
  const seq = useRef(0);

  useEffect(() => {
    const link = value.trim();

    setParsed(null);
    setParseError("");
    if (!link) {
      setChecking(false);

      return;
    }
    const current = ++seq.current;

    setChecking(true);
    const timer = window.setTimeout(() => {
      void parseChainLink(link).then((res) => {
        if (current !== seq.current) return;
        setChecking(false);
        if (res.code === 0 && res.data) setParsed(res.data);
        else setParseError(res.msg || "链接无法解析");
      });
    }, 600);

    return () => window.clearTimeout(timer);
  }, [value]);

  return (
    <div className="space-y-2">
      {currentSummary && (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-default-500">当前节点</span>
          <Chip className="text-default-600" size="sm" variant="bordered">
            {currentSummary}
          </Chip>
        </div>
      )}
      <Textarea
        description={
          currentSummary
            ? "如需更换节点，粘贴新的链接；留空保持当前节点，你的转发链接不变"
            : "支持 ss://（含 SS2022）、vless://、vmess://、trojan://、hysteria2://、socks5://、http(s)://"
        }
        errorMessage={error}
        isInvalid={!!error}
        label="我的节点链接"
        maxRows={4}
        minRows={2}
        placeholder="粘贴你自己的节点链接，例如 ss://… 或 vless://…"
        value={value}
        variant="bordered"
        onChange={(e) => onChange(e.target.value)}
      />
      {checking && <p className="text-xs text-default-500">正在解析链接…</p>}
      {parseError && !checking && (
        <p className="text-xs text-danger">{parseError}</p>
      )}
      {parsed && !checking && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-divider p-2 text-sm">
          <span className="text-default-500">将连接到</span>
          <Chip color="secondary" size="sm" variant="flat">
            {parsed.protocolLabel}
          </Chip>
          <span className="font-mono text-foreground">
            {parsed.summary.split(" · ").slice(1).join(" · ")}
          </span>
          {parsed.name && (
            <span className="text-xs text-default-500">（{parsed.name}）</span>
          )}
          {parsed.udp && (
            <span className="w-full text-xs text-default-500">
              UDP 协议的节点无法预先检测连通性，保存后请用客户端实际测试
            </span>
          )}
        </div>
      )}
      <p className="text-xs text-default-500">
        保存后会在隧道出口为你准备专属节点（首次可能需要几十秒），完成后在规则行点「复制链接」导入客户端。按普通规则计费。
      </p>
    </div>
  );
}
