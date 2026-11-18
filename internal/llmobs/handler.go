package llmobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
)

// StoreAPI is the part of Store used by the HTTP handler.
type StoreAPI interface {
	ListRequests(ctx context.Context, filter ListFilter) ([]Request, error)
	GetRequest(ctx context.Context, id string) (Request, error)
	ListPromptVersions(ctx context.Context, agent string) ([]PromptVersion, error)
	ActivatePromptVersion(ctx context.Context, id, activatedBy, reason string) error
	UsageByModel(ctx context.Context, since time.Time) ([]ModelUsage, error)
}

// NewHandler returns an authenticated /api/v1/llm handler.
func NewHandler(store StoreAPI) http.Handler {
	mux := http.NewServeMux()
	h := &handler{store: store}
	mux.HandleFunc("GET /api/v1/llm/requests", h.listRequests)
	mux.HandleFunc("GET /api/v1/llm/requests/{id}", h.getRequest)
	mux.HandleFunc("GET /api/v1/llm/prompt-versions", h.listPromptVersions)
	mux.HandleFunc("POST /api/v1/llm/prompt-versions/activate", h.activatePromptVersion)
	mux.HandleFunc("GET /api/v1/llm/usage", h.usage)
	return mux
}

type handler struct {
	store StoreAPI
}

func (h *handler) listRequests(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent) {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	items, err := h.store.ListRequests(r.Context(), ListFilter{
		RunID:  strings.TrimSpace(r.URL.Query().Get("run_id")),
		Agent:  strings.TrimSpace(r.URL.Query().Get("agent")),
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Limit:  limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) getRequest(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent) {
		return
	}
	item, err := h.store.GetRequest(r.Context(), r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "llm request not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) listPromptVersions(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent) {
		return
	}
	items, err := h.store.ListPromptVersions(r.Context(), strings.TrimSpace(r.URL.Query().Get("agent")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type activateRequest struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func (h *handler) activatePromptVersion(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	var req activateRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "id is required")
		return
	}
	if err := h.store.ActivatePromptVersion(r.Context(), req.ID, auth.Subject(r.Context()), req.Reason); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "prompt version not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "activated"})
}

// usage отдаёт расход токенов по моделям за период (?since=24h).
func (h *handler) usage(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent) {
		return
	}
	since := time.Now().Add(-24 * time.Hour)
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 && parsed <= 30*24*time.Hour {
			since = time.Now().Add(-parsed)
		}
	}
	items, err := h.store.UsageByModel(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"since": since.UTC().Format(time.RFC3339),
		"items": items,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
