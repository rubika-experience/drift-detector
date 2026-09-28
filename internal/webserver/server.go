// Package webserver serves a small single-page dashboard for triggering
// scans and viewing drift reports in a browser, on top of the same
// internal/scanner logic the CLI uses.
package webserver

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"google.golang.org/api/cloudresourcemanager/v3"

	"github.com/rubika/terraform-drift-detector-gcp/internal/alert"
	"github.com/rubika/terraform-drift-detector-gcp/internal/explain"
	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
	"github.com/rubika/terraform-drift-detector-gcp/internal/scanner"
	"github.com/rubika/terraform-drift-detector-gcp/internal/store"
)

// stateFileOption is one entry in the dashboard's state-file dropdown.
type stateFileOption struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// knownStateFiles is the legacy (no DATABASE_URL) dropdown list. In DB mode
// the dropdown instead comes from each team's team_state_files rows (see
// admin.go) — add your own real state file paths there, or here for local
// no-DB use.
var knownStateFiles = []stateFileOption{
	{Label: "demo fixture", Path: "testdata/state/sample.tfstate"},
}

// gcpProject is one project entry in the dashboard's project-ID dropdown.
type gcpProject struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
}

//go:embed static/index.html
var staticFS embed.FS

//go:embed static/admin.html
var adminPageFS embed.FS

// scanRequest is the JSON body accepted by POST /api/scan.
type scanRequest struct {
	StatePath    string `json:"state_path"`
	Provider     string `json:"provider"`
	Project      string `json:"project"`
	CloudFixture string `json:"cloud_fixture"`
	Explain      bool   `json:"explain"`
}

// scanResponse is the JSON body returned by POST /api/scan.
type scanResponse struct {
	Report       model.DriftReport `json:"report"`
	Explanation  string            `json:"explanation,omitempty"`
	ExplainError string            `json:"explain_error,omitempty"`
}

// NewHandler builds the dashboard's HTTP handler: the login page, the
// static dashboard at "/", the scan API, and — when DATABASE_URL is set —
// real multi-user Postgres auth with team-scoped state files and an admin
// panel (see auth.go, admin.go, internal/store). With DATABASE_URL unset,
// login falls back to the original single DRIFTGCP_USER/DRIFTGCP_PASS demo
// account with in-memory sessions, so the tool still runs with zero setup.
func NewHandler(ctx context.Context) (http.Handler, error) {
	var db *sql.DB
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		var err error
		db, err = store.Open(ctx, databaseURL)
		if err != nil {
			return nil, fmt.Errorf("connecting to DATABASE_URL: %w", err)
		}

		teamName := os.Getenv("DRIFTGCP_BOOTSTRAP_TEAM")
		if teamName == "" {
			teamName = "default"
		}
		if err := store.EnsureBootstrapAdmin(ctx, db,
			teamName, os.Getenv("DRIFTGCP_ADMIN_EMAIL"), os.Getenv("DRIFTGCP_ADMIN_PASSWORD"),
		); err != nil {
			return nil, fmt.Errorf("bootstrapping first admin: %w", err)
		}
	}

	auth := newAuthService(db)

	protected := http.NewServeMux()
	protected.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
	protected.HandleFunc("/api/me", auth.handleMe())
	protected.HandleFunc("/api/scan", handleScan(db))
	protected.HandleFunc("/api/state-files", handleStateFiles(db))
	protected.HandleFunc("/api/projects", handleProjects)

	if db != nil {
		protected.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
			data, err := adminPageFS.ReadFile("static/admin.html")
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(data)
		})
		protected.Handle("/api/admin/users", auth.requireAdmin(handleAdminUsers(db)))
		protected.Handle("/api/admin/users/{id}", auth.requireAdmin(handleAdminUserByID(db)))
		protected.Handle("/api/admin/state-files", auth.requireAdmin(handleAdminStateFiles(db)))
	}

	watchMgr := newWatchManager()
	protected.HandleFunc("/api/watch/start", handleWatchStart(watchMgr))
	protected.HandleFunc("/api/watch/stop", handleWatchStop(watchMgr))
	protected.HandleFunc("/api/watch/status", handleWatchStatus(watchMgr))

	mux := http.NewServeMux()
	mux.HandleFunc("/login", handleLoginPage)
	mux.HandleFunc("/api/login", auth.handleLogin())
	mux.HandleFunc("/api/logout", auth.handleLogout())
	mux.Handle("/", auth.requireAuth(protected))

	return mux, nil
}

// handleStateFiles serves the dashboard's state-file dropdown: team-scoped
// rows from Postgres in DB mode, the hardcoded legacy list otherwise.
func handleStateFiles(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if db == nil {
			_ = json.NewEncoder(w).Encode(knownStateFiles)
			return
		}

		user, _ := userFromContext(r)
		files, err := store.ListStateFiles(r.Context(), db, user.TeamID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]stateFileOption, 0, len(files))
		for _, f := range files {
			out = append(out, stateFileOption{Label: f.Label, Path: f.Path})
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

// handleProjects lists GCP projects visible to the credentials in
// GOOGLE_APPLICATION_CREDENTIALS (the same service account key used for
// scans), via the Cloud Resource Manager API, for the dashboard's
// project-ID dropdown. No interactive gcloud login is involved. This list
// is intentionally not team-scoped (see the README's known-gaps note).
func handleProjects(w http.ResponseWriter, r *http.Request) {
	svc, err := cloudresourcemanager.NewService(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "creating Cloud Resource Manager client: "+err.Error())
		return
	}

	projects := []gcpProject{}
	call := svc.Projects.Search()
	if err := call.Pages(r.Context(), func(resp *cloudresourcemanager.SearchProjectsResponse) error {
		for _, p := range resp.Projects {
			projects = append(projects, gcpProject{ProjectID: p.ProjectId, Name: p.DisplayName})
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusBadGateway, "listing GCP projects: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(projects)
}

func handleScan(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}

		var req scanRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}

		// In DB mode, a user can only scan a state file assigned to their own
		// team — not just hidden from the dropdown, actually enforced here.
		if db != nil {
			user, _ := userFromContext(r)
			ok, err := store.HasStateFile(r.Context(), db, user.TeamID, req.StatePath)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !ok {
				writeError(w, http.StatusForbidden, "state file is not assigned to your team")
				return
			}
		}

		report, err := scanner.Run(r.Context(), scanner.Options{
			StatePath:    req.StatePath,
			Provider:     req.Provider,
			Project:      req.Project,
			CloudFixture: req.CloudFixture,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		resp := scanResponse{Report: report}
		if req.Explain {
			summary, err := explain.Explain(r.Context(), report)
			if err != nil {
				resp.ExplainError = err.Error()
			} else {
				resp.Explanation = summary
			}
		}

		// Every manual scan from the dashboard posts to Slack when a webhook is
		// configured (DRIFTGCP_WEBHOOK_URL), independent of drift status — this
		// is the "Run scan" button, not watch mode's critical-only alert. Runs
		// in the background so a slow/unreachable webhook can't delay the
		// dashboard's response.
		if _, webhookURL := envWatchDefaults(); webhookURL != "" {
			go func(report model.DriftReport, explanation string) {
				if err := alert.NotifyScan(context.Background(), webhookURL, report, explanation); err != nil {
					log.Printf("slack notify failed: %v", err)
				}
			}(report, resp.Explanation)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("encoding scan response: %v", err)
		}
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
