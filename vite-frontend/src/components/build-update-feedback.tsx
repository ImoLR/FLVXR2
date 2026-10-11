import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { toast } from "react-hot-toast";

import { Card } from "@/shadcn-bridge/heroui/card";
import { siteConfig } from "@/config/site";
import { isWebViewFunc } from "@/utils/panel";

const LAST_BUILD_KEY = "flvx-last-build";
let updating = false;

// Render synchronously, even if the app's first render has not finished yet.
// The existing refresh steps proceed immediately without an extra timer.
export const showBuildUpdating = (): void => {
  if (updating) {
    return;
  }
  updating = true;
  const container = document.createElement("div");

  document.body.appendChild(container);
  flushSync(() => {
    createRoot(container).render(
      <div className="pointer-events-none fixed inset-x-4 top-4 z-[10000] flex justify-center">
        <Card
          aria-live="polite"
          className="w-72 space-y-3 bg-white p-4 text-sm text-gray-800 shadow-lg dark:bg-gray-800 dark:text-white"
          role="status"
        >
          <span>发现新版本，正在更新…</span>
          <div
            aria-label="正在更新"
            className="h-1 overflow-hidden rounded-full bg-primary/20"
            role="progressbar"
          >
            <div className="h-full w-1/3 animate-pulse rounded-full bg-primary" />
          </div>
        </Card>
      </div>,
    );
  });
};

export const showBuildUpdatedToast = (): void => {
  if (import.meta.env.DEV || isWebViewFunc()) {
    return;
  }

  try {
    const raw = localStorage.getItem(LAST_BUILD_KEY);
    let previousBuild: unknown;

    try {
      previousBuild = raw ? JSON.parse(raw)?.build : undefined;
    } catch {
      // Replace malformed storage with the current build below.
    }
    const navigation = performance.getEntriesByType(
      "navigation",
    )[0] as PerformanceNavigationTiming | undefined;
    const updated =
      (typeof previousBuild === "string" && previousBuild !== __APP_BUILD_ID__) ||
      (raw === null && navigation?.type === "reload");

    localStorage.setItem(
      LAST_BUILD_KEY,
      JSON.stringify({ build: __APP_BUILD_ID__, version: siteConfig.version }),
    );
    if (updated) {
      toast.success(`已更新到 v${siteConfig.version}`, {
        id: "flvx-build-updated",
        duration: 4000,
        style: {
          background: "var(--content1, #ffffff)",
          color: "var(--foreground, #000000)",
          border: "1px solid var(--border, #e5e7eb)",
        },
      });
    }
  } catch {
    // Unavailable storage must not prevent the app from starting.
  }
};
