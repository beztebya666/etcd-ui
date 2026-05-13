// Package audit is the in-process implementation used by the audit microservice.
// It keeps a fixed-size ring buffer for fast tail reads and appends a JSONL
// file on disk for persistence + grep-friendly tooling.
package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Event struct {
	ID        int64     `json:"id"`
	Time      time.Time `json:"time"`
	Actor     string    `json:"actor"`
	Source    string    `json:"source"` // "gateway", "api", etc.
	Method    string    `json:"method"`
	Cluster   string    `json:"cluster,omitempty"`
	Path      string    `json:"path"`
	Action    string    `json:"action"` // put / delete / bulk / txn / restore / rbac
	Key       string    `json:"key,omitempty"`
	Status    int       `json:"status"`
	IP        string    `json:"ip,omitempty"`
	UserAgent string    `json:"userAgent,omitempty"`
	Note      string    `json:"note,omitempty"`
}

type Store struct {
	mu      sync.RWMutex
	ring    []Event
	head    int // next write index
	full    bool
	cap     int
	nextID  int64
	logFile *os.File
	bw      *bufio.Writer
	enc     *json.Encoder
	dir     string

	// Retention: a periodic compactor truncates audit.jsonl when it grows past
	// maxBytes and rewrites it from the in-memory ring. Events older than maxAge
	// are dropped from the ring as well. Both default to sane values
	// (256MiB / 30 days) and can be overridden via env in the audit service.
	maxBytes int64
	maxAge   time.Duration
}

func New(dataDir string, capacity int) (*Store, error) {
	if capacity <= 0 {
		capacity = 5000
	}
	s := &Store{
		ring:     make([]Event, capacity),
		cap:      capacity,
		dir:      dataDir,
		maxBytes: 256 << 20, // 256 MiB
		maxAge:   30 * 24 * time.Hour,
	}
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(filepath.Join(dataDir, "audit.jsonl"),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		s.logFile = f
		s.bw = bufio.NewWriter(f)
		s.enc = json.NewEncoder(s.bw)
		// Best-effort load of the last `capacity` lines from disk.
		_ = s.tailFromDisk(filepath.Join(dataDir, "audit.jsonl"), capacity)
	}
	return s, nil
}

// SetRetention overrides the default retention window. Pass 0 to keep the
// current default for that dimension.
func (s *Store) SetRetention(maxBytes int64, maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxBytes > 0 {
		s.maxBytes = maxBytes
	}
	if maxAge > 0 {
		s.maxAge = maxAge
	}
}

// LogPath returns the JSONL file path, or "" if storage is disabled. Used by
// the /events/download HTTP endpoint to stream the file directly.
func (s *Store) LogPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "audit.jsonl")
}

// Compact enforces the retention policy:
//   - if audit.jsonl size > maxBytes, the file is truncated and rewritten from
//     the in-memory ring (newest events kept first);
//   - if oldest events in the ring exceed maxAge, they are dropped from the
//     ring as well (so subsequent compaction also drops them from disk).
//
// Returns (bytesBefore, bytesAfter, droppedFromRing, err) for observability.
func (s *Store) Compact() (int64, int64, int, error) {
	path := s.LogPath()
	if path == "" {
		return 0, 0, 0, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, 0, 0, nil
		}
		return 0, 0, 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. age-based eviction inside the ring
	dropped := 0
	cutoff := time.Now().UTC().Add(-s.maxAge)
	size := s.cap
	if !s.full {
		size = s.head
	}
	keep := make([]Event, 0, size)
	for i := 0; i < size; i++ {
		idx := (s.head - 1 - i + s.cap) % s.cap // newest-first
		ev := s.ring[idx]
		if ev.Time.Before(cutoff) {
			dropped++
			continue
		}
		keep = append(keep, ev)
	}

	// rebuild ring in chronological order (oldest first)
	for i := range s.ring {
		s.ring[i] = Event{}
	}
	for i := len(keep) - 1; i >= 0; i-- {
		s.ring[(len(keep)-1-i)] = keep[i]
	}
	s.head = len(keep) % s.cap
	s.full = len(keep) >= s.cap

	bytesBefore := st.Size()
	if bytesBefore <= s.maxBytes && dropped == 0 {
		return bytesBefore, bytesBefore, 0, nil
	}

	// 2. rewrite file from the ring. Close active writer, truncate, re-open.
	if s.bw != nil {
		_ = s.bw.Flush()
	}
	if s.logFile != nil {
		_ = s.logFile.Close()
	}
	tmp := path + ".compact"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return bytesBefore, bytesBefore, dropped, err
	}
	bw := bufio.NewWriter(f)
	enc := json.NewEncoder(bw)
	for i := len(keep) - 1; i >= 0; i-- { // chronological
		_ = enc.Encode(keep[i])
	}
	_ = bw.Flush()
	_ = f.Sync()
	_ = f.Close()
	if err := os.Rename(tmp, path); err != nil {
		return bytesBefore, bytesBefore, dropped, err
	}
	st2, _ := os.Stat(path)
	f2, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return bytesBefore, bytesBefore, dropped, err
	}
	s.logFile = f2
	s.bw = bufio.NewWriter(f2)
	s.enc = json.NewEncoder(s.bw)
	var after int64
	if st2 != nil {
		after = st2.Size()
	}
	return bytesBefore, after, dropped, nil
}

func (s *Store) Close() error {
	if s.bw != nil {
		_ = s.bw.Flush()
	}
	if s.logFile != nil {
		return s.logFile.Close()
	}
	return nil
}

// Append records a new event. ID is assigned and returned.
func (s *Store) Append(e Event) Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	e.ID = s.nextID
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	s.ring[s.head] = e
	s.head = (s.head + 1) % s.cap
	if s.head == 0 {
		s.full = true
	}
	if s.enc != nil {
		_ = s.enc.Encode(e)
		if s.bw != nil {
			_ = s.bw.Flush()
		}
		if s.logFile != nil {
			_ = s.logFile.Sync()
		}
	}
	return e
}

// Tail returns up to n most-recent events, newest first.
func (s *Store) Tail(n int) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	size := s.cap
	if !s.full {
		size = s.head
	}
	if n <= 0 || n > size {
		n = size
	}
	out := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		idx := (s.head - 1 - i + s.cap) % s.cap
		out = append(out, s.ring[idx])
	}
	return out
}

// Since returns events with ID strictly greater than id.
func (s *Store) Since(id int64, limit int) []Event {
	all := s.Tail(0)
	out := make([]Event, 0, len(all))
	for _, e := range all {
		if e.ID > id {
			out = append(out, e)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Store) tailFromDisk(path string, n int) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	// naive: read all, keep last n
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64*1024), 1<<20)
	tail := make([]string, 0, n)
	for scan.Scan() {
		tail = append(tail, scan.Text())
		if len(tail) > n {
			tail = tail[1:]
		}
	}
	for _, line := range tail {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.ID > s.nextID {
			s.nextID = e.ID
		}
		s.ring[s.head] = e
		s.head = (s.head + 1) % s.cap
		if s.head == 0 {
			s.full = true
		}
	}
	return nil
}
