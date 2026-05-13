package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/yourorg/etcd-ui/internal/httpx"
)

// etcdutlStatusHandler runs `etcdutl snapshot status` against an uploaded
// snapshot file and returns its hash/revision/totalKey/size. This is the
// pre-restore sanity check we tell operators to run in the restore recipe
// (`docs/features/RESTORE.md`). Implementing it server-side means an
// operator can verify a backup *before* halting their etcd quorum.
//
// Why etcdutl and not the v3 client SnapshotStatus method?
//   - SnapshotStatus is online-only (talks to a running cluster).
//   - etcdutl is *offline*: it parses the .db file directly. That's the
//     whole point of carrying it in our image.
//
// Hardening:
//   - 32 MiB max upload (configurable via ETCD_UI_ETCDUTL_MAX_MB).
//   - Snapshot is written to a temp file inside /tmp; deleted in defer.
//   - 30s wall-clock timeout for the subprocess.
//   - No subcommand other than "snapshot status" is accepted — restore
//     requires offline access to the etcd data dir on the actual server,
//     so doing it from this UI would be a footgun.
func etcdutlStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		maxMB := 32
		if v := os.Getenv("ETCD_UI_ETCDUTL_MAX_MB"); v != "" {
			fmt.Sscanf(v, "%d", &maxMB)
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxMB)*1024*1024)

		f, err := os.CreateTemp("", "etcd-ui-snap-*.db")
		if err != nil {
			httpx.Err(w, 500, fmt.Errorf("temp file: %w", err))
			return
		}
		tmp := f.Name()
		defer os.Remove(tmp)

		n, err := io.Copy(f, r.Body)
		_ = f.Close()
		if err != nil {
			httpx.Err(w, 400, fmt.Errorf("read body: %w", err))
			return
		}
		if n == 0 {
			httpx.Err(w, 400, errors.New("empty body — upload a .db snapshot file"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		// --write-out=json so we don't have to scrape the table format.
		cmd := exec.CommandContext(ctx, "etcdutl", "snapshot", "status", filepath.Clean(tmp), "--write-out=json")
		cmd.Env = append(os.Environ(), "ETCDCTL_API=3")
		var out, eb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &eb
		start := time.Now()
		runErr := cmd.Run()
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

		// On success, parse the JSON output so the SPA gets typed fields
		// instead of a stdout blob.
		var parsed map[string]any
		if exitCode == 0 {
			if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
				// Older etcdutl versions don't honour --write-out=json.
				// Fall through with raw stdout — UI can still display it.
				parsed = nil
			}
		}

		httpx.JSON(w, 200, map[string]any{
			"ok":         exitCode == 0,
			"exitCode":   exitCode,
			"sizeBytes":  n,
			"status":     parsed,
			"stdout":     out.String(),
			"stderr":     eb.String(),
			"durationMs": time.Since(start).Milliseconds(),
		})
	}
}
