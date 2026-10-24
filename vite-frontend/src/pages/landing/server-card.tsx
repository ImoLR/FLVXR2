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

export type ChipLineItem = {
  key: string;
  label: string;
  node: ReactNode;
  /**
   * Plan 149: shorter form (e.g. 「强制 IPv4」 for 「出站：强制 IPv4」), used
   * when the full line does not fit, before anything is folded.
   */
  short?: ReactNode;
  /** plan 147: always visible, never folded into 「+N」 (shown first). */
  pin?: boolean;
};

const CHIP_GAP = 4;

type ChipFit = { visible: number; short: boolean };

// Plan 145: one chip line that never wraps. Chips that do not fit are folded
// into a 「+N」 chip; tap/click it (or hover for the title) to see the rest.
// Widths come from a hidden measuring copy, re-checked only on resize.
// Plan 147: pinned chips (warnings) go first and are never folded.
// Plan 149: before folding, the line switches to the items' short forms; the
// 「+N」 fold is only a last-resort safety net.
export function ChipLine({ items: given }: { items: ChipLineItem[] }) {
  const items = [
    ...given.filter((item) => item.pin),
    ...given.filter((item) => !item.pin),
  ];
  const pinned = items.filter((item) => item.pin).length;
  const boxRef = useRef<HTMLDivElement>(null);
  const measureRef = useRef<HTMLDivElement>(null);
  const [fitState, setFit] = useState<ChipFit>({
    visible: items.length,
    short: false,
  });
  const signature = items.map((item) => `${item.key}:${item.label}`).join("|");

  useLayoutEffect(() => {
    const box = boxRef.current;
    const measure = measureRef.current;

    if (!box || !measure) return;
    const sum = (widths: number[]) =>
      widths.reduce((total, w, index) => total + w + (index ? CHIP_GAP : 0), 0);
    const fit = () => {
      const width = box.clientWidth;
      const children = Array.from(measure.children) as HTMLElement[];
      const plus = children.pop();
      const n = children.length / 2;
      const full = children.slice(0, n).map((child) => child.offsetWidth);
      const short = children.slice(n).map((child) => child.offsetWidth);

      if (sum(full) <= width) {
        setFit({ visible: n, short: false });

        return;
      }
      if (sum(short) <= width) {
        setFit({ visible: n, short: true });

        return;
      }
      const reserve = (plus?.offsetWidth ?? 32) + CHIP_GAP;
      let used = 0;
      let count = 0;

      for (const w of short) {
        const next = used + (count ? CHIP_GAP : 0) + w;

        if (next + reserve > width) break;
        used = next;
        count += 1;
      }
      setFit({ visible: Math.max(count, pinned, 1), short: true });
    };

    fit();
    const observer = new ResizeObserver(fit);

    observer.observe(box);
    observer.observe(measure);

    return () => observer.disconnect();
  }, [signature, pinned]);

  const { visible } = fitState;
  const hidden = items.slice(visible);
  const show = (item: ChipLineItem) =>
    fitState.short && item.short ? item.short : item.node;

  return (
    <div data-chip-line className="relative h-6 min-w-0">
      <div
        ref={boxRef}
        className="flex h-6 min-w-0 flex-nowrap items-center gap-1 overflow-hidden"
      >
        {items.slice(0, visible).map((item) => (
          <span key={item.key} className="flex shrink-0">
            {show(item)}
          </span>
        ))}
        {hidden.length > 0 && (
          <Dropdown>
            <DropdownTrigger>
              <button
                data-chip-more
                aria-label={`另外 ${hidden.length} 项：${hidden.map((item) => item.label).join("、")}`}
                className="shrink-0 rounded-full bg-slate-500/15 px-2 text-xs font-semibold leading-[18px] text-slate-600 hover:brightness-95 dark:bg-slate-400/20 dark:text-slate-300"
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
        {items.map((item) => (
          <span key={`s-${item.key}`} className="flex shrink-0">
            {item.short ?? item.node}
          </span>
        ))}
        <span className="shrink-0 rounded-full px-2 text-xs font-semibold leading-[18px]">
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
// button turns warning-coloured with 「· 异常」. Plan 149: tinted purple (the
// 妙妙屋共享 colour) instead of the white bordered button.
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
      color={problem ? "warning" : "secondary"}
      size="sm"
      startContent={<Info size={16} />}
      title={
        problem ? "同步异常或有提示，点开查看共享详情" : "查看妙妙屋共享详情"
      }
      variant="flat"
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
