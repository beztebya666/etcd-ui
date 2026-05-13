package discovery

import (
	"context"

	"github.com/yourorg/etcd-ui/internal/config"
)

// EnvSource exposes statically-configured clusters from config.Config.
type EnvSource struct {
	Clusters []config.Cluster
}

func (e *EnvSource) Name() string { return "env" }

func (e *EnvSource) Discover(_ context.Context) ([]config.Cluster, error) {
	return e.Clusters, nil
}
