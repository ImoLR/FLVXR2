import { Smartphone } from "lucide-react";

import { Pill } from "@/pages/landing/pill";

// Plan 297: the 「手机本地」 badge of a subscription — derived from its entry
// mode (never written into the editable name), so switching the entry back to
// the 主入口 drops it at once.

export const SUB_LOCAL_NAME = "手机本地";

export function SubEntryBadge({
  mode,
  className = "",
}: {
  mode?: string;
  className?: string;
}) {
  if (mode !== "local") return null;

  return (
    <Pill
      className={className}
      icon={<Smartphone size={11} />}
      title="在 App 里用手机本地生成的订阅：利群节点的地址是 127.0.0.1（要在开着利群客户端的设备上用）"
      tone="violet"
    >
      {SUB_LOCAL_NAME}
    </Pill>
  );
}
