import { memo } from "react";

/** Tiny SVG line (no chart library): header speed and latency rings. */
export const Sparkline = memo(function Sparkline({
  values,
  color,
  width = 120,
  height = 28,
  max,
  className = "",
  label,
}: {
  values: (number | null)[];
  color: string;
  width?: number;
  height?: number;
  max?: number;
  className?: string;
  label?: string;
}) {
  const nums = values.filter(
    (v): v is number => v != null && Number.isFinite(v),
  );
  const top = Math.max(max ?? 0, ...nums, 1);
  const step = values.length > 1 ? width / (values.length - 1) : width;
  let d = "";
  let pen = false;

  values.forEach((v, i) => {
    if (v == null || !Number.isFinite(v)) {
      pen = false;

      return;
    }
    const x = (i * step).toFixed(1);
    const y = (height - 2 - (v / top) * (height - 4)).toFixed(1);

    d += `${pen ? "L" : "M"}${x} ${y}`;
    pen = true;
  });

  return (
    <svg
      aria-label={label}
      className={className}
      height={height}
      preserveAspectRatio="none"
      role="img"
      viewBox={`0 0 ${width} ${height}`}
      width="100%"
    >
      <path
        d={d}
        fill="none"
        stroke={color}
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
});

export function seriesColors(dark: boolean) {
  // Validated pair (dataviz validator, light #fff / dark #18181b surfaces).
  return dark ? { a: "#0d9488", b: "#a855f7" } : { a: "#0d9488", b: "#9333ea" };
}
