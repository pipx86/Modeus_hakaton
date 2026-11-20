package steps

import (
	"reflect"
	"testing"

	"activity-descriptor/internal/domain"
)

func provenanceEvidence(sourceIDs ...string) []domain.Evidence {
	out := make([]domain.Evidence, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		out = append(out, domain.Evidence{ID: "ev-" + sourceID, SourceID: sourceID, RefID: "ref", Quote: "quote"})
	}
	return out
}

func nodeIDs(nodes []domain.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

// TestPartitionNodesByProvenance pins the routing rules shared by the chat
// finalizer and the batch AI chain: only a node whose every quote points at one
// standard/vacancy type leaves the Interview slot.
func TestPartitionNodesByProvenance(t *testing.T) {
	sources := []domain.Source{
		{ID: "std-1", Type: domain.SourceStandard, Title: "Профстандарт"},
		{ID: "std-2", Type: domain.SourceStandard, Title: "Второй профстандарт"},
		{ID: "vac-1", Type: domain.SourceVacancy, Title: "Вакансия"},
		{ID: "int-1", Type: domain.SourceInterview, Title: "Интервью"},
		{ID: "doc-1", Type: domain.SourceDocument, Title: "Материал"},
	}
	nodes := []domain.Node{
		{ID: "std", Evidence: provenanceEvidence("std-1")},
		{ID: "std-multi", Evidence: provenanceEvidence("std-1", "std-2")},
		{ID: "vac", Evidence: provenanceEvidence("vac-1")},
		{ID: "mixed", Evidence: provenanceEvidence("std-1", "vac-1")},
		{ID: "interview", Evidence: provenanceEvidence("int-1")},
		{ID: "document", Evidence: provenanceEvidence("doc-1")},
		{ID: "unknown", Evidence: provenanceEvidence("missing")},
		{ID: "no-evidence"},
	}

	interview, standard, vacancy := PartitionNodesByProvenance(nodes, sources)

	if got, want := nodeIDs(standard), []string{"std", "std-multi"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("standard slot = %v, want %v", got, want)
	}
	if got, want := nodeIDs(vacancy), []string{"vac"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("vacancy slot = %v, want %v", got, want)
	}
	wantInterview := []string{"mixed", "interview", "document", "unknown", "no-evidence"}
	if got := nodeIDs(interview); !reflect.DeepEqual(got, wantInterview) {
		t.Fatalf("interview slot = %v, want %v", got, wantInterview)
	}
}

func TestPartitionNodesByProvenanceEmpty(t *testing.T) {
	interview, standard, vacancy := PartitionNodesByProvenance(nil, []domain.Source{{ID: "std-1", Type: domain.SourceStandard}})
	if len(interview) != 0 || len(standard) != 0 || len(vacancy) != 0 {
		t.Fatalf("slots = %v / %v / %v, want empty", interview, standard, vacancy)
	}
}

// TestSourceMetadataKeepsPriorityWithoutContent pins the shape stored in
// run.Data: synthesis needs ids, types, titles and priorities, never the full
// document text.
func TestSourceMetadataKeepsPriorityWithoutContent(t *testing.T) {
	metadata := sourceMetadata([]domain.Source{
		{ID: "s1", Type: domain.SourceStandard, Title: "Профстандарт", Content: "большой текст", SourceURL: "http://x"},
		{ID: "s2", Type: domain.SourceVacancy, Title: "Вакансия", Priority: 7},
	})
	if len(metadata) != 2 {
		t.Fatalf("metadata = %+v, want two sources", metadata)
	}
	if metadata[0].Content != "" || metadata[0].SourceURL != "" {
		t.Fatalf("metadata kept content: %+v", metadata[0])
	}
	if metadata[0].Type != domain.SourceStandard || metadata[0].Priority != domain.PriorityStandard {
		t.Fatalf("standard metadata = %+v, want default priority %d", metadata[0], domain.PriorityStandard)
	}
	if metadata[1].Priority != 7 {
		t.Fatalf("explicit priority = %d, want 7", metadata[1].Priority)
	}
}
