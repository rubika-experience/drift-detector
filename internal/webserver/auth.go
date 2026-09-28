package webserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/rubika/terraform-drift-detector-gcp/internal/store"
)

//go:embed static/login.html
var loginFS embed.FS

const (
	sessionCookieName = "driftgcp_session"
	sessionTTL        = 12 * time.Hour
)

// defaultDemoUser/defaultDemoPass are the fallback demo credentials used in
// legacy (no-DATABASE_URL) mode when DRIFTGCP_USER / DRIFTGCP_PASS are not
// set, so the dashboard is still reachable behind a login out of the box
// (e.g. for a reviewer demo account, never for a real deployment).
const (
	defaultDemoUser = "demo"
	defaultDemoPass = "driftgcp-demo"
)

// currentUser is attached to a request's context by requireAuth once a
// session resolves, so downstream handlers (scan, state files, admin) know
// who's asking and which team to scope to. Only populated in DB mode —
// legacy mode has no concept of users/teams.
type currentUser struct {
	ID       int64
	Email    string
	TeamID   int64
	TeamName string
	Role     store.Role
}

type ctxKey int

const currentUserCtxKey ctxKey = 0

func userFromContext(r *http.Request) (currentUser, bool) {
	u, ok := r.Context().Value(currentUserCtxKey).(currentUser)
	return u, ok
}

// authService is driftgcp's login/session backend. With db set, it's real
// multi-user Postgres auth (bcrypt password hashes, durable sessions,
// team-scoped access). With db nil, it's the original single
// demo-user/in-memory-session mode, unchanged, so the tool still works with
// zero setup.
type authService struct {
	db     *sql.DB
	legacy *sessionStore // only used when db == nil
}

func newAuthService(db *sql.DB) *authService {
	a := &authService{db: db}
	if db == nil {
		a.legacy = newSessionStore()
	}
	return a
}

// sessionStore is the legacy in-memory session table. It is intentionally
// not persisted or shared across instances: this is the no-database
// fallback for a single local demo process.
type sessionStore struct {
	mu       sync.Mutex
	expiries map[string]time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{expiries: make(map[string]time.Time)}
}

func (s *sessionStore) create() string {
	token := randomToken()
	s.mu.Lock()
	s.expiries[token] = time.Now().Add(sessionTTL)
	s.mu.Unlock()
	return token
}

func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.expiries[token]
	if !ok || time.Now().After(exp) {
		delete(s.expiries, token)
		return false
	}
	return true
}

func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	delete(s.expiries, token)
	s.mu.Unlock()
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is not something we can recover from safely.
		panic("webserver: failed to generate session token: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func demoCredentials() (user, pass string) {
	user = os.Getenv("DRIFTGCP_USER")
	if user == "" {
		user = defaultDemoUser
	}
	pass = os.Getenv("DRIFTGCP_PASS")
	if pass == "" {
		pass = defaultDemoPass
	}
	return user, pass
}

func credentialsMatch(user, pass string) bool {
	wantUser, wantPass := demoCredentials()
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(wantUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(wantPass)) == 1
	return userOK && passOK
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// requireAuth wraps a handler so it only runs when the request carries a
// valid session. In DB mode it also resolves and attaches the requesting
// user's team/role to the request context. Browser navigations to "/" are
// redirected to /login on failure; every other path gets a 401 JSON body.
func (a *authService) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			a.denyAuth(w, r)
			return
		}

		if a.db == nil {
			if !a.legacy.valid(cookie.Value) {
				a.denyAuth(w, r)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		userID, err := store.GetSessionUserID(r.Context(), a.db, cookie.Value)
		if err != nil {
			a.denyAuth(w, r)
			return
		}
		user, err := store.GetUserByID(r.Context(), a.db, userID)
		if err != nil {
			a.denyAuth(w, r)
			return
		}
		membership, err := store.GetMembership(r.Context(), a.db, userID)
		if err != nil {
			// A user row with no team assignment yet (e.g. created but not
			// added to a team by an admin) can't use the dashboard.
			a.denyAuth(w, r)
			return
		}

		cu := currentUser{
			ID:       user.ID,
			Email:    user.Email,
			TeamID:   membership.TeamID,
			TeamName: membership.TeamName,
			Role:     membership.Role,
		}
		ctx := context.WithValue(r.Context(), currentUserCtxKey, cu)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *authService) denyAuth(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	writeError(w, http.StatusUnauthorized, "not authenticated")
}

// requireAdmin wraps a handler so it only runs for a DB-mode session whose
// role is admin. It must sit inside requireAuth (so currentUser is already
// populated). In legacy mode there's no such thing as an admin, so it's
// always a 404 — matching "this feature doesn't exist without a database"
// rather than a confusing 403.
func (a *authService) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.db == nil {
			writeError(w, http.StatusNotFound, "admin management requires DATABASE_URL")
			return
		}
		user, ok := userFromContext(r)
		if !ok || user.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	data, err := loginFS.ReadFile("static/login.html")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// loginRequest's Username field doubles as an email address in DB mode —
// one login page serves both modes.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *authService) handleLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}

		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}

		if a.db == nil {
			if !credentialsMatch(req.Username, req.Password) {
				writeError(w, http.StatusUnauthorized, "invalid username or password")
				return
			}
			setSessionCookie(w, a.legacy.create())
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}

		user, err := store.GetUserByEmail(r.Context(), a.db, req.Username)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}

		token, err := store.CreateSession(r.Context(), a.db, user.ID, sessionTTL)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		setSessionCookie(w, token)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

func (a *authService) handleLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if a.db != nil {
				_ = store.DeleteSession(r.Context(), a.db, cookie.Value)
			} else {
				a.legacy.revoke(cookie.Value)
			}
		}
		clearSessionCookie(w)
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

// meResponse is what GET /api/me returns, so the dashboard's JS knows
// whether to show the admin panel and (in legacy mode) that there's no
// team concept at all.
type meResponse struct {
	Email    string `json:"email,omitempty"`
	TeamName string `json:"team_name,omitempty"`
	Role     string `json:"role,omitempty"`
	DBMode   bool   `json:"db_mode"`
}

func (a *authService) handleMe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := meResponse{DBMode: a.db != nil}
		if user, ok := userFromContext(r); ok {
			resp.Email = user.Email
			resp.TeamName = user.TeamName
			resp.Role = string(user.Role)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
