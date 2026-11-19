package main

import (
	"context"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

// testAnalystAnswer is the scripted analyst response used by the synthesizer
// wiring tests. Both quotes are verbatim fragments of testFinalizeTranscript.
const testAnalystAnswer = `{"entities":[
 {"id":"role-analyst","type":"role","title":"Аналитик данных","evidence":[{"id":"ev-1","source_id":"src-interview","ref_id":"q1","quote":"Я аналитик данных."}]},
 {"id":"fn-reports","type":"function","title":"Отчётность","parent_id":"role-analyst","evidence":[{"id":"ev-2","source_id":"src-interview","ref_id":"q2","quote":"Строю отчёты."}]}
]}`

func savedTreeTitles(nodes []domain.Node) map[string]bool {
	titles := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		titles[node.Title] = true
	}
	return titles
}

// TestNewTreeFinalizerWithClientsNormalizesTitles проверяет production-обвязку
// гибридного синтезатора: chat finalizer передаёт pool-клиент синтезатору,
// детерминированный merge сохраняет id/иерархию/evidence, а LLM-нормализация
// меняет только title.
func TestNewTreeFinalizerWithClientsNormalizesTitles(t *testing.T) {
	// The analyst is also used for the best-effort ZUN enrichment, so it needs a
	// second response for that batch.
	analystClient := &fakeChatClient{responses: []string{testAnalystAnswer, "{}"}}
	// The analyst links titles to the taxonomy, so the merged node ids are the
	// canonical taxonomy ids (tax-role-data-analyst / tax-fn-reporting).
	synthesizerClient := &fakeChatClient{responses: []string{`{"nodes":[
		{"id":"tax-role-data-analyst","title":"Аналитик данных (нормализовано)"},
		{"id":"tax-fn-reporting","title":"Подготовка отчётности"}
	]}`}}
	store := &fakeFinalizeStore{}

	finalizer := newTreeFinalizerWithClients(store, analystClient, synthesizerClient, "", "", nil)
	status, err := finalizer(context.Background(), "run-synth-llm", "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done", status, err)
	}
	if synthesizerClient.calls != 1 {
		t.Fatalf("synthesizer llm calls = %d, want exactly 1", synthesizerClient.calls)
	}
	if len(store.savedTree) != 2 {
		t.Fatalf("saved tree = %d nodes, want 2", len(store.savedTree))
	}
	titles := savedTreeTitles(store.savedTree)
	for _, want := range []string{"Аналитик данных (нормализовано)", "Подготовка отчётности"} {
		if !titles[want] {
			t.Fatalf("saved tree misses normalized title %q: %v", want, titles)
		}
	}
	for _, node := range store.savedTree {
		if len(node.Evidence) == 0 {
			t.Fatalf("normalization dropped evidence of node %q", node.Title)
		}
	}
}

// TestNewTreeFinalizerWithClientsFallsBackOnBadNormalization проверяет, что
// сбой LLM-нормализации не роняет прогон: сохраняется детерминированное дерево.
func TestNewTreeFinalizerWithClientsFallsBackOnBadNormalization(t *testing.T) {
	analystClient := &fakeChatClient{responses: []string{testAnalystAnswer, "{}"}}
	synthesizerClient := &fakeChatClient{responses: []string{"not a JSON normalization answer"}}
	store := &fakeFinalizeStore{}

	finalizer := newTreeFinalizerWithClients(store, analystClient, synthesizerClient, "", "", nil)
	status, err := finalizer(context.Background(), "run-synth-fallback", "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done despite normalization failure", status, err)
	}
	if synthesizerClient.calls != 1 {
		t.Fatalf("synthesizer llm calls = %d, want exactly 1", synthesizerClient.calls)
	}
	titles := savedTreeTitles(store.savedTree)
	for _, want := range []string{"Аналитик данных", "Отчётность"} {
		if !titles[want] {
			t.Fatalf("saved tree misses deterministic title %q: %v", want, titles)
		}
	}
	for title := range titles {
		if strings.Contains(title, "нормализовано") {
			t.Fatalf("failed normalization leaked into the saved tree: %q", title)
		}
	}
}
