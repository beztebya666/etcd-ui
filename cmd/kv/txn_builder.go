package main

import (
	"fmt"
	"strconv"

	"github.com/yourorg/etcd-ui/internal/models"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func buildCompares(in []models.TxnCondition) ([]clientv3.Cmp, error) {
	out := make([]clientv3.Cmp, 0, len(in))
	for _, c := range in {
		var cmp clientv3.Cmp
		switch c.Field {
		case "value":
			cmp = clientv3.Compare(clientv3.Value(c.Key), opSign(c.Op), c.Target)
		case "createRevision":
			n, err := strconv.ParseInt(c.Target, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("createRevision target: %w", err)
			}
			cmp = clientv3.Compare(clientv3.CreateRevision(c.Key), opSign(c.Op), n)
		case "modRevision":
			n, err := strconv.ParseInt(c.Target, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("modRevision target: %w", err)
			}
			cmp = clientv3.Compare(clientv3.ModRevision(c.Key), opSign(c.Op), n)
		case "version":
			n, err := strconv.ParseInt(c.Target, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("version target: %w", err)
			}
			cmp = clientv3.Compare(clientv3.Version(c.Key), opSign(c.Op), n)
		default:
			return nil, fmt.Errorf("unsupported field %q", c.Field)
		}
		out = append(out, cmp)
	}
	return out, nil
}

func buildOps(in []models.TxnOp) ([]clientv3.Op, error) {
	out := make([]clientv3.Op, 0, len(in))
	for _, o := range in {
		switch o.Type {
		case "put":
			out = append(out, clientv3.OpPut(o.Key, o.Value))
		case "delete":
			if o.Prefix {
				out = append(out, clientv3.OpDelete(o.Key, clientv3.WithPrefix()))
			} else {
				out = append(out, clientv3.OpDelete(o.Key))
			}
		case "get":
			if o.Prefix {
				out = append(out, clientv3.OpGet(o.Key, clientv3.WithPrefix()))
			} else {
				out = append(out, clientv3.OpGet(o.Key))
			}
		default:
			return nil, fmt.Errorf("unsupported op %q", o.Type)
		}
	}
	return out, nil
}

func opSign(op string) string {
	switch op {
	case "equal", "==", "=":
		return "="
	case "not-equal", "!=":
		return "!="
	case "greater", ">":
		return ">"
	case "less", "<":
		return "<"
	}
	return "="
}
