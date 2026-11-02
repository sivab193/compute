---
version: 1
slug: "internal-server-web-index-html"
primary_target: "internal/server/web/index.html"
related_targets: ["internal/server/web/style.css","internal/server/web/app.js"]
---

Scope: the single-page control-plane dashboard served by the Go control plane
(`internal/server/web/`). Visitor mode: Operate. One operator, his own machines,
reached from a desk browser and from a phone on the tailnet. Task: read live pool
capacity, submit a job that will actually run, watch it land. Constraints: served
offline over a tailnet, so every asset is self-hosted; swap is never presented as
RAM; offline and preemptible capacity are never counted as available.

## Direction contract

THESIS: The pool's capacity and the machines holding it read as one cast
directory, not a scatter of metric cards. This surface refuses both category
defaults: the near-black dashboard with a single neon accent, and the
hero-metric row of same-size stat cards. Capacity is one measured band on a
shared baseline; every machine is a numbered plate in one directory.

OWN-WORLD: Brutalist housing-estate wayfinding. Board-marked warm concrete
grounds, incised off-white lettering, one wayfinding ochre carrying identity and
every primary action, and enamel state colours in lichen green, weathered blue
and oxide red. State never rides on hue alone: the slow lane wears ochre/black
hazard striping and preemptible machines wear diagonal hatching, so lane and
risk survive both colour-blindness and a greyscale phone. Display figures in
self-hosted Archivo Black at tight tracking, body in Archivo, measured data in
tabular mono. Hairline rules divide everything; no floating cards, no nested
panels, no rounded corners past 2px, no border-left accents.

RAISES: ranked live rows where the row is the entity and a state change
restyles the whole row without breaking the grid (from the split-flap concourse,
competitive); packed hairline modules with small header tabs and no whitespace
inflation (from the Japanese high-density site, competitive); a column armature
real enough to see in the alignment (from the Crouwel grid specimen, declined);
a state vocabulary broad enough that no two states share a treatment (from the
midnight transit diagram, declined); every node and job carrying a short code in
strict sequence, tied to its physical place (from the Factory Records catalogue
and the convention catalogue, declined).

STORY: He arrives knowing only "is anything free, and will my job run." He reads
total live capacity in one glance, sees which machines are online and what each
can take, submits a file against only the options the pool can satisfy at this
moment, and watches the run land and stream its log. He leaves certain the job is
on a machine that fits, or told plainly why nothing fits.

FIRST VIEWPORT: A slim concrete header plate carries the name and live connection
state. Below it the capacity band: figures on one shared baseline rule, hairline
divided, at display scale, with swap held in its own cell beneath a hazard-striped
rule so it can never be read as RAM. Under the band two work areas: submit and
the node directory, side by side at desktop, submit first on a phone. The file
control is the primary ochre action, and every requirement control is annotated
with what the pool can currently satisfy, with impossible options disabled rather
than hidden. The directory is one plate of hairline-separated rows, each stamped
with a code, a location and its capacity. The jobs table sits below, its rows
restyling wholesale by state. Signature interaction: that row re-stamp, a single
flap-like transition in place when a job changes state or a node drops offline,
the grid never breaking.

FORM: Brutalist estate wayfinding; candidate 7 of seven grounded directions, the
assigned index. seed:8a055498dee029f2

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
