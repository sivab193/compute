package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/siv19/compute/internal/sched"
	"github.com/siv19/compute/internal/types"
)

// handleCapabilities reports what the pool can currently be asked for, so the
// submit form can offer only satisfiable options.
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	nodes, err := s.st.ListNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sched.Capabilities(nodes, s.cfg.DefaultTimeoutSec))
}

// handleFeasibility runs the real scheduler against a hypothetical job so the
// operator learns before submitting whether anything can run it, and in which
// lane.
func (s *Server) handleFeasibility(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	var req types.FeasibilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad feasibility body: "+err.Error())
		return
	}
	nodes, err := s.st.ListNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sched.Evaluate(req, nodes))
}

// handleColab returns everything needed to bring a Colab session into the pool:
// a pre-filled cell, the reference hardware table, and — the column that can
// actually be trusted — what this pool's own Colab nodes have reported.
func (s *Server) handleColab(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	nodes, err := s.st.ListNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	info := types.ColabInfo{
		NotebookURL: "https://colab.research.google.com/#create=true",
		ServerURL:   s.publicURL(r),
		TokenKnown:  s.cfg.JoinToken != "",
		Tiers:       types.ColabTiers(),
		Caveat:      types.ColabCaveat,
	}
	for _, n := range nodes {
		if n.Labels["location"] != "colab" && !strings.Contains(strings.ToLower(n.Name), "colab") {
			continue
		}
		m := types.ColabMeasured{
			NodeName: n.Name,
			Online:   n.Status == types.NodeOnline,
			RAMMB:    n.RAMTotalMB,
			VCPUs:    n.CPUCount,
			DiskMB:   n.DiskTotalMB,
		}
		for _, g := range n.GPUs {
			m.GPU = g.Model
			m.VRAMMB = g.VRAMMB
			break
		}
		info.Measured = append(info.Measured, m)
	}
	info.Snippet = s.colabSnippet(info.ServerURL)
	writeJSON(w, http.StatusOK, info)
}

// colabSnippet builds the Colab cell, pre-filled with this control plane's URL
// and join token so the only value left to paste is a Tailscale auth key.
func (s *Server) colabSnippet(serverURL string) string {
	token := s.cfg.JoinToken
	if token == "" {
		token = ""
	}
	return fmt.Sprintf(`#@title Join this Colab session to the compute pool { display-mode: "form" }
#@markdown Paste a Tailscale auth key, then run this cell. Keep it running to stay in the pool.
TS_AUTHKEY = ""  #@param {type:"string"}
NODE_NAME  = "colab-gpu"  #@param {type:"string"}

POOL_SERVER = %q
JOIN_TOKEN  = %q

import os, subprocess
def sh(c, **k): print("+", c); return subprocess.run(c, shell=True, **k)

sh("curl -fsSL https://tailscale.com/install.sh | sh")
sh("nohup tailscaled --tun=userspace-networking --socks5-server=localhost:1055 "
   "--outbound-http-proxy-listen=localhost:1055 > /tmp/tailscaled.log 2>&1 &")
sh(f"sleep 3 && tailscale up --authkey {TS_AUTHKEY} --hostname {NODE_NAME}")
sh(f"curl -fsSL {POOL_SERVER}/dist/pool-agent-linux-amd64 -o /usr/local/bin/pool-agent "
   "&& chmod +x /usr/local/bin/pool-agent")

gpu = "cuda" if sh("nvidia-smi -L", capture_output=True).returncode == 0 else "none"
print("detected gpu-type:", gpu)

env = dict(os.environ,
    POOL_SERVER=POOL_SERVER, POOL_JOIN_TOKEN=JOIN_TOKEN, POOL_NODE_NAME=NODE_NAME,
    POOL_LABELS=f"preemptible=true,location=colab,gpu-type={gpu}",
    POOL_WORK="/content/pool-work",
    ALL_PROXY="socks5://localhost:1055",
    HTTP_PROXY="http://localhost:1055", HTTPS_PROXY="http://localhost:1055")
os.execve("/usr/local/bin/pool-agent", ["pool-agent", "--bare=false"], env)
`, serverURL, token)
}

// handleEnroll returns copy-paste onboarding commands for a new machine,
// pre-filled with this control plane's URL and join token.
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	base := s.publicURL(r)
	token := s.cfg.JoinToken
	name := r.URL.Query().Get("name")

	type recipe struct {
		ID       string `json:"id"`
		Label    string `json:"label"`
		Platform string `json:"platform"`
		Note     string `json:"note"`
		Command  string `json:"command"`
	}

	winName := orDefault(name, "rog-laptop")
	recipes := []recipe{
		{
			ID: "windows-wsl2", Label: "Gaming laptop (Windows, WSL2)", Platform: "windows",
			Note: "Run in PowerShell as Administrator first, then the second block inside Ubuntu. CUDA works through WSL2, so the NVIDIA GPU is usable without leaving Windows.",
			Command: fmt.Sprintf(`# 1. In PowerShell (Administrator) — install WSL2 + Ubuntu, then reboot if asked:
wsl --install -d Ubuntu

# 2. Open Ubuntu from the Start menu and run:
curl -fsSL %s/dist/bootstrap-node.sh -o bootstrap-node.sh
export POOL_SERVER=%q
export POOL_JOIN_TOKEN=%q
export POOL_NODE_NAME=%q
export POOL_LABELS="location=home,gpu-type=cuda"
export POOL_AGENT_URL="%s/dist/pool-agent-linux-amd64"
export TS_AUTHKEY="tskey-auth-REPLACE_ME"
sudo -E bash bootstrap-node.sh`, base, base, token, winName, base),
		},
		{
			ID: "linux", Label: "Linux machine or cloud VM", Platform: "linux",
			Note: "Provider-agnostic: identical on AWS, GCP, Azure, Lambda or bare metal. Installs Tailscale, Docker, a swapfile and the agent as a systemd service.",
			Command: fmt.Sprintf(`export POOL_SERVER=%q
export POOL_JOIN_TOKEN=%q
export POOL_NODE_NAME=%q
export POOL_LABELS="location=cloud"
export POOL_AGENT_URL="%s/dist/pool-agent-linux-amd64"
export TS_AUTHKEY="tskey-auth-REPLACE_ME"
curl -fsSL %s/dist/bootstrap-node.sh | sudo -E bash`,
				base, token, orDefault(name, "cloud-vm"), base, base),
		},
		{
			ID: "macos", Label: "Mac", Platform: "darwin",
			Note: "Runs jobs as host processes rather than in Docker, so Metal stays available.",
			Command: fmt.Sprintf(`curl -fsSL %s/dist/pool-agent-darwin-arm64 -o /usr/local/bin/pool-agent
chmod +x /usr/local/bin/pool-agent
POOL_JOIN_TOKEN=%q /usr/local/bin/pool-agent \
  --server %q --name %q --labels location=home`,
				base, token, base, orDefault(name, "mac")),
		},
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"server_url": base,
		"recipes":    recipes,
	})
}

// publicURL reconstructs the externally reachable base URL, preferring the
// proxy headers `tailscale serve` sets so generated commands point at the
// tailnet name rather than the loopback bind address.
func (s *Server) publicURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
		scheme = v
	}
	host := r.Host
	if v := r.Header.Get("X-Forwarded-Host"); v != "" {
		host = v
	}
	return scheme + "://" + host
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
