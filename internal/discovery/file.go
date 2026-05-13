package discovery

import (
	"context"
	"errors"
	"os"

	"github.com/yourorg/etcd-ui/internal/config"
	"gopkg.in/yaml.v3"
)

// FileSource reads a YAML file listing N clusters. Set CLUSTERS_FILE to its
// path. Hot-reloaded every 30s by the discovery Manager loop. Schema:
//
//	clusters:
//	  - id: vitess-prod
//	    name: Vitess (prod TopoServer)
//	    endpoints: ["http://vt-1:2379","http://vt-2:2379"]
//	  - id: vault-ha
//	    name: Vault HA storage
//	    endpoints: ["https://vault-etcd:2379"]
//	    caFile: /certs/vault/ca.crt
//	    certFile: /certs/vault/client.crt
//	    keyFile: /certs/vault/client.key
type FileSource struct {
	Path string
}

func NewFileSource() *FileSource {
	return &FileSource{Path: os.Getenv("CLUSTERS_FILE")}
}

func (f *FileSource) Name() string { return "file" }

type fileShape struct {
	Clusters []struct {
		ID        string   `yaml:"id"`
		Name      string   `yaml:"name"`
		Endpoints []string `yaml:"endpoints"`
		Username  string   `yaml:"username,omitempty"`
		Password  string   `yaml:"password,omitempty"`
		CAFile    string   `yaml:"caFile,omitempty"`
		CertFile  string   `yaml:"certFile,omitempty"`
		KeyFile   string   `yaml:"keyFile,omitempty"`
	} `yaml:"clusters"`
}

func (f *FileSource) Discover(_ context.Context) ([]config.Cluster, error) {
	if f.Path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(f.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var shape fileShape
	if err := yaml.Unmarshal(b, &shape); err != nil {
		return nil, err
	}
	out := make([]config.Cluster, 0, len(shape.Clusters))
	for _, c := range shape.Clusters {
		if c.ID == "" || len(c.Endpoints) == 0 {
			continue
		}
		name := c.Name
		if name == "" {
			name = c.ID
		}
		out = append(out, config.Cluster{
			ID:        c.ID,
			Name:      name,
			Endpoints: c.Endpoints,
			Username:  c.Username,
			Password:  c.Password,
			CAFile:    c.CAFile,
			CertFile:  c.CertFile,
			KeyFile:   c.KeyFile,
			Source:    "file",
		})
	}
	return out, nil
}
