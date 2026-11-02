package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/siv19/compute/internal/idgen"
	"github.com/siv19/compute/internal/sched"
	"github.com/siv19/compute/internal/types"
)

// ---- Agent-facing handlers ------------------------------------------------

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.requireJoinToken(w, r) {
		return
	}
	var req types.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad register body: "+err.Error())
		return
	}
	if req.NodeName == "" {
		writeErr(w, http.StatusBadRequest, "node_name required")
		return
	}
	now := time.Now()
	node := nodeFromHeartbeat(req.NodeName, req.Heartbeat, now)
	node.Status = types.NodeOnline
	node.RegisteredAt = now
	node.LastHeartbeat = now
	// Preserve an existing token if the node is re-registering, else mint one.
	if existing, _ := s.st.GetNode(req.NodeName); existing != nil && existing.Token != "" {
		node.Token = existing.Token
		node.RegisteredAt = existing.RegisteredAt
	} else {
		node.Token = idgen.Token()
	}
	if err := s.st.UpsertNode(node); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types.RegisterResponse{
		OK:            true,
		HeartbeatSec:  15,
		ServerVersion: Version,
	})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !s.requireJoinToken(w, r) {
		return
	}
	var hb types.Heartbeat
	if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
		writeErr(w, http.StatusBadRequest, "bad heartbeat: "+err.Error())
		return
	}
	existing, err := s.st.GetNode(hb.NodeName)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	node := nodeFromHeartbeat(hb.NodeName, hb, now)
	node.Status = types.NodeOnline
	node.LastHeartbeat = now
	if existing != nil {
		node.Token = existing.Token
		node.RegisteredAt = existing.RegisteredAt
	} else {
		// Heartbeat before register: accept it, registering implicitly.
		node.Token = idgen.Token()
		node.RegisteredAt = now
	}
	if err := s.st.UpsertNode(node); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleLease is the agent's "do you have work for me?" call. It runs a single
