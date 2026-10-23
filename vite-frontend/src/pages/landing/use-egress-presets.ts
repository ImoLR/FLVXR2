import { useCallback, useEffect, useState } from "react";

import { EgressPreset, listEgressPresets } from "@/api/landing-egress";

// Plan 140: one shared, lazily loaded copy of the preset library for every
// 「添加预设」 menu on the page; admin edits call invalidateEgressPresets().

type State = { presets: EgressPreset[]; admin: boolean; loaded: boolean };

let state: State = { presets: [], admin: false, loaded: false };
let inflight: Promise<void> | null = null;
const listeners = new Set<(s: State) => void>();

const publish = (next: State) => {
  state = next;
  listeners.forEach((fn) => fn(state));
};

const fetchPresets = () => {
  if (!inflight) {
    inflight = listEgressPresets()
      .then((res) => {
        if (res.code === 0) {
          publish({
            presets: res.data.presets || [],
            admin: Boolean(res.data.admin),
            loaded: true,
          });
        }
      })
      .finally(() => {
        inflight = null;
      });
  }

  return inflight;
};

export const invalidateEgressPresets = () => fetchPresets();

export const setEgressPresets = (presets: EgressPreset[]) =>
  publish({ ...state, presets, loaded: true });

export function useEgressPresets() {
  const [current, setCurrent] = useState(state);

  useEffect(() => {
    // The first menu mounted on a page refetches (the library or the logged-in
    // account may have changed since); later ones share that copy.
    const first = listeners.size === 0;

    if (first) state = { presets: [], admin: false, loaded: false };
    listeners.add(setCurrent);
    setCurrent(state);
    if (!state.loaded) void fetchPresets();

    return () => {
      listeners.delete(setCurrent);
    };
  }, []);

  const reload = useCallback(() => fetchPresets(), []);

  return { ...current, reload };
}
