package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

// zunUnsupportedStore не умеет сохранять ЗУН и проверяет 501 на POST apply.
type zunUnsupportedStore struct{ Store }

// zunSeedRun создаёт в memStore прогон с заданными узлами.
func zunSeedRun(t *testing.T, store *memStore, profession string, nodes ...domain.Node) string {
	t.Helper()
	run, err := store.CreateRun(profession)
	if err != nil {
		t.Fatal(err)
	}
	store.seedNodes(run.ID, nodes...)
	return run.ID
}

func TestZunSuggestionsHandlerAuth(t *testing.T) {
	store := newMemStore()
	runID := zunSeedRun(t, store, "Аналитик данных",
		domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "SQL-отчёты"})
	server := NewServer(store)
	tests := []struct {
		role string
		want int
	}{
		{"admin", http.StatusOK},
		{"user", http.StatusOK},
		{"teacher", http.StatusOK},
		{"rop", http.StatusOK},
		{"student", http.StatusOK},
		{"guest", http.StatusOK},
		{"", http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run("role="+test.role, func(t *testing.T) {
			recorder := doMentorHandler(t, server.handleZunSuggestions, http.MethodGet,
				"/api/v1/runs/"+runID+"/zun/suggestions", test.role, "", map[string]string{"id": runID})
			if recorder.Code != test.want {
				t.Fatalf("роль %q: ожидали %d, получили %d (%s)", test.role, test.want, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestZunSuggestionsHandlerMemoryStore(t *testing.T) {
	manual := &domain.Zun{Knowledge: []string{"Ручное знание"}, Skills: []string{"Ручное умение"}}
	store := newMemStore()
	runID := zunSeedRun(t, store, "Аналитик данных",
		domain.Node{ID: "node-manual", Type: domain.NodeAction, Title: "Ручной узел", Zun: manual},
		domain.Node{ID: "node-empty", Type: domain.NodeTask, Title: "Готовить SQL-отчёты", Description: "Строить дашборды в Power BI"},
	)
	server := NewServer(store)
	recorder := doMentorHandler(t, server.handleZunSuggestions, http.MethodGet,
		"/api/v1/runs/"+runID+"/zun/suggestions", "teacher", "", map[string]string{"id": runID})
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	suggestions := decodeMentorJSON[[]zunSuggestion](t, recorder)
	if len(suggestions) != 2 {
		t.Fatalf("ожидали предложения для 2 узлов, получили %d: %+v", len(suggestions), suggestions)
	}
	if suggestions[0].NodeID != "node-manual" {
		t.Fatalf("порядок узлов нарушен: %+v", suggestions)
	}
	if suggestions[0].CanApply {
		t.Fatal("узел с ручным ЗУН не должен быть применим")
	}
	if len(suggestions[0].Knowledge) != 0 || len(suggestions[0].Skills) != 0 || len(suggestions[0].Abilities) != 0 {
		t.Fatalf("узел с ЗУН не должен получать предложения: %+v", suggestions[0])
	}
	if suggestions[0].CurrentZun == nil || suggestions[0].CurrentZun.Knowledge[0] != "Ручное знание" {
		t.Fatalf("текущий ЗУН не сохранён: %+v", suggestions[0].CurrentZun)
	}
	if suggestions[1].NodeID != "node-empty" || !suggestions[1].CanApply || suggestions[1].CurrentZun != nil {
		t.Fatalf("пустой узел должен получать применимое предложение: %+v", suggestions[1])
	}
	if suggestions[1].Source != "rule" {
		t.Fatalf("source = %q, ожидали rule", suggestions[1].Source)
	}
	if len(suggestions[1].Knowledge) == 0 || len(suggestions[1].Skills) == 0 || len(suggestions[1].Abilities) == 0 {
		t.Fatalf("предложение пустое: %+v", suggestions[1])
	}

	// Неизвестный прогон -> 404.
	unknown := doMentorHandler(t, server.handleZunSuggestions, http.MethodGet,
		"/api/v1/runs/missing/zun/suggestions", "teacher", "", map[string]string{"id": "missing"})
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("неизвестный run: ожидали 404, получили %d (%s)", unknown.Code, unknown.Body.String())
	}
}

func TestZunApplyHandlerAuth(t *testing.T) {
	body := `{"node_id":"node-1","knowledge":["SQL"],"skills":[],"abilities":[]}`
	tests := []struct {
		role string
		want int
	}{
		{"admin", http.StatusOK},
		{"user", http.StatusOK},
		{"teacher", http.StatusOK},
		{"rop", http.StatusOK},
		{"student", http.StatusOK},
		{"guest", http.StatusOK},
		{"", http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run("role="+test.role, func(t *testing.T) {
			// Свой прогон на каждую роль: успешный apply заполняет ЗУН и
			// сделал бы следующий вызов 409.
			store := newMemStore()
			runID := zunSeedRun(t, store, "Аналитик",
				domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "SQL"})
			server := NewServer(store)
			recorder := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
				"/api/v1/runs/"+runID+"/zun/suggestions/apply", test.role, body, map[string]string{"id": runID})
			if recorder.Code != test.want {
				t.Fatalf("роль %q: ожидали %d, получили %d (%s)", test.role, test.want, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestZunApplyHandlerSuccessAndConflict(t *testing.T) {
	store := newMemStore()
	runID := zunSeedRun(t, store, "Аналитик",
		domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "SQL-отчёты"},
		domain.Node{ID: "node-manual", Type: domain.NodeAction, Title: "Ручной", Zun: &domain.Zun{Knowledge: []string{"Ручное"}}},
	)
	server := NewServer(store)

	// Нормализация: пробелы, дубли без учёта регистра, пустые пункты.
	body := `{"node_id":" node-1 ","knowledge":[" SQL ","sql","", "SQL-запросы"],"skills":["  Писать   запросы "],"abilities":["Ответственность"]}`
	recorder := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+runID+"/zun/suggestions/apply", "teacher", body, map[string]string{"id": runID})
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply: ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	response := decodeMentorJSON[zunApplyResponse](t, recorder)
	if response.RunID != runID || response.NodeID != "node-1" || !response.Applied || response.CanApply {
		t.Fatalf("неверный ответ apply: %+v", response)
	}
	if response.Source != "rule" || response.CurrentZun == nil || response.NodeTitle != "SQL-отчёты" {
		t.Fatalf("ответ не содержит сохранённого ЗУН: %+v", response)
	}
	if want := []string{"SQL", "SQL-запросы"}; !equalStringSlices(response.CurrentZun.Knowledge, want) {
		t.Fatalf("knowledge = %v, ожидали %v", response.CurrentZun.Knowledge, want)
	}
	if want := []string{"Писать запросы"}; !equalStringSlices(response.CurrentZun.Skills, want) {
		t.Fatalf("skills = %v, ожидали %v", response.CurrentZun.Skills, want)
	}
	if want := []string{"Ответственность"}; !equalStringSlices(response.CurrentZun.Abilities, want) {
		t.Fatalf("abilities = %v, ожидали %v", response.CurrentZun.Abilities, want)
	}

	// ЗУН действительно сохранён в дереве.
	nodes, err := store.GetTree(runID)
	if err != nil {
		t.Fatal(err)
	}
	applied := nodes[0].Zun
	if applied == nil || !equalStringSlices(applied.Knowledge, []string{"SQL", "SQL-запросы"}) {
		t.Fatalf("ЗУН не сохранён в узле: %+v", applied)
	}

	// Повторное применение не перезаписывает сохранённый ЗУН.
	again := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+runID+"/zun/suggestions/apply", "teacher", body, map[string]string{"id": runID})
	if again.Code != http.StatusConflict {
		t.Fatalf("повторный apply: ожидали 409, получили %d (%s)", again.Code, again.Body.String())
	}
	conflict := decodeMentorJSON[errorBody](t, again)
	if conflict.Code != "ZUN_ALREADY_SET" {
		t.Fatalf("code = %q, ожидали ZUN_ALREADY_SET", conflict.Code)
	}

	// Ручной ЗУН защищён: 409 и значения не изменились.
	manualBody := `{"node_id":"node-manual","knowledge":["Правило"],"skills":[],"abilities":[]}`
	manual := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+runID+"/zun/suggestions/apply", "admin", manualBody, map[string]string{"id": runID})
	if manual.Code != http.StatusConflict {
		t.Fatalf("ручной ЗУН: ожидали 409, получили %d (%s)", manual.Code, manual.Body.String())
	}
	nodes, err = store.GetTree(runID)
	if err != nil {
		t.Fatal(err)
	}
	if nodes[1].Zun == nil || !equalStringSlices(nodes[1].Zun.Knowledge, []string{"Ручное"}) {
		t.Fatalf("ручной ЗУН перезаписан: %+v", nodes[1].Zun)
	}
}

func TestZunApplyHandlerValidation(t *testing.T) {
	store := newMemStore()
	runID := zunSeedRun(t, store, "Аналитик",
		domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "SQL"})
	server := NewServer(store)
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: `{`},
		{name: "unknown field", body: `{"node_id":"node-1","knowledge":["SQL"],"skills":[],"abilities":[],"extra":1}`},
		{name: "missing node", body: `{"knowledge":["SQL"]}`},
		{name: "empty lists", body: `{"node_id":"node-1"}`},
		{name: "only empty items", body: `{"node_id":"node-1","knowledge":[" ",""],"skills":[],"abilities":[]}`},
		{name: "trailing data", body: `{"node_id":"node-1","knowledge":["SQL"]}{"x":1}`},
		{name: "too long item", body: `{"node_id":"node-1","knowledge":["` + strings.Repeat("а", zunSuggestionMaxItemRunes+1) + `"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
				"/api/v1/runs/"+runID+"/zun/suggestions/apply", "teacher", test.body, map[string]string{"id": runID})
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("ожидали 400, получили %d (%s)", recorder.Code, recorder.Body.String())
			}
		})
	}

	// Узел из другого прогона и неизвестный прогон -> 404.
	otherRunID := zunSeedRun(t, store, "Другой", domain.Node{ID: "node-other", Type: domain.NodeTask, Title: "Другое"})
	crossRun := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+runID+"/zun/suggestions/apply", "teacher",
		`{"node_id":"node-other","knowledge":["SQL"]}`, map[string]string{"id": runID})
	if crossRun.Code != http.StatusNotFound {
		t.Fatalf("узел чужого прогона: ожидали 404, получили %d (%s)", crossRun.Code, crossRun.Body.String())
	}
	_ = otherRunID
	unknownRun := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
		"/api/v1/runs/missing/zun/suggestions/apply", "teacher",
		`{"node_id":"node-1","knowledge":["SQL"]}`, map[string]string{"id": "missing"})
	if unknownRun.Code != http.StatusNotFound {
		t.Fatalf("неизвестный run: ожидали 404, получили %d (%s)", unknownRun.Code, unknownRun.Body.String())
	}
}

func TestZunApplyHandlerNotImplemented(t *testing.T) {
	server := NewServer(&zunUnsupportedStore{})
	recorder := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
		"/api/v1/runs/run-1/zun/suggestions/apply", "admin",
		`{"node_id":"node-1","knowledge":["SQL"]}`, map[string]string{"id": "run-1"})
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("хранилище без записи ЗУН: ожидали 501, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

// equalStringSlices сравнивает срезы строк без учёта nil/empty-разницы.
func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// TestZunSuggestionsPostgres проверяет полный цикл на реальной БД: чтение
// предложений, сохранение только в пустой ЗУН и защиту ручных значений.
func TestZunSuggestionsPostgres(t *testing.T) {
	pool := mentorFeatureTestPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	server := NewServer(store)

	source, err := store.CreateSource(domain.Source{Type: domain.SourceInterview, Title: "Auto-ZUN: интервью",
		Content: "Каждый день пишу SQL-запросы и строю дашборды", SourceURL: "https://example.org/zun"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun("Аналитик данных")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run.ID)
		pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1`, source.ID)
	})
	rootID := mentorFeatureTestUUID(t, pool)
	functionID := mentorFeatureTestUUID(t, pool)
	emptyID := mentorFeatureTestUUID(t, pool)
	manualID := mentorFeatureTestUUID(t, pool)
	nodes := []domain.Node{
		{ID: rootID, Type: domain.NodeRole, Title: "Аналитик данных", Evidence: []domain.Evidence{
			{ID: mentorFeatureTestUUID(t, pool), SourceID: source.ID, RefID: "answer-1", Quote: "Анализирую показатели"},
		}},
		{ID: functionID, Type: domain.NodeFunction, Title: "Аналитика и отчётность", ParentID: &rootID,
			Evidence: []domain.Evidence{{ID: mentorFeatureTestUUID(t, pool), SourceID: source.ID, RefID: "answer-2", Quote: "Готовлю отчётность"}}},
		{ID: emptyID, Type: domain.NodeTask, Title: "Готовить SQL-отчёты", Description: "Строить дашборды в Power BI", ParentID: &functionID,
			Evidence: []domain.Evidence{{ID: mentorFeatureTestUUID(t, pool), SourceID: source.ID, RefID: "answer-3", Quote: "Пишу SQL-запросы каждый день"}}},
		{ID: manualID, Type: domain.NodeAction, Title: "Ручной узел", ParentID: &emptyID,
			Evidence: []domain.Evidence{{ID: mentorFeatureTestUUID(t, pool), SourceID: source.ID, RefID: "answer-4", Quote: "Ручное подтверждение"}},
			Zun:      &domain.Zun{Knowledge: []string{"Ручное знание"}}},
	}
	if err = store.SaveTree(ctx, run.ID, nodes); err != nil {
		t.Fatal(err)
	}

	// GET: предложения в порядке level,id; ручной узел защищён.
	recorder := doMentorHandler(t, server.handleZunSuggestions, http.MethodGet,
		"/api/v1/runs/"+run.ID+"/zun/suggestions", "teacher", "", map[string]string{"id": run.ID})
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET suggestions: %d (%s)", recorder.Code, recorder.Body.String())
	}
	suggestions := decodeMentorJSON[[]zunSuggestion](t, recorder)
	if len(suggestions) != 4 {
		t.Fatalf("ожидали 4 предложения, получили %d: %+v", len(suggestions), suggestions)
	}
	byID := make(map[string]zunSuggestion, len(suggestions))
	for _, suggestion := range suggestions {
		byID[suggestion.NodeID] = suggestion
	}
	emptySuggestion, ok := byID[emptyID]
	if !ok || !emptySuggestion.CanApply || len(emptySuggestion.Knowledge) == 0 {
		t.Fatalf("пустой узел не получил предложение: %+v", emptySuggestion)
	}
	manualSuggestion, ok := byID[manualID]
	if !ok || manualSuggestion.CanApply || manualSuggestion.CurrentZun == nil ||
		!equalStringSlices(manualSuggestion.CurrentZun.Knowledge, []string{"Ручное знание"}) {
		t.Fatalf("ручной узел не защищён: %+v", manualSuggestion)
	}

	// POST: применяем предложение пустого узла.
	applyBody, err := json.Marshal(zunApplyRequest{
		NodeID:    emptyID,
		Knowledge: emptySuggestion.Knowledge,
		Skills:    emptySuggestion.Skills,
		Abilities: emptySuggestion.Abilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	applied := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+run.ID+"/zun/suggestions/apply", "teacher", string(applyBody), map[string]string{"id": run.ID})
	if applied.Code != http.StatusOK {
		t.Fatalf("POST apply: %d (%s)", applied.Code, applied.Body.String())
	}
	var stored []byte
	if err = pool.QueryRow(ctx, `SELECT zun FROM nodes WHERE id=$1`, emptyID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var storedZun domain.Zun
	if err = json.Unmarshal(stored, &storedZun); err != nil {
		t.Fatal(err)
	}
	if !equalStringSlices(storedZun.Knowledge, emptySuggestion.Knowledge) {
		t.Fatalf("СУБД хранит knowledge %v, ожидали %v", storedZun.Knowledge, emptySuggestion.Knowledge)
	}

	// Повторный apply -> 409, ручной узел -> 409 и без изменений.
	again := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+run.ID+"/zun/suggestions/apply", "teacher", string(applyBody), map[string]string{"id": run.ID})
	if again.Code != http.StatusConflict {
		t.Fatalf("повторный apply: ожидали 409, получили %d (%s)", again.Code, again.Body.String())
	}
	manualBody := `{"node_id":"` + manualID + `","knowledge":["Правило"],"skills":[],"abilities":[]}`
	manualApply := doMentorHandler(t, server.handleApplyZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+run.ID+"/zun/suggestions/apply", "teacher", manualBody, map[string]string{"id": run.ID})
	if manualApply.Code != http.StatusConflict {
		t.Fatalf("ручной ЗУН: ожидали 409, получили %d (%s)", manualApply.Code, manualApply.Body.String())
	}
	if err = pool.QueryRow(ctx, `SELECT zun FROM nodes WHERE id=$1`, manualID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	storedZun = domain.Zun{}
	if err = json.Unmarshal(stored, &storedZun); err != nil {
		t.Fatal(err)
	}
	if !equalStringSlices(storedZun.Knowledge, []string{"Ручное знание"}) {
		t.Fatalf("ручной ЗУН перезаписан в БД: %+v", storedZun)
	}
}
