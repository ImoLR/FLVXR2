import type { LeiKwanRule } from "@/api/leikwan-line";
import type { ReactNode } from "react";
import type { PhoneState } from "@/utils/app-bridge";

import { useEffect, useRef, useState } from "react";
import { flushSync } from "react-dom";
import {
  AlertTriangle,
  BatteryCharging,
  Check,
  KeyRound,
  Server,
  Split,
} from "lucide-react";

import { OnboardingRing } from "@/components/onboarding-ring";
import { run } from "@/pages/leikwan/panel/phone-entry";
import { dropLeiKwan } from "@/pages/leikwan/panel/rule-actions";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { appCall } from "@/utils/app-bridge";

// Plan 315 r3 (user 11-26: 「要简洁明了、有层次感」): layer 1 of 授权 → 我的入口
// with 手机本地 = the old App's first-run cards (im.gamer PubOnboarding +
// OnboardingViews, 1.0.5–1.0.7), ported to the page:
//   - three small cards in a 2-column grid: 电池设为「无限制」 / 节点地址
//     127.0.0.1 / 分应用代理里绕过 (no 彩蛋 card); each = tinted icon badge +
//     one chip + one-line face;
//   - the panel's onboarding ring (plan 146/169) on the first card not tapped
//     yet, one at a time; it moves on after a tap, never plays while a sheet
//     is open or while the grid reflows;
//   - tap → a sheet with the tip text; 「我知道了」 → the card shatters
//     (~0.6 s, fragments of the card itself) and the others reflow (FLIP);
//     the battery card's 「我知道了」 only closes the sheet: it leaves when the
//     battery really is 「无限制」 and never comes back once it was (also when
//     it already was the first time: nothing to see then);
//   - state in the WebView's localStorage (this phone only, try/catch);
//     prefers-reduced-motion → a short fade, no shatter, no glide.
// All gone → layer 1 takes no space.
// Plan 315 r4 (user 11-26): + 「填写商家账号」 as the 2nd card (2 × 2 grid on a
// phone): a tap opens the App's own account dialog (no sheet); it shatters
// only when the account really is saved (live credentials.set), like the
// battery card, and never comes back. The 127.0.0.1 card leads with the
// subscription (its addresses already are 127.0.0.1).

type CardKey = "battery" | "cred" | "node" | "bypass";
/** The cards that leave by themselves once the phone state says so. */
type LiveKey = "battery" | "cred";

const ORDER: CardKey[] = ["battery", "cred", "node", "bypass"];
const DONE = (k: CardKey) => `flvx_phone_card_done_${k}`;
const SEEN = (k: CardKey) => `flvx_phone_card_seen_${k}`;

const lsOn = (k: string) => {
  try {
    return localStorage.getItem(k) === "1";
  } catch {
    return false;
  }
};

const lsSet = (k: string) => {
  try {
    localStorage.setItem(k, "1");
  } catch {
    /* storage off: the flag lives for this page only */
  }
};

const reducedMotion = () => {
  try {
    return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  } catch {
    return false;
  }
};

const FACE: Record<CardKey, string> = {
  battery: "电池设为「无限制」",
  cred: "填写商家账号",
  node: "节点 127.0.0.1",
  bypass: "分应用代理里绕过",
};

/**
 * The face where a card is too narrow for one line (360 px / big text):
 * whole words only (keep-all), breaking at these points.
 */
const FACE_PARTS: Record<CardKey, string[]> = {
  battery: ["电池设为", "「无限制」"],
  cred: ["填写", "商家账号"],
  node: ["节点 ", "127.0.0.1"],
  bypass: ["分应用代理", "里绕过"],
};

const TONE: Record<CardKey, { badge: string; chip: string }> = {
  battery: {
    badge:
      "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300",
    chip: "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300",
  },
  cred: {
    badge:
      "bg-violet-500/15 text-violet-700 dark:bg-violet-400/20 dark:text-violet-300",
    chip: "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300",
  },
  node: {
    badge:
      "bg-blue-500/15 text-blue-700 dark:bg-blue-400/15 dark:text-blue-300",
    chip: "bg-blue-500/15 text-blue-700 dark:bg-blue-400/15 dark:text-blue-300",
  },
  bypass: {
    badge:
      "bg-teal-500/15 text-teal-700 dark:bg-teal-400/15 dark:text-teal-300",
    chip: "bg-teal-500/15 text-teal-700 dark:bg-teal-400/15 dark:text-teal-300",
  },
};

