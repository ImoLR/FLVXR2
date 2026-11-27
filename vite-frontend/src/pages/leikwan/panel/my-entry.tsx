import type {
  LeiKwanEntryNode,
  LeiKwanGrant,
  LeiKwanRule,
  LeiKwanUsageView,
} from "@/api/leikwan-line";

import { useState } from "react";
import { Settings, Smartphone } from "lucide-react";
import toast from "react-hot-toast";

import { Pill } from "@/pages/landing/pill";
import {
  PHONE_HOST,
  PHONE_KEY,
  PHONE_NAME,
  phoneEntryPill,
  usePhoneCtx,
} from "@/pages/leikwan/panel/phone-entry";
import {
  PhoneGearSheet,
  PhoneSetupPanel,
} from "@/pages/leikwan/panel/phone-setup";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { EntryLabelPills } from "@/components/entry-label";
import { setPhoneEntry } from "@/utils/app-bridge";

// Plan 315 (user 11-26): in the im.gamer App, 授权 opens with 「我的入口」 —
// the admin's own grant's entry ON THIS PHONE, picked from a dropdown:
// 手机本地 first, then the server 利群入口 (same status words as the 入口机
// rows). 手机本地 is phone-only (setEntry, the server 主入口 unchanged);
// picking the server 主入口 again only clears the phone choice; another server
// entry opens the usual 换主入口 dialog (all devices, with its per-forward
// preview) — never changed silently from the dropdown. Under it, with 手机本地
// picked: the setup + connect panel. Browsers and normal users never see it.
// Plan 315 r3: under the dropdown only three layers (first-run cards, 连接,
// status); the rest of 手机本地's settings sit behind the header's gear.

/** The entry's status word (same as the 入口机 rows: 就绪 / 离线 / 未检测到). */
function entryWord(e: LeiKwanEntryNode) {
  if (!e.online) return { tone: "red" as const, text: "离线" };
  if (e.leikwanStatus === "running")
    return { tone: "green" as const, text: "就绪" };

  return { tone: "slate" as const, text: "未检测到" };
}

export function MyEntryCard({
  grant,
  entries,
  rules,
  usage,
  onChanged,
  onSwitchServer,
  onSub,
}: {
  /** the admin's own grant */
  grant: LeiKwanGrant;
  entries: LeiKwanEntryNode[];
  /** the own rules (tip: per-专线 local ports) */
  rules: LeiKwanRule[];
  usage?: LeiKwanUsageView | null;
  /** reload the page data (rows follow the new entry) */
  onChanged: () => void;
  /** another server entry: 换主入口 preset to it */
  onSwitchServer: (nodeId: number) => void;
  /** 利群汇聚 「生成订阅」 */
  onSub?: () => void;
}) {
  const ctx = usePhoneCtx();
  const [busy, setBusy] = useState(false);
  const [gear, setGear] = useState(false);

  if (!ctx) return null;
  const s = ctx.state;
  const local = ctx.local;
  const mainKey = String(grant.mainEntryNodeId);
  const current = local ? PHONE_KEY : mainKey;
  const pill = phoneEntryPill(s);
  const main = entries.find((e) => String(e.id) === mainKey);
  // Offline entries cannot be picked (as in 换主入口); the 主入口 stays.
  const disabled = entries
    .filter((e) => !e.online && String(e.id) !== mainKey)
    .map((e) => String(e.id));

  const phoneSwitch = async (to: "local" | "server") => {
    setBusy(true);
    try {
      await setPhoneEntry(to);
      toast.success(
        to === "local"
          ? "这台手机的入口换成手机本地（面板里的主入口不变）"
          : `这台手机换回 ${grant.mainEntryName || "主入口"}（面板不用改）`,
      );
      onChanged();
    } catch (e) {
      toast.error((e as Error).message || "App 没有完成这个操作");
    }
    setBusy(false);
  };
  const pick = (key: string) => {
    if (!key || key === current) return;
    if (key === PHONE_KEY) void phoneSwitch("local");
    else if (key === mainKey) void phoneSwitch("server");
    else onSwitchServer(parseInt(key, 10));
  };

  return (
    <div
      className="space-y-3 rounded-xl border border-violet-500/30 bg-violet-500/[0.05] p-3 dark:bg-violet-400/[0.07]"
      data-phone-my-entry={local ? "local" : "server"}
      id="phone-my-entry"
    >
      <div className="flex items-center gap-1.5">
        <Smartphone
          className="shrink-0 text-violet-600 dark:text-violet-300"
          size={15}
        />
        <span className="shrink-0 text-sm font-semibold">我的入口</span>
        <span className="min-w-0 truncate text-xs text-default-500">
          这台手机用哪个入口
        </span>
        {local && (
          <button
            aria-label="手机本地设置"
            className="ml-auto flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300"
            data-phone-gear=""
            type="button"
            onClick={() => setGear(true)}
          >
            <Settings size={16} />
          </button>
        )}
      </div>
      <Select
        aria-label="我的入口"
        disabledKeys={disabled}
        isDisabled={busy}
        listboxVariant="grouped"
        placeholder="选择入口"
        selectedKeys={[current]}
        variant="bordered"
        onSelectionChange={(keys) => pick(String(Array.from(keys)[0] || ""))}
      >
        {[
          <SelectItem
            key={PHONE_KEY}
            description={`这台手机自己的利群客户端（${PHONE_HOST}），只在这台手机生效`}
            endContent={<Pill tone={pill.tone}>{pill.text}</Pill>}
            textValue={PHONE_NAME}
          >
            {PHONE_NAME}
          </SelectItem>,
          ...entries.map((e) => {
            const w = entryWord(e);
            const isMain = String(e.id) === mainKey;

            return (
              <SelectItem
                key={String(e.id)}
                description={
                  isMain
                    ? "服务器主入口：电脑和别的设备都走它"
                    : "选它 = 换主入口（所有设备一起换，会先确认）"
                }
                endContent={<Pill tone={w.tone}>{w.text}</Pill>}
                textValue={e.name}
              >
                <span className="inline-flex min-w-0 items-center gap-1.5">
                  <span className="truncate">{e.name}</span>
                  {/* Plan 321: 电信 / 联通 / IPv6 of the entry */}
                  <EntryLabelPills label={e.entryLabel} />
                </span>
              </SelectItem>
            );
          }),
        ]}
      </Select>
      {local ? (
        <>
          <PhoneSetupPanel rules={rules} usage={usage} onSub={onSub} />
          <PhoneGearSheet open={gear} onClose={() => setGear(false)} />
        </>
      ) : (
        <p
          className="text-xs leading-5 text-default-500"
          data-phone-my-entry-server=""
        >
          这台手机走服务器主入口 {grant.mainEntryName || main?.name || "—"}
          （和电脑一样）。选「{PHONE_NAME}」=
          用这台手机自己的利群客户端，只在这台手机上生效，在这里连接和设置。
        </p>
      )}
    </div>
  );
}
