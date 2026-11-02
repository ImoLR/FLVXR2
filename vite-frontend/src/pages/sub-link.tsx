import { useCallback, useEffect, useState } from "react";
import toast from "react-hot-toast";
import { Download, MoreHorizontal, Plus, RefreshCw } from "lucide-react";

import { SubEditor } from "./sub-link/editor";
import { SubSourcesDialog } from "./sub-link/sources";

import { AnimatedPage } from "@/components/animated-page";
import { useH5Mode } from "@/hooks/useH5Mode";
import { Pill } from "@/pages/landing/pill";
import { copyToClipboard } from "@/pages/landing/link-card";
import { OfflineQRCode } from "@/pages/node/node-deploy-modal";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownItem,
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
import {
  SubItem,
  SubListData,
  markSubMenuSeen,
  maskedSubLink,
  subAgo,
  subDelete,
  subFormatLabel,
  subLinkURL,
  subList,
  subModeLabel,
  subReset,
} from "@/api/sub-link";

// Plan 185 「订阅」: one card per subscription — what is in it (coloured
// counts), the link (copy / QR) and who pulled it last.

function SubCounts({ sub, compact }: { sub: SubItem; compact: boolean }) {
  const c = sub.counts;
  const empty = c.panel + c.external + c.sources.length + c.missing === 0;

  return (
    <div className="flex flex-wrap gap-1.5">
      {c.panel > 0 && (
        <Pill tone="green">
          {compact ? "面板" : "面板节点"} {c.panel}
        </Pill>
      )}
      {c.external > 0 && (
        <Pill tone="purple">
          {compact ? "外部" : "外部节点"} {c.external}
        </Pill>
      )}
      {c.sources.map((s) => (
        <Pill key={s.id} tone="amber">
          {compact
            ? `${s.name} ${s.count}`
            : `${s.name} · ${s.all ? "跟随全部" : "已选"} ${s.count}`}
        </Pill>
      ))}
      {c.missing > 0 && (
        <Pill tone="red">
          {compact ? `${c.missing} 失效` : `${c.missing} 个节点已失效`}
        </Pill>
      )}
      {empty && <Pill tone="slate">还没有节点，点「编辑」添加</Pill>}
    </div>
  );
}

function MoreMenu({
  onReset,
  onDelete,
}: {
  onReset: () => void;
  onDelete: () => void;
}) {
  return (
    <Dropdown placement="bottom-end">
      <DropdownTrigger>
        <Button
          isIconOnly
          aria-label="更多"
          className="min-h-7 min-w-8"
          size="sm"
          variant="flat"
        >
          <MoreHorizontal size={16} />
        </Button>
      </DropdownTrigger>
      <DropdownMenu aria-label="更多">
        <DropdownItem
          className="mb-1 justify-center rounded-lg bg-amber-500/15 font-semibold text-amber-700 focus:bg-amber-500/25 dark:bg-amber-400/15 dark:text-amber-300"
          onPress={onReset}
        >
          重置链接
        </DropdownItem>
        <DropdownItem
          className="justify-center rounded-lg bg-red-500/15 font-semibold text-red-700 focus:bg-red-500/25 dark:bg-red-400/20 dark:text-red-300"
          onPress={onDelete}
        >
          删除
        </DropdownItem>
      </DropdownMenu>
    </Dropdown>
  );
}

