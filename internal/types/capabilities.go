package types

// This file carries the "what can the pool actually do right now" model. The
// submit form is built from it, so that the operator is only ever offered
// choices some online machine can satisfy (and is told plainly when it cannot).

// Toolchain keys reported by each agent in its heartbeat. A node only claims a
// runtime it can really execute, probed on the host.
const (
	ToolPython   = "python"   // python3 / python on PATH
	ToolNotebook = "notebook" // papermill or jupyter nbconvert
	ToolJava     = "java"     // a working javac or a JDK 11+ java
	ToolDocker   = "docker"   // docker daemon reachable
)

// RuntimeTool maps a job runtime to the toolchain key a node must report.
func RuntimeTool(r RuntimeKind) string {
	switch r {
	case RuntimePython:
		return ToolPython
	case RuntimeNotebook:
		return ToolNotebook
	case RuntimeJava:
		return ToolJava
	}
	return ""
}

// Option is one selectable value in the submit form, with enough context for
// the interface to show why it is or is not available.
type Option struct {
	Value     string `json:"value"`
	Label     string `json:"label"`
	NodeCount int    `json:"node_count"` // online nodes offering this
	Available bool   `json:"available"`  // node_count > 0
	Detail    string `json:"detail"`     // e.g. "RTX 4070 · 8 GB VRAM"
}

// RuntimeOption additionally reports the largest job this runtime could take.
type RuntimeOption struct {
	Option
	MaxRAMFastMB uint64 `json:"max_ram_fast_mb"`
	MaxRAMSwapMB uint64 `json:"max_ram_swap_mb"`
}

// Capabilities is the live answer to "what can I ask for?", computed from
// ONLINE nodes only. Everything here is derived from current heartbeats; no
// value is hardcoded.
type Capabilities struct {
	OnlineNodes  int `json:"online_nodes"`
	OfflineNodes int `json:"offline_nodes"`
	IdleNodes    int `json:"idle_nodes"`

	Runtimes []RuntimeOption `json:"runtimes"` // python / notebook / java
	GPUTypes []Option        `json:"gpu_types"`
	Arches   []Option        `json:"arches"`
	OSes     []Option        `json:"oses"`

	// Memory ceilings across online nodes: the largest single machine, since a
	// job never spans machines.
	MaxRAMFastMB uint64 `json:"max_ram_fast_mb"` // biggest free RAM -> fast lane
	MaxRAMSwapMB uint64 `json:"max_ram_swap_mb"` // biggest RAM+swap -> slow lane
	MaxVCPUs     int    `json:"max_vcpus"`

	// Preemptible opt-in is only a meaningful control when such a node exists.
	PreemptibleNodes int `json:"preemptible_nodes"`

	DefaultTimeoutSec int `json:"default_timeout_sec"`
}

// FeasibilityRequest asks the real scheduler whether a hypothetical job could
// be placed, before the operator commits to submitting it.
type FeasibilityRequest struct {
	Runtime      RuntimeKind `json:"runtime"`
	NeedsGPU     GPUType     `json:"needs_gpu"`
	MinRAMMB     uint64      `json:"min_ram_mb"`
	NeedsArch    string      `json:"needs_arch"`
	NeedsOS      string      `json:"needs_os"`
	AllowPreempt bool        `json:"allow_preempt"`
	DiskBacked   bool        `json:"disk_backed"`
}

// FeasibilityResponse is the scheduler's verdict on that hypothetical job.
type FeasibilityResponse struct {
	Placeable    bool     `json:"placeable"`
	MatchCount   int      `json:"match_count"`    // online nodes that could run it
	IdleMatches  int      `json:"idle_matches"`   // of those, currently free
	Lane         SwapLane `json:"lane"`           // best lane available
	Nodes        []string `json:"nodes"`          // names of matching nodes
	Reason       string   `json:"reason"`         // why, in the operator's words
	SlowLaneOnly bool     `json:"slow_lane_only"` // would run in swap
}

// ---- Colab -----------------------------------------------------------------

