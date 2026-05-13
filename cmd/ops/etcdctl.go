package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
)

// etcdctlHandler shells out to the real /usr/local/bin/etcdctl binary against
// the selected cluster. It exists so users can run anything etcdctl can do
// (move-leader, member remove, role grant-permission, alarm disarm, …) without
// us re-implementing every command in REST.
//
// Hardening:
//   - Disabled by default. Opt-in with ETCD_UI_ETCDCTL=on.
//   - Subcommand allowlist (no shell, no arbitrary paths).
//   - Inherits the cluster's TLS/auth flags from the registered profile —
//     callers can't override --endpoints / --cacert / --cert / --key from
//     the request body.
//   - 25s wall-clock timeout per command.
//   - Audited via the gateway middleware (POST → mutation by default).
//
// Body: {"args":["member","list"]} → 200 {"stdout","stderr","exitCode","durationMs"}.
func etcdctlHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("ETCD_UI_ETCDCTL") != "on" {
			httpx.Err(w, 403, errors.New(
				"etcdctl shell is disabled by default. To enable, restart the etcd-ui "+
					"container with `-e ETCD_UI_ETCDCTL=on` (Helm: `etcdctl.enabled=true`). "+
					"Reason: shelling out to a real binary against a live cluster is high-risk; "+
					"explicit opt-in keeps the surface tight.",
			))
			return
		}
		id := chi.URLParam(r, "id")
		_, c, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		var req struct {
			Args []string `json:"args"`
		}
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if len(req.Args) == 0 {
			httpx.Err(w, 400, errors.New("args required"))
			return
		}
		head := req.Args[0]
		if !etcdctlSubcommandAllowed(head) {
			httpx.Err(w, 400, fmt.Errorf("subcommand %q not allowed; allowed: %s",
				head, strings.Join(allowedEtcdctlList, ", ")))
			return
		}

		// Build the wire args. Inject endpoints + TLS up front; user args
		// follow after `--`, etcdctl parses the union.
		flags := []string{
			"--endpoints=" + strings.Join(c.Endpoints, ","),
			"--command-timeout=15s",
			"--dial-timeout=5s",
		}
		if c.CAFile != "" {
			flags = append(flags, "--cacert="+c.CAFile)
		}
		if c.CertFile != "" {
			flags = append(flags, "--cert="+c.CertFile)
		}
		if c.KeyFile != "" {
			flags = append(flags, "--key="+c.KeyFile)
		}
		if c.Username != "" {
			flags = append(flags, "--user="+c.Username+":"+c.Password)
		}
		fullArgs := append(flags, req.Args...)

		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "etcdctl", fullArgs...)
		cmd.Env = append(os.Environ(), "ETCDCTL_API=3")
		var out, eb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &eb
		start := time.Now()
		runErr := cmd.Run()

		// Surface non-secret arg shape to caller so they can copy-paste a real
		// etcdctl command into their shell. Censor the password if set.
		shown := append([]string{}, flags...)
		for i, a := range shown {
			if strings.HasPrefix(a, "--user=") {
				shown[i] = "--user=" + c.Username + ":***"
			}
		}
		shown = append(shown, req.Args...)

		exitCode := 0
		if runErr != nil {
			var ee *exec.ExitError
			if errors.As(runErr, &ee) {
				exitCode = ee.ExitCode()
			} else {
				exitCode = -1
				if eb.Len() == 0 {
					eb.WriteString(runErr.Error())
				}
			}
		}

		httpx.JSON(w, 200, map[string]any{
			"stdout":     out.String(),
			"stderr":     eb.String(),
			"exitCode":   exitCode,
			"durationMs": time.Since(start).Milliseconds(),
			"command":    append([]string{"etcdctl"}, shown...),
		})
	}
}

// allowedEtcdctlList — anything etcdctl can do that's reasonable to expose.
// `watch` is excluded because it never returns until killed and we can't stream
// stdout here; users can still tail via our own Watch page.
var allowedEtcdctlList = []string{
	"get", "put", "del", "txn",
	"member", "endpoint", "alarm", "lease",
	"auth", "user", "role",
	"snapshot", "defrag", "compaction", "move-leader",
	"check", "version", "help",
}

func etcdctlSubcommandAllowed(s string) bool {
	for _, x := range allowedEtcdctlList {
		if x == s {
			return true
		}
	}
	return false
}
