package alerts

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yourorg/etcd-ui/internal/models"

	"go.uber.org/zap"
)

func TestAlerts_FiresOnHealthFlip(t *testing.T) {
	m := New(zap.NewNop())
	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	// First eval: healthy. No alert (no prior state).
	m.evaluate(models.ClusterSummary{ID: "c1", Name: "c1", Healthy: true})
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event on first eval: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	// Second eval: unhealthy. Should fire.
	m.evaluate(models.ClusterSummary{ID: "c1", Name: "c1", Healthy: false, Error: "boom"})
	select {
	case ev := <-ch:
		if ev.Kind != KindUnhealthy {
			t.Errorf("expected unhealthy, got %v", ev.Kind)
		}
		if ev.Cluster != "c1" {
			t.Errorf("wrong cluster: %v", ev.Cluster)
		}
	case <-time.After(time.Second):
		t.Fatal("expected unhealthy alert within 1s")
	}

	// Recovery should fire too.
	m.evaluate(models.ClusterSummary{ID: "c1", Name: "c1", Healthy: true})
	select {
	case ev := <-ch:
		if ev.Kind != KindRecovered {
			t.Errorf("expected recovered, got %v", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("expected recovered alert")
	}
}

func TestAlerts_LeaderFlip(t *testing.T) {
	m := New(zap.NewNop())
	ch := m.Subscribe()
	defer m.Unsubscribe(ch)
	m.evaluate(models.ClusterSummary{ID: "c1", Name: "c1", Healthy: true, LeaderID: 1})
	m.evaluate(models.ClusterSummary{ID: "c1", Name: "c1", Healthy: true, LeaderID: 2})
	select {
	case ev := <-ch:
		if ev.Kind != KindLeaderFlip {
			t.Errorf("expected leader-flip, got %v", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("expected leader flip alert")
	}
}

func TestAlerts_Throttles(t *testing.T) {
	m := New(zap.NewNop())
	// Force the throttle window to be observable in a unit test by using a
	// very tight retry. Real throttle is 5min; we just want to verify that
	// the same kind doesn't fire twice instantly.
	ch := m.Subscribe()
	defer m.Unsubscribe(ch)
	m.evaluate(models.ClusterSummary{ID: "c1", Name: "c1", Healthy: true})
	m.evaluate(models.ClusterSummary{ID: "c1", Name: "c1", Healthy: false})
	m.evaluate(models.ClusterSummary{ID: "c1", Name: "c1", Healthy: false}) // already unhealthy
	// Drain one event.
	got := int32(0)
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case <-ch:
			atomic.AddInt32(&got, 1)
		case <-deadline:
			if got != 1 {
				t.Fatalf("expected exactly one alert (throttle), got %d", got)
			}
			return
		}
	}
}

func TestAlerts_WatchHonoursContext(t *testing.T) {
	m := New(zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Watch(ctx, func() []models.ClusterSummary { return nil }, 10*time.Millisecond)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Watch did not exit on context cancel")
	}
}
