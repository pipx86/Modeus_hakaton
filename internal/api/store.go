package api

import (
	"errors"
	"fmt"
	"sync"

	"activity-descriptor/internal/domain"
)

var ErrNotFound = errors.New("NOT_FOUND")
var ErrAlreadyApproved = errors.New("EVIDENCE_ALREADY_APPROVED")
var ErrConflict = errors.New("conflict")

type Resumer interface {
	ListIncompleteRuns() ([]*domain.Run, error)
}

type Store interface {
	CreateRun(profession string) (*domain.Run, error)
	GetRun(id string) (*domain.Run, error)
	GetTree(runID string) ([]domain.Node, error)
	GetAnswers(runID string) ([]domain.Evidence, error)
	ApproveEvidence(id string) error
	SetStage(id string, stage domain.Stage) error
	SetStatus(id string, status domain.RunStatus) error
}

type memStore struct {
	mu             sync.RWMutex
	runs           map[string]*domain.Run
	nodes          map[string][]domain.Node
	evidences      map[string]domain.Evidence
	runEvidenceIDs map[string][]string
	approved       map[string]bool
	seq            int
}

func NewMemStoreForTest() Store { return newMemStore() }

func NewMemoryStore() Store { return newMemStore() }

func newMemStore() *memStore {
	return &memStore{
		runs:           map[string]*domain.Run{},
		nodes:          map[string][]domain.Node{},
		evidences:      map[string]domain.Evidence{},
		runEvidenceIDs: map[string][]string{},
		approved:       map[string]bool{},
	}
}

func (m *memStore) CreateRun(profession string) (*domain.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.seq++
	id := fmt.Sprintf("run-%d", m.seq)

	r := &domain.Run{
		ID:         id,
		Status:     domain.RunPending,
		Profession: profession,
		Stage:      domain.StageCreated,
	}
	m.runs[id] = r

	out := *r
	return &out, nil
}

func (m *memStore) GetRun(id string) (*domain.Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.runs[id]
	if !ok {
		return nil, ErrNotFound
	}

	out := *r
	return &out, nil
}

func (m *memStore) GetTree(runID string) ([]domain.Node, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.runs[runID]; !ok {
		return nil, ErrNotFound
	}

	src := m.nodes[runID]
	out := make([]domain.Node, len(src))
	copy(out, src)
	return out, nil
}

func (m *memStore) GetAnswers(runID string) ([]domain.Evidence, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.runs[runID]; !ok {
		return nil, ErrNotFound
	}

	ids := m.runEvidenceIDs[runID]
	out := make([]domain.Evidence, 0, len(ids))
	for _, id := range ids {
		if ev, ok := m.evidences[id]; ok {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (m *memStore) ApproveEvidence(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.evidences[id]; !ok {
		return ErrNotFound
	}
	if m.approved[id] {
		return ErrAlreadyApproved
	}

	m.approved[id] = true
	return nil
}

func (m *memStore) SetStage(id string, stage domain.Stage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return ErrNotFound
	}
	r.Stage = stage
	return nil
}

func (m *memStore) SetStatus(id string, status domain.RunStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return ErrNotFound
	}
	r.Status = status
	return nil
}

func (m *memStore) ListIncompleteRuns() ([]*domain.Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []*domain.Run
	for _, r := range m.runs {
		if r.Status == domain.RunRunning && r.Stage != domain.StageDone {
			cp := *r // копия, чтобы не мутировать оригинал при resume
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *memStore) Save(r *domain.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.runs[r.ID]; ok {
		existing.Status = r.Status
		existing.Stage = r.Stage
		existing.Profession = r.Profession
		existing.RootNodeID = r.RootNodeID
		existing.StartedAt = r.StartedAt
		existing.FinishedAt = r.FinishedAt
		existing.Data = r.Data // сохраняем skill_freq, level_data и т.д.
	} else {
		cp := *r
		m.runs[r.ID] = &cp
	}
	return nil
}
