#!/usr/bin/env node
// Plan 292: regenerate the im.gamer static icons from src/config/brand-palette.ts.
//
//   cd vite-frontend && node scripts/brand-icons.mjs
//
// Needs Node ≥ 22.18 (runs the .ts palette file directly) and Playwright with
// Chromium (rasterises the SVG). If `playwright` is not installed here, point
// PLAYWRIGHT_MODULE at one, e.g.
//   PLAYWRIGHT_MODULE=/root/projects/mmwx-custom/frontend/node_modules/playwright
//
// Writes public/brand/<palette>/{favicon.ico,icon-192.png,icon-512.png,
// icon-maskable-512.png,apple-touch-icon.png} (versioned by palette key, so a
// colour switch gets new URLs past year-long icon caches) and refreshes the
// conventional root copies (public/favicon.ico, apple-touch-icon.png, pwa-*.png).
import { mkdirSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  BRAND_PALETTES,
  BRAND_PALETTE_KEY,
} from "../src/config/brand-palette.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const publicDir = path.resolve(here, "../public");
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

const p = BRAND_PALETTES[BRAND_PALETTE_KEY];

// Same drawing as src/components/brand-mark.tsx (64×64 units).
const FACE = `
  <ellipse cx="24" cy="12" rx="14" ry="5" fill="#fff" opacity=".3"/>
  <g fill="#fff"><rect x="9" y="21" width="46" height="24" rx="12"/><circle cx="18" cy="42" r="9"/><circle cx="46" cy="42" r="9"/></g>
  <ellipse cx="25" cy="31" rx="3" ry="3.6" fill="#1E293B"/><ellipse cx="39" cy="31" rx="3" ry="3.6" fill="#1E293B"/>
  <circle cx="26.1" cy="29.7" r="1" fill="#fff"/><circle cx="40.1" cy="29.7" r="1" fill="#fff"/>
  <path d="M28 37.2 Q32 40.6 36 37.2" stroke="#1E293B" stroke-width="2.4" fill="none" stroke-linecap="round"/>
  <circle cx="18.5" cy="36.5" r="2.7" fill="#FF8FA3" opacity=".85"/><circle cx="45.5" cy="36.5" r="2.7" fill="#FF8FA3" opacity=".85"/>`;

const fill = (x, y, w, h, rx) =>
  p.split
    ? `<clipPath id="c"><rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${rx}"/></clipPath>
       <g clip-path="url(#c)"><rect x="0" y="0" width="32" height="64" fill="${p.from}"/><rect x="32" y="0" width="32" height="64" fill="${p.to}"/></g>`
    : `<linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="${p.from}"/><stop offset="1" stop-color="${p.to}"/></linearGradient>
       <rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${rx}" fill="url(#g)"/>`;

const svg = (size, body) =>
  `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 64 64">${body}</svg>`;

// Rounded logo on a transparent square (favicon, PWA "any").
const logoAny = (size) => svg(size, fill(2, 2, 60, 60, 18) + FACE);

// Full-bleed square; the face scaled by `scale` around the centre
// (apple-touch: iOS rounds the corners itself; maskable: safe zone).
const logoFull = (size, scale) =>
  svg(
    size,
    fill(0, 0, 64, 64, 0) +
      `<g transform="translate(32 32) scale(${scale}) translate(-32 -32)">${FACE}</g>`,
  );

async function render(page, markup, size) {
  await page.setViewportSize({ width: size, height: size });
  await page.setContent(
    `<!doctype html><html><body style="margin:0;background:transparent">${markup}</body></html>`,
  );

  return page.screenshot({
    omitBackground: true,
    clip: { x: 0, y: 0, width: size, height: size },
    type: "png",
  });
}

// ICO with PNG-compressed entries (all current browsers + Windows ≥ Vista).
function ico(images) {
  const header = Buffer.alloc(6 + 16 * images.length);

  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(images.length, 4);
  let offset = header.length;

  images.forEach(({ size, png }, i) => {
    const e = 6 + 16 * i;

    header.writeUInt8(size >= 256 ? 0 : size, e);
    header.writeUInt8(size >= 256 ? 0 : size, e + 1);
    header.writeUInt8(0, e + 2);
    header.writeUInt8(0, e + 3);
    header.writeUInt16LE(1, e + 4);
    header.writeUInt16LE(32, e + 6);
    header.writeUInt32LE(png.length, e + 8);
    header.writeUInt32LE(offset, e + 12);
    offset += png.length;
  });

  return Buffer.concat([header, ...images.map((i) => i.png)]);
}

const browser = await chromium.launch();
const page = await browser.newPage({ deviceScaleFactor: 1 });
const out = path.join(publicDir, "brand", BRAND_PALETTE_KEY);

mkdirSync(out, { recursive: true });

const faviconImages = [];

for (const size of [16, 32, 48]) {
  faviconImages.push({ size, png: await render(page, logoAny(size), size) });
}
const favicon = ico(faviconImages);
const icon192 = await render(page, logoAny(192), 192);
const icon512 = await render(page, logoAny(512), 512);
const maskable = await render(page, logoFull(512, 0.78), 512);
const apple = await render(page, logoFull(180, 64 / 60), 180);

await browser.close();

const files = {
  [path.join(out, "favicon.ico")]: favicon,
  [path.join(out, "icon-192.png")]: icon192,
  [path.join(out, "icon-512.png")]: icon512,
  [path.join(out, "icon-maskable-512.png")]: maskable,
  [path.join(out, "apple-touch-icon.png")]: apple,
  // Conventional paths some clients request without reading the page.
  [path.join(publicDir, "favicon.ico")]: favicon,
  [path.join(publicDir, "apple-touch-icon.png")]: apple,
  [path.join(publicDir, "pwa-192x192.png")]: icon192,
  [path.join(publicDir, "pwa-512x512.png")]: icon512,
  [path.join(publicDir, "pwa-maskable-512x512.png")]: maskable,
};

for (const [file, data] of Object.entries(files)) {
  writeFileSync(file, data);
  console.log(`${path.relative(publicDir, file)}  ${data.length} B`);
}
