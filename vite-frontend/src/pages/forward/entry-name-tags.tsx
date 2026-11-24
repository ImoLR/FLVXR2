import { ENTRY_TAG_TEXT } from "@/api/user-subdomain";
import { AttentionIcon } from "@/pages/landing/admin-attention";
import { landingToneClass } from "@/pages/landing/pill";

// Plan 303 round 2: a small tag under the forward address when the owner's
// 专属域名 in it has no record on purpose (「已过期 · 记录已删除」 /
// 「已停用 · 记录已删除」 / 「已删除 · 不再解析」), and for admins 「同步中」 or
// ⚠「暂用主域名」 (no name yet). The address itself keeps the name. The
// backend sends `entryNameTags` (host → tag) only when there is one.

const tone = (tag: string) =>
  tag === "fallback"
    ? landingToneClass.red
    : tag === "pending"
      ? landingToneClass.cyan
      : tag === "deleted"
        ? landingToneClass.slate
        : landingToneClass.amber;

export function EntryNameTags({
  tags,
  className = "",
}: {
  tags?: Record<string, string> | null;
  className?: string;
}) {
  const list = Object.entries(tags || {}).filter(([, t]) => ENTRY_TAG_TEXT[t]);

  if (list.length === 0) return null;
  const many = list.length > 1;

  return (
    <div
      data-entry-name-tags
      className={`flex flex-wrap items-center gap-1 ${className}`}
    >
      {list.map(([host, tag]) => (
        <span
          key={host}
          className={`inline-flex max-w-full items-center gap-1 whitespace-nowrap rounded-full px-1.5 py-px text-[10px] font-semibold leading-4 ${tone(tag)}`}
          data-entry-name-tag={tag}
          title={`${host}：${ENTRY_TAG_TEXT[tag]}`}
        >
          {tag === "fallback" ? (
            <AttentionIcon className="h-3 w-3" level="error" />
          ) : null}
          {many ? `${host.split(".")[0]} ` : ""}
          {ENTRY_TAG_TEXT[tag]}
        </span>
      ))}
    </div>
  );
}
