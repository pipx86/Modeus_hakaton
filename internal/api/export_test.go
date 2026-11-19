package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

const exportTestRunID = "run-export-1"

// exportSeedStore — завершённый прогон с полной иерархией
// role → function → task → action и тремя подтверждениями (с ref_id и source_id).
func exportSeedStore() *memStore {
	store := newMemStore()
	started := time.Date(2026, time.November, 18, 10, 0, 0, 0, time.UTC)
	finished := started.Add(12 * time.Minute)
	store.seedRun(domain.Run{
		ID:         exportTestRunID,
		Status:     domain.RunDone,
		Profession: "Аналитик данных",
		Type:       domain.RunTypeChat,
		Stage:      domain.StageDone,
		StartedAt:  &started,
		FinishedAt: &finished,
	})
	store.seedNodes(exportTestRunID,
		domain.Node{
			ID:       "node-role",
			Type:     domain.NodeRole,
			Title:    "Аналитик данных",
			Evidence: []domain.Evidence{},
		},
		domain.Node{
			ID:       "node-function",
			Type:     domain.NodeFunction,
			Title:    "Аналитика требований",
			ParentID: strPtr("node-role"),
			Evidence: []domain.Evidence{{
				ID:       "ev-function",
				SourceID: "src-interview",
				RefID:    "ref-function",
				Quote:    "Цитата про анализ требований",
				Status:   domain.EvidenceApproved,
			}},
		},
		domain.Node{
			ID:       "node-task",
			Type:     domain.NodeTask,
			Title:    "Собирать требования",
			ParentID: strPtr("node-function"),
			Evidence: []domain.Evidence{{
				ID:       "ev-task",
				SourceID: "src-interview",
				RefID:    "ref-task",
				Quote:    "Цитата про сбор требований",
				Status:   domain.EvidencePending,
			}},
		},
		domain.Node{
			ID:       "node-action",
			Type:     domain.NodeAction,
			Title:    "Интервью с заказчиком",
			ParentID: strPtr("node-task"),
			Evidence: []domain.Evidence{{
				ID:       "ev-action",
				SourceID: "src-vacancy",
				RefID:    "ref-action",
				Quote:    "Цитата про интервью",
				Status:   domain.EvidencePending,
			}},
		},
	)
	return store
}

// exportChatStub подменяет chatStarter в HTTP-тестах финального сообщения.
type exportChatStub struct {
	messages []chat.Message
	err      error
}

func (exportChatStub) Start(string, string) error { return nil }

func (s *exportChatStub) Messages(string) ([]chat.Message, error) { return s.messages, s.err }

func (exportChatStub) Post(string, string) (chat.Message, error) { return chat.Message{}, nil }

func (exportChatStub) Subscribe(string) (<-chan chat.Event, func(), error) {
	return nil, func() {}, nil
}

// exportEnrichmentStub добавляет к in-memory хранилищу PostgreSQL-only чтение
// конфликтов синтеза и метаданных источников.
type exportEnrichmentStub struct {
	Store
	conflicts   []domain.Conflict
	conflictErr error
	sources     []domain.Source
	sourceErr   error
}

func (s *exportEnrichmentStub) listSynthesisConflicts(_ context.Context, _ string) ([]domain.Conflict, error) {
	return s.conflicts, s.conflictErr
}

func (s *exportEnrichmentStub) ListRunSources(_ context.Context, _ string) ([]domain.Source, error) {
	return s.sources, s.sourceErr
}

// exportFilenameFromDisposition разбирает имя файла из Content-Disposition.
func exportFilenameFromDisposition(t *testing.T, header string) string {
	t.Helper()
	const prefix = `attachment; filename="`
	if !strings.HasPrefix(header, prefix) || !strings.HasSuffix(header, `"`) {
		t.Fatalf("некорректный Content-Disposition: %q", header)
	}
	return strings.TrimSuffix(strings.TrimPrefix(header, prefix), `"`)
}

