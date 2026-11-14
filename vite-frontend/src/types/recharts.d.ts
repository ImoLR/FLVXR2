declare module "recharts" {
  import * as React from "react";

  export const LineChart: React.ComponentType<Record<string, unknown>>;
  export const Line: React.ComponentType<Record<string, unknown>>;
  export const XAxis: React.ComponentType<Record<string, unknown>>;
  export const YAxis: React.ComponentType<Record<string, unknown>>;
  export const CartesianGrid: React.ComponentType<Record<string, unknown>>;
  export const Tooltip: React.ComponentType<Record<string, unknown>>;
  // Plan 255 (线路监测): 断流 / 无数据 bands and 丢包 markers.
  export const ReferenceArea: React.ComponentType<Record<string, unknown>>;
  export const ReferenceDot: React.ComponentType<Record<string, unknown>>;
  export const ResponsiveContainer: React.ComponentType<
    Record<string, unknown>
  >;
}
