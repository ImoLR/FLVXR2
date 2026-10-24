import type { ReactNode } from "react";

import { useLayoutEffect, useRef, useState } from "react";
import { Info } from "lucide-react";

import { SharedCaps } from "./shared-caps";

import { LandingServer, landingDate } from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";

export type ChipLineItem = { key: string; label: string; node: ReactNode };

const CHIP_GAP = 4;

// Plan 145: one chip line that never wraps. Chips that do not fit are folded
// into a 「+N」 chip; tap/click it (or hover for the title) to see the rest.
// Widths come from a hidden measuring copy, re-checked only on resize.
export function ChipLine({ items }: { items: ChipLineItem[] }) {
  const boxRef = useRef<HTMLDivElement>(null);
  const measureRef = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(items.length);
  const signature = items.map((item) => `${item.key}:${item.label}`).join("|");

  useLayoutEffect(() => {
    const box = boxRef.current;
    const measure = measureRef.current;

    if (!box || !measure) return;
    const fit = () => {
      const width = box.clientWidth;
      const children = Array.from(measure.children) as HTMLElement[];
      const plus = children.pop();
      const widths = children.map((child) => child.offsetWidth);
      const total = widths.reduce(
        (sum, w, index) => sum + w + (index ? CHIP_GAP : 0),
        0,
      );

      if (total <= width) {
        setVisible(widths.length);

        return;
      }
      const reserve = (plus?.offsetWidth ?? 32) + CHIP_GAP;
      let used = 0;
      let count = 0;

      for (const w of widths) {
        const next = used + (count ? CHIP_GAP : 0) + w;

        if (next + reserve > width) break;
        used = next;
        count += 1;
      }
      setVisible(Math.max(count, 1));
    };

    fit();
    const observer = new ResizeObserver(fit);

    observer.observe(box);
    observer.observe(measure);

    return () => observer.disconnect();
  }, [signature]);

  const hidden = items.slice(visible);

  return (
    <div className="relative h-6 min-w-0">
      <div
        ref={boxRef}
        className="flex h-6 min-w-0 flex-nowrap items-center gap-1 overflow-hidden"
      >
        {items.slice(0, visible).map((item) => (
          <span key={item.key} className="flex shrink-0">
            {item.node}
          </span>
        ))}
        {hidden.length > 0 && (
          <Dropdown>
            <DropdownTrigger>
              <button
                aria-label={`另外 ${hidden.length} 项：${hidden.map((item) => item.label).join("、")}`}
                className="shrink-0 rounded-md border border-default-300 px-2 text-xs leading-[18px] text-default-600 hover:bg-default-100"
                title={hidden.map((item) => item.label).join("、")}
                type="button"
              >
                +{hidden.length}
              </button>
            </DropdownTrigger>
            <DropdownMenu>
              <div className="flvx-landing flex max-w-[80vw] flex-wrap gap-1 p-2">
                {hidden.map((item) => (
                  <span key={item.key} className="flex">
                    {item.node}
                  </span>
                ))}
              </div>
            </DropdownMenu>
          </Dropdown>
        )}
      </div>
      <div
        ref={measureRef}
        aria-hidden
        className="pointer-events-none invisible absolute left-0 top-0 flex w-max gap-1 whitespace-nowrap"
      >
        {items.map((item) => (
          <span key={item.key} className="flex shrink-0">
            {item.node}
          </span>
        ))}
        <span className="shrink-0 rounded-md border px-2 text-xs leading-[18px]">
          +{items.length}
        </span>
      </div>
    </div>
  );
}

export function sharedHasProblem(server: LandingServer) {
  return Boolean(server.sharedSyncError || server.sharedNotice);
}

// Plan 145: the shared card's read-only block lives behind this button so
// every server card has the same size. Problems stay visible on the card: the
// button turns warning-coloured with 「· 异常」.
export function SharedDetailsButton({
  server,
  onPress,
}: {
  server: LandingServer;
  onPress: () => void;
}) {
  const problem = sharedHasProblem(server);

  return (
    <Button
      className={problem ? undefined : "text-default-600"}
      color={problem ? "warning" : "default"}
      size="sm"
      startContent={<Info size={16} />}
      title={
        problem ? "同步异常或有提示，点开查看共享详情" : "查看妙妙屋共享详情"
      }
      variant={problem ? "flat" : "bordered"}
      onPress={onPress}
    >
      共享详情
      {problem && (
        <span className="ml-0.5 inline-flex items-center gap-1">
          <span
            aria-hidden
            className="h-1.5 w-1.5 shrink-0 rounded-full bg-warning"
          />
          异常
        </span>
      )}
    </Button>
  );
}

// Same content and wording as the block that used to sit inside the card.
export function SharedDetailsDialog({
  server,
  onClose,
}: {
  server: LandingServer | null;
  onClose: () => void;
}) {
  return (
    <Modal
      isOpen={Boolean(server)}
      scrollBehavior="inside"
      size="lg"
      onOpenChange={(open) => !open && onClose()}
    >
      <ModalContent aria-describedby={undefined} className="flvx-landing">
        {server && (
          <>
            <ModalHeader>
              <div>共享详情</div>
              <div className="break-all text-sm font-normal text-default-500">
                {server.name}
              </div>
            </ModalHeader>
            <ModalBody className="space-y-3 text-sm">
              <SharedCaps server={server} />
              <p className="text-default-500">
                最近成功同步：
                {server.sharedLastSyncAt
                  ? landingDate(server.sharedLastSyncAt)
                  : "尚未同步"}
              </p>
              {server.sharedSyncError && (
                <p className="rounded-lg bg-warning-50 p-3 text-warning">
                  同步异常 · {server.sharedSyncError}
                </p>
              )}
              {server.sharedNotice && (
                <p className="rounded-lg bg-warning-50 p-3 text-warning">
                  {server.sharedNotice}
                </p>
              )}
            </ModalBody>
            <ModalFooter>
              <Button color="danger" variant="flat" onPress={onClose}>
                关闭
              </Button>
            </ModalFooter>
          </>
        )}
      </ModalContent>
    </Modal>
  );
}
