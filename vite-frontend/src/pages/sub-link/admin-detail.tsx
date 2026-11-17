import { useEffect, useState } from "react";
import { ChevronLeft } from "lucide-react";

import { RoutingReadOnly } from "./routing";

import {
  ExpandChip,
  ExpandField,
  ExpandRow,
  useExpandOne,
} from "@/components/expand-row";
import { Pill, landingToneClass } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import {
  SubAdminDetail,
  SubAdminItem,
  subAdminDetail,
  subAgo,
  subFormatLabel,
  subModeLabel,
} from "@/api/sub-link";

// Plan 260 「全部用户」 → 查看: one subscription of a user, read-only — its
// nodes in order and its 分流 list. Nothing here edits, deletes or resets.

export interface SubAdminTarget {
  id: number;
  user: string;
  name: string;
}

const chipOf: Record<
  SubAdminItem["group"],
  {
    tone: "green" | "teal" | "purple" | "amber" | "violet";
    text: string;
    h5: string;
  }
> = {
  rule: { tone: "green", text: "规则", h5: "规则" },
  landing: { tone: "teal", text: "落地", h5: "落地" },
  external: { tone: "purple", text: "外部节点", h5: "外部" },
  source: { tone: "amber", text: "订阅源", h5: "订阅源" },
  source_all: { tone: "amber", text: "整组", h5: "整组" },
  leikwan: { tone: "violet", text: "利群", h5: "利群" },
  leikwan_all: { tone: "violet", text: "利群·全部", h5: "利群·全部" },
};

function itemName(it: SubAdminItem) {
  if (it.group === "source_all")
    return `${it.sourceName || "外部订阅源"} · 整组 ${it.count || 0} 个`;
  if (it.group === "leikwan_all") return `全部利群规则 · ${it.count || 0} 条`;

  return it.name || "（已删除的节点）";
}

function protocolText(it: SubAdminItem) {
  return `${it.protocol || ""}${it.chain ? "（链式）" : ""}`;
}

function sourceText(it: SubAdminItem) {
  if (it.group === "source" || it.group === "source_all")
    return `外部订阅源「${it.sourceName || ""}」`;
  if (it.group === "rule") return "用户的转发规则";
  if (it.group === "landing") return "用户的落地节点";
  if (it.group === "leikwan" || it.group === "leikwan_all")
    return "用户的利群汇聚规则";

  return "用户的外部节点";
}