func TestExportRunLegacyDefaultIsBareTreeArray(t *testing.T) {
	handler := NewServer(exportSeedStore()).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	var nodes []domain.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("legacy export должен быть массивом Node: %v (%s)", err, rec.Body.String())
	}
	if len(nodes) != 4 {
		t.Fatalf("legacy export: ожидали 4 узла, получили %d", len(nodes))
	}
}

func TestExportRunMarkdown(t *testing.T) {
	handler := NewServer(exportSeedStore()).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=md", "teacher", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/markdown") {
		t.Fatalf("Content-Type = %q, ожидали text/markdown", contentType)
	}
	if name := exportFilenameFromDisposition(t, rec.Header().Get("Content-Disposition")); name != "run-run-export-1.md" {
		t.Fatalf("filename = %q, ожидали run-run-export-1.md", name)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"# Run export: Аналитик данных",
		"- Run ID: `run-export-1`",
		"- Profession: Аналитик данных",
		"- Status: done",
		"- Stage: done",
		"- Type: chat",
		"- Started at: 2026-11-18T10:00:00Z",
		"- Finished at: 2026-11-18T10:12:00Z",
		"- Generated at: ",
		"- Nodes: 4",
		"- Evidence: 3",
		"- Conflicts: 0",
		"## Competency tree",
		"role → function → task → action",
		"- Аналитик данных (role) `node-role`",
		"  - Аналитика требований (function) `node-function`",
		"    - Собирать требования (task) `node-task`",
		"      - Интервью с заказчиком (action) `node-action`",
		"## Evidence",
		"Цитата про анализ требований",
		"src-interview",
		"ref-function",
		"approved",
		"## Conflicts",
		"No source conflicts were resolved for this run.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Markdown не содержит %q\n---\n%s", want, body)
		}
	}
	// Иерархия должна идти сверху вниз: role → function → task → action.
	roleIndex := strings.Index(body, "`node-role`")
	functionIndex := strings.Index(body, "`node-function`")
	taskIndex := strings.Index(body, "`node-task`")
	actionIndex := strings.Index(body, "`node-action`")
	if !(roleIndex >= 0 && roleIndex < functionIndex && functionIndex < taskIndex && taskIndex < actionIndex) {
		t.Errorf("порядок иерархии нарушен: role=%d function=%d task=%d action=%d", roleIndex, functionIndex, taskIndex, actionIndex)
	}
	// Без чата финального сообщения нет — секция не должна появляться.
	if strings.Contains(body, "## Final message") {
		t.Errorf("неожиданная секция финального сообщения без чата:\n%s", body)
	}
}

