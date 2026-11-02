# Design system

The visual system of the control-plane dashboard, recorded from the built
result. Surface: `internal/server/web/`. Mode: **Operate** — the visitor comes
to read capacity and get a job running, so ambition shows as precision and
density, not decoration.

**World: brutalist housing-estate wayfinding.** Board-marked concrete grounds,
one wayfinding ochre carrying identity and every primary action, weathered
enamel state colours, and hazard marking where a caution is real. The world
contributes four things and only four: type, palette, density, and one
signature move. It never supplies the layout, the navigation model, or the
controls — those are standard web components the operator already knows.

---

## Ground material

Two concrete planes, both shipped as authored raster tiles (not CSS imitations
of material; see [`internal/server/web/img/PROVENANCE.md`](internal/server/web/img/PROVENANCE.md)
and the committed generator at [`scripts/make-concrete-tiles.mjs`](scripts/make-concrete-tiles.mjs)).

| Tile | Used on | Character |
|---|---|---|
| `concrete-ground.webp` | `body` | The page field: rougher cast surface, plank impressions readable |
| `concrete-plate.webp` | `.masthead`, `.band`, `.plate` | A lighter, smoother pour, so panels read as mounted on the field |

Panels are always **lighter** than the field they sit on, and carry a shadow
with a real offset and a soft blur (`0 2px 12px rgba(0,0,0,.3)`). A panel darker
than its ground reads as a hole punched in the concrete, which is wrong.

Regenerate with:

```sh
npx playwright install chromium
node scripts/make-concrete-tiles.mjs
cd internal/server/web/img && for f in concrete-ground concrete-plate; do \
    cwebp -q 72 -m 6 "$f.png" -o "$f.webp" && rm "$f.png"; done
```

## Colour

Strategy: **committed** — the concrete ground owns the surface, one saturated
wayfinding ochre carries identity and action, and a small set of enamel colours
do real state jobs. Dark was chosen from the use scene, not the category: this
is checked from a phone in a dark room at 1am and from a desk monitor in
daylight, so the ground is a warm mid-dark concrete rather than a near-black
slate.

```css
--ground: #272420;  /* page field          */
--plate:  #3b362e;  /* panels              */
--plate-2:#454036;  /* raised rows, inputs */
--plate-3:#4f493e;  /* selected row        */
--rule:   #58513f;  /* hairline            */
--rule-strong:#6f6654;

--ink:   #f7f3ea;   /* primary   */
--ink-2: #ccc4b2;   /* secondary */
--ink-3: #b6ad9c;   /* labels    */

--signal:      #e9a13b;  /* wayfinding ochre: identity + every primary action */
--signal-deep: #c9821f;  /* its hover        */
--on-signal:   #231f18;  /* ink on ochre     */

--enamel-green: #a8c46a;  /* online, done    */
--enamel-blue:  #8ab8d0;  /* running         */
--enamel-red:   #e89a84;  /* failed, offline */
```

All body and label text clears 4.5:1 on the surface it sits on; secondary ink is
tinted from the concrete hue rather than neutral grey. Every value here was
checked with the design detector, which reports zero contrast findings.

### State never rides on hue alone

Two states carry a **material** treatment as well as a colour, so they survive
colour-blindness and a greyscale screen:

- **Slow lane / swap** — 45° ochre-and-dark hazard striping. The swap figure's
  cell carries a hazard wash and an inline hazard chip beside its label; the
  slow-lane tag carries a hazard underline.
- **Preemptible machines** — a −45° translucent diagonal hatch across the row.

## Type

| Role | Face | Size | Tracking |
|---|---|---|---|
| Display figures | Archivo Black | `clamp(1.75rem, 9vw, 2.75rem)` | `-.035em` |
| Band heading (h1) | Archivo Black | 1.375rem, uppercase | `0` |
| Panel heading (h2) | Archivo Black | 1rem, uppercase | `.02em` |
| Sub-heading (h3) | Archivo Black | .75rem, uppercase | `.05em` |
| Body | Archivo 400–700 | 15px | default |
| Data, logs, specs | system mono | .7–.75rem, `tabular-nums` | default |

Both faces are **self-hosted** (`web/fonts/`, Archivo + Archivo Black,
OFL-1.1, latin subsets). Nothing is fetched from a font CDN: the control plane
is reachable only over the tailnet and may have no route to the internet.

Rules:

- Display and heading tracking is **zero or negative**. Positive-tracked
  uppercase micro-caps is the generic dashboard voice and is not this world's.
- Monospace is for data, measurement and code only, never as a costume for
  "technical". Labels and prose are in the body face.