const ICON: Record<CardKey, ReactNode> = {
  battery: <BatteryCharging size={16} />,
  cred: <KeyRound size={16} />,
  node: <Server size={16} />,
  bypass: <Split size={16} />,
};

const chipGreen =
  "bg-green-500/15 text-green-700 dark:bg-green-400/15 dark:text-green-300";

// ---- the sheet (bottom sheet on phones, centred dialog on desktop) ----

export function PhoneSheet({
  open,
  title,
  onClose,
  children,
  footer,
  name,
}: {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  /** data-phone-sheet value (tests / screenshots) */
  name: string;
}) {
  return (
    <Modal
      classNames={{
        base: "max-sm:!bottom-0 max-sm:!left-0 max-sm:!top-auto max-sm:!w-full max-sm:!max-w-none max-sm:!translate-x-0 max-sm:!translate-y-0 max-sm:rounded-b-none max-sm:rounded-t-2xl max-sm:border-x-0 max-sm:border-b-0 max-sm:px-5 max-sm:pb-[max(1.25rem,env(safe-area-inset-bottom))] max-sm:pt-5 sm:max-w-md",
      }}
      isOpen={open}
      scrollBehavior="inside"
      onClose={onClose}
    >
      <ModalContent data-phone-sheet={name}>
        <ModalHeader className="p-0 text-left text-base font-semibold">
          {title}
        </ModalHeader>
        <ModalBody className="space-y-3 p-0 text-sm leading-6 text-default-600">
          {children}
        </ModalBody>
        {footer && <div className="flex gap-2 pt-1">{footer}</div>}
      </ModalContent>
    </Modal>
  );
}

// ---- sheet texts (the old App's words; r2 BatteryTip / NodeTip / BypassTip) ----

function BatterySheetBody({ s }: { s: PhoneState }) {
  return (
    <>
      <p>
        im.gamer 要一直在后台开着本地端口。电池没设成「无限制」时，
        <b className="text-foreground">锁屏后系统会冻结或关掉它</b>
        ，代理客户端就会断线。
      </p>
      <p>
        点「去设置」，在弹出的窗口里选「允许」（有的手机叫「无限制」）。设好以后，这张卡片会自己消失。
      </p>
      {s.battery.hint && (
        <div className="rounded-xl bg-amber-500/10 px-3 py-2.5 text-xs leading-5 text-amber-800 dark:bg-amber-400/10 dark:text-amber-200">
          <div className="font-semibold">有的手机还有自己的开关：</div>
          {s.battery.hint}
        </div>
      )}
    </>
  );
}

function NodeSheetBody({ s, rules }: { s: PhoneState; rules: LeiKwanRule[] }) {
  const ports = s.forwards.length
    ? s.forwards.map((f) => ({
        port: f.localPort,
        name:
          rules.find((r) =>
            r.forwardId
              ? r.forwardId === f.forwardId
              : r.listenPort === f.localPort,
          )?.name ||
          f.name ||
          `利群 #${f.forwardId}`,
      }))
    : rules.map((r) => ({ port: r.listenPort, name: r.name }));

  return (
    <>
      <p
        className="text-[15px] font-semibold leading-6 text-foreground"
        data-phone-sheet-lead=""
      >
        推荐使用订阅：订阅里的节点地址已经是 127.0.0.1
      </p>
      <div className="space-y-1.5 text-xs leading-5 text-default-500">
        <p>
          手动改也行：服务器填 <span className="font-mono">127.0.0.1</span>
          ，端口按下表，其它照落地。
        </p>
        <div className="space-y-0.5 rounded-xl bg-default-100 px-3 py-2 dark:bg-default-100/60">
          {ports.map((p) => (
            <div
              key={`${p.port}-${p.name}`}
              className="flex items-baseline justify-between gap-2"
            >
              <span className="min-w-0 truncate">{dropLeiKwan(p.name)}</span>
              <span className="select-text font-mono font-medium text-default-700">
                {p.port}
              </span>
            </div>
          ))}
        </div>
      </div>
    </>
  );
}

