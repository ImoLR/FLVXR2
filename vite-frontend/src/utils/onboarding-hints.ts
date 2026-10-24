// Plan 146: first-time 落地 hints (a 15 s spinning ring on three spots). Client-side only: the state
// lives in localStorage per user, no API. A hint is (re)armed by a trigger (node / rule create), plays
// once for that trigger, and never plays again after the user has clicked its element.
import { JwtUtil } from "@/utils/jwt";

export type OnboardingHintKey = "useNode" | "targetType" | "copyLink";

interface HintState {
  done?: boolean;
  // Token of the last trigger (node id / forward id) and the token that already played.
  trigger?: number;
  played?: number;
}

type HintStore = Partial<Record<OnboardingHintKey, HintState>>;

export const ONBOARDING_HINT_EVENT = "flvx-onboarding-hint";
const KEY_PREFIX = "flvx.hint.v1.";

// The token only changes on login/logout: decode it once per token, not once per rule row render.
let cachedToken: string | null | undefined;
let cachedKey: string | null = null;

const storageKey = (): string | null => {
  const token = localStorage.getItem("token");

  if (token !== cachedToken) {
    const userId = JwtUtil.getUserIdFromToken();

    cachedToken = token;
    cachedKey = userId && userId > 0 ? `${KEY_PREFIX}${userId}` : null;
  }

  return cachedKey;
};

// Rule rows read their hint on every render: parse only when the stored string changed.
let cachedRaw: string | null = null;
let cachedStore: HintStore = {};

const readStore = (key: string): HintStore => {
  try {
    const raw = localStorage.getItem(key);

    if (raw !== cachedRaw) {
      const parsed = JSON.parse(raw || "{}");

      cachedRaw = raw;
      cachedStore = parsed && typeof parsed === "object" ? parsed : {};
    }

    return cachedStore;
  } catch {
    return {};
  }
};

const update = (
  hint: OnboardingHintKey,
  change: (state: HintState) => HintState | null,
) => {
  const key = storageKey();

  if (!key) return;
  const store = { ...readStore(key) };
  const next = change(store[hint] || {});

  if (!next) return;
  store[hint] = next;
  try {
    localStorage.setItem(key, JSON.stringify(store));
  } catch {
    return;
  }
  window.dispatchEvent(new Event(ONBOARDING_HINT_EVENT));
};

export const readOnboardingHint = (hint: OnboardingHintKey): HintState => {
  const key = storageKey();

  return key ? readStore(key)[hint] || {} : {};
};

// Arms a not-done hint for a new trigger token (it plays once more).
export const armOnboardingHint = (hint: OnboardingHintKey, token: number) =>
  update(hint, (state) =>
    state.done || !token || state.trigger === token
      ? null
      : { ...state, trigger: token },
  );

export const markOnboardingHintPlayed = (
  hint: OnboardingHintKey,
  token: number,
) =>
  update(hint, (state) =>
    state.played === token ? null : { ...state, played: token },
  );

// The user used the element: the hint never shows again.
export const completeOnboardingHint = (hint: OnboardingHintKey) =>
  update(hint, (state) => (state.done ? null : { ...state, done: true }));

// Returns the trigger token that should play now, or 0.
export const pendingOnboardingHint = (hint: OnboardingHintKey): number => {
  const state = readOnboardingHint(hint);

  return !state.done && state.trigger && state.trigger !== state.played
    ? state.trigger
    : 0;
};

// A successful 落地 node create (re)arms 「用于新规则」 (own nodes only: it rings on the user's newest
// node) and the rule dialog's 「目标类型」.
export const armLandingNodeHints = (node?: {
  id?: number;
  userId?: number;
}) => {
  const id = Number(node?.id) || 0;

  if (!id) return;
  const userId = JwtUtil.getUserIdFromToken();

  if (!node?.userId || node.userId === userId) armOnboardingHint("useNode", id);
  armOnboardingHint("targetType", id);
};
