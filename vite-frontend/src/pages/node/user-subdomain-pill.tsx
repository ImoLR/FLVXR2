import type { NodeDDNSView } from "@/api/ddns";

import { useState } from "react";

import { UserSubdomainPanel } from "./user-subdomain-panel";

import { useH5Mode } from "@/hooks/useH5Mode";
import { AttentionIcon } from "@/pages/landing/admin-attention";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";

// Plan 303: node card / list pill 「专属域名 N」 beside 「DDNS 已同步」
// (N = names pointing at this node now, moved-in ones included); the ⚠
// variant marks a sync failure, a full zone or a wildcard record (admin
// attention). Tap opens the 「专属域名 · 节点」 dialog. Admin only (the node
// list only carries the DDNS view for admins).

export function NodeUserSubdomainPill({
  ddns,
  nodeId,
  nodeName,
}: {
  ddns?: NodeDDNSView | null;
  nodeId: number;
  nodeName: string;
}) {
  const [open, setOpen] = useState(false);
  const isH5 = useH5Mode();
  const v = ddns?.userSubdomain;

  if (!v || (!v.toggle && v.active + v.paused === 0)) return null;
  const warn = v.attention;
  const title = [
    `生效 ${v.active} · 暂停 ${v.paused}`,
    v.failed ? `同步失败 ${v.failed}` : "",
    v.capped ? `${v.capped} 个用户因记录数已满改用主域名` : "",
    v.wildcard ? "区域有通配记录" : "",
    v.zoneError,
    v.toggle ? "" : "已关闭（名字保留）",
  ]
    .filter(Boolean)
    .join("\n");

  return (
    <>
      <Button
        aria-label={`专属域名 ${v.active}`}
        className={`h-6 min-w-0 shrink-0 gap-1 rounded-full px-2 text-[11px] ${
          warn
            ? "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300"
            : v.toggle
              ? "bg-teal-500/15 text-teal-700 dark:bg-teal-400/15 dark:text-teal-300"
              : "bg-slate-500/15 text-slate-600 dark:bg-slate-400/20 dark:text-slate-300"
        }`}
        data-attention-name={warn ? "error" : undefined}
        data-testid="user-subdomain-pill"
        size="sm"
        title={title}
        variant="flat"
        onClick={(event) => event.stopPropagation()}
        onPress={() => setOpen(true)}
      >
        {warn ? <AttentionIcon className="h-3.5 w-3.5" level="error" /> : null}
        专属域名 {v.active}
      </Button>
      <Modal
        isOpen={open}
        scrollBehavior="inside"
        size="4xl"
        onOpenChange={setOpen}
      >
        <ModalContent data-user-sub-dialog>
          <ModalHeader>
            <span className="min-w-0 truncate">专属域名 · {nodeName}</span>
          </ModalHeader>
          <ModalBody className="pb-5">
            <UserSubdomainPanel isH5={isH5} nodeId={nodeId} />
          </ModalBody>
        </ModalContent>
      </Modal>
    </>
  );
}
