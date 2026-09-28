package webserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/rubika/terraform-drift-detector-gcp/internal/scanner"
	"github.com/rubika/terraform-drift-detector-gcp/internal/watch"
)

const (
	watchLogLimit           = 200
	defaultWatchIntervalMin = 15.0
)

// envWatchDefaults reads the operator-configured fallbacks: an interval (in
// minutes, DRIFTGCP_WATCH_INTERVAL_MINUTES) and a webhook URL
// (DRIFTGCP_WEBHOOK_URL, the same env var the CLI's `driftgcp watch` uses).
// These never round-trip to the browser — the dashboard's toggle just
// leaves interval_seconds/webhook_url out of its start request and this
// fills them in server-side, so the webhook URL never reaches client JS.
func envWatchDefaults() (intervalMinutes float64, webhookURL string) {
	intervalMinutes = defaultWatchIntervalMin
	if raw := os.Getenv("DRIFTGCP_WATCH_INTERVAL_MINUTES"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			intervalMinutes = v
		}
	}
	webhookURL = os.Getenv("DRIFTGCP_WEBHOOK_URL")
	return intervalMinutes, webhookURL
}

// watchManager runs at most one watch.Run loop at a time for the dashboard,
// keeping its recent log lines in memory so the browser can poll them.
type watchManager struct {
	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	startedAt time.Time
	opts      watch.Options
	log       []string
}

func newWatchManager() *watchManager {
	return &watchManager{}
}

func (m *watchManager) Start(opts watch.Options) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return errAlreadyRunning
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	m.startedAt = time.Now()
	m.opts = opts
	m.log = nil

	go func() {
		watch.Run(ctx, opts, m.appendLine)
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()
	return nil
}

func (m *watchManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return errNotRunning
	}
	m.cancel()
	m.running = false
	return nil
}

func (m *watchManager) appendLine(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.log = append(m.log, line)
	if len(m.log) > watchLogLimit {
		m.log = m.log[len(m.log)-watchLogLimit:]
	}
}

type watchStatus struct {
	Running        bool     `json:"running"`
	StartedAt      string   `json:"started_at,omitempty"`
	Interval       string   `json:"interval,omitempty"`
	WebhookEnabled bool     `json:"webhook_enabled"`
	StatePath      string   `json:"state_path,omitempty"`
	Log            []string `json:"log"`
}

// Status never includes the webhook URL itself — only whether one is set —
// so it's safe to poll from the browser without exposing the secret.
func (m *watchManager) Status() watchStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := watchStatus{
		Running: m.running,
		Log:     append([]string(nil), m.log...),
	}
	if !m.startedAt.IsZero() {
		status.StartedAt = m.startedAt.Format(time.RFC3339)
		status.Interval = m.opts.Interval.String()
		status.WebhookEnabled = m.opts.WebhookURL != ""
		status.StatePath = m.opts.Scan.StatePath
	}
	return status
}

type simpleError string

func (e simpleError) Error() string { return string(e) }

const (
	errAlreadyRunning = simpleError("watch is already running; stop it first")
	errNotRunning     = simpleError("watch is not running")
)

// watchStartRequest is the JSON body accepted by POST /api/watch/start.
type watchStartRequest struct {
	StatePath      string `json:"state_path"`
	Provider       string `json:"provider"`
	Project        string `json:"project"`
	CloudFixture   string `json:"cloud_fixture"`
	IntervalSecond int    `json:"interval_seconds"`
	WebhookURL     string `json:"webhook_url"`
}

func handleWatchStart(m *watchManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}

		var req watchStartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if req.StatePath == "" {
			writeError(w, http.StatusBadRequest, "state_path is required")
			return
		}

		defaultMinutes, defaultWebhook := envWatchDefaults()
		interval := time.Duration(req.IntervalSecond) * time.Second
		if interval <= 0 {
			interval = time.Duration(defaultMinutes * float64(time.Minute))
		}
		webhookURL := req.WebhookURL
		if webhookURL == "" {
			webhookURL = defaultWebhook
		}

		err := m.Start(watch.Options{
			Scan: scanner.Options{
				StatePath:    req.StatePath,
				Provider:     req.Provider,
				Project:      req.Project,
				CloudFixture: req.CloudFixture,
			},
			Interval:   interval,
			WebhookURL: webhookURL,
		})
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(m.Status())
	}
}

func handleWatchStop(m *watchManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		if err := m.Stop(); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(m.Status())
	}
}

func handleWatchStatus(m *watchManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(m.Status())
	}
}
