package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"activity-descriptor/internal/domain"
)

type Server struct {
	store Store
}

func NewServer(store Store) *Server {
	return &Server{store: store}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.handleGetRun)
	mux.HandleFunc("GET /api/v1/runs/{id}/tree", s.handleGetTree)
	mux.HandleFunc("GET /api/v1/runs/{id}/answers", s.handleGetAnswers)
	mux.HandleFunc("POST /api/v1/evidences/{id}/approve", s.handleApproveEvidence)
	mux.HandleFunc("GET /api/v1/runs/{id}/export", s.handleExportRun)

	return requireAuth(mux)
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") || strings.TrimSpace(h[len("Bearer "):]) == "" {
			writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type createRunRequest struct {
	Profession string `json:"profession"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
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
