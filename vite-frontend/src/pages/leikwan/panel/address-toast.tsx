import type { Toast } from "react-hot-toast";

import { useEffect, useRef } from "react";
import toast from "react-hot-toast";

// Plan 272 (design 08): copying a 手动落地 利群 rule copies only 「地址:端口」.
// One card instead of the two 2 s toasts: what was copied, why there is no
// full link, how to use it; 15 s with a shrinking bar, hover keeps it open
// (react-hot-toast pauses its timer while the pointer is over the toaster;
// the bar pauses with it), a click or ✕ closes it. Only this toast is 15 s —
// the global Toaster defaults stay as they are.

const DURATION_MS = 15000;

function AddressToast({ t, address }: { t: Toast; address: string }) {
  const bar = useRef<HTMLDivElement>(null);
  const anim = useRef<Animation | null>(null);

  useEffect(() => {
    const el = bar.current;

    if (!el || typeof el.animate !== "function") return;
    anim.current = el.animate(
      [{ transform: "scaleX(1)" }, { transform: "scaleX(0)" }],
      { duration: DURATION_MS, easing: "linear", fill: "forwards" },
    );

    return () => anim.current?.cancel();
  }, []);

  const close = () => toast.dismiss(t.id);

  return (
    // The whole card closes on click; ✕ is the keyboard control.
    <div
      className={`pointer-events-auto flex w-[min(520px,calc(100vw-24px))] cursor-pointer items-start gap-3 rounded-xl bg-white px-4 py-3 text-left shadow-lg ring-1 ring-black/10 transition-all duration-200 dark:bg-slate-800 dark:ring-white/10 ${
        t.visible ? "translate-y-0 opacity-100" : "-translate-y-2 opacity-0"
      }`}
      data-leikwan-address-toast=""
      role="presentation"
      onClick={close}
      onMouseEnter={() => anim.current?.pause()}
      onMouseLeave={() => anim.current?.play()}
    >
      <span className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-emerald-500 text-xs text-white">
        ✓
      </span>
      <div className="min-w-0 flex-1 text-sm">
        <div className="font-semibold text-slate-900 dark:text-slate-100">
          已复制入口地址{" "}
          <span className="break-all font-mono font-normal text-slate-500 dark:text-slate-400">
            {address}
          </span>
        </div>
        <p className="mt-1 text-slate-600 dark:text-slate-300">
          这条规则的落地是你自己填的（手动落地），面板没有它的密码，所以生成不了完整链接，只复制了「地址:端口」。
        </p>
        <p className="mt-1 text-slate-600 dark:text-slate-300">
          用法：在客户端里打开你原来连这个落地的节点，只把
          <b className="text-slate-800 dark:text-slate-100">服务器地址和端口</b>
          改成上面这个，其他（协议、密码）都不动。
        </p>
        <div className="mt-2 flex items-center gap-2 text-xs text-slate-400 dark:text-slate-500">
          <div className="h-1 w-40 max-w-[40%] shrink-0 overflow-hidden rounded bg-slate-200 dark:bg-slate-700">
            <div ref={bar} className="h-1 origin-left bg-emerald-500" />
          </div>
          <span>15 秒后自动关闭 · 点一下关闭</span>
        </div>
      </div>
      <button
        aria-label="关闭"
        className="-mr-1 shrink-0 rounded px-1 text-base leading-none text-slate-400 hover:text-slate-600 dark:hover:text-slate-200"
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          close();
        }}
      >
        ✕
      </button>
    </div>
  );
}

/** Show the 「已复制入口地址」 card (design 08) for 15 s. */
export function showLeiKwanAddressToast(address: string) {
  toast.custom((t) => <AddressToast address={address} t={t} />, {
    duration: DURATION_MS,
  });
}
