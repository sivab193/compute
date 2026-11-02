#!/usr/bin/env bash
# bootstrap-node.sh — one-command onboarding for a Linux node (Phase 3, §5).
#
# Provider-agnostic by design: it does not care whether the VM came from AWS,
# GCP, Azure, Lambda, or Brev. It installs Tailscale, Docker (optional), and
# the pool-agent, wires a swapfile for the slow-lane tier (§6.3), and starts the
# agent as a systemd service.
#
# Usage (run as root or with sudo on the new VM):
#
#   export POOL_SERVER="https://mini.your-tailnet.ts.net"
#   export POOL_JOIN_TOKEN="…"            # shared join token from the mini
#   export TS_AUTHKEY="tskey-auth-…"       # Tailscale auth key (ephemeral ok)
#   export POOL_NODE_NAME="aws-a10g-1"     # optional; defaults to hostname
#   export POOL_LABELS="location=aws,gpu-type=cuda"  # optional extra labels
#   export POOL_AGENT_URL="https://mini.your-tailnet.ts.net/dist/pool-agent-linux-amd64"
#   curl -fsSL "$POOL_AGENT_URL/../bootstrap-node.sh" | bash
#
# The agent binary is served by the control plane under /dist (see docs/HOSTING.md),
# or copy it to /usr/local/bin/pool-agent yourself before running.
set -euo pipefail

: "${POOL_SERVER:?set POOL_SERVER to the control plane URL}"
: "${POOL_JOIN_TOKEN:?set POOL_JOIN_TOKEN}"
NODE_NAME="${POOL_NODE_NAME:-$(hostname)}"
LABELS="${POOL_LABELS:-}"
SWAP_SIZE="${POOL_SWAP_SIZE:-}"   # e.g. "32G"; empty = 1x RAM
WORK_ROOT="${POOL_WORK:-/var/lib/pool-agent/work}"

log() { echo "[bootstrap] $*"; }

# --- 1. Tailscale ----------------------------------------------------------
if ! command -v tailscale >/dev/null 2>&1; then
  log "installing Tailscale"
  curl -fsSL https://tailscale.com/install.sh | sh
fi
if [ -n "${TS_AUTHKEY:-}" ]; then
  log "joining tailnet"
  tailscale up --authkey "$TS_AUTHKEY" --hostname "$NODE_NAME" --accept-routes || true
else
  log "TS_AUTHKEY not set; assuming this host is already on the tailnet"
fi

# --- 2. Docker (optional but recommended on Linux) -------------------------
if ! command -v docker >/dev/null 2>&1; then
  log "installing Docker"
  curl -fsSL https://get.docker.com | sh || log "docker install failed; node will run jobs bare"
fi

# --- 3. Swapfile for the slow-lane tier (§6.3) -----------------------------
if [ -z "$SWAP_SIZE" ]; then
  RAM_KB=$(grep MemTotal /proc/meminfo | awk '{print $2}')
  SWAP_SIZE="$(( RAM_KB / 1024 / 1024 ))G"   # ~1x RAM
fi
if ! swapon --show | grep -q '/swapfile'; then
  log "creating ${SWAP_SIZE} swapfile on SSD"
  fallocate -l "$SWAP_SIZE" /swapfile || dd if=/dev/zero of=/swapfile bs=1M count="$(( ${SWAP_SIZE%G} * 1024 ))"
  chmod 600 /swapfile
  mkswap /swapfile
  swapon /swapfile
  grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
  # Prefer keeping application memory on SSD-backed nodes (§6.3).
  sysctl -w vm.swappiness=20 >/dev/null || true
  grep -q 'vm.swappiness' /etc/sysctl.conf || echo 'vm.swappiness=20' >> /etc/sysctl.conf
fi

# --- 4. Install the agent binary -------------------------------------------
if [ ! -x /usr/local/bin/pool-agent ]; then
  if [ -n "${POOL_AGENT_URL:-}" ]; then
    log "downloading pool-agent from $POOL_AGENT_URL"
    curl -fsSL "$POOL_AGENT_URL" -o /usr/local/bin/pool-agent
    chmod +x /usr/local/bin/pool-agent
  else
    log "ERROR: /usr/local/bin/pool-agent missing and POOL_AGENT_URL not set"
    exit 1
  fi
fi

mkdir -p "$WORK_ROOT"

# --- 5. systemd service ----------------------------------------------------
log "installing systemd unit pool-agent.service"
cat >/etc/systemd/system/pool-agent.service <<EOF
[Unit]
Description=Shared Compute Pool node agent
After=network-online.target tailscaled.service docker.service
Wants=network-online.target

[Service]
Environment=POOL_SERVER=${POOL_SERVER}
Environment=POOL_JOIN_TOKEN=${POOL_JOIN_TOKEN}
Environment=POOL_NODE_NAME=${NODE_NAME}
Environment=POOL_LABELS=${LABELS}
Environment=POOL_WORK=${WORK_ROOT}
ExecStart=/usr/local/bin/pool-agent --mem-cap-frac 0.85
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now pool-agent.service
log "done. node '${NODE_NAME}' is joining the pool."
log "check: journalctl -u pool-agent -f"
