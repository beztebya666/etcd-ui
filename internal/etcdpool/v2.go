package etcdpool

import (
	"context"
	"errors"
	"time"

	"github.com/yourorg/etcd-ui/internal/config"

	clientv2 "go.etcd.io/etcd/client/v2"
)

// V2Client wraps etcd's legacy v2 HTTP client. We only expose a tiny
// rectangle — get/put/delete — because the v2 protocol predates leases,
// watches-with-revisions, mvcc and gRPC, so wholesale parity with v3 is
// not worth the maintenance cost. Power-user features (txn, history,
// metrics scrape, snapshot) are unavailable on v2 and return ErrNotSupported.
type V2Client struct {
	api clientv2.KeysAPI
}

var ErrNotSupportedV2 = errors.New("operation is not supported on etcd v2 clusters")

// NewV2Client dials a cluster over the legacy HTTP API.
func NewV2Client(c config.Cluster) (*V2Client, error) {
	tlsCfg, err := buildV2TLS(c)
	if err != nil {
		return nil, err
	}
	transport := clientv2.DefaultTransport
	if tlsCfg != nil {
		transport = &v2Transport{tls: tlsCfg}
	}
	cli, err := clientv2.New(clientv2.Config{
		Endpoints:               c.Endpoints,
		Transport:               transport,
		HeaderTimeoutPerRequest: 5 * time.Second,
		Username:                c.Username,
		Password:                c.Password,
	})
	if err != nil {
		return nil, err
	}
	return &V2Client{api: clientv2.NewKeysAPI(cli)}, nil
}

func (v *V2Client) Get(ctx context.Context, key string, recursive bool) (*clientv2.Response, error) {
	return v.api.Get(ctx, key, &clientv2.GetOptions{Recursive: recursive, Sort: true})
}

func (v *V2Client) Set(ctx context.Context, key, value string) (*clientv2.Response, error) {
	return v.api.Set(ctx, key, value, nil)
}

func (v *V2Client) Delete(ctx context.Context, key string, recursive bool) (*clientv2.Response, error) {
	return v.api.Delete(ctx, key, &clientv2.DeleteOptions{Recursive: recursive})
}