const BYPASS_STEPS: [string, string][] = [
  ["NekoBox", "设置 → 路由设置 → 分应用代理 → 绕过"],
  ["FlClash", "工具 → 访问控制 → 黑名单模式"],
  ["v2rayNG", "分应用设置 → 启用分应用 + 绕行模式"],
  ["Clash Meta", "设置 → 网络 → 访问控制模式 → 不允许已选择的应用"],
];

function BypassSheetBody() {
  return (
    <>
      <p>
        im.gamer 要放在代理客户端的 <b className="text-foreground">VPN 外面</b>
        ，不然它自己连线路的流量会绕回代理。用客户端设置里的
        <b className="text-foreground">「分应用代理 / 访问控制」</b>把 im.gamer{" "}
        <b className="text-foreground">排除</b>
        （绕过、黑名单或不允许已选应用）。
      </p>
      <div className="space-y-2 rounded-xl bg-default-100 px-3 py-2.5 dark:bg-default-100/60">
        {BYPASS_STEPS.map(([app, how]) => (
          <div key={app} className="text-xs leading-5">
            <div className="font-semibold text-foreground">{app}</div>
            <div>{how}</div>
          </div>
        ))}
      </div>
      <p>
        <b className="text-foreground">不要用路由或规则</b>
        里的「按应用直连 / 绕过」代替：那样它的流量还是先进
        VPN，规则没匹配上或客户端重启时就会绕回代理。
      </p>
    </>
  );
}

const SHEET_TITLE: Record<CardKey, string> = {
  battery: "电池设为「无限制」",
  cred: "商家账号",
  node: "节点地址改成 127.0.0.1",
  bypass: "分应用代理里绕过 im.gamer",
};

// ---- the shatter (OnboardingViews.Shatter, in CSS) ----

const SHATTER_MS = 620;
const REFLOW_MS = 320;
const EASE = "cubic-bezier(0.4, 0, 0.2, 1)";

/**
 * Breaks `card`: ~30 jittered triangles, each a clipped copy of the card in a
 * fixed layer over it, fly out from the middle (the outer ones a few ms
 * later), turn, fall and fade in ≈0.6 s (Web Animations: transform + opacity
 * only). The card's place stays empty until `reflow` runs (60 %). False =
 * nothing done (not laid out / no Web Animations).
 */
