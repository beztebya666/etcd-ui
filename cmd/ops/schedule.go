package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/storage"
	"go.uber.org/zap"
)

// uploader is the (possibly-nil) S3 destination used by takeSnapshot to
// stream a copy of each snapshot off local disk. Initialized once from env
// in startScheduler so we don't re-read os.Environ per tick.
var uploader *storage.S3Uploader

// ETCD_UI_SNAPSHOT_SCHEDULE format: "<clusterId>:<period>:<retain>[,..]"
// where period ∈ {hourly|daily|weekly|<duration>} and retain is an int.
//
// Example: "default:hourly:24,prod:daily:30"
//
// Snapshots are written to /app/data/snapshots/<clusterId>/<RFC3339>.db.
// Anything beyond `retain` is pruned (oldest first).

type schedule struct {
	clusterID string
	period    time.Duration
	retain    int
}

func parseSchedule(s string) []schedule {
	var out []schedule
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, ":", 3)
		if len(parts) != 3 {
			continue
		}
		period := parsePeriod(parts[1])
		if period == 0 {
			continue
		}
		retain, err := strconv.Atoi(parts[2])
		if err != nil || retain <= 0 {
			retain = 7
		}
		out = append(out, schedule{clusterID: parts[0], period: period, retain: retain})
	}
	return out
}

func parsePeriod(s string) time.Duration {
	switch strings.ToLower(s) {
	case "hourly":
		return time.Hour
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func startScheduler(ctx context.Context, log *zap.Logger, pool *etcdpool.Pool, dataDir string) {
	raw := os.Getenv("ETCD_UI_SNAPSHOT_SCHEDULE")
	if raw == "" {
		return
	}
	schedules := parseSchedule(raw)
	if len(schedules) == 0 {
		return
	}
	uploader = storage.S3FromEnv()
	if uploader != nil {
		log.Info("snapshot S3 offload enabled",
			zap.String("endpoint", uploader.Endpoint()),
			zap.String("bucket", uploader.Bucket()))
	}
	for _, s := range schedules {
		go run(ctx, log, pool, dataDir, s)
	}
}

func run(ctx context.Context, log *zap.Logger, pool *etcdpool.Pool, dataDir string, s schedule) {
	dir := filepath.Join(dataDir, "snapshots", s.clusterID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warn("snapshot dir", zap.Error(err))
		return
	}
	// Take one immediately on startup so the cluster has at least one snapshot.
	_ = takeSnapshot(ctx, log, pool, s.clusterID, dir, s.retain)
	t := time.NewTicker(s.period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = takeSnapshot(ctx, log, pool, s.clusterID, dir, s.retain)
		}
	}
}

func takeSnapshot(ctx context.Context, log *zap.Logger, pool *etcdpool.Pool, id, dir string, retain int) error {
	cli, _, err := pool.Client(id)
	if err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	rc, err := cli.Snapshot(sctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	name := time.Now().UTC().Format("2006-01-02T15-04-05Z") + ".db"
	path := filepath.Join(dir, name)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	log.Info("scheduled snapshot", zap.String("cluster", id), zap.String("file", name))

	// Best-effort offload. Failures are logged but don't roll back the local
	// snapshot — having a copy on disk is better than no copy at all.
	// PutLarge auto-selects single-PUT vs multipart based on file size.
	if uploader != nil {
		go func(localPath, snap string) {
			f, err := os.Open(localPath)
			if err != nil {
				log.Warn("s3 offload open", zap.Error(err))
				return
			}
			defer f.Close()
			st, err := f.Stat()
			if err != nil {
				log.Warn("s3 offload stat", zap.Error(err))
				return
			}
			uctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			key := id + "/" + snap
			if err := uploader.PutLarge(uctx, key, f, st.Size(), "application/octet-stream"); err != nil {
				log.Warn("s3 offload failed",
					zap.String("key", key),
					zap.Int64("bytes", st.Size()),
					zap.Error(err))
				return
			}
			log.Info("s3 offload ok",
				zap.String("key", key),
				zap.Int64("bytes", st.Size()))
		}(path, name)
	}

	pruneOld(dir, retain)
	return nil
}

func pruneOld(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	files := entries[:0]
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".db") {
			files = append(files, e)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	for i := 0; i < len(files)-keep; i++ {
		_ = os.Remove(filepath.Join(dir, files[i].Name()))
	}
}

func snapshotsListHandler(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		dir := filepath.Join(dataDir, "snapshots", id)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				httpx.JSON(w, 200, []any{})
				return
			}
			httpx.Err(w, 500, err)
			return
		}
		type item struct {
			Name    string    `json:"name"`
			Size    int64     `json:"size"`
			ModTime time.Time `json:"modTime"`
		}
		out := []item{}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, item{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime()})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
		httpx.JSON(w, 200, out)
	}
}

func snapshotsDownloadHandler(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		name := chi.URLParam(r, "name")
		// Defense in depth: refuse traversal.
		if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
			http.Error(w, "bad name", 400)
			return
		}
		path := filepath.Join(dataDir, "snapshots", id, name)
		f, err := os.Open(path)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		_, _ = io.Copy(w, f)
	}
}
