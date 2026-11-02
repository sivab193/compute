// Command pool-agent is the node daemon. One static binary per machine; it
// registers with the control plane, heartbeats capacity, and runs leased jobs.
//
// Cross-compile for the fleet, e.g.:
//
//	GOOS=linux  GOARCH=amd64 go build -o pool-agent-linux-amd64  ./cmd/pool-agent
//	GOOS=linux  GOARCH=arm64 go build -o pool-agent-linux-arm64  ./cmd/pool-agent
//	GOOS=darwin GOARCH=arm64 go build -o pool-agent-darwin-arm64 ./cmd/pool-agent
//	GOOS=windows GOARCH=amd64 go build -o pool-agent.exe         ./cmd/pool-agent
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/siv19/compute/internal/agent"
)

func main() {
	var (
		serverURL = flag.String("server", os.Getenv("POOL_SERVER"), "control plane base URL (e.g. https://mini.ts.net)")
		nodeName  = flag.String("name", envOr("POOL_NODE_NAME", defaultName()), "unique node name")
		joinTok   = flag.String("join-token", os.Getenv("POOL_JOIN_TOKEN"), "shared join token")
		labelsCSV = flag.String("labels", os.Getenv("POOL_LABELS"), "extra labels, comma-separated k=v (e.g. preemptible=true,location=home)")
		workRoot  = flag.String("work", envOr("POOL_WORK", ""), "job workdir root (default: temp dir)")
		forceBare = flag.Bool("bare", runtime.GOOS == "darwin", "never use Docker, run jobs as host processes (default true on macOS)")
		memFrac   = flag.Float64("mem-cap-frac", 0, "per-job memory cap as a fraction of node RAM (0 = none; e.g. 0.8)")
	)
	flag.Parse()

	if *serverURL == "" {
		log.Fatal("--server is required (control plane base URL)")
	}

	a := agent.New(agent.Config{
		ServerURL:  strings.TrimRight(*serverURL, "/"),
		NodeName:   *nodeName,
		JoinToken:  *joinTok,
		Labels:     parseLabels(*labelsCSV),
		WorkRoot:   *workRoot,
		ForceBare:  *forceBare,
		MemCapFrac: *memFrac,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("pool-agent %s starting as %q -> %s", agent.Version, *nodeName, *serverURL)
	if err := a.Run(ctx); err != nil {
		log.Fatalf("agent: %v", err)
	}
	log.Printf("agent stopped")
}

func parseLabels(csv string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(csv, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return out
}

func defaultName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "node-" + runtime.GOOS
	}
	return h
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
