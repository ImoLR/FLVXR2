import { useCallback, useEffect, useRef, useState } from "react";
import axios from "axios";

interface NodeRealtimeMessage {
  id?: string | number;
  type?: string;
  data?: unknown;
  message?: string;
}

interface UseNodeRealtimeOptions {
  onMessage: (message: NodeRealtimeMessage) => void;
  enabled?: boolean;
}

const MAX_STANDARD_RECONNECT_ATTEMPTS = 5;
const STANDARD_RECONNECT_DELAY_MS = 3000;
const MAX_STANDARD_RECONNECT_DELAY_MS = 15000;
const FALLBACK_RECONNECT_DELAY_MS = 30000;
const AUTHENTICATED_STREAM_TYPE = 0;
const PUBLIC_STREAM_TYPE = 2;

const getRealtimeWsUrl = (streamType: number): string => {
  const baseUrl =
    axios.defaults.baseURL ||
    (import.meta.env.VITE_API_BASE
      ? `${import.meta.env.VITE_API_BASE}/api/v1/`
      : "/api/v1/");
  const url = new URL(baseUrl, window.location.origin);

  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.pathname = url.pathname.replace(/\/api\/v1\/$/, "/system-info");
  url.search = `type=${streamType}`;
  url.hash = "";

  return url.toString();
};

export const useNodeRealtime = ({
  onMessage,
  enabled = true,
}: UseNodeRealtimeOptions) => {
  const [wsConnected, setWsConnected] = useState(false);
  const [wsConnecting, setWsConnecting] = useState(false);
  const [usingPollingFallback, setUsingPollingFallback] = useState(false);

  const websocketRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const streamTypeRef = useRef(AUTHENTICATED_STREAM_TYPE);
  const onMessageRef = useRef(onMessage);

  useEffect(() => {
    onMessageRef.current = onMessage;
  }, [onMessage]);

  const clearReconnectTimer = useCallback(() => {
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }, []);

  const disconnect = useCallback(() => {
    clearReconnectTimer();
    reconnectAttemptsRef.current = 0;
    streamTypeRef.current = AUTHENTICATED_STREAM_TYPE;
    setWsConnected(false);
    setWsConnecting(false);
    setUsingPollingFallback(false);

    if (!websocketRef.current) {
      return;
    }

    websocketRef.current.onopen = null;
    websocketRef.current.onmessage = null;
    websocketRef.current.onerror = null;
    websocketRef.current.onclose = null;

    if (
      websocketRef.current.readyState === WebSocket.OPEN ||
      websocketRef.current.readyState === WebSocket.CONNECTING
    ) {
      websocketRef.current.close();
    }

    websocketRef.current = null;
  }, [clearReconnectTimer]);

  const connect = useCallback(() => {
    if (!enabled) {
      return;
    }

    if (
      websocketRef.current &&
      (websocketRef.current.readyState === WebSocket.OPEN ||
        websocketRef.current.readyState === WebSocket.CONNECTING)
    ) {
      return;
    }

    if (websocketRef.current) {
      disconnect();
    }

    try {
      setWsConnecting(true);
      const attemptedStreamType = streamTypeRef.current;

      websocketRef.current = new WebSocket(
        getRealtimeWsUrl(attemptedStreamType),
      );

      websocketRef.current.onopen = () => {
        reconnectAttemptsRef.current = 0;
        setWsConnected(true);
        setWsConnecting(false);
        setUsingPollingFallback(false);
      };

      websocketRef.current.onmessage = (event) => {
        try {
          const parsed = JSON.parse(event.data);

          if (parsed && typeof parsed === "object") {
            onMessageRef.current(parsed as NodeRealtimeMessage);
          }
        } catch {}
      };

      websocketRef.current.onerror = () => {};

      websocketRef.current.onclose = () => {
        websocketRef.current = null;
        setWsConnected(false);
        setWsConnecting(false);

        if (!enabled) {
          return;
        }

        if (attemptedStreamType === AUTHENTICATED_STREAM_TYPE) {
          streamTypeRef.current = PUBLIC_STREAM_TYPE;
          reconnectTimerRef.current = setTimeout(() => {
            reconnectTimerRef.current = null;
            connect();
          }, 250);

          return;
        }

        reconnectAttemptsRef.current += 1;
        const exhaustedStandardRetries =
          reconnectAttemptsRef.current >= MAX_STANDARD_RECONNECT_ATTEMPTS;

        setUsingPollingFallback(exhaustedStandardRetries);

        const reconnectDelay = exhaustedStandardRetries
          ? FALLBACK_RECONNECT_DELAY_MS
          : Math.min(
              STANDARD_RECONNECT_DELAY_MS * reconnectAttemptsRef.current,
              MAX_STANDARD_RECONNECT_DELAY_MS,
            );

        reconnectTimerRef.current = setTimeout(() => {
          reconnectTimerRef.current = null;
          connect();
        }, reconnectDelay);
      };
    } catch {
      setWsConnected(false);
      setWsConnecting(false);
    }
  }, [disconnect, enabled]);

  useEffect(() => {
    if (!enabled) {
      return;
    }

    connect();

    return () => {
      disconnect();
    };
  }, [connect, disconnect, enabled]);

  return {
    wsConnected,
    wsConnecting,
    usingPollingFallback,
    reconnectRealtime: connect,
    disconnectRealtime: disconnect,
  };
};
