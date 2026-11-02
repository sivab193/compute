#!/usr/bin/env node
/* Generates the dashboard's board-marked concrete tiles as real raster assets.
 *
 * No image-generation model is used: the texture is built from SVG fractal
 * noise and rasterised through headless Chromium. Committed so every shipping
 * raster can be reproduced from source (see internal/server/web/img/PROVENANCE.md).
 *
 * Usage:  node scripts/make-concrete-tiles.mjs
 * Needs:  npx playwright install chromium, and cwebp for the WebP encode step
 *         (the script writes PNGs; the Makefile target encodes them).
 *
 * Two tiles, because the surfaces are two different planes:
 *   ground — the page field, the rougher cast surface the panels sit on
 *   plate  — the panels themselves, a smoother and LIGHTER pour, so a panel
 *            reads as mounted on the field rather than cut out of it
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'fs';
import { dirname, resolve } from 'path';
import { fileURLToPath } from 'url';

const HERE = dirname(fileURLToPath(import.meta.url));
// IMG_OUT lets the script run from a directory where playwright is installed
// while still writing into this repo.
const OUT = process.env.IMG_OUT || resolve(HERE, '../internal/server/web/img');
mkdirSync(OUT, { recursive: true });

const S = 320;        // tile edge; 80px plank pitch divides it evenly
const PLANK = 80;

/** @param {{base:string, grain:number, blotch:number, plank:number, seed:number}} o */
function concreteSVG(o) {
  return `
<svg xmlns="http://www.w3.org/2000/svg" width="${S}" height="${S}" viewBox="0 0 ${S} ${S}">
  <defs>
    <filter id="grain" x="0" y="0" width="100%" height="100%">
      <feTurbulence type="fractalNoise" baseFrequency="0.85" numOctaves="4" seed="${o.seed}" result="n"/>
      <feColorMatrix in="n" type="saturate" values="0"/>
      <feComponentTransfer><feFuncA type="table" tableValues="0 0.55"/></feComponentTransfer>
    </filter>
    <filter id="blotch" x="0" y="0" width="100%" height="100%">
      <feTurbulence type="fractalNoise" baseFrequency="0.012" numOctaves="3" seed="${o.seed + 25}" result="n"/>
      <feColorMatrix in="n" type="saturate" values="0"/>
      <feComponentTransfer><feFuncA type="table" tableValues="0 0.4"/></feComponentTransfer>
    </filter>
  </defs>
  <rect width="${S}" height="${S}" fill="${o.base}"/>
  <rect width="${S}" height="${S}" filter="url(#blotch)" opacity="${o.blotch}"/>
  <rect width="${S}" height="${S}" filter="url(#grain)" opacity="${o.grain}"/>
  <g opacity="${o.plank}">
    ${Array.from({ length: S / PLANK }, (_, i) => i * PLANK).map((y) => `
      <line x1="0" y1="${y + 0.5}" x2="${S}" y2="${y + 0.5}" stroke="#191610" stroke-width="1"/>
      <line x1="0" y1="${y + 1.5}" x2="${S}" y2="${y + 1.5}" stroke="#6b6253" stroke-width="1"/>
    `).join('')}
  </g>
</svg>`;
}

const TILES = [
  // The page field: rougher, darker, plank impressions clearly readable.
  { name: 'concrete-ground', base: '#272420', grain: 0.5, blotch: 0.5, plank: 0.5, seed: 17 },
  // The panels: a lighter, smoother pour so they sit above the field.
  { name: 'concrete-plate', base: '#3b362e', grain: 0.3, blotch: 0.32, plank: 0.22, seed: 61 },
];

const browser = await chromium.launch();
for (const t of TILES) {
  const page = await browser.newPage({ viewport: { width: S, height: S }, deviceScaleFactor: 1 });
  await page.setContent(
    `<body style="margin:0;width:${S}px;height:${S}px;overflow:hidden">${concreteSVG(t)}</body>`);
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${OUT}/${t.name}.png` });
  await page.close();
  console.log('wrote', `${OUT}/${t.name}.png`);
}
await browser.close();
console.log('\nNow encode to WebP:\n  cd internal/server/web/img && ' +
  'for f in concrete-ground concrete-plate; do cwebp -q 72 -m 6 "$f.png" -o "$f.webp" && rm "$f.png"; done');
