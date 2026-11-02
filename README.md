# Shared Compute Pool

One place to add any machine — cloud VMs from any provider, Colab instances, a
Mac mini, a gaming laptop — see total available compute (CPUs, GPUs, RAM,
storage), and from anywhere submit a notebook, a Python file, or a Java file and
have it run on any system in the pool.

Self-hosted and single-user by design. All traffic rides a
[Tailscale](https://tailscale.com) tailnet — no public IPs, no port forwarding,
nothing exposed to the internet. Written in Go: three dependency-free static
binaries (control plane, node agent, CLI) that cross-compile to the whole
heterogeneous fleet (Linux amd64/arm64, macOS arm64, Windows amd64).

Section references (`§N`) below point to the design document this implements.
See [docs/HOSTING.md](docs/HOSTING.md) for deploying behind a tailnet HTTPS name.

## Architecture (three layers, §2)

```
   browser / CLI  ──►  control plane (Mac mini)  ──►  node agents  ──►  your code
   (tailnet)            compute-server              pool-agent          .py/.ipynb/.java
                        SQLite + dashboard          one static binary
                        FIFO scheduler              Docker or bare
                             ▲                            │
                             └──────── Tailscale ─────────┘
```

- **control plane** (`cmd/compute-server`) — node registry, job queue, FIFO
  scheduler with label + RAM/swap fit tiers, SSE log streaming, artifact store,
  embedded web dashboard. Never runs user code itself.
- **node agent** (`cmd/pool-agent`) — one dependency-free static binary per
  machine. Registers, heartbeats CPU/GPU/RAM/swap/disk + labels every 15s,
  leases fitting jobs, runs them (Docker on Linux, bare on macOS), streams
  logs, uploads artifacts.
- **CLI** (`cmd/pool`) — `pool submit`, `nodes`, `jobs`, `logs`, `get`.
- **fabric** — Tailscale. No public IPs, no port forwarding.

## Quick start (all on one machine, to try it)

```sh
make build
POOL_JOIN_TOKEN=dev ./bin/compute-server --addr 127.0.0.1:8080 &
POOL_JOIN_TOKEN=dev ./bin/pool-agent --server http://127.0.0.1:8080 --name local &
POOL_SERVER=http://127.0.0.1:8080 ./bin/pool submit examples/hello.py
POOL_SERVER=http://127.0.0.1:8080 ./bin/pool jobs
```

Open `http://127.0.0.1:8080` for the dashboard.

## Job model (§4)

A job is `{ files, runtime, requirements, timeout }`. Runtime is inferred from
the entry file:

| File     | How it runs |
|----------|-------------|
| `.py`    | `python file.py`; auto-venv from `requirements.txt` if present, or in a container |
| `.ipynb` | Papermill if available (writes an executed notebook), else `jupyter nbconvert --execute` |
| `.java`  | `javac *.java && java Main` (works on JDK 8+); source-launch where only a JRE 11+ exists |

Submit flags map to scheduler requirements:

```sh
pool submit train.py --needs gpu:cuda --min-ram 16g --timeout 7200
pool submit infer.py --disk-backed          # sparse mmap weights, don't demand full RAM (§6.4)
pool submit quick.ipynb --allow-preempt      # permit Colab nodes
```

## Scheduler (§4, §6)

1. **Filter** eligible nodes by labels: GPU type (a CUDA job never lands on the
   Metal-only mini), arch, OS, preemptible opt-in. Offline nodes never count.
2. **Fit check** (swap tiers, §6): fits in free RAM → **fast lane**; fits only
   in RAM+swap → **slow lane** (allowed, warned "expect a large slowdown");
   exceeds both → stays queued with a clear "no node fits" reason. Disk-backed
   jobs skip the RAM demand entirely.
3. **FIFO** when all fitting nodes are busy. One job per node; each job runs on
   exactly one machine (not a fused supercomputer — §4, §8).

## Swap as RAM (§6)

The agent reports **RAM and swap as separate numbers**; the dashboard shows them
on separate lines and never folds swap into the RAM headline. The bootstrap
script sets up an SSD swapfile sized ~1× RAM with `vm.swappiness=20`. Per-job
memory caps (`--mem-cap-frac`, Docker `--memory`) keep one job from dragging a
whole node into swap-thrash.

## Build & deploy

```sh
make build        # host binaries into bin/
make dist         # cross-compile agent for linux/{amd64,arm64}, darwin/arm64, windows
make test         # scheduler + unit tests
```

Hosting at `compute.siv19.dev` over the tailnet: **[docs/HOSTING.md](docs/HOSTING.md)**.

## Phase status (§7)

- **Phase 0–2 — built & verified:** control plane, agent, dashboard with real
  totals, `.py`/`.ipynb`/`.java` runners, live logs, artifacts, FIFO queue,
  label + fit scheduling, preemptible tier, Docker isolation on Linux,
  `requirements.txt` envs, per-job memory caps, swap tiers.
- **Phase 3 — built:** provider-agnostic cloud bootstrap script, Colab
  best-effort node, agent distribution via `/dist`, usage view. VM start/stop
  stays in the provider console/CLI for v1 (§5); stopped VMs report offline.
- **Phase 4 — not built (deliberately, "only if needed", §7):** Mesh-LLM pooled
  inference, disk-backed K3-style inference template, multi-node Ray jobs. These
  are special job types layered on the pool later, not part of v1.

## Honest limits (§8)

- v1 jobs run on one node each; the pool is a choice of machines, not a
  supercomputer.
- GPU jobs need a matching GPU type; CUDA jobs cannot run on the mini.
- Colab nodes can vanish mid-job — only short or checkpointed jobs there.
- The control plane is a single point of failure on the mini; acceptable for a
  single-user pool.
- Data movement rides Tailscale; big datasets belong on the NAS mount.
