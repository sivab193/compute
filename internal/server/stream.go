package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/siv19/compute/internal/types"
)

// handleLogStream streams a job's logs to the dashboard via Server-Sent Events.
// It replays everything stored so far, then polls for new lines until the job
// reaches a terminal state and all logs are drained, or the client disconnects.
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	id := r.PathValue("id")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	var cursor int64
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	emit := func() bool {
		lines, last, err := s.st.LogsSince(id, cursor)
		if err != nil {
			return true // keep trying
		}
		cursor = last
		for _, l := range lines {
			b, _ := json.Marshal(l)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		if len(lines) > 0 {
			flusher.Flush()
		}
		return true
	}

	// Initial replay.
	emit()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emit()
			// Stop once the job is terminal and no more logs are arriving.
			j, _ := s.st.GetJob(id)
			if j != nil && (j.State == types.JobDone || j.State == types.JobFailed) {
				// One final drain pass to be safe.
				emit()
				fmt.Fprintf(w, "event: done\ndata: %s\n\n", j.State)
				flusher.Flush()
				return
			}
		}
	}
}
