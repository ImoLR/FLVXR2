import { useState } from "react";
import toast from "react-hot-toast";

import { Pill } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import {
  SubPreview,
  SubSource,
  subAgo,
  subSourceCreate,
  subSourceDelete,
  subSourceFetch,
  subSourceRefresh,
} from "@/api/sub-link";

// Plan 185 外部订阅源: like the landing 「从订阅导入」, plus 「保持同步」 —
// the URL is stored (encrypted) and refreshed lazily every 6 h on pulls.

function SourceRow({
  source,
  onChanged,
}: {
  source: SubSource;
  onChanged: () => void;
}) {
  const [refreshing, setRefreshing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const refresh = async () => {
    setRefreshing(true);
    try {
      const res = await subSourceRefresh(source.id);

      if (res.code !== 0) toast.error(res.msg || "更新失败");
      else if (res.data.error) toast.error(`更新失败：${res.data.error}`);
      else toast.success(`已更新，${res.data.nodeCount} 个节点`);
      onChanged();
    } finally {
      setRefreshing(false);
    }
  };
  const remove = async () => {
    setDeleting(true);
    try {
      const res = await subSourceDelete(source.id);

      if (res.code !== 0) {
        toast.error(res.msg || "删除失败");

        return;
      }
      toast.success(
        res.data.removedFrom
          ? `已删除，并从 ${res.data.removedFrom} 条订阅里移除`
          : "已删除",
      );
      setConfirm(false);
      onChanged();
    } finally {
      setDeleting(false);
    }
  };
  const failed = source.lastError !== "";

  return (
    <div
      className="rounded-xl border border-divider px-3 py-2.5"
      data-sub-source={source.id}
    >
      <div className="flex items-center gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate font-semibold">{source.name}</span>
            <Pill tone="amber">{source.nodeCount} 个节点</Pill>
          </div>
          <div className="mt-0.5 text-xs text-default-500">
            <span className="break-all">{source.urlHint}</span>
            {" · "}
            {failed ? (
              <span className="text-red-600 dark:text-red-300">
                上次更新失败（{source.lastError}）
                {source.lastOkAt
                  ? `，继续用 ${subAgo(source.lastOkAt)}的结果`
                  : ""}
              </span>
            ) : (
              <span>{subAgo(source.lastOkAt)}更新成功</span>
            )}
          </div>
        </div>
        {!confirm && (
          <div className="flex shrink-0 gap-1.5">
            <Button
              color="secondary"
              isLoading={refreshing}
              size="sm"
              variant="flat"
              onPress={refresh}
            >
              立即更新
            </Button>
            <Button
              color="danger"
              size="sm"
              variant="flat"
              onPress={() => setConfirm(true)}
            >
              删除
            </Button>
          </div>
        )}
      </div>
      {confirm && (
        <div className="mt-2 flex flex-wrap items-center gap-2 rounded-lg bg-red-500/10 px-2.5 py-2 text-xs text-red-700 dark:bg-red-400/10 dark:text-red-300">
          <span className="flex-1">
            删除「{source.name}」？
            {source.usedBy > 0
              ? `会从 ${source.usedBy} 条订阅里移除它的节点。`
              : "没有订阅在用它。"}
          </span>
          <Button
            color="danger"
            size="sm"
            variant="flat"
            onPress={() => setConfirm(false)}
          >
            取消
          </Button>
          <Button
            color="danger"
            isLoading={deleting}
            size="sm"
            onPress={remove}
          >
            删除
          </Button>
        </div>
      )}
    </div>
  );
}

export function SubSourcesDialog({
  sources,
  cap,
  onChanged,
  onClose,
}: {
  sources: SubSource[];
  cap: number;
  onChanged: () => void;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
  const [fetching, setFetching] = useState(false);
  const [saving, setSaving] = useState(false);
  const [preview, setPreview] = useState<SubPreview | null>(null);
  const full = cap > 0 && sources.length >= cap;

  const fetchNow = async () => {
    setFetching(true);
    setPreview(null);
    try {
      const res = await subSourceFetch(url.trim());

      if (res.code !== 0) {
        toast.error(res.msg || "获取失败");

        return;
      }
      setPreview(res.data);
    } finally {
      setFetching(false);
    }
  };
  const save = async () => {
    if (!preview) return;
    setSaving(true);
    try {
      const res = await subSourceCreate(name.trim(), preview.fetchId);

      if (res.code !== 0) {
        toast.error(res.msg || "保存失败");

        return;
      }
      toast.success(`已添加「${res.data.name}」，${res.data.nodeCount} 个节点`);
      setName("");
      setURL("");
      setPreview(null);
      onChanged();
    } finally {
      setSaving(false);
    }
  };
  const unsupported = preview
    ? preview.items.filter((i) => !i.supported).length
    : 0;

  return (
    <Modal
      isOpen
      scrollBehavior="inside"
      size="lg"
      onOpenChange={(o) => !o && onClose()}
    >
      <ModalContent>
        <ModalHeader>外部订阅源</ModalHeader>
        <ModalBody>
          {sources.length === 0 && (
            <p className="text-sm text-default-500">
              还没有订阅源。把机场的订阅地址加进来，就能把它的节点放进你的订阅里。
            </p>
          )}
          <div className="space-y-2">
            {sources.map((s) => (
              <SourceRow key={s.id} source={s} onChanged={onChanged} />
            ))}
          </div>
          <div className="space-y-2">
            <p className="text-sm font-semibold">添加订阅源</p>
            <div className="flex flex-col gap-2 sm:flex-row">
              <Input
                aria-label="名称"
                className="sm:w-36"
                placeholder="名称"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <Input
                aria-label="订阅地址"
                className="min-w-0 flex-1"
                placeholder="https://… 订阅地址"
                value={url}
                onChange={(e) => {
                  setURL(e.target.value);
                  setPreview(null);
                }}
              />
              <Button
                color="secondary"
                isDisabled={!url.trim() || full}
                isLoading={fetching}
                variant="flat"
                onPress={fetchNow}
              >
                获取
              </Button>
            </div>
            {full && (
              <p className="text-xs text-amber-700 dark:text-amber-300">
                最多 {cap} 个订阅源，删除一个才能再添加。
              </p>
            )}
            {preview && (
              <div className="rounded-xl border border-divider">
                <div className="flex items-center justify-between gap-2 border-b border-divider px-3 py-2 text-sm">
                  <span>
                    获取到 <b>{preview.count}</b> 个可用节点
                    {unsupported > 0 && `（另有 ${unsupported} 个不支持）`}
                    {preview.info > 0 && `，跳过 ${preview.info} 条信息`}
                  </span>
                  <Button
                    color="success"
                    isLoading={saving}
                    size="sm"
                    onPress={save}
                  >
                    保存
                  </Button>
                </div>
                <div className="max-h-56 overflow-y-auto">
                  {preview.items.map((item, i) => (
                    <div
                      key={i}
                      className={`border-b border-divider px-3 py-1.5 last:border-b-0 ${item.supported ? "" : "opacity-50"}`}
                    >
                      <div className="truncate text-sm">{item.name}</div>
                      <div className="truncate text-xs text-default-500">
                        {item.supported
                          ? `${item.protocol} · ${item.detail}`
                          : `${item.protocol} · ${item.reason}`}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
            <p className="text-xs text-default-400">
              支持 Base64 / 节点链接列表 / Clash
              YAML（和链式代理导入同一套解析）。地址加密保存，只用来生成你的订阅，最多{" "}
              {cap} 个源；有客户端拉取你的订阅时，每 6 小时自动更新一次。
            </p>
          </div>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            关闭
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
