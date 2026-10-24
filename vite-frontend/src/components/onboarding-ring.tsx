// Plan 146: a 15 s "new" hint — a thin ring with a small comet dot running around an element.
// Cost model: the ring is a static, masked 2px band; only the inner conic-gradient layer rotates
// (CSS transform/opacity animation, compositor thread) one lap at a time with a rest in between.
// One IntersectionObserver while waiting to play, a 15 s setTimeout plus one rest timer per lap
// while playing, nothing at all after the 15 s (the ring is unmounted).
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";

import {
  ONBOARDING_HINT_EVENT,
  OnboardingHintKey,
  completeOnboardingHint,
  markOnboardingHintPlayed,
  pendingOnboardingHint,
} from "@/utils/onboarding-hints";

export const ONBOARDING_RING_MS = 15000;

const subscribe = (listener: () => void) => {
  window.addEventListener(ONBOARDING_HINT_EVENT, listener);
  window.addEventListener("storage", listener);

  return () => {
    window.removeEventListener(ONBOARDING_HINT_EVENT, listener);
    window.removeEventListener("storage", listener);
  };
};

// At most one ring plays at a time: a newly starting ring stops the previous one.
let stopCurrent: (() => void) | null = null;

export function useOnboardingHint(
  hint: OnboardingHintKey,
  {
    enabled = true,
    match,
  }: {
    enabled?: boolean;
    // Only play for this trigger token (e.g. the forward id of the new rule).
    match?: number;
  } = {},
) {
  const token = useSyncExternalStore(
    subscribe,
    () => pendingOnboardingHint(hint),
    () => 0,
  );
  const [element, setElement] = useState<HTMLElement | null>(null);
  const [active, setActive] = useState(false);
  const eligible =
    enabled && token > 0 && (match === undefined || match === token);

  useEffect(() => {
    if (!eligible || !element || active) return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return;
        observer.disconnect();
        // Another copy of the element (table + card view) may have played it already.
        if (pendingOnboardingHint(hint) !== token) return;
        markOnboardingHintPlayed(hint, token);
        setActive(true);
      },
      { threshold: 0.6 },
    );

    observer.observe(element);

    return () => observer.disconnect();
  }, [eligible, element, active, hint, token]);

  useEffect(() => {
    if (!active) return;
    const stop = () => setActive(false);

    stopCurrent?.();
    stopCurrent = stop;
    const timer = window.setTimeout(stop, ONBOARDING_RING_MS);

    return () => {
      window.clearTimeout(timer);
      if (stopCurrent === stop) stopCurrent = null;
    };
  }, [active]);

  const complete = useCallback(() => {
    setActive(false);
    completeOnboardingHint(hint);
  }, [hint]);

  // The ref is only attached while the hint can play, so the many rule rows that never ring
  // don't re-render or get observed.
  return { ref: eligible || active ? setElement : undefined, active, complete };
}

// Render inside a `position: relative` element; it hugs that element's rounded border.
export function OnboardingRing({ active }: { active: boolean }) {
  if (!active) return null;

  return <OnboardingRingPlaying />;
}

// The comet runs one lap, then rests (static band only, no animation running) and starts again.
// While nothing animates the compositor stays idle, so the 15 s cost is roughly halved.
const ONBOARDING_LAP_REST_MS = 2600;

function OnboardingRingPlaying() {
  const ringRef = useRef<HTMLSpanElement | null>(null);
  const [ratio, setRatio] = useState(1);
  const [lap, setLap] = useState(true);
  const restTimer = useRef<number | undefined>(undefined);

  // One size read when the ring starts: the rotating layer is stretched to the element's aspect
  // ratio so the dot travels around wide buttons and selects at an even pace.
  useLayoutEffect(() => {
    const ring = ringRef.current;

    if (!ring || !ring.offsetHeight) return;
    setRatio(Math.max(1, ring.offsetWidth / ring.offsetHeight));
  }, []);
  useEffect(() => () => window.clearTimeout(restTimer.current), []);

  return (
    <span ref={ringRef} aria-hidden className="flvx-onboarding-ring">
      <span className="flvx-onboarding-ring__band">
        <span
          className="flvx-onboarding-ring__scale"
          style={{ transform: `translate(-50%, -50%) scaleX(${ratio})` }}
        >
          <span
            className="flvx-onboarding-ring__spin"
            data-lap={lap ? "" : undefined}
            onAnimationEnd={() => {
              setLap(false);
              restTimer.current = window.setTimeout(
                () => setLap(true),
                ONBOARDING_LAP_REST_MS,
              );
            }}
          />
        </span>
      </span>
    </span>
  );
}

// For elements that can't take a ref or children (the bridge's native select): an always-present
// box over the element that is both the observed anchor and the ring host.
export function OnboardingAnchor({
  hint,
}: {
  hint: Pick<ReturnType<typeof useOnboardingHint>, "ref" | "active">;
}) {
  return (
    <span
      ref={hint.ref}
      aria-hidden
      className="pointer-events-none absolute inset-0 rounded-[inherit]"
    >
      <OnboardingRing active={hint.active} />
    </span>
  );
}
