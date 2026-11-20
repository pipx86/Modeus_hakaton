package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

// Карточка 19: изменения обоснований между версиями дерева. Поля
// evidence_added/evidence_removed аддитивны к существующему diff'у снимков.

func evidenceFixture(id, quote, source string) domain.Evidence {
	return domain.Evidence{ID: id, NodeID: "node-1", SourceID: source, Quote: quote, Status: domain.EvidencePending}
}

func TestDiffSnapshotEvidenceAddedAndRemoved(t *testing.T) {
	before := []domain.Evidence{
		evidenceFixture("ev-3", "третья", "src-3"),
		evidenceFixture("ev-1", "первая", "src-1"),
	}
	after := []domain.Evidence{
		evidenceFixture("ev-2", "вторая", "src-2"),
		evidenceFixture("ev-3", "третья", "src-3"),
	}
	added, removed := diffSnapshotEvidence(before, after)
	if len(added) != 1 || added[0].ID != "ev-2" || added[0].Quote != "вторая" || added[0].SourceID != "src-2" {
		t.Fatalf("added = %+v", added)
	}
	if len(removed) != 1 || removed[0].ID != "ev-1" || removed[0].Quote != "первая" || removed[0].SourceID != "src-1" {
		t.Fatalf("removed = %+v", removed)
	}
}

func TestDiffSnapshotEvidenceSortedByID(t *testing.T) {
	before := []domain.Evidence{evidenceFixture("ev-9", "девять", "src")}
	after := []domain.Evidence{
		evidenceFixture("ev-c", "цэ", "src"),
		evidenceFixture("ev-a", "а", "src"),
		evidenceFixture("ev-b", "бэ", "src"),
	}
	added, removed := diffSnapshotEvidence(before, after)
	wantAdded := []string{"ev-a", "ev-b", "ev-c"}
	if len(added) != len(wantAdded) {
		t.Fatalf("added = %+v", added)
	}
	for i, want := range wantAdded {
		if added[i].ID != want {
			t.Fatalf("added[%d] = %q, want %q (%+v)", i, added[i].ID, want, added)
		}
	}
	if len(removed) != 1 || removed[0].ID != "ev-9" {
		t.Fatalf("removed = %+v", removed)
	}
}

func TestDiffSnapshotEvidenceSameIDDifferentQuoteIsNotAddedOrRemoved(t *testing.T) {
	// Правка цитаты под тем же id — изменение обоснования (fields = evidence),
	// а не удаление старого и добавление нового.
	before := []domain.Evidence{evidenceFixture("ev-1", "старая цитата", "src-1")}
	after := []domain.Evidence{evidenceFixture("ev-1", "новая цитата", "src-1")}
	added, removed := diffSnapshotEvidence(before, after)
	if added == nil || removed == nil {
		t.Fatalf("срезы должны быть не-nil: added=%v removed=%v", added, removed)
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("added = %+v, removed = %+v", added, removed)
	}
}

func TestDiffSnapshotEvidenceEmpty(t *testing.T) {
	added, removed := diffSnapshotEvidence(nil, nil)
	if added == nil || removed == nil || len(added) != 0 || len(removed) != 0 {
		t.Fatalf("added = %#v, removed = %#v", added, removed)
	}
}

func TestDiffSnapshotTreesFillsEvidenceFields(t *testing.T) {
	before := domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "Собирать требования",
		Evidence: []domain.Evidence{
			evidenceFixture("ev-1", "старая цитата", "src-1"),
			evidenceFixture("ev-3", "общая цитата", "src-3"),
		}}
	after := domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "Собирать требования к интеграции",
		Evidence: []domain.Evidence{
			evidenceFixture("ev-2", "новая цитата", "src-2"),
			evidenceFixture("ev-3", "общая цитата", "src-3"),
		}}
	result := diffSnapshotTrees([]domain.Node{before}, []domain.Node{after})
	if len(result.Changed) != 1 {
		t.Fatalf("changed = %+v", result.Changed)
	}
	change := result.Changed[0]
	wantFields := []string{"title", "evidence"}
	if len(change.Fields) != len(wantFields) || change.Fields[0] != wantFields[0] || change.Fields[1] != wantFields[1] {
		t.Fatalf("fields = %v, want %v", change.Fields, wantFields)
	}
	if len(change.EvidenceAdded) != 1 || change.EvidenceAdded[0].ID != "ev-2" ||
		change.EvidenceAdded[0].Quote != "новая цитата" || change.EvidenceAdded[0].SourceID != "src-2" {
		t.Fatalf("evidence_added = %+v", change.EvidenceAdded)
	}
	if len(change.EvidenceRemoved) != 1 || change.EvidenceRemoved[0].ID != "ev-1" {
		t.Fatalf("evidence_removed = %+v", change.EvidenceRemoved)
	}
}

func TestDiffSnapshotTreesEvidenceArraysAlwaysNonNil(t *testing.T) {
	before := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль",
		Evidence: []domain.Evidence{evidenceFixture("ev-1", "цитата", "src-1")}}
	after := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль (уточнено)",
		Evidence: []domain.Evidence{evidenceFixture("ev-1", "цитата", "src-1")}}
	result := diffSnapshotTrees([]domain.Node{before}, []domain.Node{after})
	if len(result.Changed) != 1 {
		t.Fatalf("changed = %+v", result.Changed)
	}
	if result.Changed[0].EvidenceAdded == nil || result.Changed[0].EvidenceRemoved == nil {
		t.Fatalf("массивы обоснований должны быть не-nil: %+v", result.Changed[0])
	}
}

func TestSnapshotDiffHandlerEmitsEvidenceArrays(t *testing.T) {
	before := domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "Собирать требования",
		Evidence: []domain.Evidence{evidenceFixture("ev-1", "старая цитата", "src-1")}}
	after := domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "Собирать требования",
		Evidence: []domain.Evidence{
			evidenceFixture("ev-1", "старая цитата", "src-1"),
			evidenceFixture("ev-2", "новая цитата", "src-2"),
		}}
	treeDiff := diffSnapshotTrees([]domain.Node{before}, []domain.Node{after})
	stub := &snapshotDiffStub{result: snapshotDiffResponse{
		RunID:   snapshotDiffTestRunID,
		Base:    snapshotVersionRef{ID: snapshotDiffTestBaseID, Version: 1},
		Head:    snapshotVersionRef{ID: snapshotDiffTestHeadID, Version: 2},
		Added:   treeDiff.Added,
		Removed: treeDiff.Removed,
		Changed: treeDiff.Changed,
		Summary: snapshotDiffSummary{Added: len(treeDiff.Added), Removed: len(treeDiff.Removed), Changed: len(treeDiff.Changed)},
	}}
	handler := NewServer(stub).Routes()
	rec := do(t, handler, http.MethodGet, snapshotDiffTestPath(snapshotDiffTestRunID, snapshotDiffTestHeadID), "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"evidence_added":[`) || !strings.Contains(body, `"evidence_removed":[`) {
		t.Fatalf("в changed нет массивов обоснований: %s", body)
	}
	var got snapshotDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не разобрался: %v", err)
	}
	if len(got.Changed) != 1 || len(got.Changed[0].EvidenceAdded) != 1 ||
		got.Changed[0].EvidenceAdded[0].ID != "ev-2" || len(got.Changed[0].EvidenceRemoved) != 0 {
		t.Fatalf("changed: %+v", got.Changed)
	}
}