func TestExportRunJSON(t *testing.T) {
	handler := NewServer(exportSeedStore()).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=json", "admin", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q, ожидали application/json", contentType)
	}
	if name := exportFilenameFromDisposition(t, rec.Header().Get("Content-Disposition")); name != "run-run-export-1.json" {
		t.Fatalf("filename = %q, ожидали run-run-export-1.json", name)
	}

	var document runExport
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatalf("ответ не разобрался как runExport: %v\n%s", err, rec.Body.String())
	}
	if document.Schema != exportSchemaVersion {
		t.Errorf("schema = %q, ожидали %q", document.Schema, exportSchemaVersion)
	}
	if document.GeneratedAt.IsZero() {
		t.Error("generated_at пустой")
	}
	if document.Run == nil || document.Run.ID != exportTestRunID || document.Run.Status != domain.RunDone {
		t.Fatalf("run в экспорте неверный: %+v", document.Run)
	}
	if document.Run.Profession != "Аналитик данных" {
		t.Errorf("profession = %q", document.Run.Profession)
	}

	if len(document.Tree) != 1 {
		t.Fatalf("ожидали один корень дерева, получили %d", len(document.Tree))
	}
	root := document.Tree[0]
	if root.ID != "node-role" || root.Type != domain.NodeRole || len(root.Children) != 1 {
		t.Fatalf("корень дерева неверный: %+v", root)
	}
	functionNode := root.Children[0]
	if functionNode.ID != "node-function" || len(functionNode.Children) != 1 {
		t.Fatalf("function-узел неверный: %+v", functionNode)
	}
	taskNode := functionNode.Children[0]
	if taskNode.ID != "node-task" || len(taskNode.Children) != 1 {
		t.Fatalf("task-узел неверный: %+v", taskNode)
	}
	actionNode := taskNode.Children[0]
	if actionNode.ID != "node-action" || len(actionNode.Children) != 0 {
		t.Fatalf("action-узел неверный: %+v", actionNode)
	}

	if len(document.Nodes) != 4 {
		t.Errorf("nodes = %d, ожидали 4", len(document.Nodes))
	}
	if len(document.Evidence) != 3 {
		t.Fatalf("evidence = %d, ожидали 3", len(document.Evidence))
	}
	first := document.Evidence[0]
	if first.NodeID != "node-function" || first.SourceID != "src-interview" || first.RefID != "ref-function" {
		t.Errorf("evidence[0] = %+v, ожидали node_id/source_id/ref_id function-узла", first)
	}
	if first.Quote != "Цитата про анализ требований" {
		t.Errorf("evidence[0].quote = %q", first.Quote)
	}
	if document.Conflicts == nil || len(document.Conflicts) != 0 {
		t.Errorf("conflicts = %+v, ожидали пустой массив", document.Conflicts)
	}
	if document.FinalMessage != "" {
		t.Errorf("final_message = %q, ожидали пустое без чата", document.FinalMessage)
	}

	// Контракт JSON: массивы отдаются как [], а не null.
	raw := rec.Body.String()
	if !strings.Contains(raw, `"conflicts": []`) {
		t.Errorf("conflicts должен быть [], тело: %s", raw)
	}
	if !strings.Contains(raw, `"final_message": ""`) {
		t.Errorf("final_message должен присутствовать как пустая строка, тело: %s", raw)
	}
}

func TestExportRunRoles(t *testing.T) {
	handler := NewServer(exportSeedStore()).Routes()
	cases := []struct {
		role string
		want int
	}{
		{"admin", http.StatusOK},
		{"teacher", http.StatusOK},
		{"rop", http.StatusOK},
		{"guest", http.StatusOK},
		{"student", http.StatusOK},
		{"", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run("role="+tc.role, func(t *testing.T) {
			rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export", tc.role, "")
			if rec.Code != tc.want {
				t.Fatalf("роль %q: ожидали %d, получили %d (%s)", tc.role, tc.want, rec.Code, rec.Body.String())
			}
			if tc.want == http.StatusOK {
				if disposition := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disposition, "attachment;") {
					t.Fatalf("ожидали attachment, получили %q", disposition)
				}
			}
		})
	}
}

func TestExportRunFormatValidation(t *testing.T) {
	handler := NewServer(exportSeedStore()).Routes()

	for _, bad := range []string{"xml", "markdown", "html", "pdf", "md,json", "MD2"} {
		t.Run("bad="+bad, func(t *testing.T) {
			rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format="+url.QueryEscape(bad), "teacher", "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("format=%q: ожидали 400, получили %d (%s)", bad, rec.Code, rec.Body.String())
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("тело ошибки не JSON: %v", err)
			}
			if body.Code != "BAD_REQUEST" {
				t.Errorf("format=%q: code = %q, ожидали BAD_REQUEST", bad, body.Code)
			}
			if !strings.Contains(body.Message, "md") || !strings.Contains(body.Message, "json") {
				t.Errorf("format=%q: сообщение не перечисляет форматы: %q", bad, body.Message)
			}
		})
	}

	// Регистр и пробелы вокруг значения нормализуются.
	for _, tc := range []struct {
		format     string
		wantPrefix string
		wantSuffix string
	}{
		{"MD", "text/markdown", ".md"},
		{"Json", "application/json", ".json"},
		{"%20json%20", "application/json", ".json"},
	} {
		rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format="+tc.format, "teacher", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("format=%q: ожидали 200, получили %d (%s)", tc.format, rec.Code, rec.Body.String())
		}
		if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, tc.wantPrefix) {
			t.Errorf("format=%q: Content-Type = %q, ожидали префикс %q", tc.format, contentType, tc.wantPrefix)
		}
		if name := exportFilenameFromDisposition(t, rec.Header().Get("Content-Disposition")); !strings.HasSuffix(name, tc.wantSuffix) {
			t.Errorf("format=%q: filename = %q, ожидали суффикс %q", tc.format, name, tc.wantSuffix)
		}
	}
}

