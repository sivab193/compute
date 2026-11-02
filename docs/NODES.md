# Adding machines to the pool

Every machine joins the same way: get it on the tailnet, drop the `pool-agent`
binary on it, give it the join token. The agent then reports what the machine
has (CPU, GPU, RAM, swap, disk, and which runtimes it can execute) and starts
taking jobs that fit.

The dashboard generates these commands pre-filled with your control plane's URL
and join token: open **Add a machine** and copy the block for your platform.
From a terminal, `pool node-add windows|linux|macos` prints the same thing.

Before you start, have:

- the control plane URL, e.g. `https://compute.your-tailnet.ts.net`
- the join token (`POOL_JOIN_TOKEN` on the server)
- a Tailscale auth key from the [admin console](https://login.tailscale.com/admin/settings/keys)

---

## The gaming laptop (Windows + NVIDIA)

Two ways in. **Use WSL2 now**; it works today and costs you nothing. Move to a
dual-boot later if you want the last few percent of GPU performance. The agent
is identical either way, so the pool does not notice the difference.

### Option A — WSL2 (recommended, ~15 minutes)

CUDA works through WSL2: the Windows NVIDIA driver exposes the GPU to the Linux
side, so `nvidia-smi` works inside Ubuntu and the pool sees a real CUDA node
without you leaving Windows or rebooting.

**1. Install WSL2 with Ubuntu.** In PowerShell *as Administrator*:

```powershell
wsl --install -d Ubuntu
```

Reboot if it asks. When Ubuntu first launches it will ask you to create a
username and password.

**2. Make sure your NVIDIA driver is current.** Install the normal Windows
GeForce driver from NVIDIA (the Game Ready or Studio driver is fine). Do **not**
install a Linux NVIDIA driver inside WSL: that breaks the passthrough. WSL2 gets
the GPU from the Windows driver.

**3. Check the GPU is visible from Linux.** In the Ubuntu terminal:

```bash
nvidia-smi
```

You should see your GPU and its VRAM. If this fails, the pool will register the
laptop as a CPU-only node, and CUDA jobs will not be scheduled to it.

**4. Give WSL2 some swap and enough RAM.** WSL2 defaults can be stingy. Create
`C:\Users\<you>\.wslconfig` (Windows side) with something like:

```ini
[wsl2]
memory=24GB      # leave headroom for Windows; adjust to your laptop's RAM
swap=32GB        # backs the scheduler's slow lane
swapfile=C:\\wsl-swap.vhdx
```

Then `wsl --shutdown` in PowerShell and reopen Ubuntu.

**5. Join the pool.** In the Ubuntu terminal:

```bash
export POOL_SERVER="https://compute.your-tailnet.ts.net"
export POOL_JOIN_TOKEN="<join token>"
export POOL_NODE_NAME="rog-laptop"
export POOL_LABELS="location=home,gpu-type=cuda"
export POOL_AGENT_URL="$POOL_SERVER/dist/pool-agent-linux-amd64"
export TS_AUTHKEY="tskey-auth-..."
curl -fsSL "$POOL_SERVER/dist/bootstrap-node.sh" | sudo -E bash
```

That installs Tailscale and Docker, sets up a swapfile with
`vm.swappiness=20`, and runs the agent as a systemd service so it comes back
after a reboot.

**6. Confirm.** The laptop should appear under **Machines** on the dashboard
within about 15 seconds, tagged `cuda`. Check the agent with:

```bash
journalctl -u pool-agent -f
```

#### WSL2 caveats, honestly

- **The node is only online while Windows is running and WSL is up.** Closing
  the laptop lid or shutting down takes it offline, and the dashboard will show
  it under "Offline, not counted above". That is correct behavior, not a bug.
- WSL2 needs `systemd` enabled for the service to work. Recent Ubuntu images on
  WSL have it on by default. If `systemctl` complains, add
  `[boot]\nsystemd=true` to `/etc/wsl.conf` and `wsl --shutdown`.
- Isolation is weaker than a real Linux box: jobs run in Docker inside WSL
  inside Windows. For a single-user pool running your own code, this is fine.
- Disk I/O against `/mnt/c` is slow. Keep job workdirs on the Linux filesystem
  (the bootstrap script already uses `/var/lib/pool-agent/work`).

### Option B — dual-boot Ubuntu

Slightly faster for sustained GPU work and cleaner isolation, at the cost of
rebooting to switch. Install Ubuntu Server 24.04 alongside Windows, install the
proprietary NVIDIA driver (`sudo ubuntu-drivers install`), confirm
`nvidia-smi`, then run exactly the same bootstrap command as step 5 above.

The trade is availability: the laptop is only a pool node while it is booted
into Ubuntu, so if you mostly live in Windows you will have a node that is
usually offline. WSL2 keeps it online whenever the laptop is on.

---

## A cloud VM (any provider)

Provider-agnostic by design: the script does not care whether the VM came from
AWS, GCP, Azure, Lambda, Hetzner, or anywhere else.

```bash
export POOL_SERVER="https://compute.your-tailnet.ts.net"
export POOL_JOIN_TOKEN="<join token>"
export POOL_NODE_NAME="aws-a10g-1"
export POOL_LABELS="location=aws,gpu-type=cuda"   # drop gpu-type on a CPU VM
export POOL_AGENT_URL="$POOL_SERVER/dist/pool-agent-linux-amd64"
export TS_AUTHKEY="tskey-auth-..."
curl -fsSL "$POOL_SERVER/dist/bootstrap-node.sh" | sudo -E bash
```

Use `pool-agent-linux-arm64` on Graviton or Ampere instances.

Starting and stopping the VM stays in the provider's own console or CLI. The
pool only observes the result: a stopped VM misses its heartbeats and is marked
offline within about 50 seconds, and its capacity stops counting toward the
totals.

---

## A Mac

macOS nodes run jobs as host processes rather than in Docker, which keeps Metal
available (Docker on macOS runs inside a VM and cannot pass the GPU through).

```bash
curl -fsSL "$POOL_SERVER/dist/pool-agent-darwin-arm64" -o /usr/local/bin/pool-agent
chmod +x /usr/local/bin/pool-agent
POOL_JOIN_TOKEN="<join token>" /usr/local/bin/pool-agent \
  --server "$POOL_SERVER" --name mac-mini --labels location=home
```

To keep it running across reboots, install it as a LaunchAgent, or just run it
in a `tmux` session if you prefer something simpler.

Apple Silicon is labelled `gpu-type=metal`, so CUDA jobs are never scheduled
here. The agent reports no separate VRAM figure on purpose: unified memory
shares one pool with system RAM, and reporting it as VRAM would double-count it.

---

## A Google Colab session

See the **Add a Colab machine** panel on the dashboard, which generates a cell
pre-filled with your server URL and join token, lists the runtimes Colab offers,
and shows what any connected Colab node actually reported. From a terminal:

```bash
pool colab                 # runtime options, High-RAM explanation, join steps
pool colab --snippet | pbcopy
```

Colab nodes register with `preemptible=true`, so the scheduler skips them unless
a job opts in (`--allow-preempt`, or the checkbox on the submit form). They can
disappear mid-run: use them for short or checkpointed work only.

---

## Checking what the pool can do

```bash
pool caps      # runtimes, GPU types, architectures, and RAM ceilings available now
pool nodes     # every registered machine and its state
```

`pool caps` is the authoritative answer to "can I submit this?": it is computed
from live heartbeats, and the submit form in the dashboard is built from the same
data, which is why options the pool cannot satisfy appear disabled rather than
silently failing later.

## Why a machine might not take your job

The scheduler filters before it fits, and the dashboard states the reason. The
usual causes:

| Reason | What to do |
|---|---|
| `gpu-type=metal, needs cuda` | The job asked for CUDA; only an Apple machine is online. Start the laptop or a GPU VM. |
| `cannot run java jobs` | No online machine has a working JDK. Install one, or submit to a machine that has it. |
| `needs N MB, has M MB with swap` | The job's RAM floor exceeds the biggest machine. Lower it, or use `--disk-backed` if the weights are memory-mapped. |
| `preemptible; job did not opt in` | Only Colab is online. Add `--allow-preempt` if the work is short or checkpointed. |
| `all fitting nodes are busy` | Nothing is wrong; it is queued and will start when a machine frees up. |
