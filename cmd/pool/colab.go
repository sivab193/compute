package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// cmdColab prints the Colab runtime options, what each provides, and the
// ready-to-run cell for joining a Colab session to this pool.
//
//	pool colab            # options table + how to start
//	pool colab --snippet  # just the cell, for piping to the clipboard
func cmdColab(args []string) error {
	_, flags := parseArgs(args)

	var info struct {
		Snippet     string `json:"snippet"`
		NotebookURL string `json:"notebook_url"`
		ServerURL   string `json:"server_url"`
		TokenKnown  bool   `json:"token_known"`
		Caveat      string `json:"caveat"`
		Tiers       []struct {
			Runtime   string `json:"runtime"`
			GPU       string `json:"gpu"`
			VRAMGB    int    `json:"vram_gb"`
			RAMGB     int    `json:"ram_gb"`
			HighRAMGB int    `json:"high_ram_gb"`
			VCPUs     int    `json:"vcpus"`
			Plan      string `json:"plan"`
			GPUType   string `json:"gpu_type"`
			Note      string `json:"note"`
		} `json:"tiers"`
		Measured []struct {
			NodeName string `json:"node_name"`
			Online   bool   `json:"online"`
			GPU      string `json:"gpu"`
			VRAMMB   uint64 `json:"vram_mb"`
			RAMMB    uint64 `json:"ram_mb"`
			VCPUs    int    `json:"vcpus"`
		} `json:"measured"`
	}
	if err := httpGet(flags, "/api/colab", &info); err != nil {
		return err
	}

	// --snippet prints only the cell, so it can be piped straight to pbcopy.
	if _, only := flags["snippet"]; only {
		fmt.Print(info.Snippet)
		return nil
	}

	fmt.Println("COLAB RUNTIME OPTIONS")
	fmt.Println()
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RUNTIME\tACCELERATOR\tVRAM\tRAM\tHIGH-RAM\tvCPU\tPOOL LABEL\tPLAN")
	for _, t := range info.Tiers {
		gpu := t.GPU
		if gpu == "" {
			gpu = "—"
		}
		vram := "—"
		if t.VRAMGB > 0 {
			vram = fmt.Sprintf("%d GB", t.VRAMGB)
		}
		high := "not offered"
		if t.HighRAMGB > 0 {
			high = fmt.Sprintf("%d GB", t.HighRAMGB)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d GB\t%s\t%d\tgpu-type=%s\t%s\n",
			t.Runtime, gpu, vram, t.RAMGB, high, t.VCPUs, t.GPUType, t.Plan)
	}
	tw.Flush()

	fmt.Println()
	fmt.Println("NOTES")
	for _, t := range info.Tiers {
		fmt.Printf("  %-12s %s\n", t.Runtime, t.Note)
	}

	fmt.Println()
	fmt.Println("HIGH-RAM")
	fmt.Println("  High-RAM is a runtime shape, not an accelerator: it raises host RAM")
	fmt.Println("  while the GPU stays the same. On the CPU and T4 runtimes it is the")
	fmt.Println("  difference that matters most, taking host RAM from roughly 13 GB to")
	fmt.Println("  about 51 GB. L4 and A100 already run with a large RAM allowance, so")
	fmt.Println("  the toggle changes little there.")

	fmt.Println()
	fmt.Println("MEASURED IN THIS POOL")
	if len(info.Measured) == 0 {
		fmt.Println("  No Colab node has connected yet. Once one does, its real figures")
		fmt.Println("  appear here and are more trustworthy than the table above.")
	} else {
		mt := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(mt, "  NODE\tSTATUS\tGPU\tVRAM\tRAM\tvCPU")
		for _, m := range info.Measured {
			status := "offline"
			if m.Online {
				status = "online"
			}
			gpu := m.GPU
			if gpu == "" {
				gpu = "—"
			}
			fmt.Fprintf(mt, "  %s\t%s\t%s\t%d MB\t%d MB\t%d\n",
				m.NodeName, status, gpu, m.VRAMMB, m.RAMMB, m.VCPUs)
		}
		mt.Flush()
	}

	fmt.Println()
	fmt.Println("CAVEAT")
	fmt.Println("  " + wrap(info.Caveat, 72, "  "))

	fmt.Println()
	fmt.Println("START A COLAB NODE")
	fmt.Printf("  1. Open a blank notebook: %s\n", info.NotebookURL)
	fmt.Println("  2. Runtime -> Change runtime type -> pick an accelerator above.")
	fmt.Println("  3. Paste the cell below, add a Tailscale auth key, and run it.")
	fmt.Println("     Keep the cell running: the node leaves the pool when it stops.")
	if !info.TokenKnown {
		fmt.Println("     NOTE: the control plane has no join token set, so the cell")
		fmt.Println("     carries an empty one.")
	}
	fmt.Println()
	fmt.Println("  Copy it straight to the clipboard with:")
	fmt.Println("    pool colab --snippet | pbcopy")
	fmt.Println()
	fmt.Println("  Colab nodes register as preemptible, so they are skipped unless a")
	fmt.Println("  job opts in with --allow-preempt.")
	return nil
}

