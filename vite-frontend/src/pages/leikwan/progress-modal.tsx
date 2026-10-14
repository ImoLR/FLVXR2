import type { NodeApiItem } from "@/api/types";
import type { LeiKwanCheck } from "@/api/leikwan";

import { useEffect, useState } from "react";

import { getNodeList } from "@/api";
import { checkLeiKwanPipe } from "@/api/leikwan";
import { Alert } from "@/shadcn-bridge/heroui/alert";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";

export function LeiKwanProgressModal({
  pipeId,
  onClose,
}: {
  pipeId: number | null;
  onClose: () => void;
}) {
  const [nodes, setNodes] = useState<NodeApiItem[]>([]);
  const [result, setResult] = useState<LeiKwanCheck | null>(null);
  const [error, setError] = useState("");
  const [checking, setChecking] = useState(false);

  useEffect(() => {
    if (!pipeId) return;
    const controller = new AbortController();
    const deadline = Date.now() + 60000;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const expiry = setTimeout(() => {
      controller.abort();
      clearTimeout(timer);
      setChecking(false);
    }, 60000);

    setResult(null);
    setNodes([]);
    void getNodeList().then((response) => {
      if (!controller.signal.aborted && response.code === 0)
        setNodes(response.data || []);
    });
    setError("");
    setChecking(true);
    const check = async () => {
      const response = await checkLeiKwanPipe(pipeId, controller.signal);

      if (controller.signal.aborted) return;
      if (response.code === 0 && response.data) {
        setResult(response.data);
        setError("");
        if (response.data.endToEnd?.success || !response.data.entries?.length) {
          setChecking(false);
          clearTimeout(expiry);

          return;
        }
      } else {
        setError(response.msg || "检查失败");
      }
      if (Date.now() >= deadline) {
        setChecking(false);

        return;
      }
      timer = setTimeout(() => void check(), 3000);
    };

    void check();

    return () => {
      controller.abort();
      clearTimeout(timer);
      clearTimeout(expiry);
    };
  }, [pipeId]);

  return (
    <Modal
      isOpen={pipeId !== null}
      placement="center"
      scrollBehavior="inside"
      size="lg"
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <ModalContent>
        <ModalHeader>管道变更进度</ModalHeader>
        <ModalBody className="space-y-4">
          <div className="flex items-center justify-between gap-3">
            <span>1. 平台已保存</span>
            <Chip color="success" size="sm" variant="flat">
              已完成
            </Chip>
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between gap-3">
              <span>2. VPS 侧监听端口可达</span>
              {checking && <Spinner size="sm" />}
            </div>
            {result?.entries?.map((entry) => (
              <div
                key={entry.nodeId}
                className="flex items-start justify-between gap-3 rounded-lg bg-default-50 p-3 text-sm"
              >
                <div>
                  <p>{entry.nodeName}</p>
                  <p className="mt-1 text-xs text-default-500">
                    {entry.message}
                  </p>
                  {!!nodes.find((node) => node.id === entry.nodeId)
                    ?.leikwanForwards?.length && (
                    <p className="mt-1 text-xs text-default-500">
                      Agent 已检测：
                      {nodes
                        .find((node) => node.id === entry.nodeId)
                        ?.leikwanForwards?.map(
                          (forward) =>
                            `#${forward.forwardId} :${forward.listenPort}${forward.running ? "（监听中）" : "（未监听）"}`,
                        )
                        .join("、")}
                    </p>
                  )}
                </div>
                <Chip
                  color={entry.reachable ? "success" : "warning"}
                  size="sm"
                  variant="flat"
                >
                  {entry.reachable ? "可达" : "未就绪"}
                </Chip>
              </div>
            ))}
            {result && !result.entries?.length && (
              <p className="text-sm text-default-500">
                绑定隧道并选择入口节点后可检查。
              </p>
            )}
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between gap-3">
              <span>3. 管道到落地服务端到端诊断</span>
              <Chip
                color={result?.endToEnd?.success ? "success" : "default"}
                size="sm"
                variant="flat"
              >
                {result?.endToEnd?.success
                  ? "通过"
                  : checking
                    ? "检查中"
                    : result && !result.entries?.length
                      ? "待绑定"
                      : "未通过"}
              </Chip>
            </div>
            <p className="text-sm text-default-500">
              {result?.endToEnd?.message}
            </p>
          </div>
          {error && <Alert color="warning" description={error} />}
          <p className="text-xs text-default-500">
            仅在此窗口打开时检查，最长 60 秒。检查由入口节点的 Agent
            发起，不会再次请求利群平台；关闭窗口即停止。端到端检查需要入口 Agent
            为 fork.45 或更新版本。
          </p>
          {!checking && result?.entries?.length && !result.endToEnd?.success ? (
            <Alert
              color="warning"
              description="本轮检查已结束。请确认 VPS 上的利群客户端、入口 Agent 及落地服务已经就绪后，重新检查。"
            />
          ) : null}
        </ModalBody>
        <ModalFooter>
          <Button variant="flat" onPress={onClose}>
            关闭
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
