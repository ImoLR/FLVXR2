// Plan 292: im.gamer brand colours — the ONE place they live.
//
// To switch the brand colour (e.g. to Joy-Con red or the red/blue Switch
// pair) change BRAND_PALETTE_KEY below, then regenerate the static icons
// (favicon / home-screen / PWA PNGs) and rebuild:
//
//   cd vite-frontend && node scripts/brand-icons.mjs
//
// The script writes the icons to public/brand/<key>/ (new key = new URLs,
// so browsers and Cloudflare, which cache icons for a year, pick them up).
// Everything else (logo SVG, wordmark dot, login page, powered badge) reads
// the palette at run time.
//
// Plain data only: this file is also imported by scripts/brand-icons.mjs and
// vite.config.ts.

export type BrandPalette = {
  /** Display name of the palette (design gallery). */
  name: string;
  /** Logo gradient start (top left). */
  from: string;
  /** Logo gradient end (bottom right). */
  to: string;
  /** Accent: wordmark dot, focus border, chips. */
  accent: string;
  /** Soft tint: chip background, login page glow. */
  soft: string;
  /** Focus ring / glow (with alpha). */
  ring: string;
  /** Two flat halves instead of a gradient (red/blue Switch pair). */
  split?: boolean;
};

export const BRAND_PALETTES = {
  red: {
    name: "Joy-Con 电光红",
    from: "#FF6B6B",
    to: "#F0435A",
    accent: "#F0435A",
    soft: "#FFE8EB",
    ring: "rgba(240,67,90,.28)",
  },
  duo: {
    name: "红蓝双色（Switch 原配）",
    from: "#FF5A6A",
    to: "#17C3E6",
    accent: "#F0435A",
    soft: "#FFE8EB",
    ring: "rgba(240,67,90,.25)",
    split: true,
  },
  purple: {
    name: "Joy-Con 电光紫",
    from: "#A78BFA",
    to: "#7C3AED",
    accent: "#7C3AED",
    soft: "#F1EAFF",
    ring: "rgba(124,58,237,.25)",
  },
  mint: {
    name: "动森 薄荷绿",
    from: "#5EE0B0",
    to: "#14B8A6",
    accent: "#0D9488",
    soft: "#DDF7EF",
    ring: "rgba(20,184,166,.28)",
  },
  orange: {
    name: "Joy-Con 电光橙",
    from: "#FFB547",
    to: "#FF7A2F",
    accent: "#F26B1D",
    soft: "#FFF0E0",
    ring: "rgba(242,107,29,.28)",
  },
  pink: {
    name: "喷射战士 樱花粉",
    from: "#FF9AC6",
    to: "#F0509A",
    accent: "#E63E8C",
    soft: "#FFE6F2",
    ring: "rgba(230,62,140,.26)",
  },
} satisfies Record<string, BrandPalette>;

export type BrandPaletteKey = keyof typeof BRAND_PALETTES;

/** Active brand colour. User 11-22: 「先用绿色试一试」. */
export const BRAND_PALETTE_KEY: BrandPaletteKey = "mint";

export const BRAND_NAME = "im.gamer";
