package api

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"activity-descriptor/internal/domain"
)

var ErrNotFound = errors.New("NOT_FOUND")
var ErrAlreadyApproved = errors.New("EVIDENCE_ALREADY_APPROVED")
var ErrConflict = errors.New("conflict")

type Resumer interface {
	ListIncompleteRuns() ([]*domain.Run, error)
}

type NodePatch struct {
	Title       *string            `json:"title,omitempty"`
	Description *string            `json:"description,omitempty"`
	Evidence    *[]domain.Evidence `json:"evidence,omitempty"`
}

type Store interface {
	CreateRun(profession string) (*domain.Run, error)
	GetRun(id string) (*domain.Run, error)
	GetTree(runID string) ([]domain.Node, error)
	GetAnswers(runID string) ([]domain.Evidence, error)
	ApproveEvidence(id string) error
	SetStage(id string, stage domain.Stage) error
	SetStatus(id string, status domain.RunStatus) error
	UpdateNode(nodeID string, patch NodePatch) (*domain.Node, error)
	BulkApproveEvidence(ids []string) (approved int, skipped int, err error)
	DeleteSource(sourceID string) error
	ListRuns() ([]*domain.Run, error)
	CreateSource(src domain.Source) (*domain.Source, error)
}

type memStore struct {
	mu             sync.RWMutex
	runs           map[string]*domain.Run
	nodes          map[string][]domain.Node
	evidences      map[string]domain.Evidence
	sources        map[string]domain.Source // === NEW ===
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
		sources:        map[string]domain.Source{}, // === NEW ===
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
		Type:       domain.RunTypeBatch,
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
		if r.Status == domain.RunRunning && r.Stage != domain.StageDone && r.Type == domain.RunTypeBatch {
			cp := *r
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
func (m *memStore) UpdateNode(nodeID string, patch NodePatch) (*domain.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for runID, nodes := range m.nodes {
		for i := range nodes {
			if nodes[i].ID == nodeID {
				if patch.Title != nil {
					nodes[i].Title = *patch.Title
				}
				if patch.Description != nil {
					nodes[i].Description = *patch.Description
				}
				if patch.Evidence != nil {
					nodes[i].Evidence = *patch.Evidence
				}
				cp := nodes[i]
				return &cp, nil
			}
		}
		_ = runID
	}
	return nil, ErrNotFound
}

func (m *memStore) BulkApproveEvidence(ids []string) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	approved := 0
	skipped := 0
	for _, id := range ids {
		if _, ok := m.evidences[id]; !ok {
			skipped++
			continue
		}
		if m.approved[id] {
			skipped++
			continue
		}
		m.approved[id] = true
		approved++
	}
	return approved, skipped, nil
}

func (m *memStore) DeleteSource(sourceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, found := m.sources[sourceID]; !found {
		return ErrNotFound
	}
	for _, ev := range m.evidences {
		if ev.SourceID == sourceID {
			return ErrConflict
		}
	}
	delete(m.sources, sourceID)
	return nil
}

func (m *memStore) ListRuns() ([]*domain.Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*domain.Run, 0, len(m.runs))
	for _, r := range m.runs {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (m *memStore) CreateSource(src domain.Source) (*domain.Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if src.ID == "" {
		m.seq++
		src.ID = fmt.Sprintf("src-%d", m.seq)
	}

	now := time.Now()
	src.CreatedAt = &now

	m.sources[src.ID] = src

	out := src
	return &out, nil
}
