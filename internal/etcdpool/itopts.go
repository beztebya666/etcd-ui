//go:build integration

package etcdpool

import clientv3 "go.etcd.io/etcd/client/v3"

func clientWithPrefix() clientv3.OpOption { return clientv3.WithPrefix() }
