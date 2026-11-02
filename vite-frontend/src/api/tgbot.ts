import Network from "./network";

// Plan 196 设置 → Telegram 通知 (admin only). The bot token is write-only:
// no endpoint ever returns it, only the bot's @username.
export interface TgBotChat {
  chatId: number;
  kind: string; // private | group | supergroup
  title: string;
  lastError: string;
  lastErrorAt: number;
  createdTime: number;
}

export interface TgBotSettings {
  enabled: boolean;
  days: number[];
  time: string; // HH:MM, 台湾时间
  muted: string[];
}

export interface TgBotStatus {
  tokenSet: boolean;
  profile: { id: number; username: string; name: string };
  running: boolean;
  lastError: string;
  lastErrorAt: number;
  lastOkAt: number;
  lastRun: string;
  nextRunAt: number;
  bindCode?: string;
  bindExpiresAt?: number;
  chats: TgBotChat[];
  settings: TgBotSettings;
}

export interface TgBotMachine {
  /** f<FLVX 节点 id> / o<救命版 id> */
  key: string;
  name: string;
  source: "flvx" | "old";
  /** FLVX 节点在救命版里也有：那边的名字 */
  oldMatch?: string;
  cycle: string;
  nextDue: number;
  days: number;
  muted: boolean;
  /** 本期（nextDue）已点「已续费」 */
  renewed: boolean;
}

export interface TgBotView extends TgBotStatus {
  machines: TgBotMachine[];
  noExpiry: number;
  /** 救命版数据："" 未接入，"ok"，"error" */
  oldPanel: "" | "ok" | "error";
  partial?: string;
  maxChats: number;
  maxDays: number;
}

export interface TgBotBindCode {
  code: string;
  expiresAt: number;
  command: string;
  link?: string;
}

export const getTgBot = () => Network.post<TgBotView>("/config/tgbot", {});

export const getTgBotStatus = () =>
  Network.post<TgBotStatus>("/config/tgbot/status", {});

export const saveTgBotToken = (token: string) =>
  Network.post<TgBotView>("/config/tgbot/token", { token });

export const clearTgBotToken = () =>
  Network.post<TgBotView>("/config/tgbot/token", { clear: true });

export const sendTgBotTest = () =>
  Network.post<{ sent: number }>("/config/tgbot/test", {});

export const newTgBotBindCode = () =>
  Network.post<TgBotBindCode>("/config/tgbot/bind-code", {});

export const unbindTgBotChat = (chatId: number) =>
  Network.post<TgBotStatus>("/config/tgbot/unbind", {
    chatId: String(chatId),
  });

export const saveTgBotSettings = (body: {
  enabled: boolean;
  days: string;
  time: string;
  muted: string[];
}) => Network.post<TgBotView>("/config/tgbot/settings", body);
