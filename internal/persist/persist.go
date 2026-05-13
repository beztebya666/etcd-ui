// Package persist saves UI-added clusters to disk so they survive restarts.
// Stored at $ETCD_UI_DATA_DIR/clusters.json with mode 0600 (passwords inside).
package persist

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/yourorg/etcd-ui/internal/config"
)

type Store struct {
	mu   sync.Mutex
	path string
}

func New(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("data dir is required")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dataDir, "clusters.json")}, nil
}

// storedCluster mirrors config.Cluster but keeps the password (config.Cluster
// hides it with json:"-" so it can't leak via API responses).
type storedCluster struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Endpoints []string `json:"endpoints"`
	Username  string   `json:"username,omitempty"`
	Password  string   `json:"password,omitempty"`
	CAFile    string   `json:"caFile,omitempty"`
	CertFile  string   `json:"certFile,omitempty"`
	KeyFile   string   `json:"keyFile,omitempty"`
	Source    string   `json:"source"`
}

func (s *Store) Load() ([]config.Cluster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var raw []storedCluster
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]config.Cluster, len(raw))
	for i, c := range raw {
		out[i] = config.Cluster{
			ID: c.ID, Name: c.Name, Endpoints: c.Endpoints,
			Username: c.Username, Password: c.Password,
			CAFile: c.CAFile, CertFile: c.CertFile, KeyFile: c.KeyFile,
			Source: c.Source,
		}
	}
	return out, nil
}

func (s *Store) Save(clusters []config.Cluster) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw := make([]storedCluster, len(clusters))
	for i, c := range clusters {
		raw[i] = storedCluster{
			ID: c.ID, Name: c.Name, Endpoints: c.Endpoints,
			Username: c.Username, Password: c.Password,
			CAFile: c.CAFile, CertFile: c.CertFile, KeyFile: c.KeyFile,
			Source: c.Source,
		}
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