function fullTime(ms: number) {
  if (!ms) return "—";
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");

  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

function pullText(d: SubAdminDetail) {
  if (!d.lastPullAt) return "从未拉取";

  return `${subAgo(d.lastPullAt)} · ${d.lastPullClient || "未知客户端"} · ${d.lastPullNodes} 个节点`;
}

function ItemRow({ it }: { it: SubAdminItem }) {
  const chip = chipOf[it.group];
  const all = it.group === "source_all";

  return (
    <div
      className={`flex items-center gap-2 border-b border-divider px-3 py-2 last:border-b-0 ${all && !it.missing ? "bg-amber-500/10 dark:bg-amber-400/10" : ""}`}
      data-admin-item={it.group}
    >
      <span
        className={`min-w-0 flex-1 truncate text-sm ${it.missing ? "text-default-400" : ""}`}
        title={itemName(it)}
      >
        {itemName(it)}
      </span>
      <Pill
        title={
          it.group === "source" ? `外部订阅源「${it.sourceName}」` : undefined
        }
        tone={chip.tone}
      >
        {chip.text}
      </Pill>
      {it.missing ? (
        <Pill tone="slate">已失效</Pill>
      ) : (
        it.protocol && <Pill tone="pink">{protocolText(it)}</Pill>
      )}
    </div>
  );
}

export function SubAdminDetailDialog({
  isH5,
  target,
  onClose,
}: {
  isH5: boolean;
  target: SubAdminTarget;
  onClose: () => void;
}) {
  const [data, setData] = useState<SubAdminDetail | null>(null);
  const [error, setError] = useState("");
  const [tab, setTab] = useState<"nodes" | "routing">("nodes");
  const items = data?.items || [];
  const expand = useExpandOne(items.map((_, i) => i));

  useEffect(() => {
    let alive = true;

    subAdminDetail(target.id)
      .then((res) => {
        if (!alive) return;
        if (res.code === 0) setData(res.data);
        else setError(res.msg || "加载失败");
      })
      .catch(() => alive && setError("加载失败"));

    return () => {
      alive = false;
    };
  }, [target.id]);

  const title = `${data?.user || target.user} / ${data?.name || target.name}`;
  const routingCount = data?.routing?.length || 0;

  const facts = data && (
    <div
      data-admin-facts
      className={
        isH5
          ? "space-y-1"
          : "grid grid-cols-3 gap-3 rounded-xl bg-slate-500/10 px-3 py-2.5 dark:bg-slate-400/10"
      }
    >
      {isH5 ? (
        <>
          <ExpandField label="格式">{subFormatLabel[data.format]}</ExpandField>
          <ExpandField label="最近拉取">{pullText(data)}</ExpandField>
          <ExpandField label="创建时间">
            {fullTime(data.createdTime)}
          </ExpandField>
        </>
      ) : (
        (
          [
            ["格式", subFormatLabel[data.format]],
            ["最近拉取", pullText(data)],
            ["创建时间", fullTime(data.createdTime)],
          ] as const
        ).map(([label, value]) => (
          <div key={label} className="min-w-0">
            <div className="text-xs text-default-500">{label}</div>
            <div className="truncate text-sm" title={value}>
              {value}
            </div>
          </div>
        ))
      )}
    </div>
  );

  const nodes =
    items.length === 0 ? (
      <p className="p-6 text-center text-sm text-default-400">
        这条订阅里还没有节点
      </p>
    ) : isH5 ? (
      items.map((it, i) => {
        const chip = chipOf[it.group];

        return (
          <ExpandRow
            key={i}
            chip={
              <ExpandChip
                className={
                  it.missing
                    ? landingToneClass.slate
                    : landingToneClass[chip.tone]
                }
              >
                {it.missing ? "已失效" : chip.h5}
              </ExpandChip>
            }
            className={`border-b border-divider last:border-b-0 ${it.group === "source_all" && !it.missing ? "bg-amber-500/10 dark:bg-amber-400/10" : ""}`}
            name={itemName(it)}
            nameClassName={it.missing ? "text-default-400" : ""}
            nameTitle={itemName(it)}
            open={expand.open === i}
            rowProps={{ "data-admin-item": it.group }}
            onToggle={() => expand.toggle(i)}
          >
            {it.missing ? (
              <ExpandField label="状态">
                <span className="text-default-500">
                  已失效（节点已删除或已不可用）
                </span>
              </ExpandField>
            ) : it.group === "source_all" ? (
              <ExpandField label="节点">
                现在 {it.count || 0} 个 · 整组跟随
              </ExpandField>
            ) : (
              it.protocol && (
                <ExpandField label="协议">{protocolText(it)}</ExpandField>
              )
            )}
            <ExpandField label="来源">{sourceText(it)}</ExpandField>
          </ExpandRow>
        );
      })
    ) : (
      items.map((it, i) => <ItemRow key={i} it={it} />)
    );

  const routing =
    data &&
    (data.routing ? (
      <RoutingReadOnly
        catalog={data.routingCatalog || []}
        isH5={isH5}
        items={data.items}
        mode={data.ruleMode}
        routing={data.routing}
      />
    ) : (
      <div
        data-routing-unset
        className="space-y-2 rounded-2xl border border-divider px-4 py-5 text-center"
      >
        <p className="text-sm text-default-500">
          未设置分流（使用旧版规则：国内直连 / 全部走代理）
        </p>
        <Pill tone={data.ruleMode === "global" ? "slate" : "teal"}>
          {subModeLabel[data.ruleMode]}
        </Pill>
      </div>
    ));

  const body = error ? (
    <p className="text-sm text-danger">{error}</p>
  ) : !data ? (
    <p className="p-6 text-center text-sm text-default-400">加载中…</p>
  ) : tab === "routing" ? (
    routing
  ) : (
    <div
      data-admin-nodes
      className={`overflow-y-auto rounded-2xl border border-divider ${isH5 ? "min-h-0 flex-1" : "max-h-[52vh]"}`}
    >
      {nodes}
    </div>
  );

  if (isH5) {
    return (
      <Modal isOpen size="full" onOpenChange={(o) => !o && onClose()}>
        <ModalContent className="flex flex-col gap-0 overflow-hidden p-0">
          <div className="flex items-center gap-1 px-3 pb-2 pt-3">
            <button
              aria-label="返回"
              className="p-1"
              type="button"
              onClick={onClose}
            >
              <ChevronLeft size={20} />
            </button>
            <span className="truncate text-base font-semibold">{title}</span>
          </div>
          <div className="space-y-2 px-3 pb-2">
            {facts}
            {data && (
              <div className="flex rounded-xl bg-slate-500/10 p-0.5 dark:bg-slate-400/15">
                {(
                  [
                    ["nodes", `节点 ${items.length}`],
                    [
                      "routing",
                      data.routing ? `分流 ${routingCount}` : "分流 未设置",
                    ],
                  ] as const
                ).map(([k, text]) => (
                  <button
                    key={k}
                    className={`flex-1 rounded-lg py-1.5 text-sm ${
                      tab === k
                        ? "bg-white font-semibold shadow-sm dark:bg-slate-700"
                        : "text-default-600"
                    }`}
                    data-admin-tab={k}
                    type="button"
                    onClick={() => setTab(k)}
                  >
                    {text}
                  </button>
                ))}
              </div>
            )}
          </div>
          <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-3 pb-2">
            {body}
          </div>
          <div className="sticky bottom-0 border-t border-divider bg-content1 px-3 py-2.5">
            <Button
              className="w-full"
              color="danger"
              variant="flat"
              onPress={onClose}
            >
              关闭
            </Button>
          </div>
        </ModalContent>
      </Modal>
    );
  }

  return (
    <Modal
      isOpen
      scrollBehavior="inside"
      size="2xl"
      onOpenChange={(o) => !o && onClose()}
    >
      <ModalContent>
        <ModalHeader>
          <span className="min-w-0 truncate" title={title}>
            {title}
          </span>
        </ModalHeader>
        <ModalBody>
          {facts}
          {data && (
            <div className="flex gap-1 border-b border-divider">
              {(
                [
                  ["nodes", "节点", String(items.length)],
                  [
                    "routing",
                    "分流",
                    data.routing ? String(routingCount) : "未设置",
                  ],
                ] as const
              ).map(([k, text, n]) => (
                <button
                  key={k}
                  className={`-mb-px flex items-center gap-1.5 border-b-2 px-3 pb-2 text-sm ${
                    tab === k
                      ? "border-primary font-semibold text-foreground"
                      : "border-transparent text-default-500"
                  }`}
                  data-admin-tab={k}
                  type="button"
                  onClick={() => setTab(k)}
                >
                  {text}
                  <Pill tone={k === "routing" ? "cyan" : "slate"}>{n}</Pill>
                </button>
              ))}
            </div>
          )}
          <div className="min-h-[280px]">{body}</div>
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
