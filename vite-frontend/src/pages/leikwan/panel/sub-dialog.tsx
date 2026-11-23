import type { LeiKwanGrant, LeiKwanSub } from "@/api/leikwan-line";

import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Copy } from "lucide-react";
import toast from "react-hot-toast";

import { getLeiKwanRuleSub } from "@/api/leikwan-line";
import {
  maskedSubLink,
  subAgo,
  subFormatLabel,
  subLinkURL,
  type SubFormat,
} from "@/api/sub-link";
import { useH5Mode } from "@/hooks/useH5Mode";
import { copyToClipboard } from "@/pages/landing/link-card";
import { Pill } from "@/pages/landing/pill";
import { SubEntryBadge } from "@/pages/sub-link/entry-badge";
import { OfflineQRCode } from "@/components/qr-code";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";

// Plan 272 「生成订阅」: the owner's 「利群汇聚」 subscription — one link for
// every 利群 rule (an ordinary sub, created on first use or reused). Shows
// the link (masked like the 订阅 page) + 复制 + QR, how many 利群 rules are in
// it, and 「详细设置」 → the 订阅 page editor of this sub. Admins pick a granted
// user (default themselves).
// Plan 297: a subscription follows the entry it was made / updated under
// (entry_mode). The browser shows the 利群汇聚 sub as before; a 手机本地 one
// carries the badge and 「改成跟主入口」. In the im.gamer App (admin, own
// subscriptions) while this phone's entry is 手机本地: the newest 手机本地 sub
// (or 「新建手机本地订阅」 / 「把这条改成手机本地」), and a switcher between the
// own 利群 subs.

/** Default user of the admin picker: the admin's own grant, else the first. */
export function defaultSubUser(grants: LeiKwanGrant[], me?: string | null) {
  return (grants.find((g) => me && g.userName === me) || grants[0])?.userId;
}