function shatter(card: HTMLElement, reflow: () => void): boolean {
  const r = card.getBoundingClientRect();

  if (r.width <= 0 || r.height <= 0 || typeof card.animate !== "function")
    return false;
  const COLS = 5;
  const ROWS = 3;
  const w = r.width;
  const h = r.height;
  const rnd = Math.random;
  const px: number[][] = [];
  const py: number[][] = [];

  for (let i = 0; i <= ROWS; i++) {
    px.push([]);
    py.push([]);
    for (let j = 0; j <= COLS; j++) {
      const jx = j === 0 || j === COLS ? 0 : (rnd() - 0.5) * 0.6 * (w / COLS);
      const jy = i === 0 || i === ROWS ? 0 : (rnd() - 0.5) * 0.6 * (h / ROWS);

      px[i].push((j * w) / COLS + jx);
      py[i].push((i * h) / ROWS + jy);
    }
  }
  const layer = document.createElement("div");

  layer.setAttribute("aria-hidden", "true");
  layer.setAttribute("data-phone-shards", "");
  Object.assign(layer.style, {
    position: "fixed",
    left: `${r.left}px`,
    top: `${r.top}px`,
    width: `${w}px`,
    height: `${h}px`,
    pointerEvents: "none",
    zIndex: "60",
  });
  const tpl = card.cloneNode(true) as HTMLElement;

  tpl.querySelectorAll(".flvx-onboarding-ring").forEach((e) => e.remove());
  tpl.removeAttribute("data-phone-card");
  tpl.removeAttribute("id");
  Object.assign(tpl.style, {
    position: "absolute",
    left: "0",
    top: "0",
    margin: "0",
    width: `${w}px`,
    height: `${h}px`,
    visibility: "visible",
    transform: "none",
  });
  const mx = w / 2;
  const my = h / 2;
  const far = Math.hypot(mx, my);
  const total = SHATTER_MS / 1000;
  const g = 1500;
  const STEPS = 10;

  for (let i = 0; i < ROWS; i++) {
    for (let j = 0; j < COLS; j++) {
      const slash = (i + j) % 2 === 0;
      const a = [
        [px[i][j], py[i][j]],
        [px[i][j + 1], py[i][j + 1]],
        slash
          ? [px[i + 1][j + 1], py[i + 1][j + 1]]
          : [px[i + 1][j], py[i + 1][j]],
      ];
      const b = slash
        ? [
            [px[i][j], py[i][j]],
            [px[i + 1][j + 1], py[i + 1][j + 1]],
            [px[i + 1][j], py[i + 1][j]],
          ]
        : [
            [px[i][j + 1], py[i][j + 1]],
            [px[i + 1][j + 1], py[i + 1][j + 1]],
            [px[i + 1][j], py[i + 1][j]],
          ];

      for (const tri of [a, b]) {
        const cx = (tri[0][0] + tri[1][0] + tri[2][0]) / 3;
        const cy = (tri[0][1] + tri[1][1] + tri[2][1]) / 3;
        const dx = cx - mx;
        const dy = cy - my;
        const d = Math.max(1, Math.hypot(dx, dy));
        const speed = 140 + rnd() * 240;
        const vx = (dx / d) * speed;
        const vy = (dy / d) * speed * 0.7 - (60 + rnd() * 200);
        const spin = (rnd() - 0.5) * 840;
        const delay = (d / far) * 0.07;
        const frag = tpl.cloneNode(true) as HTMLElement;

        frag.style.clipPath = `polygon(${tri.map((p) => `${p[0].toFixed(1)}px ${p[1].toFixed(1)}px`).join(", ")})`;
        frag.style.transformOrigin = `${cx.toFixed(1)}px ${cy.toFixed(1)}px`;
        frag.style.willChange = "transform, opacity";
        const frames: Keyframe[] = [];

        for (let k = 0; k <= STEPS; k++) {
          const t = (k / STEPS) * total;
          const tt = Math.max(0, t - delay);
          const life = tt / Math.max(0.05, total - delay);
          const alpha = life < 0.25 ? 1 : Math.max(0, 1 - (life - 0.25) / 0.75);
          const scale = 1 - 0.2 * life;
          const tx = vx * tt;
          const ty = vy * tt + 0.5 * g * tt * tt;

          frames.push({
            offset: k / STEPS,
            opacity: alpha,
            transform: `translate(${tx.toFixed(1)}px, ${ty.toFixed(1)}px) rotate(${(spin * tt).toFixed(1)}deg) scale(${scale.toFixed(3)})`,
          });
        }
        layer.appendChild(frag);
        frag.animate(frames, {
          duration: SHATTER_MS,
          easing: "linear",
          fill: "forwards",
        });
      }
    }
  }
  document.body.appendChild(layer);
  card.style.visibility = "hidden";
  window.setTimeout(reflow, SHATTER_MS * 0.6);
  window.setTimeout(() => layer.remove(), SHATTER_MS + 60);

  return true;
}

// ---- the cards ----

