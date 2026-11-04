import type { FlowChartPoint } from "./flow-chart-card";
import type { useTapTooltip } from "@/hooks/useTapTooltip";

import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

// The 24 h flow chart body (plan 209: own chunk, see flow-chart-lazy.ts).
// Moved unchanged from flow-chart-card.tsx.
export default function FlowChartPlot({
  chartData,
  formatFlow,
  tapTooltip,
}: {
  chartData: FlowChartPoint[];
  formatFlow: (value: number, unit?: string) => string;
  tapTooltip: ReturnType<typeof useTapTooltip>;
}) {
  return (
    <ResponsiveContainer height="100%" width="100%">
      <LineChart data={chartData} onClick={tapTooltip.onChartClick}>
        <CartesianGrid className="opacity-30" strokeDasharray="3 3" />
        <XAxis
          axisLine={{ stroke: "#e5e7eb", strokeWidth: 1 }}
          dataKey="time"
          // Every n-th hour at equal steps; the default preserveEnd
          // dropped a single label before the last one (QA R11).
          // (equidistantPreserveEnd kept only one clipped label.)
          interval="equidistantPreserveStart"
          tick={{ fontSize: 12 }}
          tickLine={false}
        />
        <YAxis
          axisLine={{ stroke: "#e5e7eb", strokeWidth: 1 }}
          tick={{ fontSize: 12 }}
          tickFormatter={(value: number | string) => {
            const v = typeof value === "number" ? value : Number(value);

            if (!Number.isFinite(v)) return String(value);
            if (v === 0) return "0";
            if (v < 1024) return `${v}B`;
            if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)}K`;
            if (v < 1024 * 1024 * 1024)
              return `${(v / (1024 * 1024)).toFixed(1)}M`;

            return `${(v / (1024 * 1024 * 1024)).toFixed(1)}G`;
          }}
          tickLine={false}
        />
        <Tooltip
          {...tapTooltip.tooltipProps}
          content={({
            active,
            payload,
            label,
          }: {
            active?: boolean;
            payload?: Array<{ value?: number | string }>;
            label?: string | number;
          }) => {
            if (active && payload && payload.length) {
              const firstValue = payload[0]?.value;
              const numericValue =
                typeof firstValue === "number"
                  ? firstValue
                  : Number(firstValue);
              const flowValue = Number.isFinite(numericValue)
                ? numericValue
                : 0;

              return (
                <div className="bg-white dark:bg-default-100 border border-default-200 rounded-lg shadow-lg p-3">
                  <p className="font-medium text-foreground">{`时间: ${label ?? ""}`}</p>
                  <p className="text-primary">{`流量: ${formatFlow(flowValue)}`}</p>
                </div>
              );
            }

            return null;
          }}
        />
        <Line
          activeDot={tapTooltip.activeDot({
            r: 4,
            stroke: "#8b5cf6",
            strokeWidth: 2,
            fill: "#fff",
          })}
          dataKey="flow"
          dot={false}
          stroke="#8b5cf6"
          strokeWidth={3}
          type="monotone"
        />
      </LineChart>
    </ResponsiveContainer>
  );
}