// cmdCaps prints what the pool can currently be asked for.
func cmdCaps(args []string) error {
	_, flags := parseArgs(args)
	var caps struct {
		OnlineNodes      int    `json:"online_nodes"`
		OfflineNodes     int    `json:"offline_nodes"`
		IdleNodes        int    `json:"idle_nodes"`
		PreemptibleNodes int    `json:"preemptible_nodes"`
		MaxRAMFastMB     uint64 `json:"max_ram_fast_mb"`
		MaxRAMSwapMB     uint64 `json:"max_ram_swap_mb"`
		MaxVCPUs         int    `json:"max_vcpus"`
		Runtimes         []struct {
			Value     string `json:"value"`
			Label     string `json:"label"`
			NodeCount int    `json:"node_count"`
			Available bool   `json:"available"`
			Detail    string `json:"detail"`
		} `json:"runtimes"`
		GPUTypes []struct {
			Value  string `json:"value"`
			Label  string `json:"label"`
			Detail string `json:"detail"`
		} `json:"gpu_types"`
		Arches []struct{ Value, Detail string } `json:"arches"`
		OSes   []struct{ Value, Detail string } `json:"oses"`
	}
	if err := httpGet(flags, "/api/capabilities", &caps); err != nil {
		return err
	}
	if _, j := flags["json"]; j {
		b, _ := json.MarshalIndent(caps, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("POOL CAPACITY  %d online, %d idle, %d offline\n",
		caps.OnlineNodes, caps.IdleNodes, caps.OfflineNodes)
	if caps.OnlineNodes == 0 {
		fmt.Println("\nNothing is online, so nothing can be submitted right now.")
		return nil
	}
	fmt.Printf("  largest machine: %d vCPU · %d MB free RAM (fast lane) · %d MB with swap (slow lane)\n",
		caps.MaxVCPUs, caps.MaxRAMFastMB, caps.MaxRAMSwapMB)
	if caps.PreemptibleNodes > 0 {
		fmt.Printf("  %d preemptible machine(s); add --allow-preempt to use them\n", caps.PreemptibleNodes)
	}

	fmt.Println("\nRUNTIMES YOU CAN SUBMIT")
	for _, r := range caps.Runtimes {
		mark := "yes"
		if !r.Available {
			mark = "NO "
		}
		fmt.Printf("  [%s] %-18s %s\n", mark, r.Label, r.Detail)
	}

	fmt.Println("\nGPU TYPES AVAILABLE")
	if len(caps.GPUTypes) == 0 {
		fmt.Println("  none — no online machine reports a GPU")
	}
	for _, g := range caps.GPUTypes {
		fmt.Printf("  --needs gpu:%-6s %s (%s)\n", g.Value, g.Label, g.Detail)
	}

	fmt.Println("\nARCHITECTURES")
	for _, a := range caps.Arches {
		fmt.Printf("  --arch %-8s %s\n", a.Value, a.Detail)
	}
	fmt.Println("\nOPERATING SYSTEMS")
	for _, o := range caps.OSes {
		fmt.Printf("  --os %-10s %s\n", o.Value, o.Detail)
	}
	return nil
}

// cmdNodeAdd prints copy-paste onboarding commands for a new machine.
func cmdNodeAdd(args []string) error {
	pos, flags := parseArgs(args)
	path := "/api/enroll"
	if v := flags["name"]; v != "" {
		path += "?name=" + v
	}
	var out struct {
		ServerURL string `json:"server_url"`
		Recipes   []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Note    string `json:"note"`
			Command string `json:"command"`
		} `json:"recipes"`
	}
	if err := httpGet(flags, path, &out); err != nil {
		return err
	}
	want := ""
	if len(pos) > 0 {
		want = pos[0]
	}
	fmt.Printf("Control plane: %s\n", out.ServerURL)
	for _, r := range out.Recipes {
		if want != "" && !strings.Contains(r.ID, want) {
			continue
		}
		fmt.Printf("\n=== %s ===\n", r.Label)
		fmt.Println(wrap(r.Note, 76, ""))
		fmt.Println()
		fmt.Println(r.Command)
	}
	if want == "" {
		fmt.Println("\nNarrow to one platform with: pool node-add windows | linux | macos")
	}
	return nil
}

// wrap hard-wraps text at width, prefixing continuation lines with indent.
func wrap(s string, width int, indent string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		if lineLen > 0 && lineLen+1+len(w) > width {
			b.WriteString("\n" + indent)
			lineLen = 0
		} else if i > 0 {
			b.WriteString(" ")
			lineLen++
		}
		b.WriteString(w)
		lineLen += len(w)
	}
	return b.String()
}
