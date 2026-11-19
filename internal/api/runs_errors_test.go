package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

type failingRunStore struct {
	Store

	createRunErr  error
	getRunErr     error
	getTreeErr    error
	getAnswersErr error
	listRunsErr   error
}

func (s *failingRunStore) CreateRun(profession string) (*domain.Run, error) {
	if s.createRunErr != nil {
		return nil, s.createRunErr
	}
	return s.Store.CreateRun(profession)
}

func (s *failingRunStore) GetRun(id string) (*domain.Run, error) {
	if s.getRunErr != nil {
		return nil, s.getRunErr
	}
	return s.Store.GetRun(id)
}

func (s *failingRunStore) GetTree(runID string) ([]domain.Node, error) {
	if s.getTreeErr != nil {
		return nil, s.getTreeErr
	}
	return s.Store.GetTree(runID)
}

func (s *failingRunStore) GetAnswers(runID string) ([]domain.Evidence, error) {
	if s.getAnswersErr != nil {
		return nil, s.getAnswersErr
	}
	return s.Store.GetAnswers(runID)
}

func (s *failingRunStore) ListRuns() ([]*domain.Run, error) {
	if s.listRunsErr != nil {
		return nil, s.listRunsErr
	}
	return s.Store.ListRuns()
}

func TestCreateRunEmptyBody(t *testing.T) {
	h := NewServer(newMemStore()).Routes()

	for _, body := range []string{"", " ", `{"profession":" "}`} {
		rr := do(t, h, http.MethodPost, "/api/v1/runs", "teacher", body)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %q: expected 400, got %d", body, rr.Code)
		}
	}
}

func TestCreateRunStoreError(t *testing.T) {
	m := &failingRunStore{
		Store:        newMemStore(),
		createRunErr: errors.New("database unavailable"),
	}

	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodPost, "/api/v1/runs", "teacher", `{"profession":"cook"}`)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}

	assertInternalError(t, rr)
}

func TestGetRunStoreError(t *testing.T) {
	m := &failingRunStore{
		Store:     newMemStore(),
		getRunErr: errors.New("database unavailable"),
	}

	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1", "teacher", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}

	assertInternalError(t, rr)
}

func TestGetRunTreeStoreError(t *testing.T) {
	base := newMemStore()
	base.seedRun(domain.Run{
		ID:         "r1",
		Status:     domain.RunDone,
		Profession: "cook",
		Stage:      domain.StageDone,
	})

	m := &failingRunStore{
		Store:      base,
		getTreeErr: errors.New("database unavailable"),
	}

	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1/tree", "teacher", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}

	assertInternalError(t, rr)
}

func TestGetRunAnswersStoreError(t *testing.T) {
	base := newMemStore()
	base.seedRun(domain.Run{
		ID:         "r1",
		Status:     domain.RunDone,
		Profession: "cook",
		Stage:      domain.StageDone,
	})

	m := &failingRunStore{
		Store:         base,
		getAnswersErr: errors.New("database unavailable"),
	}

	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1/answers", "teacher", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}

	assertInternalError(t, rr)
}

func TestExportRunGetRunStoreError(t *testing.T) {
	m := &failingRunStore{
		Store:     newMemStore(),
		getRunErr: errors.New("database unavailable"),
	}

	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1/export", "teacher", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}

	assertInternalError(t, rr)
}

func TestExportRunGetTreeStoreError(t *testing.T) {
	base := newMemStore()
	base.seedRun(domain.Run{
		ID:         "r1",
		Status:     domain.RunDone,
		Profession: "cook",
		Stage:      domain.StageDone,
	})

	m := &failingRunStore{
		Store:      base,
		getTreeErr: errors.New("database unavailable"),
	}

	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1/export", "teacher", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}

	assertInternalError(t, rr)
}

func TestListRunsStoreError(t *testing.T) {
	m := &failingRunStore{
		Store:       newMemStore(),
		listRunsErr: errors.New("database unavailable"),
	}

	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs", "teacher", "")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}

	assertInternalError(t, rr)
}

