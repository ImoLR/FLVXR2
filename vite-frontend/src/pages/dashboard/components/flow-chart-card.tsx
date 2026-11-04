import { Suspense } from "react";

import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { PageEmptyState } from "@/components/page-state";
import { useTapTooltip } from "@/hooks/useTapTooltip";
import { FlowChartPlot } from "@/pages/dashboard/flow-chart-lazy";

export interface FlowChartPoint {
  time: string;
  flow: number;
}

interface FlowChartCardProps {
  statisticsFlowsCount: number;
  chartData: FlowChartPoint[];
  formatFlow: (value: number, unit?: string) => string;
}

export const FlowChartCard = ({
  statisticsFlowsCount,
  chartData,
  formatFlow,
}: FlowChartCardProps) => {
  const tapTooltip = useTapTooltip();

  return (
    <Card className="mb-6 lg:mb-8 border border-gray-200 dark:border-default-200 shadow-md">
      <CardHeader className="pb-3">
        <div className="flex items-center gap-2">
          <svg
            aria-hidden="true"
            className="w-5 h-5 text-primary"
            fill="currentColor"
            viewBox="0 0 20 20"
          >
            <path d="M2 10a8 8 0 018-8v8h8a8 8 0 11-16 0z" />
            <path d="M12 2.252A8.014 8.014 0 0117.748 8H12V2.252z" />
          </svg>
          <h2 className="text-lg lg:text-xl font-semibold text-foreground">
            24小时流量统计
          </h2>
        </div>
      </CardHeader>
      <CardBody className="pt-0">
        {statisticsFlowsCount === 0 ? (
          <PageEmptyState className="h-48" message="暂无流量统计数据" />
        ) : (
          <div ref={tapTooltip.containerRef} className="h-64 lg:h-80 w-full">
            <Suspense fallback={null}>
              <FlowChartPlot
                chartData={chartData}
                formatFlow={formatFlow}
                tapTooltip={tapTooltip}
              />
            </Suspense>
          </div>
        )}
      </CardBody>
    </Card>
  );
};
