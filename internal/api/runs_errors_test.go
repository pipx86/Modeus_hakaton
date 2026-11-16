package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

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
