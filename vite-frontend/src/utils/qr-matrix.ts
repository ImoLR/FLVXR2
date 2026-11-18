import QRCode from "qrcode";

// QR matrix from the `qrcode` library (spec-complete encoder: auto version, auto
// mask, Reed-Solomon). The whole text goes in as ONE byte segment (UTF-8) so every
// scanner reads it the same way. EC level L keeps long share links small.
// Returns null when the text does not fit in a QR code (version 40).
export const createQRMatrix = (text: string): boolean[][] | null => {
  try {
    const data = new TextEncoder().encode(text);
    const { modules } = QRCode.create([{ data, mode: "byte" }], {
      errorCorrectionLevel: "L",
    });
    const size = modules.size;

    return Array.from({ length: size }, (_, r) =>
      Array.from({ length: size }, (_, c) => !!modules.get(r, c)),
    );
  } catch {
    return null;
  }
};
