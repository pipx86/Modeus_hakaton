package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/idempotency"
)

type Server struct {
	store        Store
	idemStore    idempotency.Store
	ssePoll      time.Duration
	sseHeartbeat time.Duration
}

func NewServer(store Store) *Server {
	hb := 15 * time.Second
	if v := os.Getenv("SSE_HEARTBEAT_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hb = time.Duration(n) * time.Second
		}
	}
	return &Server{
		store:        store,
		idemStore:    idempotency.NewMemStore(time.Hour), // дефолтный TTL 1 час
		ssePoll:      200 * time.Millisecond,
		sseHeartbeat: hb,
	}
}

// Routes собирает HTTP-маршрутизатор.
// Порядок слоёв: root (health) -> auth.JWT -> idempotency -> mux (handlers).
// Идемпотентность применяется только к POST/PUT/PATCH внутри защищённой зоны.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.handleGetRun)
	mux.HandleFunc("GET /api/v1/runs/{id}/tree", s.handleGetTree)
	mux.HandleFunc("GET /api/v1/runs/{id}/answers", s.handleGetAnswers)
	mux.HandleFunc("POST /api/v1/evidences/{id}/approve", s.handleApproveEvidence)
	mux.HandleFunc("GET /api/v1/runs/{id}/export", s.handleExportRun)
	mux.HandleFunc("GET /api/v1/runs/{id}/events", s.handleSSE)

	root := http.NewServeMux()
	root.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Цепочка: JWT -> Idempotency -> Handlers
	protected := idempotency.Middleware(s.idemStore, time.Hour, mux)
	root.Handle("/", auth.Middleware(protected))
	return root
}

type createRunRequest struct {
	Profession string `json:"profession"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}

	var req createRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Profession == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "profession is required")
		return
	}

	run, err := s.store.CreateRun(req.Profession)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent) {
		return
	}
	id := r.PathValue("id")

	run, err := s.store.GetRun(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleGetTree(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	id := r.PathValue("id")

	nodes, err := s.store.GetTree(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	if nodes == nil {
		nodes = []domain.Node{}
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) handleGetAnswers(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	id := r.PathValue("id")

	answers, err := s.store.GetAnswers(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	if answers == nil {
		answers = []domain.Evidence{}
	}
	writeJSON(w, http.StatusOK, answers)
}

func (s *Server) handleApproveEvidence(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher) {
		return
	}
	id := r.PathValue("id")

	err := s.store.ApproveEvidence(id)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "evidence not found")
		case errors.Is(err, ErrAlreadyApproved):
			writeErr(w, http.StatusConflict, "ALREADY_APPROVED", "evidence already approved")
		default:
			writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleExportRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	id := r.PathValue("id")

	run, err := s.store.GetRun(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	if run.Status != domain.RunDone {
		writeErr(w, http.StatusConflict, "RUN_NOT_FINISHED", "run is not finished yet")
		return
	}

	nodes, err := s.store.GetTree(run.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	if nodes == nil {
		nodes = []domain.Node{}
	}
	writeJSON(w, http.StatusOK, nodes)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Code: code, Message: msg})
}

// --- helpers for live curl demonstration (card 20 acceptance) ---

func NewServerForDemo() *Server { return NewServer(newMemStore()) }

func (s *Server) DemoRunID() string {
	r, _ := s.store.CreateRun("cook")
	return r.ID
}

func (s *Server) DemoSetStage(id string, stage domain.Stage) error {
	ms, ok := s.store.(*memStore)
	if !ok {
		return ErrNotFound
	}
	return ms.SetStage(id, stage)
}

func (s *Server) DemoSetStatus(id string, status domain.RunStatus) error {
	ms, ok := s.store.(*memStore)
	if !ok {
		return ErrNotFound
	}
	return ms.SetStatus(id, status)
}