function SubCard({
  sub,
  isH5,
  onEdit,
  onQR,
  onReset,
  onDelete,
}: {
  sub: SubItem;
  isH5: boolean;
  onEdit: () => void;
  onQR: () => void;
  onReset: () => void;
  onDelete: () => void;
}) {
  const copy = async () => {
    if (await copyToClipboard(subLinkURL(sub.token)))
      toast.success("订阅链接已复制");
    else toast.error("复制失败，请长按二维码下方的链接手动复制");
  };
  const pulled = sub.lastPullAt
    ? isH5
      ? `${subAgo(sub.lastPullAt)} · ${sub.lastPullClient}`
      : `最近拉取：${subAgo(sub.lastPullAt)} · ${sub.lastPullClient} · 输出 ${sub.lastPullNodes} 个节点`
    : "还没有客户端拉取";

  return (
    <div
      className="rounded-2xl border border-divider bg-content1 p-4 shadow-sm"
      data-sub-card={sub.id}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="mr-1 truncate text-base font-semibold">
            {sub.name}
          </span>
          {!isH5 && (
            <>
              <Pill tone="purple">{subFormatLabel[sub.format]}</Pill>
              <Pill tone={sub.ruleMode === "global" ? "slate" : "teal"}>
                {subModeLabel[sub.ruleMode]}
              </Pill>
            </>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          <Button
            className="min-h-7 px-3"
            color="primary"
            size="sm"
            variant="flat"
            onPress={onEdit}
          >
            编辑
          </Button>
          <MoreMenu onDelete={onDelete} onReset={onReset} />
        </div>
      </div>
      <div className="mt-2">
        <SubCounts compact={isH5} sub={sub} />
      </div>
      {isH5 ? (
        <div className="mt-3 flex gap-2">
          <Button className="flex-1" color="success" size="sm" onPress={copy}>
            复制订阅链接
          </Button>
          <Button color="success" size="sm" variant="flat" onPress={onQR}>
            二维码
          </Button>
        </div>
      ) : (
        <div className="mt-3 flex items-center gap-2 rounded-xl bg-slate-500/10 px-3 py-2 dark:bg-slate-400/10">
          <span className="min-w-0 flex-1 truncate font-mono text-[13px] text-default-700">
            {maskedSubLink(sub.token)}
          </span>
          <Button color="success" size="sm" onPress={copy}>
            复制订阅
          </Button>
          <Button color="success" size="sm" variant="flat" onPress={onQR}>
            二维码
          </Button>
        </div>
      )}
      <p className="mt-2 text-xs text-default-500">{pulled}</p>
    </div>
  );
}

type Confirm = { kind: "reset" | "delete"; sub: SubItem } | null;

export default function SubLinkPage() {
  const isH5 = useH5Mode();
  const [data, setData] = useState<SubListData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<SubItem | "new" | null>(null);
  const [sourcesOpen, setSourcesOpen] = useState(false);
  const [qr, setQR] = useState<SubItem | null>(null);
  const [confirm, setConfirm] = useState<Confirm>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await subList();

      if (res.code === 0) {
        setData(res.data);
        setError("");
      } else setError(res.msg || "加载失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    markSubMenuSeen();
    void load();
  }, [load]);

  const subs = data?.subs || [];
  const cap = data?.cap || 0;
  const full = cap > 0 && subs.length >= cap;
  const runConfirm = async () => {
    if (!confirm) return;
    setBusy(true);
    try {
      const res =
        confirm.kind === "reset"
          ? await subReset(confirm.sub.id)
          : await subDelete(confirm.sub.id);

      if (res.code !== 0) {
        toast.error(res.msg || "操作失败");

        return;
      }
      toast.success(
        confirm.kind === "reset"
          ? "已重置，旧链接已失效，请把新链接导入客户端"
          : "已删除",
      );
      setConfirm(null);
      void load();
    } finally {
      setBusy(false);
    }
  };

  const sourcesButton = (
    <Button
      className={isH5 ? "flex-1" : ""}
      color="secondary"
      size="sm"
      startContent={<Download size={15} />}
      variant="flat"
      onPress={() => setSourcesOpen(true)}
    >
      外部订阅源{data?.sources.length ? ` (${data.sources.length})` : ""}
    </Button>
  );
  const newButton = (
    <Button
      className={isH5 ? "flex-1" : ""}
      color="success"
      isDisabled={full}
      size="sm"
      startContent={<Plus size={15} />}
      title={full ? `每个账号最多 ${cap} 条订阅` : undefined}
      onPress={() => setEditing("new")}
    >
      新建订阅
    </Button>
  );

  return (
    <AnimatedPage className="px-3 py-8 lg:px-6">
      <div className="mx-auto max-w-5xl space-y-3">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <h1 className="text-xl font-semibold">
              {isH5 ? "订阅" : "我的订阅"}
            </h1>
            {data?.enabled !== false && (
              <p className="mt-1 text-sm text-default-500">
                一条链接，所有客户端通用
                {cap > 0
                  ? ` · 已用 ${subs.length} / ${cap}`
                  : subs.length
                    ? ` · ${subs.length} 条`
                    : ""}
              </p>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {!isH5 && data?.enabled !== false && (
              <>
                {sourcesButton}
                {newButton}
              </>
            )}
            <Button
              isIconOnly
              aria-label="刷新"
              isLoading={loading}
              size="sm"
              variant="flat"
              onPress={load}
            >
              <RefreshCw size={16} />
            </Button>
          </div>
        </div>
        {error && (
          <p className="rounded-lg bg-danger-50 p-2 text-sm text-danger">
            {error}
          </p>
        )}
        {data?.enabled === false && (
          <div className="rounded-2xl border border-divider bg-content1 p-6 text-center text-sm text-default-500">
            订阅功能已被管理员关闭。
          </div>
        )}
        {data?.enabled !== false && data && subs.length === 0 && (
          <div className="rounded-2xl border border-divider bg-content1 p-6 text-center text-sm text-default-500">
            还没有订阅。新建一条，把面板里的节点和外部节点放进去，客户端用一条链接就能导入。
          </div>
        )}
        {subs.map((sub) => (
          <SubCard
            key={sub.id}
            isH5={isH5}
            sub={sub}
            onDelete={() => setConfirm({ kind: "delete", sub })}
            onEdit={() => setEditing(sub)}
            onQR={() => setQR(sub)}
            onReset={() => setConfirm({ kind: "reset", sub })}
          />
        ))}
        {isH5 && data?.enabled !== false && data && (
          <div className="flex gap-2 pt-1">
            {sourcesButton}
            {newButton}
          </div>
        )}
      </div>

      {editing && (
        <SubEditor
          isH5={isH5}
          sub={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void load();
          }}
        />
      )}
      {sourcesOpen && data && (
        <SubSourcesDialog
          cap={data.sourceCap}
          sources={data.sources}
          onChanged={load}
          onClose={() => setSourcesOpen(false)}
        />
      )}

      <Modal isOpen={!!qr} size="sm" onOpenChange={(o) => !o && setQR(null)}>
        <ModalContent>
          <ModalHeader>{qr?.name} · 二维码</ModalHeader>
          <ModalBody>
            {qr && (
              <div className="flex flex-col items-center gap-2">
                <OfflineQRCode value={subLinkURL(qr.token)} />
                <p className="break-all text-center font-mono text-xs text-default-500">
                  {maskedSubLink(qr.token)}
                </p>
                <p className="text-center text-xs text-default-500">
                  用手机客户端扫码导入（Shadowrocket、Clash、sing-box、v2rayNG、Surge、Loon、Quantumult
                  X 都能识别）
                </p>
              </div>
            )}
          </ModalBody>
          <ModalFooter>
            <Button color="danger" variant="flat" onPress={() => setQR(null)}>
              关闭
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      <Modal
        isOpen={!!confirm}
        size="sm"
        onOpenChange={(o) => !o && setConfirm(null)}
      >
        <ModalContent>
          <ModalHeader>
            {confirm?.kind === "reset" ? "重置链接" : "删除订阅"}
          </ModalHeader>
          <ModalBody>
            <p className="text-sm text-default-600">
              {confirm?.kind === "reset"
                ? `「${confirm?.sub.name}」会换一条新链接，旧链接立刻失效，已经导入旧链接的客户端需要重新导入。链接泄露时用。`
                : `删除「${confirm?.sub.name}」后，这条链接立刻失效。里面的节点本身不受影响。`}
            </p>
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setConfirm(null)}
            >
              取消
            </Button>
            <Button
              color={confirm?.kind === "reset" ? "warning" : "danger"}
              isLoading={busy}
              onPress={runConfirm}
            >
              {confirm?.kind === "reset" ? "重置链接" : "删除"}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </AnimatedPage>
  );
}