// scheduling pass restricted to the requesting node and, if a queued job fits,
// marks it running and returns it.
func (s *Server) handleLease(w http.ResponseWriter, r *http.Request) {
	if !s.requireJoinToken(w, r) {
		return
	}
	var body struct {
		NodeName string `json:"node_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NodeName == "" {
		writeErr(w, http.StatusBadRequest, "node_name required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	node, err := s.st.GetNode(body.NodeName)
	if err != nil || node == nil || node.Status != types.NodeOnline {
		writeJSON(w, http.StatusOK, types.LeaseResponse{})
		return
	}
	// A node leases one job at a time in v1.
	if node.RunningJobs > 0 {
		writeJSON(w, http.StatusOK, types.LeaseResponse{})
		return
	}

	queued, err := s.st.QueuedJobsFIFO()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, j := range queued {
		// Place against just this node: we want the FIFO-oldest job that this
		// specific node can run.
		dec := sched.Place(j, []*types.Node{node})
		if dec.Node == nil {
			continue
		}
		// Assign.
		now := time.Now()
		j.State = types.JobRunning
		j.AssignedNode = node.Name
		j.Lane = dec.Lane
		j.StartedAt = &now
		j.Reason = dec.Reason
		if err := s.st.UpdateJob(j); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		node.RunningJobs++
		_ = s.st.UpsertNode(node)
		_ = s.st.AppendLog(&types.LogLine{
			JobID: j.ID, Stream: "system",
			Text: fmt.Sprintf("scheduled on %s (%s lane)", node.Name, dec.Lane), At: now,
		})
		writeJSON(w, http.StatusOK, types.LeaseResponse{Job: j, Lane: dec.Lane})
		return
	}
	writeJSON(w, http.StatusOK, types.LeaseResponse{})
}

func (s *Server) handleBundleDownload(w http.ResponseWriter, r *http.Request) {
	if !s.requireJoinToken(w, r) {
		return
	}
	id := r.PathValue("id")
	w.Header().Set("Content-Type", "application/gzip")
	if err := s.bundles.writeBundleTar(id, w); err != nil {
		// Headers may already be sent; log server-side.
		fmt.Printf("bundle download error for %s: %v\n", id, err)
	}
}

func (s *Server) handleLogAppend(w http.ResponseWriter, r *http.Request) {
	if !s.requireJoinToken(w, r) {
		return
	}
	id := r.PathValue("id")
	// Accept either a single line or a newline-delimited batch of JSON lines.
	dec := json.NewDecoder(r.Body)
	for dec.More() {
		var l types.LogLine
		if err := dec.Decode(&l); err != nil {
			writeErr(w, http.StatusBadRequest, "bad log line: "+err.Error())
			return
		}
		l.JobID = id
		if l.At.IsZero() {
			l.At = time.Now()
		}
		if err := s.st.AppendLog(&l); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleArtifactUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireJoinToken(w, r) {
		return
	}
	id := r.PathValue("id")
	n, err := s.bundles.extractArtifactTar(id, r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "artifact extract: "+err.Error())
		return
	}
	if j, _ := s.st.GetJob(id); j != nil {
		j.ArtifactCount = n
		_ = s.st.UpdateJob(j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": n})
}

func (s *Server) handleJobResult(w http.ResponseWriter, r *http.Request) {
	if !s.requireJoinToken(w, r) {
		return
	}
	id := r.PathValue("id")
	var res types.JobResult
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		writeErr(w, http.StatusBadRequest, "bad result: "+err.Error())
		return
	}
	j, err := s.st.GetJob(id)
	if err != nil || j == nil {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	now := time.Now()
	ec := res.ExitCode
	j.ExitCode = &ec
	j.State = res.State
	if j.State == "" {
		if res.ExitCode == 0 {
			j.State = types.JobDone
		} else {
			j.State = types.JobFailed
		}
	}
	if res.Reason != "" {
		j.Reason = res.Reason
	}
	j.FinishedAt = &now
	if err := s.st.UpdateJob(j); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Free the node slot.
	if node, _ := s.st.GetNode(j.AssignedNode); node != nil && node.RunningJobs > 0 {
		node.RunningJobs--
		_ = s.st.UpsertNode(node)
	}
	_ = s.st.AppendLog(&types.LogLine{
		JobID: id, Stream: "system",
		Text: fmt.Sprintf("job %s with exit code %d", j.State, res.ExitCode), At: now,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- Dashboard/CLI-facing handlers ---------------------------------------

func (s *Server) handleAggregate(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	nodes, err := s.st.ListNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jobs, err := s.st.ListJobs()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	agg := aggregate(nodes, jobs)
	writeJSON(w, http.StatusOK, agg)
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	jobs, err := s.st.ListJobs()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	u := types.Usage{
		ByState:   map[string]int{},
		ByRuntime: map[string]int{},
		ByNode:    map[string]int{},
	}
	for _, j := range jobs {
		u.TotalJobs++
		u.ByState[string(j.State)]++
		u.ByRuntime[string(j.Runtime)]++
		if j.AssignedNode != "" {
			u.ByNode[j.AssignedNode]++
		}
		if j.Lane == types.LaneSlow {
			u.SlowLaneJobs++
		}
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	nodes, err := s.st.ListNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if nodes == nil {
		nodes = []*types.Node{}
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := s.st.DeleteNode(name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	jobs, err := s.st.ListJobs()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []*types.Job{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	j, err := s.st.GetJob(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if j == nil {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	id := r.PathValue("id")
	files, err := s.bundles.listArtifacts(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if files == nil {
		files = []string{}
	}
	writeJSON(w, http.StatusOK, files)
}

func (s *Server) handleArtifactDownload(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	id := r.PathValue("id")
	name := r.PathValue("name")
	f, err := s.bundles.openArtifact(id, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "artifact not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, name, time.Now(), f)
}

// ---- helpers --------------------------------------------------------------

func nodeFromHeartbeat(name string, hb types.Heartbeat, now time.Time) *types.Node {
	if hb.Labels == nil {
		hb.Labels = map[string]string{}
	}
	return &types.Node{
		Name:            name,
		CPUCount:        hb.CPUCount,
		LoadAvg1:        hb.LoadAvg1,
		GPUs:            hb.GPUs,
		RAMTotalMB:      hb.RAMTotalMB,
		RAMFreeMB:       hb.RAMFreeMB,
		DiskTotalMB:     hb.DiskTotalMB,
		DiskFreeMB:      hb.DiskFreeMB,
		SwapTotalMB:     hb.SwapTotalMB,
		SwapFreeMB:      hb.SwapFreeMB,
		Labels:          hb.Labels,
		RunningJobs:     hb.RunningJobs,
		DockerAvailable: hb.DockerAvailable,
		Toolchains:      hb.Toolchains,
		AgentVersion:    hb.AgentVersion,
		LastHeartbeat:   now,
	}
}

// aggregate computes the dashboard header totals across ONLINE nodes only (§3).
func aggregate(nodes []*types.Node, jobs []*types.Job) types.Aggregate {
	var a types.Aggregate
	for _, n := range nodes {
		if n.Status != types.NodeOnline {
			a.OfflineNodes++
			continue
		}
		a.OnlineNodes++
		a.VCPUs += n.CPUCount
		// A job takes a whole machine in v1, so the cores that can actually
		// accept work right now are those on idle machines.
		if n.RunningJobs == 0 {
			a.VCPUsIdle += n.CPUCount
		}
		a.RAMTotalMB += n.RAMTotalMB
		a.RAMFreeMB += n.RAMFreeMB
		a.SwapTotalMB += n.SwapTotalMB
		a.DiskTotalMB += n.DiskTotalMB
		a.DiskFreeMB += n.DiskFreeMB
		a.LoadAvg1 += n.LoadAvg1
		a.RunningJobs += n.RunningJobs
		for _, g := range n.GPUs {
			a.GPUCount++
			a.VRAMTotalMB += g.VRAMMB
			switch g.Type {
			case types.GPUCUDA:
				a.CUDAVRAMMB += g.VRAMMB
			case types.GPUMetal:
				a.MetalVRAMMB += g.VRAMMB
			}
		}
	}
	for _, j := range jobs {
		if j.State == types.JobQueued {
			a.QueuedJobs++
		}
	}
	return a
}
