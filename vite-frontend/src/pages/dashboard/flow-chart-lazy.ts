// Plan 209: recharts (the biggest library) loads in its own chunk. The login
// page prefetches it while idle; 主页 starts it on mount, in parallel with its
// data, and the chart fills its fixed-height box when ready.

import { lazyWithPreload } from "@/routes/lazy-page";

export const FlowChartPlot = lazyWithPreload(
  () => import("./components/flow-chart-plot"),
);