- Functional text never goes below **11px**.
- Tabular numerals everywhere a figure sits in a column.

## Composition

- **One capacity band, not a row of stat cards.** Figures share a baseline, are
  divided by hairlines, and vary in weight (three primary, four secondary)
  rather than repeating as equal tiles. No card has its own background or radius.
- **The node directory is one plate of rows**, each stamped with a wayfinding
  code (`M1`), its location, its capacity, and its load. Not floating cards.
- **Hairlines divide everything.** No radius above 2px anywhere. No coloured
  `border-left` accents.
- Two work areas side by side at ≥900px; single column below, with submit first
  because that is the phone job.

## Honesty rules the interface enforces

These are product rules the visual system is built to serve, not decoration:

1. **Swap is never folded into RAM.** It has its own cell, its own hazard
   marking, and a sentence in the band foot saying it is disk standing in for
   RAM and is never counted above.
2. **"Available" means available.** The band's headline figures report what is
   *free* (idle vCPUs, free RAM, free disk) with the installed total in the
   label beneath. Reporting installed capacity under a heading that says
   available overstates the pool.
3. **Apple unified memory is not VRAM.** A Metal machine reports no separate
   VRAM, so the cell says `unified · shares the RAM above` rather than `0 MB`,
   which would read as "no GPU memory", or repeating the RAM figure, which
   would double-count it.
4. **Offline capacity is shown separately** and never counted in the totals.
5. **Unavailable options are disabled with their reason**, not hidden. The
   submit form is built from `/api/capabilities`, and the combination is checked
   against the real scheduler via `/api/feasibility` before submitting.
6. **Reference data is labelled as such.** The Colab runtime table carries an
   explicit caveat and sits beside a measured column filled from the pool's own
   connected nodes.

## Signature move

**The row re-stamp.** When a job changes state or a machine drops offline, that
row re-stamps in place: a single ochre-to-transparent flash over 0.7s on an
exponential ease-out, the grid never breaking. State also restyles the *whole*
row (ground tint plus code colour), not a chip in one cell. This is the one
authored motion moment; there are no entrance animations, and
`prefers-reduced-motion` reduces every transition to effectively zero.

## Controls and browser surfaces

Standard web controls throughout — buttons, selects, number inputs,
checkboxes, tabs, tables, a details disclosure. No costume of another
interface.

The file input is the one native control replaced: its "Choose Files / No file
chosen" is browser chrome, and this is the form's primary action, so the input
is visually hidden behind an ochre label with an authored upload glyph and a
filename readout in the product's own words.

Surfaces the browser would otherwise style itself are themed from the palette:
text selection, caret colour, scrollbar track and thumb, focus ring
(`2px solid var(--signal)` with offset), select chevron, checkbox tick,
underline offset, and tabular numerals.

Icons are authored SVG on a 24×24 grid at a consistent 2px round-join stroke.
No unicode glyphs or emoji stand in for an icon.

## Responsive

Mobile-first. Breakpoints at 560px, 700px, 900px, 1100px.

Below **700px**, the jobs table and the Colab runtime table stop being tables
and become labelled records: each job leads with its code and label in the
display face, then its state, then compact label-value pairs, then a full-width
**Read log** action. Horizontal scroll inside an invisible box is not a mobile
treatment — zero page overflow is not the same as content being reachable, and
both are verified (0px overflow and zero clipped content blocks at 390/768/1440).

Touch targets are ≥44px for every interactive element; raw checkboxes are 18px
but sit inside 44px clickable labels. Commands wrap rather than clipping
mid-token, with a Copy button as the real path.

## What this system refuses

- The near-black dashboard with a single neon accent.
- The hero-metric template: big number, small label, supporting stats, accent.
- Same-size cards of icon + heading + text as the page structure; nested cards.
- Kickers and eyebrows above headings.
- Gradient text, glass-as-decoration, hard zero-blur offset shadows.
- Faked physicality: CSS bevels, embossing, inset rings, or text-shadow
  imitating a material the page never renders. Material is shipped as a real
  asset or not claimed.
- A system display face. The display voice is self-hosted.
- Monospace as a signal for "technical".

## Known open items

The finish reviewer's standing disposition on this surface is **rebuild**, with
two items deliberately not taken:

- **Figures as incised raster material.** The reviewer asked for the band's
  primary figures to ship as raster or alpha-matte material. Declined on
  product truth: these are live values refreshed every three seconds and must
  remain selectable, accessible, tabular text. The achievable part — giving the
  plate they sit on real material — was done.
- **Visible column armature and incised lettering** remain unreached ceiling
  items from the world's fuller vocabulary.
