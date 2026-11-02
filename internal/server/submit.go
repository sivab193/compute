package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/siv19/compute/internal/idgen"
	"github.com/siv19/compute/internal/types"
)

// handleSubmitJob accepts a multipart form:
//
//	files        one or more source files (the entry file is inferred, or named
//	             by the "entry" field)
//	entry        optional: which uploaded file is the entry point
//	needs_gpu    optional: "cuda" | "metal"
//	min_ram_mb   optional: integer
//	needs_arch   optional: "amd64" | "arm64"
//	needs_os     optional: "linux" | "darwin" | "windows"
//	allow_preempt optional: "true"
//	disk_backed  optional: "true"  (§6.4 sparse mmap weights)
//	timeout_sec  optional: integer
//	name         optional: human label
//
// This is the single API both the dashboard and the CLI post to.
func (s *Server) handleSubmitJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireDashToken(w, r) {
		return
	}
	// 512 MB cap on in-flight upload parsing; large data belongs on the NAS.
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "parse multipart: "+err.Error())
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeErr(w, http.StatusBadRequest, "at least one file is required under 'files'")
		return
	}

	// Determine entry file + runtime.
	entry := r.FormValue("entry")
	var runtime types.RuntimeKind
	var entryName string
	found := false
	for _, fh := range files {
		base := fh.Filename
		if entry != "" {
			if base == entry {
				if rt, ok := RuntimeFromFilename(base); ok {
					runtime, entryName, found = rt, base, true
				}
			}
			continue
		}
		// No explicit entry: pick the first recognizable source file.
		if rt, ok := RuntimeFromFilename(base); ok {
			runtime, entryName, found = rt, base, true
			break
		}
	}
	if !found {
		writeErr(w, http.StatusBadRequest,
			"no runnable entry file found (need a .py, .ipynb, or .java); specify 'entry' if ambiguous")
		return
	}

	job := &types.Job{
		ID:           idgen.JobID(),
		Name:         firstNonEmpty(r.FormValue("name"), entryName),
		Runtime:      runtime,
		EntryFile:    entryName,
		State:        types.JobQueued,
		NeedsGPU:     parseGPU(r.FormValue("needs_gpu")),
		MinRAMMB:     parseUint(r.FormValue("min_ram_mb")),
		NeedsArch:    strings.TrimSpace(r.FormValue("needs_arch")),
		NeedsOS:      strings.TrimSpace(r.FormValue("needs_os")),
		AllowPreempt: r.FormValue("allow_preempt") == "true",
		DiskBacked:   r.FormValue("disk_backed") == "true",
		TimeoutSec:   s.defaultTimeout(int(parseUint(r.FormValue("timeout_sec")))),
		SubmittedAt:  time.Now(),
	}

	// Persist the job row, then save every uploaded file into its bundle.
	if err := s.st.InsertJob(job); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "open upload: "+err.Error())
			return
		}
		err = s.bundles.saveBundleFile(job.ID, fh.Filename, f)
		f.Close()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "save bundle: "+err.Error())
			return
		}
	}
	_ = s.st.AppendLog(&types.LogLine{
		JobID: job.ID, Stream: "system",
		Text: "job submitted, queued", At: time.Now(),
	})
	writeJSON(w, http.StatusCreated, job)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func parseGPU(s string) types.GPUType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cuda":
		return types.GPUCUDA
	case "metal":
		return types.GPUMetal
	default:
		return ""
	}
}

func parseUint(s string) uint64 {
	v, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return v
}