export function LeiKwanSubDialog({
  open,
  admin,
  grants = [],
  me,
  app = false,
  phoneLocal = false,
  onClose,
}: {
  open: boolean;
  admin: boolean;
  /** Plan 297: im.gamer App (admin): entry controls on the own subs */
  app?: boolean;
  /** Plan 297: this phone's entry is 手机本地 (App only) */
  phoneLocal?: boolean;
  /** admin: the granted users to pick from */
  grants?: LeiKwanGrant[];
  me?: string | null;
  onClose: () => void;
}) {
  const h5 = useH5Mode();
  const navigate = useNavigate();
  const [userId, setUserId] = useState<number | undefined>(undefined);
  const [sub, setSub] = useState<LeiKwanSub | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  // Plan 297: the shown sub (App switcher) + a running create / convert.
  const [pickId, setPickId] = useState<number | undefined>(undefined);
  const [busy, setBusy] = useState("");

  useEffect(() => {
    if (!open) return;
    setSub(null);
    setError("");
    setPickId(undefined);
    setUserId(admin ? defaultSubUser(grants, me) : undefined);
    // grants only seed the default when the dialog opens
  }, [open, admin]);

  const owner = grants.find((g) => g.userId === userId);
  // The 订阅 page edits only the caller's own subscriptions.
  const own = !admin || !owner || (!!me && owner.userName === me);
  // Plan 297: 手机本地 only on the admin's own subscriptions in the App.
  const local = phoneLocal && app && admin && own;

  useEffect(() => {
    if (!open || (admin && !userId)) return;
    let gone = false;

    setLoading(true);
    setError("");
    void getLeiKwanRuleSub(admin ? userId : undefined, {
      ...(local ? { entry: "local" as const } : {}),
      ...(pickId ? { id: pickId } : {}),
    }).then((res) => {
      if (gone) return;
      setLoading(false);
      if (res.code !== 0 || !res.data) {
        const msg = res.msg || "生成订阅失败";

        toast.error(msg);
        setSub(null);
        setError(msg);
        // Users have nothing else to pick: close right away.
        if (!admin) onClose();

        return;
      }
      setSub(res.data);
    });

    return () => {
      gone = true;
    };
  }, [open, admin, userId, local, pickId]);

  // Plan 297: 新建 (手机本地) / 改成手机本地 / 改成跟主入口.
  const act = async (action: "create" | "convert", toLocal: boolean) => {
    if (!sub) return;
    setBusy(action + (toLocal ? "-local" : "-main"));
    const res = await getLeiKwanRuleSub(admin ? userId : undefined, {
      action,
      ...(toLocal ? { entry: "local" as const } : {}),
      ...(action === "convert" ? { id: sub.id } : {}),
    });

    setBusy("");
    if (res.code !== 0 || !res.data) {
      toast.error(res.msg || "操作失败");

      return;
    }
    setSub(res.data);
    setPickId(res.data.id);
    toast.success(
      action === "create"
        ? "已新建手机本地订阅"
        : toLocal
          ? "这条订阅改成手机本地了"
          : "这条订阅改回跟主入口了",
    );
  };

  const url = sub ? subLinkURL(sub.token) : "";
  const copy = async () => {
    if (!url) return;
    if (await copyToClipboard(url)) toast.success("订阅链接已复制");
    else toast.error("复制失败，请长按二维码下方的链接手动复制");
  };
  const format = (sub?.format || "auto") as SubFormat;
  const subLocal = sub?.entryMode === "local";
  const mainName = sub?.mainEntry || owner?.mainEntryName || "主入口";
  const mySubs = (app && own && sub?.subs) || [];

  return (
    <Modal
      classNames={{
        base: "!w-[calc(100%-24px)] !mx-auto sm:!w-full sm:max-w-lg rounded-2xl",
      }}
      isOpen={open}
      placement="center"
      scrollBehavior="inside"
      size="md"
      onClose={onClose}
    >
      <ModalContent data-leikwan-sub-dialog="">
        <ModalHeader>利群汇聚订阅</ModalHeader>
        <ModalBody className="space-y-3">
          {admin && (
            <Select
              label="用户"
              placeholder="选择已授权的用户"
              selectedKeys={userId ? [String(userId)] : []}
              size="sm"
              variant="bordered"
              onSelectionChange={(keys) => {
                const v = parseInt(String(Array.from(keys)[0] || ""), 10);

                if (v) setUserId(v);
              }}
            >
              {grants.map((g) => (
                <SelectItem key={String(g.userId)}>
                  {`${g.userName || `#${g.userId}`}${me && g.userName === me ? "（我）" : ""}`}
                </SelectItem>
              ))}
            </Select>
          )}
          {admin && grants.length === 0 && (
            <p className="text-sm text-default-500">还没有授权用户。</p>
          )}
          {loading && (
            <div className="flex justify-center py-8">
              <Spinner />
            </div>
          )}
          {!loading && error && !sub && (
            <p className="rounded-lg bg-danger-50 p-2 text-sm text-danger dark:bg-danger-500/10">
              {error}
            </p>
          )}
          {!loading && sub && (
            <>
              {mySubs.length > 1 && (
                <div
                  className="flex flex-wrap items-center gap-1.5"
                  data-sub-switcher=""
                >
                  <span className="text-xs text-default-500">订阅</span>
                  {mySubs.map((x) => (
                    <button
                      key={x.id}
                      className={`inline-flex items-center gap-1 rounded-full border px-2.5 py-1 text-xs ${
                        x.id === sub.id
                          ? "border-primary bg-primary/10 font-semibold text-foreground"
                          : "border-divider text-default-600"
                      }`}
                      type="button"
                      onClick={() => setPickId(x.id)}
                    >
                      {x.name}
                      <SubEntryBadge mode={x.entryMode} />
                    </button>
                  ))}
                </div>
              )}
              {own && local && !subLocal && (
                <div
                  className="space-y-2 rounded-xl border border-violet-500/30 bg-violet-500/[0.05] p-3 text-xs leading-5 text-default-600 dark:bg-violet-400/[0.07]"
                  data-sub-entry-choice="local"
                >
                  <p>
                    这台手机的入口是<b>手机本地</b>。这条订阅跟主入口（
                    {mainName}
                    ），用它的设备（比如电脑）照常走 {mainName}。
                  </p>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      color="primary"
                      data-sub-create-local=""
                      isLoading={busy === "create-local"}
                      size="sm"
                      variant="flat"
                      onPress={() => void act("create", true)}
                    >
                      新建手机本地订阅
                    </Button>
                    <Button
                      color="secondary"
                      data-sub-convert="local"
                      isLoading={busy === "convert-local"}
                      size="sm"
                      variant="flat"
                      onPress={() => void act("convert", true)}
                    >
                      把这条改成手机本地
                    </Button>
                  </div>
                  <p className="text-[11px] text-default-500">
                    改这条的话，用这条订阅的其他设备刷新后也会变成 127.0.0.1。
                  </p>
                </div>
              )}
              {own && subLocal && (
                <div
                  className="space-y-2 rounded-xl border border-violet-500/30 bg-violet-500/[0.05] p-3 text-xs leading-5 text-default-600 dark:bg-violet-400/[0.07]"
                  data-sub-entry-choice="main"
                >
                  <p>
                    这是<b>手机本地</b>订阅：利群节点的地址是
                    127.0.0.1:端口，要在开着利群客户端的设备上用（比如装了 App
                    的手机）。
                  </p>
                  <Button
                    color="secondary"
                    data-sub-convert="main"
                    isLoading={busy === "convert-main"}
                    size="sm"
                    variant="flat"
                    onPress={() => void act("convert", false)}
                  >
                    改成跟主入口（{mainName}）
                  </Button>
                </div>
              )}
              <div className="flex flex-wrap gap-1.5">
                <Pill tone="violet">利群转发 {sub.leikwanNodes} 条</Pill>
                {sub.total > sub.leikwanNodes && (
                  <Pill tone="slate">订阅共 {sub.total} 个节点</Pill>
                )}
                <Pill title={sub.name} tone="slate">
                  {sub.name}
                </Pill>
                <SubEntryBadge mode={sub.entryMode} />
              </div>
              {sub.manual > 0 && (
                <p className="rounded-lg bg-amber-500/15 px-3 py-2 text-xs text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
                  {sub.manual}{" "}
                  条手动落地的转发不能进订阅（面板没有它们的密码），这几条请用「复制链接」。
                </p>
              )}
              <div className="flex items-center gap-2 rounded-xl bg-slate-500/10 px-3 py-2 dark:bg-slate-400/10">
                <span
                  className="min-w-0 flex-1 truncate font-mono text-[13px] text-default-700"
                  title="完整链接只在复制时给出"
                >
                  {maskedSubLink(sub.token)}
                </span>
                <Button
                  color="success"
                  size="sm"
                  startContent={<Copy size={14} />}
                  variant="flat"
                  onPress={() => void copy()}
                >
                  复制
                </Button>
              </div>
              <div className="flex justify-center">
                <OfflineQRCode minSize={h5 ? 176 : 208} value={url} />
              </div>
              <p className="text-center text-xs text-default-500">
                {sub.note || "推荐用订阅：换入口时刷新订阅即可，不用改地址"}
              </p>
              <p className="text-center text-xs text-default-400">
                {format === "auto"
                  ? "客户端自动识别格式（Clash / Surge / sing-box / v2rayN …）"
                  : `格式：${subFormatLabel[format] || format}（在「详细设置」里改）`}
                {sub.lastPullAt ? ` · 最近拉取 ${subAgo(sub.lastPullAt)}` : ""}
              </p>
              {!own && (
                <p className="text-center text-xs text-default-400">
                  这是「{owner?.userName}
                  」的订阅：详细设置要用户自己在「订阅」页改。
                </p>
              )}
            </>
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            关闭
          </Button>
          <Button
            color="primary"
            isDisabled={!sub || !own}
            title={
              !own
                ? "只能在「订阅」页编辑自己的订阅"
                : "在「订阅」页打开这条订阅"
            }
            variant="flat"
            onPress={() => {
              if (!sub) return;
              onClose();
              navigate(`/sub?edit=${sub.id}`);
            }}
          >
            详细设置
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
