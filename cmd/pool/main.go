// Command pool is the terminal client for the shared compute pool. It talks to
// the same control-plane API the dashboard uses.
//
//	pool submit file.py --needs gpu:cuda --min-ram 16g
//	pool submit nb.ipynb --allow-preempt
//	pool nodes
//	pool jobs
//	pool logs <job-id>
//	pool get <job-id>
//
// Server URL comes from --server or $POOL_SERVER; dashboard token from
// --token or $POOL_DASH_TOKEN.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "submit":
		err = cmdSubmit(args)
	case "nodes":
		err = cmdNodes(args)
	case "jobs":
		err = cmdJobs(args)
	case "logs":
		err = cmdLogs(args)
	case "get":
		err = cmdGet(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`pool — shared compute pool CLI

Usage:
  pool submit <file> [more files…] [flags]
  pool nodes
  pool jobs
  pool get <job-id>
  pool logs <job-id>

Submit flags:
  --needs gpu:cuda|gpu:metal   require a GPU type
  --min-ram 16g|512m|2048      minimum RAM (suffix g/m, bare = MB)
  --arch amd64|arm64           require an architecture
  --os linux|darwin|windows    require an OS
  --allow-preempt              allow preemptible (Colab) nodes
  --disk-backed                sparse mmap weights; don't demand full RAM (§6.4)
  --timeout 3600               per-job timeout seconds
  --name "my job"              human label
  --entry file.py              which uploaded file is the entry point

Global flags (or env):
  --server URL    control plane base URL     ($POOL_SERVER)
  --token TOKEN   dashboard token if required ($POOL_DASH_TOKEN)
`)
}

// ---- shared HTTP plumbing -------------------------------------------------

func serverURL(flags map[string]string) (string, error) {
	u := flags["server"]
	if u == "" {
		u = os.Getenv("POOL_SERVER")
	}
	if u == "" {
		return "", fmt.Errorf("no server: pass --server or set POOL_SERVER")
	}
	return strings.TrimRight(u, "/"), nil
}

func token(flags map[string]string) string {
	if t := flags["token"]; t != "" {
		return t
	}
	return os.Getenv("POOL_DASH_TOKEN")
}

