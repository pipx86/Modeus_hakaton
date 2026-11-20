package api

import (
	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/idempotency"
	"activity-descriptor/internal/ingestion"
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
	importFiles  ingestion.ImportFiles
	store        Store
	autoStart    bool
	chat         chatStarter
	idemStore    idempotency.Store
	eventStore   sse.EventStore
	ssePoll      time.Duration
	sseHeartbeat time.Duration
	batchPlan    steps.BatchPlan
	learningPlan LearningPlanner
	github       githubEvaluator
	// indexSource — фоновая индексация новых источников (фрагменты + векторы).
	// nil означает, что автоиндексация не включена (in-memory store, demo API
	// или явно отключённая настройка SOURCE_AUTO_INDEX).
	indexSource sourceIndexFunc
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

// SetBatchPlan wires the batch orchestrator chain built in cmd/server. The
// create-run handler and the resume processor both read the same plan, so the
// AI agent pipeline cannot diverge between the two paths.
func (s *Server) SetBatchPlan(plan steps.BatchPlan) { s.batchPlan = plan }

// SetLearningPlanner wires the optional learning-plan generator.
func (s *Server) SetLearningPlanner(planner LearningPlanner) { s.learningPlan = planner }

// batchPipeline returns the configured batch plan, or the vacancy-only default
// when the server was built without the cmd/server LLM wiring (tests, demo
// binaries). The default keeps the historical behaviour for those callers.
func (s *Server) batchPipeline(store steps.AITreeStore) steps.BatchPlan {
	if len(s.batchPlan.AI) > 0 || len(s.batchPlan.Fallback) > 0 {
		return s.batchPlan
	}
	return steps.NewBatchPlan(steps.BatchOptions{Store: store, VacancyTopK: 10})
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Статическая страница и спецификация API.
	mux.Handle("/swagger/", http.StripPrefix("/swagger/", http.FileServer(http.Dir("./docs"))))

	mux.HandleFunc("GET /api/v1/subscriptions", s.handlePublicationSubscriptions)
	mux.HandleFunc("POST /api/v1/subscriptions", s.handleCreatePublicationSubscription)
	mux.HandleFunc("DELETE /api/v1/subscriptions/{id}", s.handleDeletePublicationSubscription)
	mux.HandleFunc("GET /api/v1/notifications", s.handlePublicationNotifications)
	mux.HandleFunc("GET /api/v1/notifications/unread-count", s.handlePublicationUnreadCount)
	mux.HandleFunc("POST /api/v1/notifications/{id}/read", s.handleReadPublicationNotification)
	mux.HandleFunc("GET /api/v1/internal/subscriptions", s.handlePublicationPollingTargets)
	mux.HandleFunc("POST /api/v1/webhooks/new-publications", s.handlePublicationUpdates)
	mux.HandleFunc("GET /api/v1/academic/programs", s.handleAcademicPrograms)
	mux.HandleFunc("GET /api/v1/academic/programs/{id}", s.handleAcademicProgram)
	mux.HandleFunc("GET /api/v1/academic/programs/{id}/summary", s.handleAcademicSummary)
	mux.HandleFunc("POST /api/v1/academic/analyze-gap-local", s.handleLocalAcademicGap)
	mux.HandleFunc("GET /api/v1/taxonomies", s.handleLocalTaxonomies)
	mux.HandleFunc("GET /api/v1/taxonomies/{id}", s.handleLocalTaxonomy)
	mux.HandleFunc("GET /api/v1/forecast/observed", s.handleObservedSkills)
	mux.HandleFunc("GET /api/v1/forecast/all", s.handleAllSkillForecasts)
	mux.HandleFunc("GET /api/v1/forecast/top", s.handleTopSkillForecasts)
	mux.HandleFunc("GET /api/v1/forecast/popular", s.handlePopularSkillForecasts)
	mux.HandleFunc("GET /api/v1/forecast/engine", s.handleSkillForecastStatus)
	mux.HandleFunc("GET /api/v1/forecast/{skill}", s.handleSkillForecast)
	mux.HandleFunc("GET /api/v1/vacancies", s.handleVacancyCatalog)
	mux.HandleFunc("GET /api/v1/vacancies/{id}", s.handleVacancyDetail)
	mux.HandleFunc("GET /api/v1/vacancies/info", s.handleVacancyInfo)
	mux.HandleFunc("GET /api/v1/vacancies/stats/summary", s.handleVacancyStats)
	mux.HandleFunc("GET /api/v1/vacancies/stats/analytics", s.handleVacancyAnalytics)
	mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	mux.HandleFunc("POST /api/v1/runs/{id}/sources", s.handleAttachRunSource)
	mux.HandleFunc("GET /api/v1/runs/estimate", s.handleRunEstimate)
	mux.HandleFunc("GET /api/v1/runs/{id}/cost", s.handleRunCost)
	mux.HandleFunc("GET /api/v1/runs/{id}/insights", s.handleRunInsights)
	mux.HandleFunc("GET /api/v1/runs/{id}/coverage", s.handleRunCoverage)
	mux.HandleFunc("GET /api/v1/runs/{id}/compare", s.handleCompareRuns)
	mux.HandleFunc("GET /api/v1/runs/{id}/zun/suggestions", s.handleZunSuggestions)
	mux.HandleFunc("GET /api/v1/runs/{id}/learning-plan", s.handleGetLearningPlan)
	mux.HandleFunc("POST /api/v1/runs/{id}/zun/suggestions/apply", s.handleApplyZunSuggestion)
	mux.HandleFunc("POST /api/v1/runs/{id}/zun/suggestions/reject", s.handleRejectZunSuggestion)
	mux.HandleFunc("GET /api/v1/runs/{id}/evidence/{evidence_id}/context", s.handleEvidenceContext)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.handleGetRun)
	mux.HandleFunc("PUT /api/v1/runs/{id}/tree", s.handlePutTree)
	mux.HandleFunc("GET /api/v1/runs/{id}/tree", s.handleGetTree)
	mux.HandleFunc("POST /api/v1/runs/{id}/snapshots", s.handleCreateSnapshot)
	mux.HandleFunc("GET /api/v1/runs/{id}/snapshots", s.handleListSnapshots)
	mux.HandleFunc("GET /api/v1/runs/{id}/snapshots/{snapshot_id}", s.handleGetSnapshot)
	mux.HandleFunc("GET /api/v1/runs/{id}/snapshots/{snapshot_id}/diff", s.handleSnapshotDiff)
	mux.HandleFunc("GET /api/v1/reviews/evidence", s.handleReviewQueue)
	mux.HandleFunc("GET /api/v1/reviews/evidence/progress", s.handleReviewProgress)
	mux.HandleFunc("GET /api/v1/runs/{id}/answers", s.handleGetAnswers)
	mux.HandleFunc("POST /api/v1/evidences/{id}/approve", s.handleApproveEvidence)
	mux.HandleFunc("POST /api/v1/evidences/{id}/reject", s.handleRejectEvidence)
	mux.HandleFunc("GET /api/v1/runs/{id}/export", s.handleExportRun)
	mux.HandleFunc("GET /api/v1/runs/{id}/events", s.handleSSE)
	mux.HandleFunc("GET /api/v1/runs/{id}/messages", s.handleGetMessages)
	mux.HandleFunc("POST /api/v1/runs/{id}/messages", s.handlePostMessage)
	mux.HandleFunc("GET /api/v1/runs/{id}/conflicts", s.handleRunConflicts)
	mux.HandleFunc("GET /api/v1/runs/{id}/agent-messages", s.handleRunAgentMessages)
	mux.HandleFunc("GET /api/v1/runs/{id}/search", s.handleSearch)
	mux.HandleFunc("POST /api/v1/sources/{id}/index", s.handleIndexSource)
	mux.HandleFunc("PATCH /api/v1/nodes/{id}", s.handlePatchNode)
	mux.HandleFunc("POST /api/v1/evidences/bulk-approve", s.handleBulkApprove)
	mux.HandleFunc("DELETE /api/v1/sources/{id}", s.handleDeleteSource)
	mux.HandleFunc("DELETE /api/v1/runs/{id}", s.handleDeleteRun)
	mux.HandleFunc("GET /api/v1/users", s.handleListUsers)
	mux.HandleFunc("PATCH /api/v1/users/{id}/role", s.handleUpdateUserRole)
	mux.HandleFunc("POST /api/v1/github/evaluate", s.handleEvaluateGitHub)
	mux.HandleFunc("GET /api/v1/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/v1/dashboard/correlation", s.handleDashboardCorrelation)
	mux.HandleFunc("GET /api/v1/dashboard/skills", s.handleDashboardSkills)
	mux.HandleFunc("GET /api/v1/admin/sources/quality", s.handleSourceQuality)
	mux.HandleFunc("POST /api/v1/sources", s.handleCreateSource)
	mux.HandleFunc("POST /api/v1/sources/upload", s.handleUploadSource)
	mux.HandleFunc("GET /api/v1/sources", s.handleListSources)
	mux.HandleFunc("GET /api/v1/sources/{id}", s.handleGetSource)
	mux.HandleFunc("GET /api/v1/evidences", s.handleListEvidences)
	mux.HandleFunc("GET /api/v1/evidences/{id}", s.handleGetEvidence)
	mux.HandleFunc("PATCH /api/v1/evidences/{id}", s.handleUpdateEvidence)
	mux.HandleFunc("POST /api/v1/evidences", s.handleCreateEvidence)

	root := http.NewServeMux()
	root.Handle("POST /api/v1/ingest", auth.Middleware(http.HandlerFunc(s.handleFileImport)))
	root.HandleFunc("GET /api/v1/ready", s.handleReady)
	root.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	root.HandleFunc("POST /api/v1/auth/register", s.handleRegister)
	root.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	protected := idempotency.Middleware(s.idemStore, time.Hour, mux)
	root.Handle("/", auth.MiddlewareWithWhitelist(protected, []string{"/swagger"}))
	return withCORS(root)
}

