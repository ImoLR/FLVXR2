import {
  ENTRY_NOTICE_ADMIN_TEXT,
  ENTRY_NOTICE_TEXT,
  ENTRY_PENDING_HINT,
  ENTRY_TAG_TEXT,
  type EntryNotice,
} from "@/api/user-subdomain";
import { AttentionIcon } from "@/pages/landing/admin-attention";
import { landingToneClass } from "@/pages/landing/pill";

export type { EntryNotice };

// Plan 303 round 2: a small tag under the forward address when the owner's
// 专属域名 in it has no record on purpose (「已过期 · 记录已删除」 /
// 「已停用 · 记录已删除」 / 「已删除 · 不再解析」). The address itself keeps
// the name. The backend sends `entryNameTags` (host → tag) only when there
// is one.
// Round 3: 「正在就绪」 (users + admins) while a fresh name's record is being
// created — the address is already final, usable in a few seconds. An entry
// whose name cannot be handed out yet has NO address; `entryNotices` says
// why (users: 「入口域名名额已满 / 创建失败，请联系管理员」; admins: red ⚠
// with the withheld name and the Cloudflare error).

const tone = (tag: string) =>
  tag === "capped" || tag === "failed"
    ? landingToneClass.red
    : tag === "pending"
      ? landingToneClass.cyan
      : tag === "deleted"
        ? landingToneClass.slate
        : landingToneClass.amber;

const pill =
  "inline-flex max-w-full items-center gap-1 whitespace-nowrap rounded-full px-1.5 py-px text-[10px] font-semibold leading-4";

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
          className="inline-flex max-w-full items-center gap-1"
          data-entry-name-tag={tag}
          title={`${host}：${ENTRY_TAG_TEXT[tag]}${tag === "pending" ? `（${ENTRY_PENDING_HINT}）` : ""}`}
        >
          <span className={`${pill} ${tone(tag)}`}>
            {tag === "capped" || tag === "failed" ? (
              <AttentionIcon className="h-3 w-3" level="error" />
            ) : null}
            {tag === "pending" ? (
              <span
                aria-hidden
                className="h-1.5 w-1.5 animate-pulse rounded-full bg-current"
              />
            ) : null}
            {many ? `${host.split(".")[0]} ` : ""}
            {ENTRY_TAG_TEXT[tag]}
          </span>
          {tag === "pending" ? (
            <span className="whitespace-nowrap text-[10px] text-default-500">
              {ENTRY_PENDING_HINT}
            </span>
          ) : null}
        </span>
      ))}
    </div>
  );
}

/**
 * Round 3: entries of a forward that have no address (yet). Users get the
 * plain reason; admins a red ⚠ with the withheld name and the error.
 */
export function EntryNotices({
  notices,
  admin = false,
  className = "",
}: {
  notices?: EntryNotice[] | null;
  admin?: boolean;
  className?: string;
}) {
  const list = (notices || []).filter((n) => n && n.tag);

  if (list.length === 0) return null;

  return (
    <div
      data-entry-notices
      className={`flex flex-col items-start gap-0.5 ${className}`}
    >
      {list.map((n, i) => {
        const pending = n.tag === "pending";
        const text = admin
          ? ENTRY_NOTICE_ADMIN_TEXT[n.tag] || ENTRY_NOTICE_ADMIN_TEXT.failed
          : ENTRY_NOTICE_TEXT[n.tag] || ENTRY_NOTICE_TEXT.failed;
        const detail = admin
          ? [n.domain, n.reason].filter(Boolean).join("：")
          : "";

        return (
          <span
            key={`${n.nodeId}-${n.port}-${i}`}
            className={`${pill} max-w-full whitespace-normal text-left ${pending ? landingToneClass.cyan : landingToneClass.red}`}
            data-entry-notice={n.tag}
            title={detail || text}
          >
            {pending ? (
              <span
                aria-hidden
                className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-current"
              />
            ) : (
              <AttentionIcon className="h-3 w-3 shrink-0" level="error" />
            )}
            <span className="min-w-0">
              {list.length > 1 && n.port ? `:${n.port} ` : ""}
              {text}
              {admin && n.domain ? (
                <span className="ml-1 font-mono font-normal opacity-80">
                  {n.domain}
                </span>
              ) : null}
            </span>
          </span>
        );
      })}
    </div>
  );
}

/** Round 3: a 「正在就绪」 tag or notice is on screen (short poll). */
export const hasReadyingEntry = (f: {
  entryNameTags?: Record<string, string> | null;
  entryNotices?: EntryNotice[] | null;
}) =>
  Object.values(f.entryNameTags || {}).some((t) => t === "pending") ||
  (f.entryNotices || []).some((n) => n.tag === "pending");

/**
 * Plan 305: 「保持原地址」 (admins only) — the forward existed before the
 * node's 用户专属域名 went on and keeps the node address (no name).
 */
export function EntryKeptTag({
  admin,
  nodes,
  className = "",
}: {
  admin: boolean;
  nodes?: number[];
  className?: string;
}) {
  if (!admin || !nodes?.length) return null;

  return (
    <div data-entry-kept className={`flex flex-wrap gap-1 ${className}`}>
      <span
        className="rounded-full bg-default-200/70 px-1.5 text-[10px] text-default-500"
        title="开启「用户专属域名」前就有的转发：继续用节点原来的地址（已有转发保持原地址）"
      >
        保持原地址
      </span>
    </div>
  );
}
