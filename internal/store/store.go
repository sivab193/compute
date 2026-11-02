// Package store is the control plane's persistence layer: a SQLite database
// holding the node registry, the job queue, and job logs. It uses the pure-Go
// modernc.org/sqlite driver so the server itself is still a single static
// binary with no CGO.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/siv19/compute/internal/types"
)

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite writers serialize; one conn avoids "database is locked"
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS nodes (
	name             TEXT PRIMARY KEY,
	token            TEXT NOT NULL,
	status           TEXT NOT NULL,
	cpu_count        INTEGER NOT NULL DEFAULT 0,
	load_avg_1       REAL NOT NULL DEFAULT 0,
	gpus_json        TEXT NOT NULL DEFAULT '[]',
	ram_total_mb     INTEGER NOT NULL DEFAULT 0,
	ram_free_mb      INTEGER NOT NULL DEFAULT 0,
	disk_total_mb    INTEGER NOT NULL DEFAULT 0,
	disk_free_mb     INTEGER NOT NULL DEFAULT 0,
	swap_total_mb    INTEGER NOT NULL DEFAULT 0,
	swap_free_mb     INTEGER NOT NULL DEFAULT 0,
	labels_json      TEXT NOT NULL DEFAULT '{}',
	running_jobs     INTEGER NOT NULL DEFAULT 0,
	docker_available INTEGER NOT NULL DEFAULT 0,
	agent_version    TEXT NOT NULL DEFAULT '',
	last_heartbeat   TEXT NOT NULL DEFAULT '',
	registered_at    TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS jobs (
	id             TEXT PRIMARY KEY,
	name           TEXT NOT NULL,
	runtime        TEXT NOT NULL,
	entry_file     TEXT NOT NULL,
	state          TEXT NOT NULL,
	needs_gpu      TEXT NOT NULL DEFAULT '',
	min_ram_mb     INTEGER NOT NULL DEFAULT 0,
	needs_arch     TEXT NOT NULL DEFAULT '',
	needs_os       TEXT NOT NULL DEFAULT '',
	allow_preempt  INTEGER NOT NULL DEFAULT 0,
	disk_backed    INTEGER NOT NULL DEFAULT 0,
	timeout_sec    INTEGER NOT NULL DEFAULT 0,
	assigned_node  TEXT NOT NULL DEFAULT '',
	lane           TEXT NOT NULL DEFAULT '',
	exit_code      INTEGER,
	reason         TEXT NOT NULL DEFAULT '',
	submitted_at   TEXT NOT NULL,
	started_at     TEXT,
	finished_at    TEXT,
	artifact_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS logs (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id  TEXT NOT NULL,
	stream  TEXT NOT NULL,
	text    TEXT NOT NULL,
	at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_logs_job ON logs(job_id, id);
CREATE INDEX IF NOT EXISTS idx_jobs_state ON jobs(state, submitted_at);
`
	_, err := s.db.Exec(schema)
	return err
}

// ---- Node operations ------------------------------------------------------

// UpsertNode inserts or updates a node from a register/heartbeat.
func (s *Store) UpsertNode(n *types.Node) error {
	gpus, _ := json.Marshal(n.GPUs)
	labels, _ := json.Marshal(n.Labels)
	_, err := s.db.Exec(`
INSERT INTO nodes (name, token, status, cpu_count, load_avg_1, gpus_json,
	ram_total_mb, ram_free_mb, disk_total_mb, disk_free_mb, swap_total_mb,
	swap_free_mb, labels_json, running_jobs, docker_available, agent_version,
	last_heartbeat, registered_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET
	status=excluded.status, cpu_count=excluded.cpu_count,
	load_avg_1=excluded.load_avg_1, gpus_json=excluded.gpus_json,
	ram_total_mb=excluded.ram_total_mb, ram_free_mb=excluded.ram_free_mb,
	disk_total_mb=excluded.disk_total_mb, disk_free_mb=excluded.disk_free_mb,
	swap_total_mb=excluded.swap_total_mb, swap_free_mb=excluded.swap_free_mb,
	labels_json=excluded.labels_json, running_jobs=excluded.running_jobs,
	docker_available=excluded.docker_available, agent_version=excluded.agent_version,
	last_heartbeat=excluded.last_heartbeat`,
		n.Name, n.Token, n.Status, n.CPUCount, n.LoadAvg1, string(gpus),
		n.RAMTotalMB, n.RAMFreeMB, n.DiskTotalMB, n.DiskFreeMB, n.SwapTotalMB,
		n.SwapFreeMB, string(labels), n.RunningJobs, boolToInt(n.DockerAvailable),
		n.AgentVersion, n.LastHeartbeat.Format(time.RFC3339Nano),
		n.RegisteredAt.Format(time.RFC3339Nano))
	return err
}

// GetNode returns a node by name, or (nil, nil) if absent.
func (s *Store) GetNode(name string) (*types.Node, error) {
	row := s.db.QueryRow(`SELECT name, token, status, cpu_count, load_avg_1,
		gpus_json, ram_total_mb, ram_free_mb, disk_total_mb, disk_free_mb,
		swap_total_mb, swap_free_mb, labels_json, running_jobs, docker_available,
		agent_version, last_heartbeat, registered_at FROM nodes WHERE name=?`, name)
	return scanNode(row)
}

// ListNodes returns all nodes ordered by name.
func (s *Store) ListNodes() ([]*types.Node, error) {
	rows, err := s.db.Query(`SELECT name, token, status, cpu_count, load_avg_1,
		gpus_json, ram_total_mb, ram_free_mb, disk_total_mb, disk_free_mb,
		swap_total_mb, swap_free_mb, labels_json, running_jobs, docker_available,
		agent_version, last_heartbeat, registered_at FROM nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*types.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SetNodeStatus flips a node's status (used by the offline sweeper).
func (s *Store) SetNodeStatus(name string, status types.NodeStatus) error {
	_, err := s.db.Exec(`UPDATE nodes SET status=? WHERE name=?`, status, name)
	return err
}

// DeleteNode removes a node from the registry.
func (s *Store) DeleteNode(name string) error {
	_, err := s.db.Exec(`DELETE FROM nodes WHERE name=?`, name)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanNode(sc scanner) (*types.Node, error) {
	var n types.Node
	var gpusJSON, labelsJSON, lastHB, regAt string
	var dockerAvail int
	err := sc.Scan(&n.Name, &n.Token, &n.Status, &n.CPUCount, &n.LoadAvg1,
		&gpusJSON, &n.RAMTotalMB, &n.RAMFreeMB, &n.DiskTotalMB, &n.DiskFreeMB,
		&n.SwapTotalMB, &n.SwapFreeMB, &labelsJSON, &n.RunningJobs, &dockerAvail,
		&n.AgentVersion, &lastHB, &regAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(gpusJSON), &n.GPUs)
	_ = json.Unmarshal([]byte(labelsJSON), &n.Labels)
	n.DockerAvailable = dockerAvail != 0
	n.LastHeartbeat, _ = time.Parse(time.RFC3339Nano, lastHB)
	n.RegisteredAt, _ = time.Parse(time.RFC3339Nano, regAt)
	return &n, nil
}

// ---- Job operations -------------------------------------------------------

// InsertJob persists a newly submitted job.
func (s *Store) InsertJob(j *types.Job) error {
	_, err := s.db.Exec(`
INSERT INTO jobs (id, name, runtime, entry_file, state, needs_gpu, min_ram_mb,
	needs_arch, needs_os, allow_preempt, disk_backed, timeout_sec, assigned_node,
	lane, exit_code, reason, submitted_at, started_at, finished_at, artifact_count)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Name, j.Runtime, j.EntryFile, j.State, j.NeedsGPU, j.MinRAMMB,
		j.NeedsArch, j.NeedsOS, boolToInt(j.AllowPreempt), boolToInt(j.DiskBacked),
		j.TimeoutSec, j.AssignedNode, j.Lane, j.ExitCode, j.Reason,
		fmtTime(&j.SubmittedAt), fmtTime(j.StartedAt), fmtTime(j.FinishedAt),
		j.ArtifactCount)
	return err
}

// UpdateJob rewrites the mutable fields of a job.
func (s *Store) UpdateJob(j *types.Job) error {
	_, err := s.db.Exec(`UPDATE jobs SET state=?, assigned_node=?, lane=?,
		exit_code=?, reason=?, started_at=?, finished_at=?, artifact_count=?
		WHERE id=?`,
		j.State, j.AssignedNode, j.Lane, j.ExitCode, j.Reason,
		fmtTime(j.StartedAt), fmtTime(j.FinishedAt), j.ArtifactCount, j.ID)
	return err
}

// GetJob returns a job by id, or (nil, nil) if absent.
func (s *Store) GetJob(id string) (*types.Job, error) {
	row := s.db.QueryRow(jobSelect+` WHERE id=?`, id)
	return scanJob(row)
}

// ListJobs returns all jobs, newest first.
func (s *Store) ListJobs() ([]*types.Job, error) {
	rows, err := s.db.Query(jobSelect + ` ORDER BY submitted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*types.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// QueuedJobsFIFO returns queued jobs oldest-first (FIFO scheduling order).
func (s *Store) QueuedJobsFIFO() ([]*types.Job, error) {
	rows, err := s.db.Query(jobSelect + ` WHERE state='queued' ORDER BY submitted_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*types.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

const jobSelect = `SELECT id, name, runtime, entry_file, state, needs_gpu,
	min_ram_mb, needs_arch, needs_os, allow_preempt, disk_backed, timeout_sec,
	assigned_node, lane, exit_code, reason, submitted_at, started_at,
	finished_at, artifact_count FROM jobs`

func scanJob(sc scanner) (*types.Job, error) {
	var j types.Job
	var allowPreempt, diskBacked int
	var submittedAt string
	var startedAt, finishedAt sql.NullString
	var exitCode sql.NullInt64
	err := sc.Scan(&j.ID, &j.Name, &j.Runtime, &j.EntryFile, &j.State,
		&j.NeedsGPU, &j.MinRAMMB, &j.NeedsArch, &j.NeedsOS, &allowPreempt,
		&diskBacked, &j.TimeoutSec, &j.AssignedNode, &j.Lane, &exitCode,
		&j.Reason, &submittedAt, &startedAt, &finishedAt, &j.ArtifactCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.AllowPreempt = allowPreempt != 0
	j.DiskBacked = diskBacked != 0
	if exitCode.Valid {
		ec := int(exitCode.Int64)
		j.ExitCode = &ec
	}
	j.SubmittedAt, _ = time.Parse(time.RFC3339Nano, submittedAt)
	j.StartedAt = parseNullTime(startedAt)
	j.FinishedAt = parseNullTime(finishedAt)
	return &j, nil
}

// ---- Log operations -------------------------------------------------------

// AppendLog stores one log line.
func (s *Store) AppendLog(l *types.LogLine) error {
	_, err := s.db.Exec(`INSERT INTO logs (job_id, stream, text, at) VALUES (?,?,?,?)`,
		l.JobID, l.Stream, l.Text, l.At.Format(time.RFC3339Nano))
	return err
}

// LogsSince returns log lines for a job with id greater than afterID, plus the
// highest id returned (for cursor-based SSE streaming).
func (s *Store) LogsSince(jobID string, afterID int64) ([]types.LogLine, int64, error) {
	rows, err := s.db.Query(`SELECT id, job_id, stream, text, at FROM logs
		WHERE job_id=? AND id>? ORDER BY id ASC`, jobID, afterID)
	if err != nil {
		return nil, afterID, err
	}
	defer rows.Close()
	var out []types.LogLine
	last := afterID
	for rows.Next() {
		var id int64
		var l types.LogLine
		var at string
		if err := rows.Scan(&id, &l.JobID, &l.Stream, &l.Text, &at); err != nil {
			return nil, last, err
		}
		l.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, l)
		last = id
	}
	return out, last, rows.Err()
}

// CountJobArtifacts is a helper kept here for symmetry; the number itself is
// tracked on the job row and set by the agent via result reporting.
func (s *Store) CountJobArtifacts(jobID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT artifact_count FROM jobs WHERE id=?`, jobID).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// ---- helpers --------------------------------------------------------------

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func fmtTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

func parseNullTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

// Stat is a tiny convenience for sanity checks / tests.
func (s *Store) Stat() (nodes, jobs int, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&nodes); err != nil {
		return
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil {
		return
	}
	return
}

var _ = fmt.Sprintf // keep fmt imported for future diagnostics