func TestExportRunUnknownRun(t *testing.T) {
	handler := NewServer(exportSeedStore()).Routes()
	for _, path := range []string{
		"/api/v1/runs/missing/export",
		"/api/v1/runs/missing/export?format=json",
		"/api/v1/runs/not-a-uuid/export",
	} {
		rec := do(t, handler, http.MethodGet, path, "admin", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: ожидали 404, получили %d (%s)", path, rec.Code, rec.Body.String())
		}
		var body errorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "NOT_FOUND" {
			t.Fatalf("%s: ожидали код NOT_FOUND, получили %q (%v)", path, body.Code, err)
		}
	}
}

func TestExportRunFormatCheckedBeforeRunLookup(t *testing.T) {
	handler := NewServer(exportSeedStore()).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/missing/export?format=xml", "teacher", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("невалидный format должен давать 400 даже для несуществующего run, получили %d", rec.Code)
	}
}

func TestExportRunStateErrors(t *testing.T) {
	t.Run("not finished", func(t *testing.T) {
		store := newMemStore()
		store.seedRun(domain.Run{ID: "run-pending", Status: domain.RunPending, Profession: "cook", Stage: domain.StageCreated})
		handler := NewServer(store).Routes()
		rec := do(t, handler, http.MethodGet, "/api/v1/runs/run-pending/export", "teacher", "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("ожидали 409, получили %d (%s)", rec.Code, rec.Body.String())
		}
		var body errorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "RUN_NOT_FINISHED" {
			t.Fatalf("ожидали код RUN_NOT_FINISHED, получили %q (%v)", body.Code, err)
		}
	})

	t.Run("done without tree", func(t *testing.T) {
		store := newMemStore()
		store.seedRun(domain.Run{ID: "run-empty", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
		handler := NewServer(store).Routes()
		rec := do(t, handler, http.MethodGet, "/api/v1/runs/run-empty/export", "teacher", "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("ожидали 409, получили %d (%s)", rec.Code, rec.Body.String())
		}
		var body errorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "RUN_HAS_NO_TREE" {
			t.Fatalf("ожидали код RUN_HAS_NO_TREE, получили %q (%v)", body.Code, err)
		}
	})
}

func TestExportRunIncludesConflictsAndSources(t *testing.T) {
	const secretSourceBody = "SECRET SOURCE BODY DO NOT EXPORT"
	stub := &exportEnrichmentStub{
		Store:     exportSeedStore(),
		conflicts: []domain.Conflict{sampleConflict("conflict-1", "node-function")},
		sources: []domain.Source{{
			ID:        "src-interview",
			Type:      domain.SourceInterview,
			Title:     "Интервью с аналитиком",
			SourceURL: "https://example.test/interview",
			Content:   secretSourceBody,
		}},
	}
	handler := NewServer(stub).Routes()

	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=json", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	var document runExport
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatalf("ответ не разобрался как runExport: %v", err)
	}
	if len(document.Conflicts) != 1 || document.Conflicts[0].ID != "conflict-1" {
		t.Fatalf("conflicts = %+v, ожидали один conflict-1", document.Conflicts)
	}
	if len(document.Sources) != 1 {
		t.Fatalf("sources = %+v, ожидали один источник", document.Sources)
	}
	if document.Sources[0].ID != "src-interview" || document.Sources[0].Type != domain.SourceInterview {
		t.Errorf("sources[0] = %+v", document.Sources[0])
	}
	// Полный текст источника наружу не отдаётся даже если хранилище его вернуло.
	if document.Sources[0].Content != "" {
		t.Errorf("content источника попал в JSON-экспорт: %q", document.Sources[0].Content)
	}
	if strings.Contains(rec.Body.String(), secretSourceBody) {
		t.Error("тело источника утекло в JSON-экспорт")
	}

	markdown := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=md", "teacher", "")
	if markdown.Code != http.StatusOK {
		t.Fatalf("markdown: ожидали 200, получили %d", markdown.Code)
	}
	body := markdown.Body.String()
	for _, want := range []string{
		"## Conflicts",
		"conflict-1",
		"Winner: Аналитик",
		"higher source priority wins: interview (300)",
		"## Sources",
		"Интервью с аналитиком",
		"https://example.test/interview",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Markdown не содержит %q\n---\n%s", want, body)
		}
	}
	if strings.Contains(body, secretSourceBody) {
		t.Error("тело источника утекло в Markdown-экспорт")
	}
}

