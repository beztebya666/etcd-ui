package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
)

// restoreRecipeHandler doesn't actually restore anything (that requires
// offline access to the etcd data dir). It generates a copy-paste runbook
// for two restore styles: bare etcdctl on a host, or a Kubernetes Job that
// runs etcdutl in a sidecar container.
func restoreRecipeHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		_, c, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		snapshotPath := r.URL.Query().Get("path")
		if snapshotPath == "" {
			snapshotPath = "/var/lib/etcd/snapshot.db"
		}
		dataDir := r.URL.Query().Get("dataDir")
		if dataDir == "" {
			dataDir = "/var/lib/etcd/restored"
		}
		token := r.URL.Query().Get("token")
		if token == "" {
			token = "etcd-restore-" + c.Name
		}
		mode := r.URL.Query().Get("mode")
		if mode == "" {
			mode = "etcdctl"
		}

		var body string
		switch mode {
		case "k8s":
			body = generateK8sJob(c.Name, snapshotPath, dataDir, token, c.Endpoints)
		default:
			body = generateEtcdctl(c.Name, snapshotPath, dataDir, token, c.Endpoints)
		}

		httpx.JSON(w, 200, map[string]any{
			"mode":         mode,
			"cluster":      c.Name,
			"snapshotPath": snapshotPath,
			"dataDir":      dataDir,
			"token":        token,
			"body":         body,
		})
	}
}

func generateEtcdctl(name, snap, dataDir, token string, endpoints []string) string {
	initialCluster := []string{}
	for i, ep := range endpoints {
		host := stripScheme(ep)
		// peer URL convention: same host on :2380
		peer := strings.SplitN(host, ":", 2)[0] + ":2380"
		initialCluster = append(initialCluster, fmt.Sprintf("%s-%d=https://%s", name, i, peer))
	}
	return fmt.Sprintf(`# 1. Stop every etcd member.
# 2. Run this on EACH member, substituting --name for the member's identity:
ETCDCTL_API=3 etcdutl snapshot restore %s \
  --name %s-0 \
  --initial-cluster=%s \
  --initial-cluster-token %s \
  --initial-advertise-peer-urls https://$(hostname):2380 \
  --data-dir %s

# 3. Point each etcd member at the new data dir (--data-dir=%s) and restart it.
# 4. Verify:
#    etcdctl --endpoints=%s endpoint status --write-out=table
`,
		snap, name, strings.Join(initialCluster, ","), token, dataDir, dataDir,
		strings.Join(endpoints, ","))
}

func generateK8sJob(name, snap, dataDir, token string, endpoints []string) string {
	_ = endpoints
	return fmt.Sprintf(`# This Job runs etcdutl in a side-container next to the etcd PVC.
# Stop the etcd StatefulSet first (replicas=0), apply the Job, wait for it to
# complete, then scale the StatefulSet back up — pods will use the restored data dir.

apiVersion: batch/v1
kind: Job
metadata:
  name: etcd-restore-%s
spec:
  template:
    spec:
      restartPolicy: OnFailure
      containers:
        - name: restore
          image: quay.io/coreos/etcd:v3.5.15
          command: ["/bin/sh", "-c"]
          args:
            - |
              etcdutl snapshot restore %s \
                --name %s-0 \
                --initial-cluster=%s-0=https://%s-0:2380 \
                --initial-cluster-token %s \
                --initial-advertise-peer-urls https://%s-0:2380 \
                --data-dir %s
          volumeMounts:
            - { name: data, mountPath: /var/lib/etcd }
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: data-%s-0     # PVC of the first etcd pod
`,
		name, snap, name, name, name, token, name, dataDir, name)
}

func stripScheme(s string) string {
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(s, p) {
			return s[len(p):]
		}
	}
	return s
}
