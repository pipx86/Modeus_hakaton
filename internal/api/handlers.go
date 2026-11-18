package api

import (
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/idempotency"
	"activity-descriptor/internal/logging" // === NEW: Импорт логгера ===
	"activity-descriptor/internal/sse"
	"activity-descriptor/internal/steps"
	"activity-descriptor/internal/store"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// chatStarter is the surface we need from chat.Service (allows mocking in tests).
type chatStarter interface {
	Start(runID, profession string) error
	Messages(runID string) ([]chat.Message, error)
	Post(runID, content string) (chat.Message, error)
	Subscribe(runID string) (<-chan chat.Event, func(), error)
}

type Server struct {
	store        Store
	autoStart    bool
	chat         chatStarter
	idemStore    idempotency.Store
	eventStore   sse.EventStore
	ssePoll      time.Duration
	sseHeartbeat time.Duration
}

func NewServer(store Store) *Server {
	return NewServerWithChat(store, nil)
}

func NewServerWithChat(store Store, chatService chatStarter) *Server {
	hb := 15 * time.Second
	if v := os.Getenv("SSE_HEARTBEAT_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hb = time.Duration(n) * time.Second
		}
	}
	server := &Server{
		store:        store,
		autoStart:    true,
		chat:         chatService,
		idemStore:    idempotency.NewMemStore(time.Hour),
		eventStore:   sse.NewMemEventStore(1000),
		ssePoll:      200 * time.Millisecond,
		sseHeartbeat: hb,
	}
	if pg, ok := store.(*PGStore); ok {
		server.eventStore = sse.NewPGEventStore(pg.Pool)
	}
	return server
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Статическая страница и спецификация API.
	mux.Handle("/swagger/", http.StripPrefix("/swagger/", http.FileServer(http.Dir("./api-docs"))))

	mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.handleGetRun)
	mux.HandleFunc("PUT /api/v1/runs/{id}/tree", s.handlePutTree)
	mux.HandleFunc("GET /api/v1/runs/{id}/tree", s.handleGetTree)
	mux.HandleFunc("POST /api/v1/runs/{id}/snapshots", s.handleCreateSnapshot)
	mux.HandleFunc("GET /api/v1/runs/{id}/snapshots", s.handleListSnapshots)
	mux.HandleFunc("GET /api/v1/runs/{id}/snapshots/{snapshot_id}", s.handleGetSnapshot)
	mux.HandleFunc("GET /api/v1/runs/{id}/answers", s.handleGetAnswers)
	mux.HandleFunc("POST /api/v1/evidences/{id}/approve", s.handleApproveEvidence)
	mux.HandleFunc("GET /api/v1/runs/{id}/export", s.handleExportRun)
	mux.HandleFunc("GET /api/v1/runs/{id}/recommendations", s.handleGetRecommendations)
	mux.HandleFunc("GET /api/v1/runs/{id}/events", s.handleSSE)
	mux.HandleFunc("GET /api/v1/runs/{id}/messages", s.handleGetMessages)
	mux.HandleFunc("POST /api/v1/runs/{id}/messages", s.handlePostMessage)
	mux.HandleFunc("GET /api/v1/runs/{id}/search", s.handleSearch)
	mux.HandleFunc("POST /api/v1/sources/{id}/index", s.handleIndexSource)
	mux.HandleFunc("PATCH /api/v1/nodes/{id}", s.handlePatchNode)
	mux.HandleFunc("POST /api/v1/evidences/bulk-approve", s.handleBulkApprove)
	mux.HandleFunc("DELETE /api/v1/sources/{id}", s.handleDeleteSource)
	mux.HandleFunc("DELETE /api/v1/runs/{id}", s.handleDeleteRun)
	mux.HandleFunc("GET /api/v1/users", s.handleListUsers)
	mux.HandleFunc("GET /api/v1/runs", s.handleListRuns)
	mux.HandleFunc("POST /api/v1/sources", s.handleCreateSource)
	mux.HandleFunc("GET /api/v1/sources", s.handleListSources)
	mux.HandleFunc("GET /api/v1/sources/{id}", s.handleGetSource)
	mux.HandleFunc("GET /api/v1/evidences", s.handleListEvidences)
	mux.HandleFunc("GET /api/v1/evidences/{id}", s.handleGetEvidence)
	mux.HandleFunc("POST /api/v1/evidences", s.handleCreateEvidence)

	root := http.NewServeMux()
	root.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	protected := idempotency.Middleware(s.idemStore, time.Hour, mux)
	root.Handle("/", auth.MiddlewareWithWhitelist(protected, []string{"/swagger"}))
	return withCORS(root)
}

