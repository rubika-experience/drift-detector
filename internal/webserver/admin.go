package webserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/rubika/terraform-drift-detector-gcp/internal/store"
)

// adminMemberResponse is one row in GET /api/admin/users.
type adminMemberResponse struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type createUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// handleAdminUsers serves the admin panel's team member list (GET) and lets
// an admin add a new teammate directly to their own team (POST) — this
// dashboard only manages membership within the admin's own team; see the
// README for the multi-team limitation.
func handleAdminUsers(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, _ := userFromContext(r)

		switch r.Method {
		case http.MethodGet:
			members, err := store.ListTeamMembers(r.Context(), db, admin.TeamID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out := make([]adminMemberResponse, 0, len(members))
			for _, m := range members {
				out = append(out, adminMemberResponse{UserID: m.UserID, Email: m.Email, Role: string(m.Role)})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)

		case http.MethodPost:
			var req createUserRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
			req.Email = strings.TrimSpace(req.Email)
			if req.Email == "" || req.Password == "" {
				writeError(w, http.StatusBadRequest, "email and password are required")
				return
			}
			role := store.Role(req.Role)
			if role != store.RoleAdmin && role != store.RoleMember {
				role = store.RoleMember
			}

			hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			userID, err := store.CreateUser(r.Context(), db, req.Email, string(hash))
			if err != nil {
				writeError(w, http.StatusConflict, "could not create user (email may already exist): "+err.Error())
				return
			}
			if err := store.AddMember(r.Context(), db, admin.TeamID, userID, role); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(adminMemberResponse{UserID: userID, Email: req.Email, Role: string(role)})

		default:
			writeError(w, http.StatusMethodNotAllowed, "use GET or POST")
		}
	}
}

// handleAdminUserByID lets an admin change a teammate's role (PATCH) or
// remove them from the team (DELETE), scoped to r.PathValue("id"). Both
// refuse to touch the last remaining admin, so a team can't lock itself out
// of its own admin panel.
func handleAdminUserByID(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, _ := userFromContext(r)

		targetID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user id")
			return
		}

		switch r.Method {
		case http.MethodPatch:
			var req struct {
				Role string `json:"role"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
			role := store.Role(req.Role)
			if role != store.RoleAdmin && role != store.RoleMember {
				writeError(w, http.StatusBadRequest, "role must be \"admin\" or \"member\"")
				return
			}

			if targetID == admin.ID && role != store.RoleAdmin {
				writeError(w, http.StatusConflict, "you can't demote yourself")
				return
			}
			if role == store.RoleMember {
				if err := refuseIfLastAdmin(r, db, admin.TeamID, targetID); err != nil {
					writeError(w, http.StatusConflict, err.Error())
					return
				}
			}

			if err := store.UpdateMemberRole(r.Context(), db, admin.TeamID, targetID, role); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					writeError(w, http.StatusNotFound, "no such member on your team")
				} else {
					writeError(w, http.StatusInternalServerError, err.Error())
				}
				return
			}
			w.WriteHeader(http.StatusNoContent)

		case http.MethodDelete:
			if targetID == admin.ID {
				writeError(w, http.StatusConflict, "you can't remove yourself")
				return
			}
			if err := refuseIfLastAdmin(r, db, admin.TeamID, targetID); err != nil {
				writeError(w, http.StatusConflict, err.Error())
				return
			}

			if err := store.RemoveMember(r.Context(), db, admin.TeamID, targetID); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					writeError(w, http.StatusNotFound, "no such member on your team")
				} else {
					writeError(w, http.StatusInternalServerError, err.Error())
				}
				return
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			writeError(w, http.StatusMethodNotAllowed, "use PATCH or DELETE")
		}
	}
}

// refuseIfLastAdmin errors when targetID is teamID's only remaining admin —
// called before demoting or removing someone, so a team can't accidentally
// lock itself out of its own admin panel. Members list is small enough
// that this two-query check is simpler than a single clever SQL statement.
func refuseIfLastAdmin(r *http.Request, db *sql.DB, teamID, targetID int64) error {
	members, err := store.ListTeamMembers(r.Context(), db, teamID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.UserID == targetID && m.Role == store.RoleAdmin {
			n, err := store.CountAdmins(r.Context(), db, teamID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return errLastAdmin
			}
		}
	}
	return nil
}

var errLastAdmin = errors.New("can't remove or demote the last admin on the team")

// adminStateFileResponse is one row in GET /api/admin/state-files.
type adminStateFileResponse struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

func handleAdminStateFiles(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := userFromContext(r)

		switch r.Method {
		case http.MethodGet:
			files, err := store.ListStateFiles(r.Context(), db, user.TeamID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out := make([]adminStateFileResponse, 0, len(files))
			for _, f := range files {
				out = append(out, adminStateFileResponse{ID: f.ID, Label: f.Label, Path: f.Path})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)

		case http.MethodPost:
			var req struct {
				Label string `json:"label"`
				Path  string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
			if req.Label == "" || req.Path == "" {
				writeError(w, http.StatusBadRequest, "label and path are required")
				return
			}
			id, err := store.CreateStateFile(r.Context(), db, user.TeamID, req.Label, req.Path)
			if err != nil {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(adminStateFileResponse{ID: id, Label: req.Label, Path: req.Path})

		case http.MethodDelete:
			idStr := r.URL.Query().Get("id")
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid or missing id")
				return
			}
			if err := store.DeleteStateFile(r.Context(), db, user.TeamID, id); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					writeError(w, http.StatusNotFound, "no such state file for your team")
				} else {
					writeError(w, http.StatusInternalServerError, err.Error())
				}
				return
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			writeError(w, http.StatusMethodNotAllowed, "use GET, POST or DELETE")
		}
	}
}