func httpGet(flags map[string]string, path string, out any) error {
	base, err := serverURL(flags)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	if t := token(flags); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ---- submit ---------------------------------------------------------------

func cmdSubmit(args []string) error {
	files, flags := parseArgs(args)
	if len(files) == 0 {
		return fmt.Errorf("submit needs at least one file")
	}
	base, err := serverURL(flags)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		fw, err := mw.CreateFormFile("files", filepath.Base(path))
		if err != nil {
			f.Close()
			return err
		}
		if _, err := io.Copy(fw, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}

	// Map CLI flags to form fields.
	if v := flags["needs"]; v != "" {
		if strings.HasPrefix(v, "gpu:") {
			_ = mw.WriteField("needs_gpu", strings.TrimPrefix(v, "gpu:"))
		}
	}
	if v := flags["min-ram"]; v != "" {
		mb, err := parseRAM(v)
		if err != nil {
			return err
		}
		_ = mw.WriteField("min_ram_mb", strconv.FormatUint(mb, 10))
	}
	if v := flags["arch"]; v != "" {
		_ = mw.WriteField("needs_arch", v)
	}
	if v := flags["os"]; v != "" {
		_ = mw.WriteField("needs_os", v)
	}
	if _, ok := flags["allow-preempt"]; ok {
		_ = mw.WriteField("allow_preempt", "true")
	}
	if _, ok := flags["disk-backed"]; ok {
		_ = mw.WriteField("disk_backed", "true")
	}
	if v := flags["timeout"]; v != "" {
		_ = mw.WriteField("timeout_sec", v)
	}
	if v := flags["name"]; v != "" {
		_ = mw.WriteField("name", v)
	}
	if v := flags["entry"]; v != "" {
		_ = mw.WriteField("entry", v)
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, base+"/api/jobs", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if t := token(flags); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
	}
	var job struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(body, &job)
	fmt.Printf("queued job %s (%s)\n", job.ID, job.Name)
	fmt.Printf("  pool logs %s\n", job.ID)
	return nil
}

// ---- nodes / jobs / get / logs -------------------------------------------

func cmdNodes(args []string) error {
	_, flags := parseArgs(args)
	var nodes []map[string]any
	if err := httpGet(flags, "/api/nodes", &nodes); err != nil {
		return err
	}
	if len(nodes) == 0 {
		fmt.Println("no nodes registered")
		return nil
	}
	fmt.Printf("%-18s %-8s %-6s %-10s %-8s %s\n", "NAME", "STATUS", "CPU", "RAM(MB)", "RUNNING", "LABELS")
	for _, n := range nodes {
		labels, _ := n["labels"].(map[string]any)
		fmt.Printf("%-18v %-8v %-6v %-10v %-8v %v\n",
			n["name"], n["status"], jint(n["cpu_count"]), jint(n["ram_total_mb"]),
			jint(n["running_jobs"]), compactLabels(labels))
	}
	return nil
}

func cmdJobs(args []string) error {
	_, flags := parseArgs(args)
	var jobs []map[string]any
	if err := httpGet(flags, "/api/jobs", &jobs); err != nil {
		return err
	}
	if len(jobs) == 0 {
		fmt.Println("no jobs")
		return nil
	}
	fmt.Printf("%-18s %-10s %-9s %-16s %-6s %s\n", "ID", "RUNTIME", "STATE", "NODE", "EXIT", "NAME")
	for _, j := range jobs {
		exit := "—"
		if v, ok := j["exit_code"]; ok && v != nil {
			exit = fmt.Sprintf("%v", jint(v))
		}
		fmt.Printf("%-18v %-10v %-9v %-16v %-6v %v\n",
			j["id"], j["runtime"], j["state"], orDash(j["assigned_node"]), exit, j["name"])
	}
	return nil
}

func cmdGet(args []string) error {
	ids, flags := parseArgs(args)
	if len(ids) == 0 {
		return fmt.Errorf("get needs a job id")
	}
	var job map[string]any
	if err := httpGet(flags, "/api/jobs/"+ids[0], &job); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(job, "", "  ")
	fmt.Println(string(b))
	return nil
}

// cmdLogs tails a job's logs over SSE until the job finishes.
func cmdLogs(args []string) error {
	ids, flags := parseArgs(args)
	if len(ids) == 0 {
		return fmt.Errorf("logs needs a job id")
	}
	base, err := serverURL(flags)
	if err != nil {
		return err
	}
	url := base + "/api/jobs/" + ids[0] + "/logs/stream"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if t := token(flags); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	client := &http.Client{Timeout: 0} // no timeout for a stream
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(b))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: done") {
			fmt.Println("--- job finished ---")
			return nil
		}
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			var l struct {
				Stream string `json:"stream"`
				Text   string `json:"text"`
			}
			if json.Unmarshal([]byte(payload), &l) == nil && l.Text != "" {
				prefix := ""
				if l.Stream == "stderr" {
					prefix = "[err] "
				} else if l.Stream == "system" {
					prefix = "[sys] "
				}
				fmt.Println(prefix + l.Text)
			}
		}
	}
	return sc.Err()
}

// ---- tiny arg parser ------------------------------------------------------

// parseArgs splits positional args from --flags. Boolean flags (allow-preempt,
// disk-backed) take no value; everything else consumes the next token.
func parseArgs(args []string) (positional []string, flags map[string]string) {
	flags = map[string]string{}
	boolFlags := map[string]bool{"allow-preempt": true, "disk-backed": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			key := strings.TrimPrefix(a, "--")
			if eq := strings.Index(key, "="); eq >= 0 {
				flags[key[:eq]] = key[eq+1:]
				continue
			}
			if boolFlags[key] {
				flags[key] = "true"
				continue
			}
			if i+1 < len(args) {
				flags[key] = args[i+1]
				i++
			} else {
				flags[key] = "true"
			}
		} else {
			positional = append(positional, a)
		}
	}
	return positional, flags
}

// parseRAM converts "16g", "512m", or "2048" (MB) to megabytes.
func parseRAM(s string) (uint64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	mult := uint64(1)
	switch {
	case strings.HasSuffix(s, "g"):
		mult, s = 1024, strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "m"):
		mult, s = 1, strings.TrimSuffix(s, "m")
	}
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad --min-ram %q", s)
	}
	return v * mult, nil
}

func jint(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func orDash(v any) any {
	if v == nil || v == "" {
		return "—"
	}
	return v
}

func compactLabels(m map[string]any) string {
	if m == nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"gpu-type", "arch", "os", "preemptible", "location"} {
		if v, ok := m[k]; ok && v != "" && v != "false" && v != "none" {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(parts, " ")
}

var _ = time.Now // reserved for future --since filtering
