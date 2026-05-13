package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ACL edit history.  Every Save() snapshot lands in
// $ETCD_UI_DATA_DIR/acl-history/<timestamp>-<actor>.json so we keep a
// per-edit full record even when the audit-log Note is truncated to the
// 4 KB header budget. Files are immutable + retention-bounded.

type historyEntry struct {
	Path  string
	When  time.Time
	Actor string
}

// HistorySnapshot is one rotated ACL file. Returned by ListHistory().
type HistorySnapshot struct {
	When  time.Time `json:"when"`
	Actor string    `json:"actor"`
	File  string    `json:"file"` // server-side path; not returned to clients
	Rules []ACLRule `json:"rules"`
}

// HistoryDir is the on-disk folder. Empty disables history.
func (a *ACL) HistoryDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.historyDir
}

// SetHistoryDir wires the history folder. Idempotent; called once at
// boot from the gateway. Passing "" disables history.
func (a *ACL) SetHistoryDir(dir string) {
	a.mu.Lock()
	a.historyDir = dir
	a.mu.Unlock()
	if dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
}

// SnapshotHistory writes the current rule set as a history entry. Called
// by the gateway around Save() so we capture both before- and after-images
// on every edit (two files per edit). Filename format:
// <unix-nano>-<actor>-<phase>.json where phase ∈ {before, after}.
// Returns the base filename (without dir) so callers can link to it in
// the audit log. The history GC keeps the newest N (default 200).
func (a *ACL) SnapshotHistory(actor, phase string, rules []ACLRule) (string, error) {
	a.mu.RLock()
	dir := a.historyDir
	a.mu.RUnlock()
	if dir == "" {
		return "", nil
	}
	if rules == nil {
		rules = a.Rules()
	}
	safeActor := sanitiseActor(actor)
	name := fmt.Sprintf("%d-%s-%s.json", time.Now().UnixNano(), safeActor, phase)
	path := filepath.Join(dir, name)
	body, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	a.pruneHistory(dir, 200)
	return name, nil
}

// ListHistory returns the N most-recent snapshots, newest first.
func (a *ACL) ListHistory(limit int) ([]HistorySnapshot, error) {
	a.mu.RLock()
	dir := a.historyDir
	a.mu.RUnlock()
	if dir == "" {
		return nil, errors.New("ACL history disabled")
	}
	entries, err := readHistoryEntries(dir)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]HistorySnapshot, 0, len(entries))
	for _, e := range entries {
		body, err := os.ReadFile(e.Path)
		if err != nil {
			continue
		}
		var rules []ACLRule
		if err := json.Unmarshal(body, &rules); err != nil {
			continue
		}
		out = append(out, HistorySnapshot{
			When: e.When, Actor: e.Actor, File: e.Path, Rules: rules,
		})
	}
	return out, nil
}

// Restore swaps the live rule set back to a historical snapshot. Used by
// the "undo last edit" path in the Permissions editor.
func (a *ACL) Restore(file string) error {
	a.mu.RLock()
	dir := a.historyDir
	a.mu.RUnlock()
	if dir == "" {
		return errors.New("ACL history disabled")
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(abs, dirAbs+string(filepath.Separator)) {
		return errors.New("history path outside ACL history dir — refusing")
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	var rules []ACLRule
	if err := json.Unmarshal(body, &rules); err != nil {
		return err
	}
	return a.Save(rules)
}

func (a *ACL) pruneHistory(dir string, keep int) {
	entries, err := readHistoryEntries(dir)
	if err != nil {
		return
	}
	// readHistoryEntries returns newest-first; drop anything past the
	// keep window.
	for i := keep; i < len(entries); i++ {
		_ = os.Remove(entries[i].Path)
	}
}

func readHistoryEntries(dir string) ([]historyEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []historyEntry
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ts, actor := parseHistoryName(e.Name())
		out = append(out, historyEntry{
			Path: filepath.Join(dir, e.Name()), When: ts, Actor: actor,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out, nil
}

// parseHistoryName decomposes `<unix-nano>-<actor>-<phase>.json` back into
// its parts. Robust to underscores in actor names and trailing extension.
func parseHistoryName(name string) (time.Time, string) {
	base := strings.TrimSuffix(name, ".json")
	parts := strings.SplitN(base, "-", 2)
	if len(parts) < 2 {
		return time.Time{}, ""
	}
	nsStr := parts[0]
	rest := parts[1]
	// Trim trailing -before / -after if present so the actor remains clean.
	for _, suf := range []string{"-before", "-after"} {
		if strings.HasSuffix(rest, suf) {
			rest = strings.TrimSuffix(rest, suf)
			break
		}
	}
	var ns int64
	for _, c := range nsStr {
		if c < '0' || c > '9' {
			return time.Time{}, ""
		}
		ns = ns*10 + int64(c-'0')
	}
	return time.Unix(0, ns).UTC(), rest
}

// sanitiseActor strips characters that would break path safety. Email
// addresses become alice_at_corp; service principals stay readable.
func sanitiseActor(s string) string {
	if s == "" {
		return "anonymous"
	}
	s = strings.ReplaceAll(s, "@", "_at_")
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '.' || c == '_' || c == '-' || c == ':':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}