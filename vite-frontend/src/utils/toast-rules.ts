import toast, { type ToastOptions } from "react-hot-toast";

// Plan 216: an error toast stays long enough to read — about 0.12 s per
// character, at least 6 s, at most 15 s. Success / info toasts keep the
// 2 s default set on the Toaster.
export const ERROR_TOAST_MIN_MS = 6000;
export const ERROR_TOAST_MAX_MS = 15000;
export const ERROR_TOAST_MS_PER_CHAR = 120;

export function errorToastDuration(message: unknown, requested?: number) {
  const text = typeof message === "string" ? message : "";
  const byLength = Math.min(
    ERROR_TOAST_MAX_MS,
    Math.max(
      ERROR_TOAST_MIN_MS,
      Array.from(text).length * ERROR_TOAST_MS_PER_CHAR,
    ),
  );

  // A call site may ask for longer (e.g. Infinity for a sticky error), never shorter.
  return typeof requested === "number" && requested > byLength
    ? requested
    : byLength;
}

let installed = false;

// installErrorToastRule routes every toast.error(...) through the one rule,
// so call sites passing their own short duration still get a readable toast.
export function installErrorToastRule() {
  if (installed) return;
  installed = true;
  const original = toast.error;

  toast.error = (
    message: Parameters<typeof original>[0],
    options?: ToastOptions,
  ) =>
    original(message, {
      ...options,
      duration: errorToastDuration(message, options?.duration),
    });
}
