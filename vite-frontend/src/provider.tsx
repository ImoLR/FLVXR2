import * as React from "react";
import { useHref, useNavigate } from "react-router-dom";
import toast, { ToastBar, Toaster } from "react-hot-toast";
import { I18nProvider } from "@react-aria/i18n";

import { HeroUIProvider } from "@/shadcn-bridge/heroui/system";
import { ThemeProvider } from "@/components/theme-provider";
import { installErrorToastRule } from "@/utils/toast-rules";

// Plan 216: error toasts stay readable (length-scaled, 6–15 s).
installErrorToastRule();

export interface ProvidersProps {
  children: React.ReactNode;
}

export function Provider({ children }: ProvidersProps) {
  const navigate = useNavigate();

  return (
    <I18nProvider locale="zh-CN">
      <HeroUIProvider navigate={navigate} useHref={useHref}>
        <ThemeProvider>
          {children}
          <Toaster
            position="top-center"
            toastOptions={{
              duration: 2000,
              className: "dark:bg-gray-800 dark:text-white",
              style: {
                background: "var(--toaster-bg, #ffffff)",
                color: "var(--toaster-color, #000000)",
                border: "1px solid var(--toaster-border, #e5e7eb)",
              },
              success: {
                duration: 2000,
                style: {
                  background: "#10b981",
                  color: "#ffffff",
                },
              },
              error: {
                style: {
                  background: "#ef4444",
                  color: "#ffffff",
                },
              },
            }}
          >
            {(t) => (
              // Plan 216: tap / click anywhere on a toast dismisses it; long
              // error toasts also get a ✕ so they never block anything.
              <ToastBar toast={t}>
                {({ icon, message }) => (
                  <div
                    className="flex cursor-pointer items-center"
                    role="presentation"
                    title="点击关闭"
                    onClick={() => toast.dismiss(t.id)}
                  >
                    {icon}
                    {message}
                    {t.type === "error" && (
                      <button
                        aria-label="关闭"
                        className="ml-1 shrink-0 rounded px-1 text-base leading-none opacity-80 hover:opacity-100"
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          toast.dismiss(t.id);
                        }}
                      >
                        ✕
                      </button>
                    )}
                  </div>
                )}
              </ToastBar>
            )}
          </Toaster>
        </ThemeProvider>
      </HeroUIProvider>
    </I18nProvider>
  );
}