func TestExportRunEnrichmentErrorsDoNotFailExport(t *testing.T) {
	stub := &exportEnrichmentStub{
		Store:       exportSeedStore(),
		conflictErr: errors.New("conflict query failed"),
		sourceErr:   errors.New("source query failed"),
	}
	handler := NewServer(stub).Routes()

	for _, suffix := range []string{"?format=md", "?format=json"} {
		rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export"+suffix, "teacher", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("suffix=%q: вспомогательные ошибки не должны ломать экспорт, получили %d (%s)", suffix, rec.Code, rec.Body.String())
		}
		if suffix == "?format=md" && !strings.Contains(rec.Body.String(), "No source conflicts were resolved for this run.") {
			t.Errorf("ожидали пустую секцию конфликтов, получили:\n%s", rec.Body.String())
		}
	}
}

func TestExportRunFinalMessageFromChat(t *testing.T) {
	chatStub := &exportChatStub{messages: []chat.Message{
		{ID: "m1", Role: "agent", Content: "Первый вопрос интервью"},
		{ID: "m2", Role: "user", Content: "Ответ респондента"},
		{ID: "m3", Role: "agent", Content: "Интервью завершено. Курсы Stepik: SQL с нуля"},
	}}
	handler := NewServerWithChat(exportSeedStore(), chatStub).Routes()

	markdown := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=md", "teacher", "")
	if markdown.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", markdown.Code, markdown.Body.String())
	}
	body := markdown.Body.String()
	for _, want := range []string{
		"## Final message (recommendations / courses)",
		"Интервью завершено. Курсы Stepik: SQL с нуля",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Markdown не содержит %q\n---\n%s", want, body)
		}
	}
	if strings.Contains(body, "Первый вопрос интервью") {
		t.Error("в экспорт попало не финальное сообщение чата")
	}

	jsonRec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=json", "teacher", "")
	var document runExport
	if err := json.Unmarshal(jsonRec.Body.Bytes(), &document); err != nil {
		t.Fatalf("ответ не разобрался как runExport: %v", err)
	}
	if document.FinalMessage != "Интервью завершено. Курсы Stepik: SQL с нуля" {
		t.Errorf("final_message = %q", document.FinalMessage)
	}
}

func TestExportRunFinalMessageUnavailable(t *testing.T) {
	for _, chatErr := range []error{chat.ErrNotFound, errors.New("chat service down")} {
		t.Run("err="+chatErr.Error(), func(t *testing.T) {
			handler := NewServerWithChat(exportSeedStore(), &exportChatStub{err: chatErr}).Routes()
			markdown := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=md", "teacher", "")
			if markdown.Code != http.StatusOK {
				t.Fatalf("ожидали 200, получили %d (%s)", markdown.Code, markdown.Body.String())
			}
			if strings.Contains(markdown.Body.String(), "## Final message") {
				t.Error("секция финального сообщения не должна появляться без сообщения")
			}
			jsonRec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=json", "teacher", "")
			var document runExport
			if err := json.Unmarshal(jsonRec.Body.Bytes(), &document); err != nil {
				t.Fatalf("ответ не разобрался как runExport: %v", err)
			}
			if document.FinalMessage != "" {
				t.Errorf("final_message = %q, ожидали пустое", document.FinalMessage)
			}
		})
	}
}

