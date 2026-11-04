// The router's lazy route pages (plan 209), shared with the idle prefetch
// (login page → 主页, 主页 → 规则 / 隧道 / 落地) so a prefetched page renders at
// once on navigation.

import { lazyPage } from "./lazy-page";
import { pageLoaders, type PageKey } from "./page-loaders";

export const pages = Object.fromEntries(
  Object.entries(pageLoaders).map(([key, load]) => [key, lazyPage(load)]),
) as Record<PageKey, ReturnType<typeof lazyPage>>;
