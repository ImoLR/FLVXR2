import type { TunnelDiagnosisApiItem } from "@/api/types";
import type {
  LeiKwanDiagDone,
  LeiKwanDiagItem,
  LeiKwanDiagStart,
} from "@/api/leikwan-line";

import axios from "axios";

import { diagnoseLeiKwanRule } from "@/api/leikwan-line";
import { clearSession, getToken } from "@/utils/session";

const DIAGNOSIS_STREAM_TIMEOUT_MS = 2 * 60 * 1000;

type RawObject = Record<string, unknown>;

interface DiagnosisStreamRawEvent {
  type?: string;
  data?: unknown;
  ts?: number;
}

export interface DiagnosisStreamProgress {
  total: number;
  completed: number;
  success: number;
  failed: number;
  timedOut?: boolean;
}

export interface DiagnosisStreamItemPayload<TItem = TunnelDiagnosisApiItem> {
  index: number;
  result: TItem;
  progress: DiagnosisStreamProgress;
}

export interface DiagnosisStreamRunResult {
  fallback: boolean;
  completed: boolean;
  timedOut: boolean;
  receivedItems: number;
}

export interface DiagnosisStreamCallbacks<TItem = TunnelDiagnosisApiItem> {
  onStart?: (payload: RawObject) => void;
  onItem: (payload: DiagnosisStreamItemPayload<TItem>) => void;
  onDone?: (payload: DiagnosisStreamProgress) => void;
  onError?: (message: string) => void;
  /** Plan 272: the raw data of the done event (e.g. a summary). */
  onDoneData?: (data: RawObject) => void;
  /**
   * Plan 272: the endpoint answered with an error envelope ({code≠0, msg},
   * e.g. a rate limit) instead of a stream. Without it such a line is
   * ignored as before.
   */
  onEnvelopeError?: (message: string) => void;
}

export interface RunDiagnosisStreamOptions<TItem = TunnelDiagnosisApiItem>
  extends DiagnosisStreamCallbacks<TItem> {
  path: string;
  body: RawObject;
  signal?: AbortSignal;
}

const normalizeProgress = (
  payload: unknown,
  fallback: DiagnosisStreamProgress,
): DiagnosisStreamProgress => {
  if (!payload || typeof payload !== "object") {
    return fallback;
  }
  const candidate = payload as RawObject;
  const total = Number(candidate.total);
  const completed = Number(candidate.completed);
  const success = Number(candidate.success);
  const failed = Number(candidate.failed);

  return {
    total: Number.isFinite(total) && total >= 0 ? total : fallback.total,
    completed:
      Number.isFinite(completed) && completed >= 0
        ? completed
        : fallback.completed,
    success:
      Number.isFinite(success) && success >= 0 ? success : fallback.success,
    failed: Number.isFinite(failed) && failed >= 0 ? failed : fallback.failed,
    timedOut:
      typeof candidate.timedOut === "boolean"
        ? candidate.timedOut
        : fallback.timedOut,
  };
};

