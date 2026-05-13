// Supervisor is pid 1 inside the container. It starts the four microservices
// in dependency order, propagates signals, and restarts any service that
// crashes (capped exponential backoff).
package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/yourorg/etcd-ui/internal/logger"
	"go.uber.org/zap"
)

type proc struct {
	name string
	path string
}

func main() {
	log := logger.New("supervisor")
	dir := filepath.Dir(os.Args[0])
	procs := []proc{
		{"audit", filepath.Join(dir, "audit")},
		{"cluster", filepath.Join(dir, "cluster")},
		{"kv", filepath.Join(dir, "kv")},
		{"ops", filepath.Join(dir, "ops")},
		{"gateway", filepath.Join(dir, "gateway")},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Add(1)
		go func(p proc) {
			defer wg.Done()
			supervise(ctx, log, p)
		}(p)
	}

	sig := <-sigCh
	log.Info("received signal, shutting down", zap.String("signal", sig.String()))
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		log.Warn("forcing exit, services did not stop in time")
	}
}

func supervise(ctx context.Context, log *zap.Logger, p proc) {
	backoff := 500 * time.Millisecond
	const maxBackoff = 15 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		log.Info("starting", zap.String("proc", p.name))
		cmd := exec.CommandContext(ctx, p.path)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		err := cmd.Run()
		if ctx.Err() != nil {
			return
		}
		log.Warn("process exited", zap.String("proc", p.name), zap.Error(err))

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}
