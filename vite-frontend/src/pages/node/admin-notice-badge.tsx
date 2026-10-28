import { TriangleAlert } from "lucide-react";

import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// 节点提示 (plan 154): the admin-written notice of a node, same family as the
// IPv6 RA badge. node/list only returns it to admins. `compact` drops the
// 「提示」 label (icon only) where other badges already crowd the node name.
export function NodeAdminNoticeBadge({
  notice,
  compact = false,
}: {
  notice?: string | null;
  compact?: boolean;
}) {
  const text = notice?.trim();

  if (!text) return null;

  return (
    <Dropdown>
      <DropdownTrigger>
        <Button
          aria-label="节点提示"
          className="h-6 min-w-0 shrink-0 gap-1 px-1 text-xs text-warning"
          color="warning"
          data-testid="node-admin-notice-badge"
          size="sm"
          title={text}
          variant="light"
          onClick={(event) => event.stopPropagation()}
        >
          <TriangleAlert aria-hidden="true" className="h-4 w-4 shrink-0" />
          <span className={compact ? "hidden" : "hidden sm:inline"}>提示</span>
        </Button>
      </DropdownTrigger>
      <DropdownMenu aria-label="节点提示详情">
        <DropdownMenuLabel className="max-w-[min(22rem,calc(100vw-2rem))] whitespace-normal text-xs font-normal">
          <p className="flex items-center gap-1 font-medium text-warning">
            <TriangleAlert
              aria-hidden="true"
              className="h-3.5 w-3.5 shrink-0"
            />
            节点提示
          </p>
          <p className="mt-1.5 whitespace-pre-line break-words text-foreground">
            {text}
          </p>
          <p className="mt-2 text-default-500">仅管理员可见</p>
        </DropdownMenuLabel>
      </DropdownMenu>
    </Dropdown>
  );
}