func assertInternalError(t *testing.T, rr interface {
	Result() *http.Response
}) {
	t.Helper()

	var body errorBody
	if err := json.NewDecoder(rr.Result().Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if body.Code != "INTERNAL" {
		t.Fatalf("expected error code INTERNAL, got %q", body.Code)
	}

	if body.Message == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestGetRunNotFound(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/missing", "teacher", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestExportRunNotFinished(t *testing.T) {
	base := newMemStore()
	base.seedRun(domain.Run{
		ID:         "r1",
		Status:     domain.RunPending,
		Profession: "cook",
		Stage:      domain.StageCreated,
	})
	h := NewServer(base).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1/export", "teacher", "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSaveTreeNotImplementedOnMemStore(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	// Valid minimal tree that passes ValidateTree (role + evidence)
	body := `[{
		"id": "n1",
		"type": "role",
		"title": "Cook",
		"parent_id": null,
		"evidence": [{"id": "e1", "source_id": "s1", "quote": "test quote"}]
	}]`
	rr := do(t, h, http.MethodPut, "/api/v1/runs/r1/tree", "teacher", body)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", rr.Code, rr.Body.String())
	}
	var eb errorBody
	if err := json.NewDecoder(rr.Result().Body).Decode(&eb); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if eb.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("expected NOT_IMPLEMENTED, got %q", eb.Code)
	}
}

func TestCreateRunServiceUnavailable(t *testing.T) {
	m := &failingRunStore{
		Store:        newMemStore(),
		createRunErr: ErrServiceUnavailable,
	}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodPost, "/api/v1/runs", "teacher", `{"profession":"cook"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "SERVICE_UNAVAILABLE")
}

func TestCreateRunGatewayTimeout(t *testing.T) {
	m := &failingRunStore{
		Store:        newMemStore(),
		createRunErr: ErrGatewayTimeout,
	}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodPost, "/api/v1/runs", "teacher", `{"profession":"cook"}`)
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "GATEWAY_TIMEOUT")
}

func TestGetRunServiceUnavailable(t *testing.T) {
	m := &failingRunStore{
		Store:     newMemStore(),
		getRunErr: ErrServiceUnavailable,
	}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1", "teacher", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "SERVICE_UNAVAILABLE")
}

func TestGetRunGatewayTimeout(t *testing.T) {
	m := &failingRunStore{
		Store:     newMemStore(),
		getRunErr: ErrGatewayTimeout,
	}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1", "teacher", "")
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "GATEWAY_TIMEOUT")
}

func TestGetTreeServiceUnavailable(t *testing.T) {
	base := newMemStore()
	base.seedRun(domain.Run{ID: "r1", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	m := &failingRunStore{Store: base, getTreeErr: ErrServiceUnavailable}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1/tree", "teacher", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "SERVICE_UNAVAILABLE")
}

func TestGetTreeGatewayTimeout(t *testing.T) {
	base := newMemStore()
	base.seedRun(domain.Run{ID: "r1", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	m := &failingRunStore{Store: base, getTreeErr: ErrGatewayTimeout}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1/tree", "teacher", "")
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "GATEWAY_TIMEOUT")
}

func TestGetAnswersServiceUnavailable(t *testing.T) {
	base := newMemStore()
	base.seedRun(domain.Run{ID: "r1", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	m := &failingRunStore{Store: base, getAnswersErr: ErrServiceUnavailable}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1/answers", "teacher", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "SERVICE_UNAVAILABLE")
}

func TestExportRunServiceUnavailable(t *testing.T) {
	m := &failingRunStore{
		Store:     newMemStore(),
		getRunErr: ErrServiceUnavailable,
	}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs/r1/export", "teacher", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "SERVICE_UNAVAILABLE")
}

func TestListRunsServiceUnavailable(t *testing.T) {
	m := &failingRunStore{
		Store:       newMemStore(),
		listRunsErr: ErrServiceUnavailable,
	}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs", "teacher", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "SERVICE_UNAVAILABLE")
}

func TestListRunsGatewayTimeout(t *testing.T) {
	m := &failingRunStore{
		Store:       newMemStore(),
		listRunsErr: ErrGatewayTimeout,
	}
	h := NewServer(m).Routes()
	rr := do(t, h, http.MethodGet, "/api/v1/runs", "teacher", "")
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "GATEWAY_TIMEOUT")
}

func assertErrorCode(t *testing.T, rr interface {
	Result() *http.Response
}, wantCode string) {
	t.Helper()
	var body errorBody
	if err := json.NewDecoder(rr.Result().Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if body.Code != wantCode {
		t.Fatalf("expected error code %q, got %q (msg=%q)", wantCode, body.Code, body.Message)
	}
	if body.Message == "" {
		t.Fatal("expected non-empty error message")
	}
}

// failingChat is a test double that always fails Start (to exercise 502).
type failingChat struct{}

func (failingChat) Start(runID, profession string) error {
	return errors.New("llm provider unavailable")
}
func (failingChat) Messages(runID string) ([]chat.Message, error) {
	return nil, errors.New("not implemented")
}
func (failingChat) Post(runID, content string) (chat.Message, error) {
	return chat.Message{}, errors.New("not implemented")
}
func (failingChat) Subscribe(runID string) (<-chan chat.Event, func(), error) {
	return nil, func() {}, errors.New("not implemented")
}

func TestCreateRunBadGateway(t *testing.T) {
	// Force the chat path so Start is called and returns 502
	h := NewServerWithChat(newMemStore(), failingChat{}).Routes()
	rr := do(t, h, http.MethodPost, "/api/v1/runs", "teacher", `{"profession":"cook"}`)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "BAD_GATEWAY")
}

// TestRunsErrorCodesMatrix verifies that the Runs API returns the expected
// status codes for the main error scenarios (500/501/502/503/504 + common ones).
func TestRunsErrorCodesMatrix(t *testing.T) {
	type tc struct {
		name       string
		method     string
		path       string
		role       string
		body       string
		setup      func() http.Handler
		wantStatus int
		wantCode   string // empty = do not check body code
	}

	validTreeBody := `[{
		"id": "n1",
		"type": "role",
		"title": "Cook",
		"parent_id": null,
		"evidence": [{"id": "e1", "source_id": "s1", "quote": "test quote"}]
	}]`

	tests := []tc{
		// 400
		{
			name:   "400_bad_request_empty_profession",
			method: http.MethodPost, path: "/api/v1/runs", role: "teacher",
			body:       `{"profession":""}`,
			setup:      func() http.Handler { return NewServer(newMemStore()).Routes() },
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST",
		},
		// 401
		{
			name:   "401_unauthorized",
			method: http.MethodPost, path: "/api/v1/runs", role: "",
			body:       `{"profession":"cook"}`,
			setup:      func() http.Handler { return NewServer(newMemStore()).Routes() },
			wantStatus: http.StatusUnauthorized,
		},
		// 201
		{
			name:   "201_student_can_create",
			method: http.MethodPost, path: "/api/v1/runs", role: "student",
			body:       `{"profession":"cook"}`,
			setup:      func() http.Handler { return NewServer(newMemStore()).Routes() },
			wantStatus: http.StatusCreated,
		},
		// 404
		{
			name:   "404_run_not_found",
			method: http.MethodGet, path: "/api/v1/runs/missing", role: "teacher",
			setup:      func() http.Handler { return NewServer(newMemStore()).Routes() },
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND",
		},
		// 409
		{
			name:   "409_export_not_finished",
			method: http.MethodGet, path: "/api/v1/runs/r1/export", role: "teacher",
			setup: func() http.Handler {
				m := newMemStore()
				m.seedRun(domain.Run{ID: "r1", Status: domain.RunPending, Profession: "cook", Stage: domain.StageCreated})
				return NewServer(m).Routes()
			},
			wantStatus: http.StatusConflict, wantCode: "RUN_NOT_FINISHED",
		},
		// 500
		{
			name:   "500_create_store_error",
			method: http.MethodPost, path: "/api/v1/runs", role: "teacher",
			body: `{"profession":"cook"}`,
			setup: func() http.Handler {
				m := &failingRunStore{Store: newMemStore(), createRunErr: errors.New("database unavailable")}
				return NewServer(m).Routes()
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL",
		},
		// 501
		{
			name:   "501_tree_not_implemented",
			method: http.MethodPut, path: "/api/v1/runs/r1/tree", role: "teacher",
			body:       validTreeBody,
			setup:      func() http.Handler { return NewServer(newMemStore()).Routes() },
			wantStatus: http.StatusNotImplemented, wantCode: "NOT_IMPLEMENTED",
		},
		// 502
		{
			name:   "502_chat_bad_gateway",
			method: http.MethodPost, path: "/api/v1/runs", role: "teacher",
			body: `{"profession":"cook"}`,
			setup: func() http.Handler {
				return NewServerWithChat(newMemStore(), failingChat{}).Routes()
			},
			wantStatus: http.StatusBadGateway, wantCode: "BAD_GATEWAY",
		},
		// 503
		{
			name:   "503_service_unavailable",
			method: http.MethodPost, path: "/api/v1/runs", role: "teacher",
			body: `{"profession":"cook"}`,
			setup: func() http.Handler {
				m := &failingRunStore{Store: newMemStore(), createRunErr: ErrServiceUnavailable}
				return NewServer(m).Routes()
			},
			wantStatus: http.StatusServiceUnavailable, wantCode: "SERVICE_UNAVAILABLE",
		},
		// 504
		{
			name:   "504_gateway_timeout",
			method: http.MethodPost, path: "/api/v1/runs", role: "teacher",
			body: `{"profession":"cook"}`,
			setup: func() http.Handler {
				m := &failingRunStore{Store: newMemStore(), createRunErr: ErrGatewayTimeout}
				return NewServer(m).Routes()
			},
			wantStatus: http.StatusGatewayTimeout, wantCode: "GATEWAY_TIMEOUT",
		},
		// 429
		{
			name:   "429_too_many_requests",
			method: http.MethodPost, path: "/api/v1/runs", role: "teacher",
			body: `{"profession":"cook"}`,
			setup: func() http.Handler {
				m := &failingRunStore{Store: newMemStore(), createRunErr: ErrTooManyRequests}
				return NewServer(m).Routes()
			},
			wantStatus: http.StatusTooManyRequests, wantCode: "TOO_MANY_REQUESTS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := tt.setup()
			rr := do(t, h, tt.method, tt.path, tt.role, tt.body)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status: got %d, want %d; body=%s", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantCode != "" {
				assertErrorCode(t, rr, tt.wantCode)
			}
			t.Logf("✓ %s → %d %s", tt.name, rr.Code, tt.wantCode)
		})
	}
}