export function PhoneFirstRun({
  s,
  rules,
  onCredentials,
  onSub,
}: {
  s: PhoneState;
  rules: LeiKwanRule[];
  /** the App's own 商家账号 dialog */
  onCredentials: () => void;
  /** 利群汇聚 「生成订阅」 (the 127.0.0.1 sheet's button) */
  onSub?: () => void;
}) {
  const unrestricted = !!s.battery.unrestricted;
  const credSet = !!s.credentials.set;
  const [cards, setCards] = useState<CardKey[]>(() => {
    // 「无限制」 / account saved already: that card retires for good, unseen.
    if (unrestricted) lsSet(DONE("battery"));
    if (credSet) lsSet(DONE("cred"));

    return ORDER.filter((k) => !lsOn(DONE(k)));
  });
  const [seen, setSeen] = useState<CardKey[]>(() =>
    ORDER.filter((k) => lsOn(SEEN(k))),
  );
  const [leaving, setLeaving] = useState<CardKey[]>([]);
  const [settling, setSettling] = useState(false);
  const [sheet, setSheet] = useState<CardKey | null>(null);
  // What the sheet shows (kept while it closes).
  const [view, setView] = useState<CardKey>("node");
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const els = useRef(new Map<CardKey, HTMLButtonElement>());
  const cardsRef = useRef(cards);

  cardsRef.current = cards;

  /** Takes `key` out; the others glide to their new place (FLIP). */
  const removeCard = (key: CardKey, animate: boolean) => {
    const wrap = wrapRef.current;
    const rest = cardsRef.current.filter((k) => k !== key);
    const before = new Map<CardKey, DOMRect>();

    for (const k of rest) {
      const e = els.current.get(k);

      if (e) before.set(k, e.getBoundingClientRect());
    }
    const h0 = wrap?.offsetHeight || 0;

    flushSync(() => {
      setCards(rest);
      setLeaving((l) => l.filter((k) => k !== key));
      setSettling(animate && !!wrap);
    });
    if (!animate || !wrap) return;
    const h1 = rest.length ? wrap.offsetHeight : 0;

    wrap.style.overflow = "hidden";
    // The last card: hold 0 px until the block unmounts (no 16 px jump).
    const grow = wrap.animate([{ height: `${h0}px` }, { height: `${h1}px` }], {
      duration: REFLOW_MS,
      easing: EASE,
      fill: rest.length ? "none" : "forwards",
    });

    for (const k of rest) {
      const e = els.current.get(k);
      const was = before.get(k);

      if (!e || !was) continue;
      const now = e.getBoundingClientRect();
      const dx = was.left - now.left;
      const dy = was.top - now.top;

      if (dx || dy)
        e.animate(
          [{ transform: `translate(${dx}px, ${dy}px)` }, { transform: "none" }],
          { duration: REFLOW_MS, easing: EASE },
        );
    }
    grow.onfinish = () => {
      wrap.style.overflow = "";
      setSettling(false);
    };
  };

  const start = (key: CardKey) => {
    const el = els.current.get(key);
    const motion = !reducedMotion();

    if (el && motion && shatter(el, () => removeCard(key, true))) return;
    if (!el || typeof el.animate !== "function") {
      removeCard(key, false);

      return;
    }
    const fade = el.animate([{ opacity: 1 }, { opacity: 0 }], {
      duration: 180,
      easing: EASE,
      fill: "forwards",
    });

    fade.onfinish = () => removeCard(key, motion);
  };

  /** Puts `key` away after `delayMs`: it shatters (reduced motion: fades). */
  const leave = (key: CardKey, delayMs: number) => {
    setLeaving((l) => (l.includes(key) ? l : [...l, key]));
    window.setTimeout(() => start(key), delayMs);
  };

  // The battery turned 「无限制」 / the account got saved with its card on
  // screen: ✓ 已设置 / 已填, then it breaks away (once; never comes back).
  const settle = (key: LiveKey) => {
    lsSet(DONE(key));
    if (cardsRef.current.includes(key) && !leaving.includes(key)) {
      setSheet((k) => (k === key ? null : k));
      leave(key, 650);
    }
  };

  useEffect(() => {
    if (unrestricted) settle("battery");
  }, [unrestricted]);
  useEffect(() => {
    if (credSet) settle("cred");
  }, [credSet]);

  const tap = (key: CardKey) => {
    if (leaving.includes(key)) return;
    // The first tap stops this card's ring for good; it moves on.
    if (!seen.includes(key)) {
      lsSet(SEEN(key));
      setSeen((v) => [...v, key]);
    }
    // 商家账号: straight to the App's dialog, no sheet.
    if (key === "cred") {
      onCredentials();

      return;
    }
    setView(key);
    setSheet(key);
  };
  const ok = (key: CardKey) => {
    setSheet(null);
    // 节点 / 分应用代理 go for good; the battery card waits for 「无限制」.
    if (key === "battery" || key === "cred") return;
    lsSet(DONE(key));
    leave(key, 200);
  };

  if (cards.length === 0 && !settling) return null;
  // The ring: the first card that stays and was not tapped yet; never while a
  // sheet is open or while cards leave / reflow.
  const ringOn =
    sheet || settling || leaving.length
      ? null
      : cards.find((k) => !seen.includes(k)) || null;

  return (
    <div ref={wrapRef} className="pb-4" data-phone-cards="">
      <div className="grid max-w-md grid-cols-2 gap-2.5">
        {cards.map((k) => {
          const live = k === "battery" || k === "cred";
          const set = k === "battery" ? unrestricted : k === "cred" && credSet;
          const chip =
            k === "battery"
              ? set
                ? "已设置"
                : "未设置"
              : k === "cred"
                ? set
                  ? "已填"
                  : "未填"
                : "必看";

          return (
            <button
              key={k}
              ref={(e) => {
                if (e) els.current.set(k, e);
                else els.current.delete(k);
              }}
              aria-label={`${FACE[k]}，${chip}${ringOn === k ? "，先看这张" : ""}`}
              className="relative flex min-h-[84px] flex-col items-stretch gap-2.5 rounded-2xl bg-content1 p-3 text-left text-foreground shadow-[0_1px_3px_rgba(15,23,42,0.08)] ring-1 ring-black/[0.04] transition-transform active:scale-[0.98] dark:bg-default-100 dark:ring-white/[0.06]"
              data-phone-card={k}
              data-phone-card-ring={ringOn === k ? "" : undefined}
              type="button"
              onClick={() => tap(k)}
            >
              <span className="flex items-center justify-between gap-2">
                <span
                  className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full ${TONE[k].badge}`}
                >
                  {ICON[k]}
                </span>
                <span
                  className={`inline-flex shrink-0 items-center gap-0.5 whitespace-nowrap rounded-full px-2 text-[11px] font-semibold leading-[18px] ${set ? chipGreen : TONE[k].chip}`}
                >
                  {live &&
                    (set ? (
                      <Check size={11} strokeWidth={3} />
                    ) : (
                      <AlertTriangle size={11} strokeWidth={2.5} />
                    ))}
                  {chip}
                </span>
              </span>
              <span className="line-clamp-2 text-[13px] font-semibold leading-[18px] [word-break:keep-all]">
                {FACE_PARTS[k].map((part, i) => (
                  <span key={part}>
                    {i > 0 && <wbr />}
                    {part}
                  </span>
                ))}
              </span>
              <OnboardingRing active={ringOn === k} />
            </button>
          );
        })}
      </div>

      <PhoneSheet
        footer={
          view === "battery" ? (
            <>
              <Button
                className="h-11 flex-1"
                color="default"
                data-phone-sheet-ok=""
                variant="flat"
                onPress={() => ok("battery")}
              >
                我知道了
              </Button>
              <Button
                className="h-11 flex-1 font-semibold"
                color="primary"
                data-phone-sheet-go=""
                onPress={() => {
                  setSheet(null);
                  void run(appCall("openBatterySettings"));
                }}
              >
                去设置
              </Button>
            </>
          ) : view === "node" && onSub ? (
            <>
              <Button
                className="h-11 flex-1"
                color="secondary"
                data-phone-sheet-sub=""
                variant="flat"
                onPress={() => {
                  ok("node");
                  onSub();
                }}
              >
                生成订阅
              </Button>
              <Button
                className="h-11 flex-1 font-semibold"
                color="primary"
                data-phone-sheet-ok=""
                onPress={() => ok("node")}
              >
                我知道了
              </Button>
            </>
          ) : (
            <Button
              className="h-11 flex-1 font-semibold"
              color="primary"
              data-phone-sheet-ok=""
              onPress={() => ok(view)}
            >
              我知道了
            </Button>
          )
        }
        name={view}
        open={!!sheet}
        title={SHEET_TITLE[view]}
        onClose={() => setSheet(null)}
      >
        {view === "battery" && <BatterySheetBody s={s} />}
        {view === "node" && <NodeSheetBody rules={rules} s={s} />}
        {view === "bypass" && <BypassSheetBody />}
      </PhoneSheet>
    </div>
  );
}