func TestExportRunKeepsOrphanNode(t *testing.T) {
	store := exportSeedStore()
	store.seedNodes(exportTestRunID, domain.Node{
		ID:       "node-orphan",
		Type:     domain.NodeAction,
		Title:    "Осиротевшее действие",
		ParentID: strPtr("missing-parent"),
		Evidence: []domain.Evidence{{ID: "ev-orphan", SourceID: "src-doc", RefID: "ref-orphan", Quote: "Цитата сироты"}},
	})
	handler := NewServer(store).Routes()

	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=json", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rec.Code)
	}
	var document runExport
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatalf("ответ не разобрался как runExport: %v", err)
	}
	if len(document.Tree) != 2 {
		t.Fatalf("ожидали два корня (роль + сирота), получили %d", len(document.Tree))
	}
	found := false
	for _, root := range document.Tree {
		if root.ID == "node-orphan" {
			found = true
		}
	}
	if !found {
		t.Fatalf("осиротевший узел потерян из дерева: %+v", document.Tree)
	}

	markdown := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export?format=md", "teacher", "")
	if !strings.Contains(markdown.Body.String(), "Осиротевшее действие") {
		t.Error("Markdown не содержит осиротевший узел")
	}
}

func TestExportRunSafeFilename(t *testing.T) {
	const unsafeRunID = `bad"name;..*<>|Проф`
	store := newMemStore()
	store.seedRun(domain.Run{ID: unsafeRunID, Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	store.seedNodes(unsafeRunID, domain.Node{
		ID: "n1", Type: domain.NodeRole, Title: "Cook",
		Evidence: []domain.Evidence{{ID: "e1", SourceID: "s1", Quote: "q1"}},
	})
	handler := NewServer(store).Routes()
	escapedID := url.PathEscape(unsafeRunID)

	for _, tc := range []struct {
		suffix     string
		wantSuffix string
	}{
		{"?format=md", ".md"},
		{"?format=json", ".json"},
	} {
		rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+escapedID+"/export"+tc.suffix, "teacher", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("suffix=%q: ожидали 200, получили %d (%s)", tc.suffix, rec.Code, rec.Body.String())
		}
		name := exportFilenameFromDisposition(t, rec.Header().Get("Content-Disposition"))
		if !strings.HasPrefix(name, "run-") || !strings.HasSuffix(name, tc.wantSuffix) {
			t.Errorf("filename = %q, ожидали run-*.%s", name, strings.TrimPrefix(tc.wantSuffix, "."))
		}
		for _, forbidden := range []string{`/`, `\`, `"`, `;`, `*`, `<`, `>`, `|`, `..`} {
			if strings.Contains(name, forbidden) {
				t.Errorf("filename = %q содержит небезопасный фрагмент %q", name, forbidden)
			}
		}
	}
}

func TestExportRunDoesNotLeakSecrets(t *testing.T) {
	sentinels := map[string]string{
		"JWT_SECRET":        "sentinel-jwt-secret-value",
		"DATABASE_URL":      "postgres://sentinel-user:sentinel-password@db:5432/app",
		"LLM_API_KEY":       "sentinel-llm-api-key",
		"POSTGRES_PASSWORD": "sentinel-postgres-password",
	}
	for key, value := range sentinels {
		t.Setenv(key, value)
	}

	handler := NewServerWithChat(exportSeedStore(), &exportChatStub{
		messages: []chat.Message{{ID: "m1", Role: "agent", Content: "Интервью завершено. Курсы готовы"}},
	}).Routes()

	for _, suffix := range []string{"", "?format=json"} {
		rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+exportTestRunID+"/export"+suffix, "teacher", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("suffix=%q: ожидали 200, получили %d", suffix, rec.Code)
		}
		body := rec.Body.String()
		for key, value := range sentinels {
			if strings.Contains(body, value) {
				t.Errorf("suffix=%q: в экспорт утекло значение %s", suffix, key)
			}
		}
	}
}