type createRunRequest struct {
	Profession string `json:"profession"`
	// Type selects chat or batch. Empty preserves the historical behaviour:
	// chat when a chat service is configured, otherwise batch.
	Type string `json:"type,omitempty"`
	// Transcript optionally supplies the interview transcript of a batch run.
	// With an LLM-backed batch plan the analyst consumes it and the resulting
	// tree goes through the same synthesizer/review stages as the vacancy
	// pipeline. Chat runs ignore it (the chat service collects the dialog).
	Transcript string `json:"transcript,omitempty"`
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
// @Param        body  body      object{profession=string,transcript=string}  true  "Run parameters"
// @Success      201   {object}  domain.Run
// @Failure      400   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs [post]

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
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

	runType := domain.RunTypeBatch
	switch strings.ToLower(strings.TrimSpace(req.Type)) {
	case "":
		if s.chat != nil {
			runType = domain.RunTypeChat
		}
	case string(domain.RunTypeChat):
		runType = domain.RunTypeChat
	case string(domain.RunTypeBatch):
		runType = domain.RunTypeBatch
	case string(domain.RunTypeLearning):
		if s.learningPlan == nil {
			writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "генератор плана обучения не настроен")
			return
		}
		runType = domain.RunTypeLearning
	default:
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "type must be chat, batch or learning")
		return
	}
	if runType == domain.RunTypeChat && s.chat == nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "chat pipeline is not available")
		return
	}
	providedTranscript := strings.TrimSpace(req.Transcript)
	if pg, ok := s.store.(*PGStore); ok && pg.Pool != nil && s.autoStart &&
		runType == domain.RunTypeBatch && providedTranscript != "" && !s.batchPlan.HasAI() {
		writeErr(w, http.StatusServiceUnavailable, "LLM_UNAVAILABLE", "анализ транскрипта недоступен: проверьте настройки модели")
		return
	}

	var run *domain.Run
	var err error
	if creator, ok := s.store.(runOwnerCreator); ok {
		run, err = creator.CreateRunForUser(req.Profession, auth.Subject(r.Context()))
	} else {
		run, err = s.store.CreateRun(req.Profession)
	}
	if err != nil {
		logging.Logger.Error("create_run_store_error", "error", err)
		if mapStoreError(w, err) {
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось создать исследование")
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

	if runType == domain.RunTypeChat {
		if pgStore, ok := s.store.(*PGStore); ok {
			if err := pgStore.SetRunType(run.ID, domain.RunTypeChat); err != nil {
				logging.Logger.Error("chat_run_type_store_failed", "run_id", run.ID, "error", err)
				writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось создать исследование")
				return
			}
			run.Type = domain.RunTypeChat
		}
		if err := s.store.SetStage(run.ID, domain.StageInterview); err != nil {
			logging.Logger.Error("chat_run_stage_store_failed", "run_id", run.ID, "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось создать исследование")
			return
		}
		run.Stage = domain.StageInterview
		if err := s.chat.Start(run.ID, run.Profession); err != nil {
			_ = s.store.SetStage(run.ID, domain.StageFailed)
			logging.Logger.Error("chat_start_failed", "run_id", run.ID, "error", err)
			writeErr(w, http.StatusBadGateway, "BAD_GATEWAY", "не удалось начать интервью")
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

	run.Type = runType
	if runType == domain.RunTypeLearning {
		pgStore, ok := s.store.(*PGStore)
		if !ok {
			writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "план обучения требует PostgreSQL")
			return
		}
		starter, ok := s.chat.(learningChatStarter)
		if !ok || s.chat == nil {
			writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "интервьюер для плана обучения не настроен")
			return
		}
		if err := pgStore.SetRunType(run.ID, domain.RunTypeLearning); err != nil {
			logging.Logger.Error("learning_run_type_store_failed", "run_id", run.ID, "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if err := s.store.SetStage(run.ID, domain.StageInterview); err != nil {
			logging.Logger.Error("learning_run_stage_store_failed", "run_id", run.ID, "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		run.Stage = domain.StageInterview
		if err := starter.StartLearning(run.ID, run.Profession); err != nil {
			_ = s.store.SetStage(run.ID, domain.StageFailed)
			logging.Logger.Error("learning_chat_start_failed", "run_id", run.ID, "error", err)
			writeErr(w, http.StatusBadGateway, "BAD_GATEWAY", err.Error())
			return
		}
		stageEvent := sse.NewEvent(run.ID, sse.EventStageChange, map[string]any{
			"stage":  string(domain.StageInterview),
			"status": string(domain.RunRunning),
		})
		if err := s.eventStore.Append(stageEvent); err != nil {
			logging.Logger.Error("learning_stage_event_failed", "run_id", run.ID, "error", err)
		}
		writeJSON(w, http.StatusCreated, run)
		return
	}

	if pgStore, ok := s.store.(*PGStore); runType == domain.RunTypeBatch && ok {
		if err := pgStore.SetRunType(run.ID, domain.RunTypeBatch); err != nil {
			logging.Logger.Error("batch_run_type_store_failed", "run_id", run.ID, "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось создать исследование")
			return
		}
	}

	if pgStore, ok := s.store.(*PGStore); s.autoStart && ok && pgStore.Pool != nil {
		st := store.New(pgStore.Pool)
		reader := store.NewVacancyAdapter(st)
		plan := s.batchPipeline(pgStore)

		go func(id string) {
			bgCtx := context.Background()
			workerRun, getErr := pgStore.GetRun(id)
			if getErr != nil || workerRun == nil {
				logging.Logger.Error("orchestrator_fetch_failed", "run_id", id, "error", getErr)
				return
			}

			// The batch create API has no interviewer, so the only real
			// transcript input is the one supplied with the request. Persist it
			// as the run's interview source before the AI steps run, which makes
			// the same transcript recoverable by the resume processor. A failed
			// persist does not abort the live run: the review step creates the
			// source itself, only restarability is lost.
			hasTranscript := false
			if providedTranscript != "" && plan.HasAI() {
				sourceID, persistErr := steps.PersistInterviewTranscript(bgCtx, pgStore, id, providedTranscript)
				if persistErr != nil {
					logging.Logger.Error("batch_transcript_persist_failed", "run_id", id, "error", persistErr)
				}
				steps.SetTranscript(workerRun, providedTranscript, sourceID)
				hasTranscript = true
			}
			runSteps, stepTimeout := plan.For(hasTranscript)
			pipeline := "vacancy"
			if plan.HasAI() && hasTranscript {
				pipeline = "ai"
			}

			logging.Logger.Info("orchestrator_started", "run_id", id, "pipeline", pipeline)
			if err := domain.Orchestrate(bgCtx, workerRun, runSteps, eventRunStore{pgStore, s.eventStore}, reader, plan.MaxRetries, stepTimeout); err != nil {
				logging.Logger.Error("orchestrator_pipeline_failed", "run_id", id, "pipeline", pipeline, "error", err)
			} else {
				logging.Logger.Info("orchestrator_completed_successfully", "run_id", id, "pipeline", pipeline)
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
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	id := r.PathValue("id")

	run, err := s.store.GetRun(id)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("get_run_internal_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать исследование")
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
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
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
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сохранить дерево")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TreeResponse — ответ GET /runs/{id}/tree: узлы и детерминированные инсайты
// (провенанс, health, red flags, market heat, coverage, объяснения). Инсайты
// встроены в ответ дерева, чтобы клиенту не нужен был отдельный запрос.
type TreeResponse struct {
	Nodes    []domain.Node     `json:"nodes"`
	Insights analysis.Insights `json:"insights"`
}

// handleGetTree godoc
// @Summary      Get competency tree for a run
// @Description  Returns the flat list of nodes associated with the run's tree plus deterministic insights: provenance per node, tree health score with breakdown, five red-flag rules, market heat, coverage matrix, run sources and template-only node explanations. Insights are computed from the same nodes and are best-effort for stores without source access.
// @Tags         trees
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {object}  api.TreeResponse
// @Failure      404  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/tree [get]
func (s *Server) handleGetTree(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	id := r.PathValue("id")

	nodes, err := s.store.GetTree(id)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("get_tree_internal_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать дерево")
		return
	}

	if nodes == nil {
		nodes = []domain.Node{}
	}

	insights := analysis.Insights{}
	if reader, ok := s.store.(insightsReader); ok {
		if loaded, loadErr := s.loadRunInsights(r.Context(), reader, id); loadErr == nil {
			insights = loaded
		} else {
			logging.Logger.Warn("get_tree_insights_failed", "run_id", id, "error", loadErr)
		}
	}
	if insights.RunID == "" {
		var skillFreq map[string]int
		if run, runErr := s.store.GetRun(id); runErr == nil && run != nil {
			skillFreq = run.SkillFreq
		}
		insights = analysis.BuildInsights(nodes, nil, skillFreq)
		insights.RunID = id
	}

	writeJSON(w, http.StatusOK, TreeResponse{Nodes: nodes, Insights: insights})
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
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	id := r.PathValue("id")

	answers, err := s.store.GetAnswers(id)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("get_answers_internal_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать подтверждения")
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
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist) {
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
		case errors.Is(err, ErrAlreadyRejected):
			writeErr(w, http.StatusConflict, "ALREADY_REJECTED", "evidence already rejected")
		default:
			logging.Logger.Error("approve_evidence_internal_error", "evidence_id", id, "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось подтвердить цитату")
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleRejectEvidence godoc
// @Summary      Reject a single piece of evidence
// @Description  Marks a specific quote/snippet as rejected; only pending evidence can be rejected.
// @Tags         evidences
// @Produce      json
// @Param        id   path      string  true  "Evidence ID"
// @Success      204   "No Content"
// @Failure      404   {object}  errorBody
// @Failure      409   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/evidences/{id}/reject [post]
func (s *Server) handleRejectEvidence(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist) {
		return
	}
	id := r.PathValue("id")

	err := s.store.RejectEvidence(id)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "evidence not found")
		case errors.Is(err, ErrAlreadyRejected):
			writeErr(w, http.StatusConflict, "ALREADY_REJECTED", "evidence already rejected")
		case errors.Is(err, ErrAlreadyApproved):
			writeErr(w, http.StatusConflict, "ALREADY_APPROVED", "evidence already approved")
		default:
			logging.Logger.Error("reject_evidence_internal_error", "evidence_id", id, "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось отклонить цитату")
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
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
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сохранить узел")
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
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist) {
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
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось подтвердить цитаты")
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
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось удалить источник")
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
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	runID := r.PathValue("id")
	run, err := s.store.GetRun(runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось удалить исследование")
		return
	}
	if role, _ := auth.FromContext(r.Context()); role != auth.RoleAdmin {
		owner := auth.Subject(r.Context())
		if !ownerlessRunAllowed(run) && run.CreatedBy != owner {
			writeErr(w, http.StatusForbidden, "FORBIDDEN", "можно удалить только своё исследование")
			return
		}
	}

	if err := s.store.DeleteRun(runID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
			return
		}
		logging.Logger.Error("delete_run_internal_error", "run_id", runID, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось удалить исследование")
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
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}

	runs, err := s.store.ListRuns()
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("list_runs_internal_error", "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать список исследований")
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
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
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
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сохранить источник")
		return
	}
	// Индексируем в фоне так же, как загрузку файла: семантический поиск должен
	// работать для источника, созданного через API, без отдельного вызова
	// POST /sources/{id}/index.
	if created != nil {
		s.indexSourceAsync(created.ID)
	}

	writeJSON(w, http.StatusCreated, created)
}
