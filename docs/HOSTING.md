# Hosting the pool at `compute.siv19.dev` (tailnet-only)

The control plane runs on the always-on Mac mini and is reachable **only over
your Tailscale tailnet** — nothing is exposed to the public internet (§2 Layer
3, §8). This is the serving model you confirmed.

## 1. Run the control plane on the mini

Bare (recommended on macOS, §open-decision 2):

```sh
make build dist
POOL_JOIN_TOKEN="$(openssl rand -hex 16)" \
POOL_DASH_TOKEN="$(openssl rand -hex 16)" \
./bin/compute-server --addr 127.0.0.1:8080 --dist ./dist
```

Note the two tokens it prints/uses:

- `POOL_JOIN_TOKEN` — every agent must present this to join.
- `POOL_DASH_TOKEN` — optional; required to view/submit via the dashboard & CLI.
  In the browser set it once: open the dashboard, then in the devtools console
  run `localStorage.poolToken = "<dash-token>"` and reload.

Keep it bound to `127.0.0.1` — `tailscale serve` is what exposes it to the
tailnet, and only the tailnet.

## 2. Put it on the tailnet with a stable HTTPS name

`tailscale serve` terminates HTTPS with a tailnet cert and proxies to the local
server:

```sh
sudo tailscale serve --bg --https=443 http://127.0.0.1:8080
```

This gives the mini an HTTPS URL at its MagicDNS name, e.g.
`https://mini.<your-tailnet>.ts.net`. That is the `POOL_SERVER` every agent and
the CLI use.

### Making the name literally `compute.siv19.dev`

Two ways, depending on what `siv19.dev` is:

1. **`siv19.dev` is a public domain you own (registrar + DNS).**
   Point a DNS record `compute.siv19.dev` at the mini's Tailscale IP (the
   `100.x.y.z` address). Because that IP is only routable *inside* your tailnet,
   the name resolves for everyone but only connects for your devices — public
   but unreachable off-tailnet. For a real TLS cert on that exact hostname,
   use Tailscale's HTTPS with a custom domain, or run Caddy on the mini with the
   DNS-01 challenge (`siv19.dev` API token) in front of `127.0.0.1:8080`.

2. **You just want a memorable name inside the tailnet.**
   Rename the mini's machine to `compute` in the Tailscale admin console; its
   MagicDNS name becomes `https://compute.<your-tailnet>.ts.net`. Simplest, zero
   DNS work, still tailnet-only.

> If you want it reachable from the public internet (not just the tailnet),
> that is `tailscale funnel` instead of `serve` — but the plan explicitly says
> nothing public (§2, §8), so this is off by default.

## 3. Onboard nodes

Build the fleet binaries once and let the server hand them out:

```sh
make dist                       # produces dist/pool-agent-<os>-<arch>
# server already started with --dist ./dist, so they're at /dist/...
```

**Mac mini itself** (also the control-plane host — bare, Metal GPU):

```sh
POOL_JOIN_TOKEN=<join> ./bin/pool-agent \
  --server https://compute.<tailnet>.ts.net \
  --name mini --labels location=home
# --bare defaults to true on macOS; Metal is detected automatically.
```

**ROG laptop (Ubuntu or WSL2 Ubuntu), cloud VMs — one command** (§5):

```sh
export POOL_SERVER="https://compute.<tailnet>.ts.net"
export POOL_JOIN_TOKEN="<join>"
export TS_AUTHKEY="tskey-auth-..."
export POOL_AGENT_URL="$POOL_SERVER/dist/pool-agent-linux-amd64"
curl -fsSL "$POOL_SERVER/dist/bootstrap-node.sh" | sudo -E bash
```

The bootstrap script installs Tailscale + Docker, sets up a swapfile for the
slow lane (§6.3), and runs the agent as a systemd service.

**Colab** (best-effort, preemptible): paste `scripts/colab-node.py` into a cell,
fill in the four constants, run it. The node registers with `preemptible=true`
so the scheduler skips it unless a job passes `--allow-preempt` (§3, §5).

## 4. Use it

- Dashboard: open `https://compute.<tailnet>.ts.net` in any browser on the
  tailnet. Totals, nodes, queue, submit form, live logs, usage.
- CLI: `export POOL_SERVER=... POOL_DASH_TOKEN=...` then
  `pool submit train.py --needs gpu:cuda --min-ram 16g`.

## Notes

- The control plane is a single point of failure on the mini (§8). If the mini
  is down, nothing new schedules; jobs already running on other nodes continue.
- Large datasets belong on the NAS mount, read by nodes from the mount — not
  re-uploaded per job (§5, §8).
