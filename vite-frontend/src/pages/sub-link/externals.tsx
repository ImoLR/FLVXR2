import { useCallback, useEffect, useState } from "react";
import toast from "react-hot-toast";
import { Download, Plus } from "lucide-react";

import { ExternalList, externalList } from "@/api/landing-external";
import { SubProbeResult, subExternalProbe } from "@/api/sub-link";
import {
  AddExternalDialog,
  ExternalManageList,
} from "@/pages/landing/chain-proxy";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";

// Plan 256: the 订阅 page's entry to the user's 外部节点 — the same list and
// cap as the landing 「链式代理」 dialog, so users without a landing node can
// add their own nodes and put them into subscriptions.

export function SubExternalsDialog({
  isH5,
  onClose,
  onChanged,
}: {
  isH5: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [list, setList] = useState<ExternalList | null>(null);
  const [loadError, setLoadError] = useState("");
  const [adding, setAdding] = useState<"paste" | "sub" | null>(null);
  const [probes, setProbes] = useState<Record<number, SubProbeResult>>({});
  const [probing, setProbing] = useState(false);

  const load = useCallback(async () => {
    const res = await externalList();

    if (res.code === 0 && res.data) {
      setList(res.data);
      setLoadError("");
    } else setLoadError(res.msg || "读取外部节点失败");
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const changed = () => {
    void load();
    onChanged();
  };
  const probeAll = async () => {
    setProbing(true);
    const res = await subExternalProbe();

    setProbing(false);
    if (res.code !== 0 || !res.data) {
      toast.error(res.msg || "检测失败");

      return;
    }
    const next: Record<number, SubProbeResult> = {};

    res.data.items.forEach((r) => (next[r.id] = r));
    setProbes(next);
  };
  const items = list?.items || [];
  const full = !!list?.cap && list.count >= list.cap;

  return (
    <>
      <Modal
        isOpen
        scrollBehavior="inside"
        size={isH5 ? "full" : "lg"}
        onClose={onClose}
      >
        <ModalContent>
          <ModalHeader className="flex items-baseline gap-2">
            <span>外部节点</span>
            {list && (
              <span className="text-sm font-normal text-default-500">
                {list.count}
                {list.cap ? ` / ${list.cap}` : ""}
              </span>
            )}
          </ModalHeader>
          <ModalBody data-sub-externals className="space-y-3">
            <div className="flex flex-wrap items-center gap-2">
              <Button
                color="success"
                isDisabled={full}
                size="sm"
                startContent={<Plus size={14} />}
                title={full ? `外部节点最多 ${list?.cap} 个` : undefined}
                onPress={() => setAdding("paste")}
              >
                粘贴链接
              </Button>
              <Button
                color="warning"
                isDisabled={full}
                size="sm"
                startContent={<Download size={14} />}
                variant="flat"
                onPress={() => setAdding("sub")}
              >
                从订阅导入
              </Button>
              <span className="flex-1" />
              <Button
                color="secondary"
                isDisabled={!items.length}
                isLoading={probing}
                size="sm"
                variant="flat"
                onPress={() => void probeAll()}
              >
                检测全部
              </Button>
            </div>
            {loadError && <p className="text-xs text-danger">{loadError}</p>}
            {!list && !loadError && <Spinner size="sm" />}
            {list && (
              <ExternalManageList
                items={items}
                probes={probes}
                probing={probing}
                variant="sub"
                onChanged={changed}
                onProbe={() => void probeAll()}
              />
            )}
            <p className="text-xs text-default-500">
              就是落地「链式代理」里的那份外部节点（同一份列表）。链式代理在用的不能删；订阅在用的可以删，删了那些订阅里就没有它了。检测是从面板测的
              TCP 连接时间，只说明端口通不通，和你自己的网速无关。
            </p>
          </ModalBody>
          <ModalFooter>
            <Button color="danger" variant="flat" onPress={onClose}>
              关闭
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      {adding && (
        <AddExternalDialog
          initialTab={adding}
          shared={false}
          onAdded={() => changed()}
          onClose={() => setAdding(null)}
        />
      )}
    </>
  );
}
