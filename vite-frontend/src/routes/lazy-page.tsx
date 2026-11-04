import {
  createElement,
  lazy,
  useState,
  type ComponentType,
  type ElementType,
} from "react";

import { recoverChunkLoad } from "@/utils/build-freshness";

export type Preloadable<P extends object> = ComponentType<P> & {
  // Starts (once) loading the chunk; resolves when it is ready.
  preload: () => Promise<unknown>;
};

// A component in its own chunk (plan 209). `preload()` lets idle prefetch and
// the boot code fetch the chunk early; once the chunk is loaded the component
// renders synchronously (no Suspense fallback flash on navigation). Render it
// under a <Suspense> boundary for the not-yet-loaded case.
export function lazyWithPreload<P extends object>(
  load: () => Promise<{ default: ComponentType<P> }>,
): Preloadable<P> {
  let loaded: ComponentType<P> | null = null;
  let pending: Promise<{ default: ComponentType<P> }> | null = null;

  const preload = (): Promise<{ default: ComponentType<P> }> => {
    if (!pending) {
      pending = load().then(
        (module) => {
          loaded = module.default;

          return module;
        },
        (error: unknown) => {
          pending = null;
          throw error;
        },
      );
    }

    return pending;
  };

  const Lazy = lazy(() =>
    preload().catch((error: unknown) => recoverChunkLoad(error, preload)),
  );

  function Preloaded(props: P) {
    // Chosen once per mount so the element type never changes under it.
    const [Component] = useState<ElementType>(() => loaded ?? Lazy);

    return createElement(Component, props);
  }

  return Object.assign(Preloaded, { preload });
}

// A route page (no props) in its own chunk.
export const lazyPage = (
  load: () => Promise<{ default: ComponentType }>,
): Preloadable<object> => lazyWithPreload<object>(load);
