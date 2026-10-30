// Plan 146: 落地 "new" hints (a 15 s spinning ring on three spots). Client-side only: the state
// lives in localStorage per user, no API. Plan 169: no trigger any more — a hint plays on every
// page visit until the user has clicked its element once, then never again.
import { JwtUtil } from "@/utils/jwt";

export type OnboardingHintKey = "useNode" | "targetType" | "copyLink";

interface HintState {
  done?: boolean;
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

// The user used the element: the hint never shows again.
export const completeOnboardingHint = (hint: OnboardingHintKey) =>
  update(hint, (state) => (state.done ? null : { ...state, done: true }));

// Plays (again) on the next page visit until the user has used the element.
export const pendingOnboardingHint = (hint: OnboardingHintKey): boolean =>
  !readOnboardingHint(hint).done;
