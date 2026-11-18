import { useMemo } from "react";

import { createQRMatrix } from "@/utils/qr-matrix";

const QUIET_ZONE = 4;
const PX_PER_MODULE = 4; // phone cameras need a few CSS px per module
const PADDING_PX = 16; // p-2 on both sides

// Offline QR code: inline SVG, no canvas, no network. White square, black modules,
// 4-module quiet zone. The box grows with the code (4 px per module) but never
// gets wider than its container; `minSize` is the smallest edge in CSS px.
export function OfflineQRCode({
  value,
  minSize = 208,
}: {
  value: string;
  minSize?: number;
}) {
  const matrix = useMemo(() => createQRMatrix(value), [value]);

  if (!matrix) {
    return (
      <div className="rounded-md border border-warning-300 bg-warning-50 p-3 text-xs text-warning-700">
        QR payload is too long for the offline renderer. Copy the QR payload
        instead.
      </div>
    );
  }
  const size = matrix.length + QUIET_ZONE * 2;
  const edge = Math.max(minSize, size * PX_PER_MODULE + PADDING_PX);
  let path = "";

  matrix.forEach((row, r) => {
    let c = 0;

    while (c < row.length) {
      if (!row[c]) {
        c++;
        continue;
      }
      const start = c;

      while (c < row.length && row[c]) c++;
      path += `M${start + QUIET_ZONE} ${r + QUIET_ZONE}h${c - start}v1h${start - c}z`;
    }
  });

  return (
    <svg
      aria-label="QR code"
      className="aspect-square h-auto max-w-full shrink-0 rounded bg-white p-2"
      shapeRendering="crispEdges"
      style={{ width: edge }}
      viewBox={`0 0 ${size} ${size}`}
    >
      <rect fill="white" height={size} width={size} x="0" y="0" />
      <path d={path} fill="black" />
    </svg>
  );
}
