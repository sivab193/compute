# colab-node.py — join a Google Colab instance to the pool as a best-effort,
# preemptible node (Phase 3, §5). Paste into a Colab cell and run.
#
# Honest flag (§5, §8): Colab is the weakest node type — session caps, idle
# timeouts, GPU lottery. The agent registers with preemptible=true so the
# scheduler excludes it by default; only jobs that pass --allow-preempt land
# here. Never run anything you cannot afford to lose mid-run.
#
# Fill these in before running:
TS_AUTHKEY   = "tskey-auth-REPLACE_ME"          # Tailscale ephemeral auth key
POOL_SERVER  = "https://mini.your-tailnet.ts.net"
JOIN_TOKEN   = "REPLACE_ME"
NODE_NAME    = "colab-gpu"
AGENT_URL    = "https://mini.your-tailnet.ts.net/dist/pool-agent-linux-amd64"

import os, subprocess, sys

def sh(cmd, **kw):
    print("+", cmd)
    return subprocess.run(cmd, shell=True, check=False, **kw)

# 1. Tailscale in userspace-networking mode (Colab has no root tun device).
sh("curl -fsSL https://tailscale.com/install.sh | sh")
# Start tailscaled in userspace mode in the background.
sh("nohup tailscaled --tun=userspace-networking "
   "--socks5-server=localhost:1055 --outbound-http-proxy-listen=localhost:1055 "
   "> /tmp/tailscaled.log 2>&1 &")
sh(f"sleep 3 && tailscale up --authkey {TS_AUTHKEY} --hostname {NODE_NAME}")

# 2. Fetch the agent binary.
sh(f"curl -fsSL {AGENT_URL} -o /usr/local/bin/pool-agent && chmod +x /usr/local/bin/pool-agent")

# 3. Detect the GPU for the label (Colab usually hands out a CUDA GPU).
gpu_label = "gpu-type=none"
if sh("nvidia-smi -L", capture_output=True).returncode == 0:
    gpu_label = "gpu-type=cuda"

# 4. Run the agent in the foreground (keep the cell running to stay online).
env = os.environ.copy()
env.update({
    "POOL_SERVER": POOL_SERVER,
    "POOL_JOIN_TOKEN": JOIN_TOKEN,
    "POOL_NODE_NAME": NODE_NAME,
    # preemptible=true is the whole point: default scheduling skips this node.
    "POOL_LABELS": f"preemptible=true,location=colab,{gpu_label}",
    "POOL_WORK": "/content/pool-work",
    # Route agent traffic through the userspace SOCKS proxy.
    "ALL_PROXY": "socks5://localhost:1055",
    "HTTP_PROXY": "http://localhost:1055",
    "HTTPS_PROXY": "http://localhost:1055",
})
print("starting agent; keep this cell running to stay in the pool")
os.execve("/usr/local/bin/pool-agent",
          ["pool-agent", "--bare=false"], env)
