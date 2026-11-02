# Product

<!-- impeccable:product-schema 1 -->

> **Provenance note.** Written without a product interview: this session had no
> structured question tool and the user asked to proceed with the build. Facts
> below are drawn from the user's brief, `shared-compute-plan.md`, and the
> committed code. Lines marked _[inferred]_ were not confirmed by the user and
> should be corrected rather than built upon.

## Platform

web

## Users

A single operator — the owner of the machines — who is also the only user.
No multi-tenancy, no teams, no roles. He reaches the dashboard from wherever he
is: a laptop browser on the tailnet, and (newly required) **a phone**, which is
the common case when he is away from his desk and wants to check whether a run
finished or kick one off. _[inferred: that phone use is mostly checking state
and submitting a known job, not authoring new work.]_

## Product Purpose

One place to add any machine he owns — cloud VMs from any provider, Google Colab
instances, an always-on Mac mini, a Windows gaming laptop — see the pool's total
available compute (vCPUs, GPUs and VRAM, RAM, swap, disk), and from anywhere
submit a notebook, a Python file, or a Java file and have it run on a machine
that can actually run it.

Success is that submitting a job is boring and certain: he picks a file, the
interface only offers him choices the pool can satisfy right now, and the job
lands on a machine that fits instead of sitting in a queue for reasons he has to
go digging for.

## Positioning

Self-hosted and provider-agnostic. A machine in AWS, a Colab session, and the
mini at home are indistinguishable to the scheduler once they are on the
tailnet. Nothing is exposed to the public internet; the tailnet is the security
boundary. This is deliberately not a hosted control plane with per-hour billing
(the Brev model it borrows its workspace ideas from) and not an LLM-only mesh
(the Mesh-LLM model it borrows its pooling ideas from): it runs arbitrary jobs.

## Operating Context

- **Fabric:** Tailscale. The control plane binds loopback on the Mac mini and is
  fronted by `tailscale serve`; agents and browsers reach it over the tailnet.
- **Heterogeneous fleet, unevenly online.** The mini is always on. The gaming
  laptop is only a node when booted into Linux (or running its WSL2 agent).
  Cloud VMs are offline whenever they are stopped. Colab sessions expire on
  their own schedule. So the set of available capabilities changes hour to hour,
  and the interface must reflect the pool as it is at this moment.
- **Node classes in play:** Mac mini M4 (Metal, always-on, also the control
  plane host), ROG gaming laptop (CUDA, Windows-dual-boot or WSL2), cloud VMs
  (any provider, CUDA or CPU), Colab (CUDA or TPU, preemptible), optionally a
  home server + NAS where the NAS is storage rather than compute.
- **Job shapes:** a single `.py`, a single `.ipynb`, or one or more `.java`
  files, optionally with a `requirements.txt`. Each job runs on exactly one
  node.

## Capabilities and Constraints

- Jobs are `{ files, runtime, requirements, timeout }`; runtime is inferred from
  the entry file extension.
- The scheduler filters nodes on labels (GPU type, architecture, OS,
  preemptible opt-in) and then fit-checks memory in two tiers: fits in free RAM
  is the **fast lane**; fits only in RAM + swap is the **slow lane**, allowed but
  explicitly warned as much slower; fits in neither stays queued with a stated
  reason. Disk-backed (sparse mmap) jobs skip the RAM demand.
- **Swap is never folded into the RAM headline.** RAM and swap are reported and
  displayed as separate numbers, because presenting their sum as capacity would
  misstate what the pool can do.
- Offline nodes contribute nothing to available totals and are shown separately
  as offline capacity.
- Preemptible nodes (Colab) are excluded from scheduling unless a job opts in,
  because those sessions can vanish mid-run.
- One job per node in v1. A single job never spans machines; the pool is a
  choice of machines, not one fused supercomputer.
- A CUDA job cannot run on the Metal-only mini, and vice versa.
- The control plane is a single point of failure on the mini: if it is down,
  nothing new schedules, though jobs already running elsewhere continue.
- Auth is a shared join token for agents and an optional dashboard token, with
  Tailscale ACLs as the real boundary.

### Terminology (use these words in the interface)

node, job, the pool, control plane, agent, fast lane / slow lane, preemptible,
artifact, queued / running / done / failed, vCPU, VRAM, swap.

### Explicitly undecided

- Whether `compute.siv19.dev` resolves as a public DNS record pointed at the
  mini's Tailscale IP, or simply as the mini's MagicDNS name. Asked, not yet
  answered.
- Cloud VM start/stop remains manual in the provider's own console or CLI for
  v1; the pool only observes that a stopped VM is offline.

## Brand Commitments

- The system is addressed as `compute.siv19.dev`; "Shared Compute Pool" is the
  descriptive name. No logo, wordmark, color, or typeface has been specified,
  and no visual constraint has been made binding.

## Evidence on Hand

- Real, verified behavior from this machine: a Python job and a notebook job
  each ran end to end and produced artifacts; the notebook computed `sum=5050`.
  Measured mini capacity: 10 vCPU, 1 Metal GPU, 16 GB RAM, 3 GB swap, ~233 GB
  disk.
- Agent binaries cross-compile to linux/amd64, linux/arm64, darwin/arm64,
  windows/amd64.
- **No** usage history, cost data, benchmarks, customers, or multi-node
  production history exists. There is no GPU fleet running today beyond the
  mini. Future work must not fabricate utilization charts, dollar figures, or
  job history that has not happened.
- Colab hardware tiers are published by Google and change over time; any tier
  table shipped in this product is reference material that can go stale, and the
  authoritative numbers are whatever a connected Colab node actually reports.

## Product Principles

1. **Show the pool as it is right now.** Capability claims and input choices
   come from live node state, never from a hardcoded list of what might exist.
2. **Never overstate capacity.** Swap is not RAM; offline is not available;
   preemptible is not dependable. Each gets its own honest presentation.
3. **A refused job must explain itself.** "No node fits" is only acceptable
   with the reason attached.
4. **One operator, many machines, variable availability.** Adding, losing, and
   re-adding machines is the normal case, not an exception path.
5. **Reachable from whatever device is at hand**, including a phone on the
   tailnet.

## Accessibility & Inclusion

No user-specific requirement was established. _[inferred]_ Treat standard
practice as the floor: usable at small viewport widths, touch targets large
enough for a phone, state never encoded by color alone (lane and job state carry
text, not just a hue), and legible contrast in the chosen palette.
