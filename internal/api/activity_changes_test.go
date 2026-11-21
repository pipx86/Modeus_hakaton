package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

// Карточка 31: семантический матчинг узлов двух независимых исследований,
// чистый сборщик отчёта и HTTP-контракт на pgxmock (без реальной PostgreSQL).

const (
	acRunID         = "10101010-1010-4010-8010-101010101010"
	acAgainstRunID  = "20202020-2020-4020-8020-202020202020"
	acRootID        = "30303030-3030-4030-8030-303030303030"
	acFunctionID    = "40404040-4040-4040-8040-404040404040"
	acTaskID        = "50505050-5050-4050-8050-505050505050"
	acActionID      = "60606060-6060-4060-8060-606060606060"
	acAddedTaskID   = "70707070-7070-4070-8070-707070707070"
	acRemovedTaskID = "80808080-8080-4080-8080-808080808080"
	acSourceOneID   = "90909090-9090-4090-8090-909090909090"
	acSourceTwoID   = "a0a0a0a0-a0a0-40a0-80a0-a0a0a0a0a0a0"
	acSourceThreeID = "b0b0b0b0-b0b0-40b0-80b0-b0b0b0b0b0b0"
	acSourceFourID  = "c0c0c0c0-c0c0-40c0-80c0-c0c0c0c0c0c0"
)

// --- чистые хелперы ---

func TestNormalizeActivityTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"регистр и пробелы", "  Писать SQL-запросы ", "писать sql запросы"},
		{"ё приводится к е", "Отчётность и Ёмкость", "отчетность и емкость"},
		{"пунктуация становится пробелом", "Проверка!!!   данных.", "проверка данных"},
		{"дефис внутри слова", "SQL-запросы", "sql запросы"},
		{"пустая строка", "   ", ""},
		{"цифры сохраняются", "Отчёт 2.0", "отчет 2 0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeActivityTitle(test.input); got != test.want {
				t.Fatalf("normalizeActivityTitle(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestActivitySemanticKey(t *testing.T) {
	base := activitySemanticKey(domain.NodeTask, "Собирать требования", "аналитик данных\x1fанализ данных")
	same := activitySemanticKey(domain.NodeTask, "  собирать   ТРЕБОВАНИЯ ", "аналитик данных\x1fанализ данных")
	if base != same {
		t.Fatalf("регистр и пробелы не должны влиять на ключ: %q != %q", base, same)
	}
	if base == activitySemanticKey(domain.NodeAction, "Собирать требования", "аналитик данных\x1fанализ данных") {
		t.Fatal("разные типы узлов не должны совпадать")
	}
	if base == activitySemanticKey(domain.NodeTask, "Собирать требования", "аналитик данных\x1fдругая функция") {
		t.Fatal("разный путь предков не должен совпадать")
	}
	// Название и путь не склеиваются: разделитель держит границу.
	left := activitySemanticKey(domain.NodeTask, "а б", "в")
	right := activitySemanticKey(domain.NodeTask, "а", "б в")
	if left == right {
		t.Fatalf("ключи с разной границей названия и пути совпали: %q", left)
	}
}

// acEvidence собирает обоснование для чистых тестов отчёта.
func acEvidence(id, sourceID, sourceTitle string, sourceType domain.SourceType) activityChangeEvidence {
	return activityChangeEvidence{ID: id, SourceID: sourceID, SourceTitle: sourceTitle, SourceType: sourceType}
}

// acNode собирает узел дерева для чистых тестов отчёта.
func acNode(id, parentID string, nodeType domain.NodeType, title, description string, evidence ...activityChangeEvidence) activityChangeTreeNode {
	return activityChangeTreeNode{ID: id, ParentID: parentID, Type: nodeType, Title: title, Description: description, Evidence: evidence}
}

// acSampleTrees — пара деревьев с добавленным, удалённым и изменёнными узлами.
func acSampleTrees() ([]activityChangeTreeNode, []activityChangeTreeNode) {
	run := []activityChangeTreeNode{
		acNode(acRootID, "", domain.NodeRole, "Аналитик данных", ""),
		acNode(acFunctionID, acRootID, domain.NodeFunction, "Анализ данных", ""),
		acNode(acTaskID, acFunctionID, domain.NodeTask, "Собирать требования", "",
			acEvidence("e1", acSourceOneID, "Интервью №1", domain.SourceInterview),
			acEvidence("e2", acSourceTwoID, "Вакансия DS", domain.SourceVacancy)),
		acNode(acActionID, acFunctionID, domain.NodeAction, "Проверять гипотезы", "Новое описание",
			acEvidence("e3", acSourceOneID, "Интервью №1", domain.SourceInterview)),
		acNode(acAddedTaskID, acFunctionID, domain.NodeTask, "Готовить отчёт", "",
			acEvidence("e4", acSourceThreeID, "Профстандарт", domain.SourceStandard)),
	}
	against := []activityChangeTreeNode{
		acNode(acRootID, "", domain.NodeRole, "Аналитик данных", ""),
		acNode(acFunctionID, acRootID, domain.NodeFunction, "Анализ данных", ""),
		acNode(acTaskID, acFunctionID, domain.NodeTask, "собирать требования.", "",
			acEvidence("e5", acSourceOneID, "Интервью №1", domain.SourceInterview)),
		acNode(acActionID, acFunctionID, domain.NodeAction, "Проверять гипотезы", "Старое описание",
			acEvidence("e6", acSourceOneID, "Интервью №1", domain.SourceInterview)),
		acNode(acRemovedTaskID, acFunctionID, domain.NodeTask, "Устаревшая задача", "",
			acEvidence("e7", acSourceFourID, "Старый отчёт", domain.SourceDocument)),
	}
	return run, against
}

func TestBuildActivityChanges(t *testing.T) {
	run, against := acSampleTrees()
	report := buildActivityChanges(acRunID, acAgainstRunID, "Аналитик данных", run, "Аналитик данных", against)
	if !report.SameProfession {
		t.Fatal("одинаковые профессии должны давать same_profession=true")
	}
	if len(report.Added) != 1 || len(report.Removed) != 1 || len(report.Changed) != 2 {
		t.Fatalf("категории отчёта: %+v", report)
	}
	added := report.Added[0]
	if added.NodeID != acAddedTaskID || added.NodeType != domain.NodeTask {
		t.Fatalf("добавленный узел: %+v", added)
	}
	if added.ParentTitle == nil || *added.ParentTitle != "Анализ данных" {
		t.Fatalf("родитель добавленного узла: %+v", added.ParentTitle)
	}
	if added.EvidenceCount != 1 || len(added.NewSources) != 1 || added.NewSources[0].SourceID != acSourceThreeID {
		t.Fatalf("новые материалы добавленного узла: %+v", added)
	}
	if len(added.ChangedFields) != 0 {
		t.Fatalf("у added не должно быть changed_fields: %+v", added.ChangedFields)
	}
	removed := report.Removed[0]
	if removed.NodeID != acRemovedTaskID || removed.ParentTitle == nil || *removed.ParentTitle != "Анализ данных" {
		t.Fatalf("удалённый узел: %+v", removed)
	}
	if removed.EvidenceCount != 1 || removed.NewSources == nil || len(removed.NewSources) != 0 {
		t.Fatalf("у removed new_sources должен быть пустым массивом: %+v", removed)
	}
	// Изменения отсортированы по уровню: задача раньше действия.
	changedTask := report.Changed[0]
	if changedTask.NodeID != acTaskID {
		t.Fatalf("первым должен идти изменённый task: %+v", report.Changed)
	}
	if len(changedTask.ChangedFields) != 1 || changedTask.ChangedFields[0] != "title" {
		t.Fatalf("поля изменённой задачи: %+v", changedTask.ChangedFields)
	}
	if changedTask.EvidenceCount != 2 || len(changedTask.NewSources) != 1 || changedTask.NewSources[0].SourceID != acSourceTwoID {
		t.Fatalf("новые материалы изменённой задачи: %+v", changedTask)
	}
	changedAction := report.Changed[1]
	if changedAction.NodeID != acActionID || len(changedAction.ChangedFields) != 1 || changedAction.ChangedFields[0] != "description" {
		t.Fatalf("изменённое действие: %+v", changedAction)
	}
	if changedAction.NewSources == nil || len(changedAction.NewSources) != 0 {
		t.Fatalf("у действия нет новых материалов: %+v", changedAction.NewSources)
	}
	want := activityChangeSummary{Added: 1, Removed: 1, Changed: 2, SourcesAdded: 2, SourcesRemoved: 1}
	if report.Summary != want {
		t.Fatalf("summary = %+v, want %+v", report.Summary, want)
	}
}

func TestBuildActivityChangesMatchesByAncestorPath(t *testing.T) {
	// Одинаковая задача под одноимёнными функциями, но с разными путями
	// предков: это разные узлы деятельности, а не изменение.
	run := []activityChangeTreeNode{
		acNode("r", "", domain.NodeRole, "Роль", ""),
		acNode("f1", "r", domain.NodeFunction, "Функция А", ""),
		acNode("t1", "f1", domain.NodeTask, "Готовить отчёт", ""),
	}
	against := []activityChangeTreeNode{
		acNode("r", "", domain.NodeRole, "Роль", ""),
		acNode("f2", "r", domain.NodeFunction, "Функция Б", ""),
		acNode("t2", "f2", domain.NodeTask, "Готовить отчёт", ""),
	}
	report := buildActivityChanges("run", "against", "Роль", run, "Роль", against)
	// Одноимённые функции с разными путями тоже считаются разными узлами,
	// поэтому в added попадают и функция, и задача.
	if added := acFindNode(report.Added, "t1"); added == nil {
		t.Fatalf("добавленный узел по другому пути: %+v", report.Added)
	}
	if removed := acFindNode(report.Removed, "t2"); removed == nil {
		t.Fatalf("удалённый узел по другому пути: %+v", report.Removed)
	}
	if len(report.Changed) != 0 {
		t.Fatalf("узлы с разным путём предков не должны считаться изменёнными: %+v", report.Changed)
	}
}

func TestBuildActivityChangesDuplicateSemanticKeys(t *testing.T) {
	// Два узла с одинаковым ключом (одинаковое название под одноимёнными
	// функциями) сопоставляются по порядку, лишний честно попадает в added.
	run := []activityChangeTreeNode{
		acNode("r1", "", domain.NodeRole, "Роль", ""),
		acNode("f1", "r1", domain.NodeFunction, "Функция", ""),
		acNode("t1", "f1", domain.NodeTask, "Отчёт", ""),
		acNode("r2", "", domain.NodeRole, "Роль", ""),
		acNode("f2", "r2", domain.NodeFunction, "Функция", ""),
		acNode("t2", "f2", domain.NodeTask, "Отчёт", ""),
	}
	against := []activityChangeTreeNode{
		acNode("a1", "", domain.NodeRole, "Роль", ""),
		acNode("b1", "a1", domain.NodeFunction, "Функция", ""),
		acNode("c1", "b1", domain.NodeTask, "Отчёт", ""),
	}
	report := buildActivityChanges("run", "against", "Роль", run, "Роль", against)
	if added := acFindNode(report.Added, "t2"); added == nil {
		t.Fatalf("лишний дубль должен попасть в added: %+v", report.Added)
	}
	if acFindNode(report.Added, "t1") != nil {
		t.Fatalf("сопоставленный дубль не должен быть в added: %+v", report.Added)
	}
	if len(report.Removed) != 0 || len(report.Changed) != 0 {
		t.Fatalf("duplicates: removed=%+v changed=%+v", report.Removed, report.Changed)
	}
}

// acFindNode ищет узел отчёта по id.
func acFindNode(nodes []activityChangeNode, nodeID string) *activityChangeNode {
	for i := range nodes {
		if nodes[i].NodeID == nodeID {
			return &nodes[i]
		}
	}
	return nil
}

func TestBuildActivityChangesDifferentProfessions(t *testing.T) {
	run, against := acSampleTrees()
	report := buildActivityChanges(acRunID, acAgainstRunID, "Аналитик данных", run, "Инженер данных", against)
	if report.SameProfession {
		t.Fatal("разные профессии должны давать same_profession=false")
	}
	if report.Summary.Added != 1 || report.Summary.Removed != 1 {
		t.Fatalf("разные профессии не отменяют сравнение: %+v", report.Summary)
	}
	// Регистр и «ё» не должны давать ложную пометку.
	same := buildActivityChanges(acRunID, acAgainstRunID, "Аналитик данных", run, "  аналитик ДАННЫХ ", against)
	if !same.SameProfession {
		t.Fatal("нормализованно одинаковые профессии должны совпадать")
	}
}

func TestBuildActivityChangesNewSourcesIgnoreSourcesOfOtherNodes(t *testing.T) {
	// Источник, который уже используется в дереве противника (пусть и другим
	// узлом), не является новым материалом.
	run := []activityChangeTreeNode{
		acNode("n1", "", domain.NodeTask, "Новая задача", "", acEvidence("e1", acSourceOneID, "Интервью", domain.SourceInterview)),
	}
	against := []activityChangeTreeNode{
		acNode("m1", "", domain.NodeFunction, "Функция", "", acEvidence("e2", acSourceOneID, "Интервью", domain.SourceInterview)),
	}
	report := buildActivityChanges("run", "against", "Профессия", run, "Профессия", against)
	if len(report.Added) != 1 || len(report.Added[0].NewSources) != 0 {
		t.Fatalf("источник противника не должен считаться новым: %+v", report.Added)
	}
	if report.Summary.SourcesAdded != 0 || report.Summary.SourcesRemoved != 0 {
		t.Fatalf("summary источников: %+v", report.Summary)
	}
}

func TestBuildActivityChangesEmptyTrees(t *testing.T) {
	report := buildActivityChanges(acRunID, acAgainstRunID, "Профессия", nil, "Профессия", nil)
	if report.Added == nil || report.Removed == nil || report.Changed == nil {
		t.Fatalf("срезы должны быть не-nil, чтобы JSON отдавал []: %+v", report)
	}
	if report.Summary != (activityChangeSummary{}) {
		t.Fatalf("пустые деревья дают нулевой summary: %+v", report.Summary)
	}
}

// --- HTTP-контракт на pgxmock ---

// acTreeRows собирает строки запроса дерева исследования.
func acTreeRows(profession string, rows ...[]string) *pgxmock.Rows {
	result := pgxmock.NewRows([]string{
		"profession", "node", "parent", "type", "title", "description",
		"evidence", "source", "source_title", "source_type",
	})
	for _, row := range rows {
		values := make([]any, 0, len(row))
		for _, value := range row {
			values = append(values, value)
		}
		result.AddRow(values...)
	}
	return result
}

// acEmptyTreeRow — строка прогона без узлов: запрос возвращает её, чтобы
// отличить «дерева нет» (409) от «исследования нет» (404).
func acEmptyTreeRow(profession string) []string {
	return []string{profession, "", "", "", "", "", "", "", "", ""}
}

func TestActivityChangesHTTPWithPostgresMock(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(acRunID).
		WillReturnRows(runCardsGetRunRow(acRunID, ""))
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(acAgainstRunID).
		WillReturnRows(runCardsGetRunRow(acAgainstRunID, ""))
	pool.ExpectQuery("FROM runs r").WithArgs(acRunID).WillReturnRows(acTreeRows("Аналитик данных",
		[]string{"Аналитик данных", acRootID, "", "role", "Аналитик данных", "", "", "", "", ""},
		[]string{"Аналитик данных", acFunctionID, acRootID, "function", "Анализ данных", "", "", "", "", ""},
		[]string{"Аналитик данных", acTaskID, acFunctionID, "task", "Собирать требования", "", "e1", acSourceOneID, "Интервью №1", "interview"},
		[]string{"Аналитик данных", acTaskID, acFunctionID, "task", "Собирать требования", "", "e2", acSourceTwoID, "Вакансия DS", "vacancy"},
		[]string{"Аналитик данных", acAddedTaskID, acFunctionID, "task", "Готовить отчёт", "", "e4", acSourceThreeID, "Профстандарт", "standard"},
	))
	pool.ExpectQuery("FROM runs r").WithArgs(acAgainstRunID).WillReturnRows(acTreeRows("Аналитик данных",
		[]string{"Аналитик данных", acRootID, "", "role", "Аналитик данных", "", "", "", "", ""},
		[]string{"Аналитик данных", acFunctionID, acRootID, "function", "Анализ данных", "", "", "", "", ""},
		[]string{"Аналитик данных", acTaskID, acFunctionID, "task", "Собирать требования", "", "e5", acSourceOneID, "Интервью №1", "interview"},
	))
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet,
		"/api/v1/runs/"+acRunID+"/activity-changes?against="+acAgainstRunID, "specialist", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		RunID          string `json:"run_id"`
		AgainstRunID   string `json:"against_run_id"`
		SameProfession bool   `json:"same_profession"`
		Added          []struct {
			NodeID        string  `json:"node_id"`
			ParentTitle   *string `json:"parent_title"`
			EvidenceCount int     `json:"evidence_count"`
			NewSources    []struct {
				SourceID    string `json:"source_id"`
				SourceTitle string `json:"source_title"`
				SourceType  string `json:"source_type"`
			} `json:"new_sources"`
			ChangedFields []string `json:"changed_fields"`
		} `json:"added"`
		Removed []map[string]any      `json:"removed"`
		Changed []map[string]any      `json:"changed"`
		Summary activityChangeSummary `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	if body.RunID != acRunID || body.AgainstRunID != acAgainstRunID || !body.SameProfession {
		t.Fatalf("шапка отчёта: %s", rec.Body.String())
	}
	if len(body.Added) != 1 || body.Added[0].NodeID != acAddedTaskID {
		t.Fatalf("added: %s", rec.Body.String())
	}
	if body.Added[0].ParentTitle == nil || *body.Added[0].ParentTitle != "Анализ данных" {
		t.Fatalf("parent_title: %s", rec.Body.String())
	}
	if body.Added[0].EvidenceCount != 1 || len(body.Added[0].NewSources) != 1 ||
		body.Added[0].NewSources[0].SourceID != acSourceThreeID ||
		body.Added[0].NewSources[0].SourceTitle != "Профстандарт" ||
		body.Added[0].NewSources[0].SourceType != "standard" {
		t.Fatalf("new_sources: %s", rec.Body.String())
	}
	if body.Summary.Added != 1 || body.Summary.SourcesAdded != 2 || body.Summary.SourcesRemoved != 0 {
		t.Fatalf("summary: %+v", body.Summary)
	}
	for _, key := range []string{"run_id", "against_run_id", "same_profession", "added", "removed", "changed", "summary"} {
		if _, exists := jsonObject(t, rec.Body.Bytes())[key]; !exists {
			t.Fatalf("в ответе нет поля %q: %s", key, rec.Body.String())
		}
	}
}

func TestActivityChangesTreeMissing(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(acRunID).
		WillReturnRows(runCardsGetRunRow(acRunID, ""))
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(acAgainstRunID).
		WillReturnRows(runCardsGetRunRow(acAgainstRunID, ""))
	pool.ExpectQuery("FROM runs r").WithArgs(acRunID).
		WillReturnRows(acTreeRows("Аналитик данных", acEmptyTreeRow("Аналитик данных")))
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet,
		"/api/v1/runs/"+acRunID+"/activity-changes?against="+acAgainstRunID, "user", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (%s), want 409", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "TREE_MISSING" {
		t.Fatalf("code = %q, want TREE_MISSING", code)
	}
}

func TestActivityChangesMissingAgainstRun(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(acRunID).
		WillReturnRows(runCardsGetRunRow(acRunID, ""))
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(acAgainstRunID).
		WillReturnError(pgx.ErrNoRows)
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet,
		"/api/v1/runs/"+acRunID+"/activity-changes?against="+acAgainstRunID, "user", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d (%s), want 404", rec.Code, rec.Body.String())
	}
}

func TestActivityChangesInvalidAgainst(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	for _, path := range []string{
		"/api/v1/runs/" + acRunID + "/activity-changes",
		"/api/v1/runs/" + acRunID + "/activity-changes?against=",
		"/api/v1/runs/" + acRunID + "/activity-changes?against=not-a-uuid",
	} {
		rec := do(t, handler, http.MethodGet, path, "user", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d (%s), want 400", path, rec.Code, rec.Body.String())
		}
	}
}

func TestActivityChangesRequireAuthAndAccess(t *testing.T) {
	store := newMemStore()
	store.seedRun(domain.Run{ID: acRunID, Profession: "Аналитик данных", CreatedBy: "test-user"})
	store.seedRun(domain.Run{ID: acAgainstRunID, Profession: "Аналитик данных", CreatedBy: "test-user"})
	foreign := "d0d0d0d0-d0d0-40d0-80d0-d0d0d0d0d0d0"
	store.seedRun(domain.Run{ID: foreign, Profession: "Аналитик данных", CreatedBy: "someone-else"})
	handler := NewServer(store).Routes()
	path := "/api/v1/runs/" + acRunID + "/activity-changes?against=" + acAgainstRunID

	if rec := do(t, handler, http.MethodGet, path, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("без токена: status = %d, want 401", rec.Code)
	}
	// Чужой противник не раскрывается: доступ проверяется к обоим прогонам.
	foreignPath := "/api/v1/runs/" + acRunID + "/activity-changes?against=" + foreign
	if rec := do(t, handler, http.MethodGet, foreignPath, "user", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("чужой противник (user): status = %d (%s), want 403", rec.Code, rec.Body.String())
	}
	if rec := do(t, handler, http.MethodGet, foreignPath, "specialist", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("чужой противник (specialist): status = %d, want 403", rec.Code)
	}
	if rec := do(t, handler, http.MethodGet, foreignPath, "admin", ""); rec.Code != http.StatusNotImplemented {
		t.Fatalf("чужой противник (admin): status = %d, want 501", rec.Code)
	}
	for _, role := range []string{"user", "specialist", "admin"} {
		if rec := do(t, handler, http.MethodGet, path, role, ""); rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s на in-memory store: status = %d (%s), want 501", role, rec.Code, rec.Body.String())
		}
	}
}

// jsonObject разбирает ответ в map для проверки наличия полей.
func jsonObject(t *testing.T, payload []byte) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("ответ не JSON-объект: %v (%s)", err, string(payload))
	}
	return body
}

// errorCode читает машиночитаемый код ошибки.
func errorCode(t *testing.T, payload []byte) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("ошибка не JSON: %v (%s)", err, string(payload))
	}
	return body.Code
}