const resolveApiPath = (path: string): string => {
  const normalizedPath = path.replace(/^\//, "");
  const baseURL = axios.defaults.baseURL || "/api/v1/";
  const normalizedBase = baseURL.endsWith("/") ? baseURL : `${baseURL}/`;

  return `${normalizedBase}${normalizedPath}`;
};

const isStreamSupported = (): boolean => {
  return (
    typeof window !== "undefined" &&
    typeof fetch === "function" &&
    typeof TextDecoder !== "undefined"
  );
};

const handleTokenExpired = () => {
  clearSession();
  if (window.location.pathname !== "/") {
    window.location.href = "/";
  }
};

const combineAbortSignals = (signals: AbortSignal[]): AbortSignal => {
  const controller = new AbortController();
  const onAbort = () => {
    if (!controller.signal.aborted) {
      controller.abort();
    }
  };

  signals.forEach((signal) => {
    if (signal.aborted) {
      onAbort();

      return;
    }
    signal.addEventListener("abort", onAbort, { once: true });
  });

  return controller.signal;
};

const parseMessage = (err: unknown, fallback: string): string => {
  if (err instanceof Error && err.message) {
    return err.message;
  }

  return fallback;
};

export const runDiagnosisStream = async <TItem = TunnelDiagnosisApiItem>({
  path,
  body,
  signal,
  onStart,
  onItem,
  onDone,
  onError,
  onDoneData,
  onEnvelopeError,
}: RunDiagnosisStreamOptions<TItem>): Promise<DiagnosisStreamRunResult> => {
  if (!isStreamSupported()) {
    return {
      fallback: true,
      completed: false,
      timedOut: false,
      receivedItems: 0,
    };
  }

  let receivedItems = 0;
  let completed = false;
  let timedOut = false;
  let currentProgress: DiagnosisStreamProgress = {
    total: 0,
    completed: 0,
    success: 0,
    failed: 0,
  };

  const timeoutController = new AbortController();
  const timeoutId = window.setTimeout(() => {
    timedOut = true;
    timeoutController.abort();
  }, DIAGNOSIS_STREAM_TIMEOUT_MS);

  const mergedSignal = signal
    ? combineAbortSignals([timeoutController.signal, signal])
    : timeoutController.signal;

  try {
    const response = await fetch(resolveApiPath(path), {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/x-ndjson, application/json",
        ...(getToken() ? { Authorization: getToken() || "" } : {}),
      },
      credentials: "include",
      body: JSON.stringify(body),
      signal: mergedSignal,
    });

    if (response.status === 401) {
      handleTokenExpired();

      return {
        fallback: false,
        completed: false,
        timedOut: false,
        receivedItems,
      };
    }

    if (response.status === 404) {
      return {
        fallback: true,
        completed: false,
        timedOut: false,
        receivedItems,
      };
    }

    if (!response.ok || !response.body) {
      const fallbackMessage = `请求失败(${response.status})`;
      let message = fallbackMessage;

      try {
        const data = (await response.json()) as RawObject;

        if (typeof data.msg === "string" && data.msg.trim()) {
          message = data.msg;
        }
      } catch {}
      if (receivedItems === 0) {
        return {
          fallback: true,
          completed: false,
          timedOut: false,
          receivedItems,
        };
      }
      onError?.(message);

      return {
        fallback: false,
        completed: false,
        timedOut: false,
        receivedItems,
      };
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    const processLine = (line: string) => {
      if (!line) {
        return;
      }
      let parsed: DiagnosisStreamRawEvent;

      try {
        parsed = JSON.parse(line) as DiagnosisStreamRawEvent;
      } catch {
        return;
      }

      const eventType = (parsed.type || "").toLowerCase();
      const envelope = parsed as RawObject;

      if (
        !eventType &&
        onEnvelopeError &&
        typeof envelope.code === "number" &&
        envelope.code !== 0
      ) {
        onEnvelopeError(
          typeof envelope.msg === "string" && envelope.msg.trim()
            ? envelope.msg
            : "诊断失败",
        );

        return;
      }

      if (eventType === "start") {
        if (parsed.data && typeof parsed.data === "object") {
          const startData = parsed.data as RawObject;
          const startTotal = Number(startData.total);

          if (Number.isFinite(startTotal) && startTotal >= 0) {
            currentProgress = { ...currentProgress, total: startTotal };
          }
          onStart?.(startData);
        }

        return;
      }

      if (eventType === "item") {
        if (!parsed.data || typeof parsed.data !== "object") {
          return;
        }
        const itemData = parsed.data as RawObject;
        const index = Number(itemData.index);
        const result = itemData.result as TItem | undefined;

        if (!Number.isFinite(index) || !result || typeof result !== "object") {
          return;
        }
        const progress = normalizeProgress(itemData.progress, currentProgress);

        currentProgress = progress;
        receivedItems += 1;
        onItem({
          index,
          result,
          progress,
        });

        return;
      }

      if (eventType === "done") {
        completed = true;
        const donePayload =
          parsed.data && typeof parsed.data === "object"
            ? (parsed.data as RawObject)
            : {};
        const doneProgress = normalizeProgress(
          donePayload.progress ?? donePayload,
          currentProgress,
        );

        if (typeof donePayload.timedOut === "boolean") {
          doneProgress.timedOut = donePayload.timedOut;
          timedOut = donePayload.timedOut;
        }
        currentProgress = doneProgress;
        onDone?.(doneProgress);
        onDoneData?.(donePayload);
      }
    };

    while (true) {
      const { value, done } = await reader.read();

      if (done) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");

      buffer = lines.pop() ?? "";
      lines.forEach((line) => processLine(line.trim()));
    }

    const tail = buffer.trim();

    if (tail) {
      processLine(tail);
    }

    if (!completed && timedOut) {
      const timeoutProgress = {
        ...currentProgress,
        timedOut: true,
      };

      onDone?.(timeoutProgress);
    }

    return {
      fallback: false,
      completed,
      timedOut,
      receivedItems,
    };
  } catch (error) {
    if (timedOut) {
      const timeoutProgress = {
        ...currentProgress,
        timedOut: true,
      };

      onDone?.(timeoutProgress);

      return {
        fallback: false,
        completed: false,
        timedOut: true,
        receivedItems,
      };
    }

    if (signal?.aborted) {
      return {
        fallback: false,
        completed: false,
        timedOut: false,
        receivedItems,
      };
    }

    if (receivedItems === 0) {
      return {
        fallback: true,
        completed: false,
        timedOut: false,
        receivedItems,
      };
    }

    onError?.(parseMessage(error, "流式诊断中断"));

    return {
      fallback: false,
      completed: false,
      timedOut: false,
      receivedItems,
    };
  } finally {
    clearTimeout(timeoutId);
  }
};

export const diagnoseTunnelStream = (
  tunnelId: number,
  callbacks: DiagnosisStreamCallbacks,
  signal?: AbortSignal,
) => {
  return runDiagnosisStream({
    path: "/tunnel/diagnose/stream",
    body: { tunnelId },
    signal,
    ...callbacks,
  });
};

export const diagnoseForwardStream = (
  forwardId: number,
  callbacks: DiagnosisStreamCallbacks,
  signal?: AbortSignal,
) => {
  return runDiagnosisStream({
    path: "/forward/diagnose/stream",
    body: { forwardId },
    signal,
    ...callbacks,
  });
};

// ---- Plan 272: 利群规则诊断 (NDJSON like the rules page, non-stream fallback) ----

export interface LeiKwanDiagCallbacks {
  onStart: (data: LeiKwanDiagStart) => void;
  onItem: (payload: DiagnosisStreamItemPayload<LeiKwanDiagItem>) => void;
  onDone: (data: LeiKwanDiagDone) => void;
  /** a request error / rate limit (「诊断太频繁，请 10 秒后再试」) */
  onError: (message: string) => void;
}

export const diagnoseLeiKwanRuleStream = async (
  id: number,
  callbacks: LeiKwanDiagCallbacks,
  signal?: AbortSignal,
): Promise<DiagnosisStreamRunResult> => {
  const res = await runDiagnosisStream<LeiKwanDiagItem>({
    path: "/leikwan-rule/diagnose/stream",
    body: { id },
    signal,
    onStart: (data) => callbacks.onStart(data as unknown as LeiKwanDiagStart),
    onItem: callbacks.onItem,
    onDoneData: (data) => callbacks.onDone(data as unknown as LeiKwanDiagDone),
    onError: callbacks.onError,
    onEnvelopeError: callbacks.onError,
  });

  if (!res.fallback || signal?.aborted) return res;

  // No stream (old proxy / fetch unsupported): one plain request.
  const plain = await diagnoseLeiKwanRule(id);

  if (signal?.aborted) return { ...res, fallback: true };
  if (plain.code !== 0 || !plain.data) {
    callbacks.onError(plain.msg || "诊断失败");

    return { ...res, fallback: true };
  }
  const d = plain.data;
  const items = d.items || [];
  const success = items.filter((x) => !x.skipped && x.success).length;
  const failed = items.filter((x) => !x.skipped && !x.success).length;

  callbacks.onStart({
    total: items.length,
    timestamp: Date.now(),
    admin: d.admin,
    ruleName: d.ruleName,
    path: d.path,
    items,
  });
  callbacks.onDone({
    progress: { total: items.length, completed: items.length, success, failed },
    timedOut: d.timedOut,
    summary: d.summary,
    items,
  });

  return {
    fallback: true,
    completed: true,
    timedOut: d.timedOut,
    receivedItems: items.length,
  };
};