// ColabTier is one Google Colab runtime option.
//
// IMPORTANT: these figures are reference values for planning only. Google
// changes Colab's hardware and quotas without notice, and what a given account
// is offered varies by tier and availability. The authoritative numbers for a
// pool are whatever a connected Colab node actually reports in its heartbeat,
// which is why the dashboard shows a measured column beside this table.
type ColabTier struct {
	ID        string `json:"id"`
	Runtime   string `json:"runtime"`     // label shown in Colab's runtime picker
	GPU       string `json:"gpu"`         // accelerator, or "" for CPU-only
	VRAMGB    int    `json:"vram_gb"`     // 0 when not a GPU runtime
	RAMGB     int    `json:"ram_gb"`      // standard RAM
	HighRAMGB int    `json:"high_ram_gb"` // with the High-RAM shape, 0 if unavailable
	VCPUs     int    `json:"vcpus"`
	Plan      string `json:"plan"`     // where it is typically offered
	GPUType   string `json:"gpu_type"` // cuda / tpu / none -> pool label
	Note      string `json:"note"`
}

// ColabInfo is everything the dashboard needs to get a Colab node running.
type ColabInfo struct {
	// Ready-to-run cell contents, pre-filled with this control plane's URL and
	// join token so there is nothing to edit but the Tailscale auth key.
	Snippet string `json:"snippet"`
	// NotebookURL opens a blank Colab notebook.
	NotebookURL string      `json:"notebook_url"`
	ServerURL   string      `json:"server_url"`
	TokenKnown  bool        `json:"token_known"`
	Tiers       []ColabTier `json:"tiers"`
	Caveat      string      `json:"caveat"`
	// Measured reports what Colab nodes in this pool have actually reported,
	// keyed by node name. This is the trustworthy column.
	Measured []ColabMeasured `json:"measured"`
}

// ColabMeasured is a real observation from a connected Colab node.
type ColabMeasured struct {
	NodeName string `json:"node_name"`
	Online   bool   `json:"online"`
	GPU      string `json:"gpu"`
	VRAMMB   uint64 `json:"vram_mb"`
	RAMMB    uint64 `json:"ram_mb"`
	VCPUs    int    `json:"vcpus"`
	DiskMB   uint64 `json:"disk_mb"`
}

// ColabTiers is the reference table. Figures are approximate and drift; see the
// ColabTier doc comment. Ordered cheapest-to-strongest.
func ColabTiers() []ColabTier {
	return []ColabTier{
		{
			ID: "cpu", Runtime: "CPU", GPU: "", VRAMGB: 0,
			RAMGB: 13, HighRAMGB: 51, VCPUs: 2, Plan: "Free and paid",
			GPUType: "none",
			Note:    "No accelerator. Fine for CPU-bound scripts and notebooks.",
		},
		{
			ID: "t4", Runtime: "T4 GPU", GPU: "NVIDIA Tesla T4", VRAMGB: 16,
			RAMGB: 13, HighRAMGB: 51, VCPUs: 2, Plan: "Free and paid",
			GPUType: "cuda",
			Note:    "The common free-tier GPU. 16 GB VRAM is the usual ceiling.",
		},
		{
			ID: "l4", Runtime: "L4 GPU", GPU: "NVIDIA L4", VRAMGB: 24,
			RAMGB: 53, HighRAMGB: 53, VCPUs: 8, Plan: "Paid (Pro)",
			GPUType: "cuda",
			Note:    "More VRAM than T4 and a much larger host RAM allowance.",
		},
		{
			ID: "a100", Runtime: "A100 GPU", GPU: "NVIDIA A100 40GB", VRAMGB: 40,
			RAMGB: 84, HighRAMGB: 84, VCPUs: 12, Plan: "Paid (Pro+)",
			GPUType: "cuda",
			Note:    "Largest CUDA option; consumes compute units quickly.",
		},
		{
			ID: "tpu", Runtime: "TPU", GPU: "Cloud TPU", VRAMGB: 0,
			RAMGB: 47, HighRAMGB: 0, VCPUs: 48, Plan: "Free and paid",
			GPUType: "none",
			Note:    "TPUs need JAX or TensorFlow written for them; the pool labels a TPU node gpu-type=none because no CUDA or Metal job can use it.",
		},
	}
}

// ColabCaveat is shown wherever the reference table appears.
const ColabCaveat = "Reference figures only. Google changes Colab hardware, " +
	"RAM shapes and quotas without notice, and what you are offered depends on " +
	"your plan and current availability. Trust the measured column: it is what " +
	"your own connected node reported."
