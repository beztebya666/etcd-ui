// Package alerts emits health-change notifications. A background watcher in
// the cluster service polls Summary() for every known cluster every 15
// seconds. When a cluster flips healthy → unhealthy (or the alarms list
// changes), every configured sink is fired in parallel.
//
// Two transports ship in-box:
//
//   - **Webhook** — POSTs a small JSON payload to one or more URLs. Slack
//     incoming-webhooks, Discord, Mattermost, and Microsoft Teams all accept
//     the same shape with `{"text": "…"}`, so we send both `text` and a
//     structured `event` object — sinks pick whichever they understand.
//
//   - **SSE stream** — `/alerts/stream` lets the SPA subscribe and fire a
//     browser Notification (and an in-app toast).
//
// Both are opt-in:
//
//	ETCD_UI_ALERT_WEBHOOKS=https://hooks.slack.com/services/T/B/X,https://discord.com/api/webhooks/…
//	ETCD_UI_ALERT_BROWSER=on
//
// Throttle: per-cluster, no more than one alert of the same kind every 5
// minutes. Prevents storms during member flapping.

package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yourorg/etcd-ui/internal/models"
	"github.com/yourorg/etcd-ui/internal/tracing"

	"go.uber.org/zap"
)

// defaultThrottle is the per-recipient throttle window if env override is
// unset. Five minutes is "noisy enough to notice, quiet enough to ignore"
// for the typical Slack/Discord audience.
const defaultThrottle = 5 * time.Minute

type Kind string

const (
	KindUnhealthy   Kind = "unhealthy"
	KindRecovered   Kind = "recovered"
	KindAlarm       Kind = "alarm"
	KindLeaderFlip  Kind = "leader-flip"
)

type Event struct {
	Time    time.Time `json:"time"`
	Kind    Kind      `json:"kind"`
	Cluster string    `json:"cluster"`
	Detail  string    `json:"detail,omitempty"`
}

// Manager is the alert dispatcher. Construct with New, register sinks, then
// call Watch in a goroutine.
type Manager struct {
	log       *zap.Logger
	webhooks  []string
	throttle  time.Duration
	client    *http.Client

	mu         sync.RWMutex
	lastFired  map[string]time.Time // cluster|kind|recipient -> fired-at
	lastHealth map[string]bool
	lastLeader map[string]uint64
	lastAlarms map[string]string

	subsMu sync.RWMutex
	subs   map[chan Event]struct{}
}

func New(log *zap.Logger) *Manager {
	m := &Manager{
		log:        log,
		throttle:   defaultThrottle,
		lastFired:  map[string]time.Time{},
		lastHealth: map[string]bool{},
		lastLeader: map[string]uint64{},
		lastAlarms: map[string]string{},
		subs:       map[chan Event]struct{}{},
		client: &http.Client{
			Timeout:   5 * time.Second,
			Transport: tracing.WrapTransport(http.DefaultTransport),
		},
	}
	if raw := os.Getenv("ETCD_UI_ALERT_WEBHOOKS"); raw != "" {
		for _, u := range strings.Split(raw, ",") {
			if u = strings.TrimSpace(u); u != "" {
				m.webhooks = append(m.webhooks, u)
			}
		}
	}
	if v := os.Getenv("ETCD_UI_ALERT_THROTTLE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			m.throttle = d
		}
	}
	return m
}

// Enabled returns true when at least one sink is configured.
func (m *Manager) Enabled() bool {
	return len(m.webhooks) > 0 || len(m.subs) > 0
}

// Watch polls the given fetcher every `period`, diffs against the previous
// state, and fires alerts on changes. Blocks until ctx is cancelled.
func (m *Manager) Watch(ctx context.Context, fetcher func() []models.ClusterSummary, period time.Duration) {
	if period <= 0 {
		period = 15 * time.Second
	}
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, s := range fetcher() {
				m.evaluate(s)
			}
		}
	}
}

