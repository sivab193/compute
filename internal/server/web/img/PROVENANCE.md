# Image provenance

Every raster the dashboard ships is listed here with how it was made, so it can
be reproduced rather than taken on trust.

## concrete-ground.webp and concrete-plate.webp

- **What they are:** two seamlessly tileable board-marked concrete surfaces.
  `concrete-ground` is the page field, the rougher cast surface. `concrete-plate`
  is a lighter, smoother pour used on the masthead, the capacity band and every
  panel, so a panel reads as mounted on the field rather than cut out of it.
- **How they were made:** generated procedurally. Not photographed, and not
  produced by an image model. Two SVG `feTurbulence` fractal-noise layers (fine
  aggregate grain at `baseFrequency 0.85`, broader cure and stain blotching at
  `0.012`) over a warm concrete base, plus 1px dark/light line pairs every 80px
  standing in for the plank impressions of board-marked shuttering. Rasterised
  at 320×320 through headless Chromium, then encoded to WebP at quality 72.
- **Why procedural:** no image-generation tool existed in the build environment.
  A procedural texture is still an authored asset. Imitating the material with
  CSS bevels, inset rings or embossed text shadows is not, and is explicitly
  refused by the design craft floor, so it was not done anywhere in this UI.
- **Tiling:** 320px tile with an 80px plank pitch, so both repeat seamlessly on
  each axis.
- **Reproduce them:**

  ```sh
  npx playwright install chromium          # once
  node scripts/make-concrete-tiles.mjs     # writes the PNGs
  cd internal/server/web/img && for f in concrete-ground concrete-plate; do \
      cwebp -q 72 -m 6 "$f.png" -o "$f.webp" && rm "$f.png"; done
  ```

  The generator, including the full source SVG and every parameter above, is
  committed at [`scripts/make-concrete-tiles.mjs`](../../../../scripts/make-concrete-tiles.mjs).
  If `playwright` is not resolvable from the repo, run the script from a
  directory where it is installed and pass `IMG_OUT=<abs path to this folder>`.
- **Size:** about 4 KB and 0.7 KB respectively.
- **Licence:** original work for this project; no third-party assets.

## Fonts (`../fonts/`)

- `archivo-var-latin.woff2`, `archivo-black-latin.woff2` — Archivo and Archivo
  Black by Omnibus-Type, SIL Open Font License 1.1. Latin subsets taken from
  Google Fonts and self-hosted so the dashboard renders with no external
  request, which matters because the control plane is reachable only over the
  tailnet and may have no route to the internet at all.