type createRunRequest struct {
	Profession string `json:"profession"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// mapStoreError writes the appropriate HTTP status for known store/sentinel errors.
// Returns true if the error was handled.
func mapStoreError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "not found")
		return true
	case errors.Is(err, ErrAlreadyApproved):
		writeErr(w, http.StatusConflict, "ALREADY_APPROVED", "evidence already approved")
		return true
	case errors.Is(err, ErrConflict):
		writeErr(w, http.StatusConflict, "ALREADY_EXISTS", "conflict")
		return true
	case errors.Is(err, ErrServiceUnavailable):
		writeErr(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service temporarily unavailable")
		return true
	case errors.Is(err, ErrGatewayTimeout):
		writeErr(w, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT", "upstream timeout")
		return true
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT", "operation timed out")
		return true
	case errors.Is(err, ErrTooManyRequests):
		writeErr(w, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "too many requests")
		return true
	default:
		return false
	}
}

// handleCreateRun godoc
// @Summary      Create a new research run
// @Description  Starts an asynchronous analysis pipeline for the given profession. Returns immediately with run ID; progress streamed via SSE.
// @Tags         runs
// @Accept       json
// @Produce      json
// @Param        body  body      object{profession=string,type=string}  true  "Run parameters"
// @Success      201   {object}  domain.Run
// @Failure      400   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs [post]

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}

	var req createRunRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logging.Logger.Warn("create_run_bad_request", "error", err)
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	req.Profession = strings.TrimSpace(req.Profession)
	if req.Profession == "" {
		logging.Logger.Warn("create_run_bad_request", "error", "profession is empty")
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "profession is required")
		return
	}

	run, err := s.store.CreateRun(req.Profession)
	if err != nil {
		logging.Logger.Error("create_run_store_error", "error", err)
		if mapStoreError(w, err) {
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	initialEvent := sse.NewEvent(run.ID, sse.EventStageChange, map[string]any{
		"stage":   string(domain.StageCreated),
		"status":  string(domain.RunPending),
		"message": "Research created",
	})

	if appendErr := s.eventStore.Append(initialEvent); appendErr != nil {
		logging.Logger.Error("event_append_failed", "run_id", run.ID, "error", appendErr)
	}

	if s.chat != nil {
		if pgStore, ok := s.store.(*PGStore); ok {
			if err := pgStore.SetRunType(run.ID, domain.RunTypeChat); err != nil {
				logging.Logger.Error("chat_run_type_store_failed", "run_id", run.ID, "error", err)
				writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
				return
			}
			run.Type = domain.RunTypeChat
		}
		if err := s.store.SetStage(run.ID, domain.StageInterview); err != nil {
			logging.Logger.Error("chat_run_stage_store_failed", "run_id", run.ID, "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		run.Stage = domain.StageInterview
		if err := s.chat.Start(run.ID, run.Profession); err != nil {
			_ = s.store.SetStage(run.ID, domain.StageFailed)
			logging.Logger.Error("chat_start_failed", "run_id", run.ID, "error", err)
			writeErr(w, http.StatusBadGateway, "BAD_GATEWAY", err.Error())
			return
		}
		stageEvent := sse.NewEvent(run.ID, sse.EventStageChange, map[string]any{
			"stage":  string(domain.StageInterview),
			"status": string(domain.RunRunning),
		})
		if err := s.eventStore.Append(stageEvent); err != nil {
			logging.Logger.Error("chat_stage_event_failed", "run_id", run.ID, "error", err)
		}
		writeJSON(w, http.StatusCreated, run)
		return
	}

	if pgStore, ok := s.store.(*PGStore); s.autoStart && ok && pgStore.Pool != nil {
		st := store.New(pgStore.Pool)
		reader := store.NewVacancyAdapter(st)
		realSteps := []domain.Step{
			&steps.InterviewStep{},
			&steps.AnalysisStep{},
			&steps.SynthesisStep{},
			&steps.ReviewStep{TopK: 10, Store: pgStore},
		}

		go func(id string) {
			bgCtx := context.Background()
			workerRun, getErr := pgStore.GetRun(id)
			if getErr != nil || workerRun == nil {
				logging.Logger.Error("orchestrator_fetch_failed", "run_id", id, "error", getErr)
				return
			}

			logging.Logger.Info("orchestrator_started", "run_id", id)
			if err := domain.Orchestrate(bgCtx, workerRun, realSteps, eventRunStore{pgStore, s.eventStore}, reader, 2, 30*time.Second); err != nil {
				logging.Logger.Error("orchestrator_pipeline_failed", "run_id", id, "error", err)
			} else {
				logging.Logger.Info("orchestrator_completed_successfully", "run_id", id)
			}
		}(run.ID)
	}

	writeJSON(w, http.StatusCreated, run)
}

// handleGetRun godoc
// @Summary      Get run details by ID
// @Description  Retrieves full information about a specific research run including nodes and evidence.
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200   {object}  domain.Run
// @Failure      404   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id} [get]
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent, auth.RoleGuest) {
		return
	}
	id := r.PathValue("id")

	run, err := s.store.GetRun(id)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("get_run_internal_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, run)
}

// handlePutTree godoc
// @Summary      Save competency tree for a run
// @Description  Replaces or creates the full node hierarchy for the specified run. Validates structure before saving.
// @Tags         trees
// @Accept       json
// @Produce      json
// @Param        id   path      string          true  "Run ID"
// @Param        body body      []domain.Node   true  "Array of nodes forming the tree"
// @Success      204   "No Content"
// @Failure      400   {object}  errorBody
// @Failure      409   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/tree [put]
func (s *Server) handlePutTree(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	id := r.PathValue("id")

	var nodes []domain.Node
	if err := json.NewDecoder(r.Body).Decode(&nodes); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	if err := domain.ValidateTree(nodes); err != nil {
		var tvErr *domain.TreeValidationError
		if errors.As(err, &tvErr) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"code":    tvErr.Code,
				"details": tvErr.Details,
			})
			return
		}
		writeErr(w, http.StatusUnprocessableEntity, "TREE_VALIDATION_FAILED", err.Error())
		return
	}

	pgStore, ok := s.store.(*PGStore)
	if !ok {
		logging.Logger.Error("save_tree_requires_pgstore", "run_id", id)
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "tree write requires PGStore")
		return
	}

	if err := pgStore.SaveTree(r.Context(), id, nodes); err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("save_tree_db_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleGetTree godoc
// @Summary      Get competency tree for a run
// @Description  Returns the flat list of nodes associated with the run's tree.
// @Tags         trees
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200   {array}   domain.Node
// @Failure      404   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/tree [get]
func (s *Server) handleGetTree(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleGuest) {
		return
	}
	id := r.PathValue("id")

	nodes, err := s.store.GetTree(id)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("get_tree_internal_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	if nodes == nil {
		nodes = []domain.Node{}
	}
	writeJSON(w, http.StatusOK, nodes)
}

// handleGetAnswers godoc
// @Summary      Get collected answers (evidence) for a run
// @Description  Returns all raw quotes and snippets gathered during the interview phase.
// @Tags         evidences
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200   {array}   domain.Evidence
// @Failure      404   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/answers [get]
func (s *Server) handleGetAnswers(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleGuest) {
		return
	}
	id := r.PathValue("id")

	answers, err := s.store.GetAnswers(id)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("get_answers_internal_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	if answers == nil {
		answers = []domain.Evidence{}
	}
	writeJSON(w, http.StatusOK, answers)
}

// handleApproveEvidence godoc
// @Summary      Approve a single piece of evidence
// @Description  Marks a specific quote/snippet as verified and usable in synthesis.
// @Tags         evidences
// @Produce      json
// @Param        id   path      string  true  "Evidence ID"
// @Success      204   "No Content"
// @Failure      404   {object}  errorBody
// @Failure      409   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/evidences/{id}/approve [post]
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
			logging.Logger.Error("approve_evidence_internal_error", "evidence_id", id, "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleExportRun godoc
// @Summary      Export final results of a completed run
// @Description  Returns the finalized competency tree only if the run status is 'done'.
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200   {array}   domain.Node
// @Failure      404   {object}  errorBody
// @Failure      409   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/export [get]
func (s *Server) handleExportRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleGuest) {
		return
	}
	id := r.PathValue("id")

	run, err := s.store.GetRun(id)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("export_get_run_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	if run.Status != domain.RunDone {
		writeErr(w, http.StatusConflict, "RUN_NOT_FINISHED", "run is not finished yet")
		return
	}

	nodes, err := s.store.GetTree(run.ID)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("export_get_tree_error", "run_id", id, "error", err)
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

// handlePatchNode godoc
// @Summary      Update properties of a specific node
// @Description  Modifies title, description, type or parent relationship of a tree node.
// @Tags         nodes
// @Accept       json
// @Produce      json
// @Param        id   path      string           true  "Node ID"
// @Param        body body      NodePatch        true  "Fields to update"
// @Success      200   {object}  domain.Node
// @Failure      404   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/nodes/{id} [patch]
func (s *Server) handlePatchNode(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	nodeID := r.PathValue("id")

	var patch NodePatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	updated, err := s.store.UpdateNode(nodeID, patch)
	if err != nil {
		var validation *domain.TreeValidationError
		if errors.As(err, &validation) {
			writeTreeError(w, err)
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "node not found")
			return
		}
		logging.Logger.Error("update_node_internal_error", "node_id", nodeID, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

type bulkApproveRequest struct {
	IDs []string `json:"ids"`
}

type bulkApproveResponse struct {
	Approved int `json:"approved"`
	Skipped  int `json:"skipped"`
}

// handleBulkApprove godoc
// @Summary      Bulk approve multiple pieces of evidence
// @Description  Verifies several quotes at once. Skips those that are already approved.
// @Tags         evidences
// @Accept       json
// @Produce      json
// @Param        body  body      bulkApproveRequest  true  "List of Evidence IDs"
// @Success      200   {object}  bulkApproveResponse
// @Failure      400   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/evidences/bulk-approve [post]
func (s *Server) handleBulkApprove(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher) {
		return
	}

	var req bulkApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "ids array is required and must not be empty")
		return
	}

	approved, skipped, err := s.store.BulkApproveEvidence(req.IDs)
	if err != nil {
		logging.Logger.Error("bulk_approve_internal_error", "count", len(req.IDs), "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, bulkApproveResponse{Approved: approved, Skipped: skipped})
}

// handleDeleteSource godoc
// @Summary      Delete a data source
// @Description  Удаляет источник только если на него не ссылаются защищённые записи.
// @Tags         sources
// @Produce      json
// @Param        id   path      string  true  "Source ID"
// @Success      204   "No Content"
// @Failure      404   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/sources/{id} [delete]
func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	sourceID := r.PathValue("id")

	if err := s.store.DeleteSource(sourceID); err != nil {
		if errors.Is(err, ErrConflict) {
			writeErr(w, http.StatusConflict, "SOURCE_IN_USE", "источник используется и не может быть удалён")
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "source not found")
			return
		}
		logging.Logger.Error("delete_source_internal_error", "source_id", sourceID, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteRun godoc
// @Summary      Delete a research run
// @Description  Удаляет исследование вместе с деревом, источниками и сообщениями (каскадно).
// @Tags         runs
// @Param        id   path      string  true  "Run ID"
// @Success      204   "No Content"
// @Failure      404   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id} [delete]
func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	runID := r.PathValue("id")

	if err := s.store.DeleteRun(runID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
			return
		}
		logging.Logger.Error("delete_run_internal_error", "run_id", runID, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleListRuns godoc
// @Summary      List all research runs
// @Description  Returns a list of all existing runs with their current status and stage.
// @Tags         runs
// @Produce      json
// @Success      200   {array}   domain.Run
// @Security     BearerAuth
// @Router       /api/v1/runs [get]
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleGuest) {
		return
	}

	runs, err := s.store.ListRuns()
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("list_runs_internal_error", "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	if runs == nil {
		runs = []*domain.Run{}
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleCreateSource godoc
// @Summary      Create a data source
// @Description  Adds a new vacancy, interview transcript, or standard document to the system.
// @Tags         sources
// @Accept       json
// @Produce      json
// @Param        body  body      object{type=string,title=string,content=string}  true  "Source parameters"
// @Success      201   {object}  domain.Source
// @Failure      400   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/sources [post]
func (s *Server) handleCreateSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher) {
		return
	}

	var req struct {
		Type      domain.SourceType `json:"type"`
		Title     string            `json:"title"`
		Content   string            `json:"content,omitempty"`
		SourceURL string            `json:"source_url,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" || (req.Type != domain.SourceVacancy && req.Type != domain.SourceInterview && req.Type != domain.SourceStandard && req.Type != domain.SourceDocument) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "нужны название и допустимый тип источника")
		return
	}

	src := domain.Source{
		Type:      req.Type,
		Title:     req.Title,
		Content:   req.Content,
		SourceURL: req.SourceURL,
	}

	created, err := s.store.CreateSource(src)
	if err != nil {
		logging.Logger.Error("create_source_internal_error", "title", src.Title, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, created)
}