// evaluate diffs one cluster summary against the previous snapshot and emits
// any alerts that the diff implies.
func (m *Manager) evaluate(s models.ClusterSummary) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prevHealth, hadHealth := m.lastHealth[s.ID]
	prevLeader := m.lastLeader[s.ID]
	prevAlarms := m.lastAlarms[s.ID]

	alarms := strings.Join(s.Alarms, ",")

	switch {
	case hadHealth && prevHealth && !s.Healthy:
		m.fireLocked(Event{
			Time: time.Now().UTC(), Kind: KindUnhealthy, Cluster: s.Name,
			Detail: firstNonEmpty(s.Error, "cluster reports unhealthy"),
		})
	case hadHealth && !prevHealth && s.Healthy:
		m.fireLocked(Event{
			Time: time.Now().UTC(), Kind: KindRecovered, Cluster: s.Name,
			Detail: "cluster is healthy again",
		})
	}

	if prevLeader != 0 && s.LeaderID != 0 && prevLeader != s.LeaderID {
		m.fireLocked(Event{
			Time: time.Now().UTC(), Kind: KindLeaderFlip, Cluster: s.Name,
			Detail: fmt.Sprintf("leader %x → %x", prevLeader, s.LeaderID),
		})
	}

	if alarms != "" && alarms != prevAlarms {
		m.fireLocked(Event{
			Time: time.Now().UTC(), Kind: KindAlarm, Cluster: s.Name,
			Detail: alarms,
		})
	}

	m.lastHealth[s.ID] = s.Healthy
	if s.LeaderID != 0 {
		m.lastLeader[s.ID] = s.LeaderID
	}
	m.lastAlarms[s.ID] = alarms
}

// fireLocked dispatches one event after applying throttle per recipient.
// Must be called with m.mu held. The SSE subscriber stream uses a single
// throttle key ("__sse__") so the UI gets the event at most once per window;
// each webhook URL has its own key so quiet recipients don't suppress noisy
// ones (and a Slack/Discord pair both fire even if their cadence overlaps).
func (m *Manager) fireLocked(ev Event) {
	now := time.Now()
	allowed := func(recipient string) bool {
		key := ev.Cluster + "|" + string(ev.Kind) + "|" + recipient
		if last, ok := m.lastFired[key]; ok && now.Sub(last) < m.throttle {
			return false
		}
		m.lastFired[key] = now
		return true
	}
	// Fan out per recipient with independent throttles.
	for _, url := range m.webhooks {
		if allowed("webhook:" + url) {
			go m.postWebhook(url, ev)
		}
	}
	if allowed("__sse__") {
		go m.broadcastSSE(ev)
	}
}

func (m *Manager) broadcastSSE(ev Event) {
	m.subsMu.RLock()
	for ch := range m.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	m.subsMu.RUnlock()
}

func (m *Manager) postWebhook(url string, ev Event) {
	payload := map[string]any{
		"text":  fmt.Sprintf("[etcd-ui] %s: %s — %s", ev.Kind, ev.Cluster, ev.Detail),
		"event": ev,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := m.client.Do(req)
	if err != nil {
		m.log.Warn("alert webhook failed", zap.String("url", url), zap.Error(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		m.log.Warn("alert webhook non-2xx",
			zap.String("url", url),
			zap.Int("status", resp.StatusCode))
	}
}

// Subscribe returns a channel that receives every fired event. Unsubscribe
// with `defer m.Unsubscribe(ch)` to avoid leaking goroutines.
func (m *Manager) Subscribe() chan Event {
	ch := make(chan Event, 16)
	m.subsMu.Lock()
	m.subs[ch] = struct{}{}
	m.subsMu.Unlock()
	return ch
}

func (m *Manager) Unsubscribe(ch chan Event) {
	m.subsMu.Lock()
	delete(m.subs, ch)
	m.subsMu.Unlock()
	close(ch)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
