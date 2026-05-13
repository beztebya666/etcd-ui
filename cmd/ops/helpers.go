package main

import (
	clientv3 "go.etcd.io/etcd/client/v3"
)

func asLeaseID(n int64) clientv3.LeaseID { return clientv3.LeaseID(n) }

// currentEndpoint returns one endpoint string the client knows about (used for
// Status / Compact calls that require an explicit endpoint).
func currentEndpoint(cli *clientv3.Client) string {
	eps := cli.Endpoints()
	if len(eps) == 0 {
		return ""
	}
	return eps[0]
}
