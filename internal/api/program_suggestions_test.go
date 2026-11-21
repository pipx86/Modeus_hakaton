package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

// Карточка 32: чистый сборщик предложений по программе, приоритеты,
// нейтральные формулировки и HTTP-контракт на pgxmock (без PostgreSQL).

const (
	psRunID        = "11111111-2222-4333-8444-555555555555"
	psProgramID    = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	psNodeTask     = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	psNodeAction   = "12345678-1234-4234-8234-123456789012"
	psNodeMapped   = "23456789-2345-4345-8345-234567890123"
	psNodeConflict = "34567890-3456-4456-8456-345678901234"
	psCompetencyA  = "45678901-4567-4567-8567-456789012345"
	psCompetencyU  = "56789012-5678-4678-8678-567890123456"
	psCompetencyR  = "67890123-6789-4789-8789-678901234567"
	psCompetencyC  = "78901234-7890-4890-8890-789012345678"
)

// psGeneratedAt фиксирует время отчёта в чистых тестах.
var psGeneratedAt = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// psSampleInput — узлы, связи и компетенции, покрывающие все четыре вида
// предложений карточки 32.
func psSampleInput() ([]programSuggestionNode, []programSuggestionLink, []programSuggestionCompetency) {
	nodes := []programSuggestionNode{
		{ID: psNodeTask, Title: "Собирать требования", Type: domain.NodeTask},
		{ID: psNodeAction, Title: "Проверять гипотезы", Type: domain.NodeAction},
		{ID: psNodeMapped, Title: "Строить отчёт", Type: domain.NodeTask},
		{ID: psNodeConflict, Title: "Согласовывать макет", Type: domain.NodeAction},
	}
	links := []programSuggestionLink{
		{NodeID: psNodeMapped, CompetencyID: psCompetencyA, CompetencyTitle: "Анализ данных", Status: domain.ProgramLinkApproved},
		{NodeID: psNodeMapped, CompetencyID: psCompetencyU, CompetencyTitle: "Коммуникация", Status: domain.ProgramLinkProposed},
		// Дубль предложения (ревизия снимка) не должен раздувать счётчик.
		{NodeID: psNodeMapped, CompetencyID: psCompetencyU, CompetencyTitle: "Коммуникация", Status: domain.ProgramLinkProposed},
		{NodeID: psNodeConflict, CompetencyID: psCompetencyA, CompetencyTitle: "Анализ данных", Status: domain.ProgramLinkApproved},
		{NodeID: psNodeConflict, CompetencyID: psCompetencyR, CompetencyTitle: "Дизайн", Status: domain.ProgramLinkRejected},
	}
	competencies := []programSuggestionCompetency{
		{ID: psCompetencyA, Title: "Анализ данных"},
		{ID: psCompetencyU, Title: "Коммуникация"},
		{ID: psCompetencyR, Title: "Дизайн"},
		{ID: psCompetencyC, Title: "Проектирование"},
	}
	return nodes, links, competencies
}

// psFindProposal ищет предложение по виду и id цели.
func psFindProposal(proposals []programProposal, kind, targetID string) *programProposal {
	for i := range proposals {
		proposal := &proposals[i]
		if proposal.Kind != kind {
			continue
		}
		switch {
		case proposal.Target.NodeID != nil && *proposal.Target.NodeID == targetID:
			return proposal
		case proposal.Target.CompetencyID != nil && *proposal.Target.CompetencyID == targetID:
			return proposal
		}
	}
	return nil
}

func TestBuildProgramSuggestions(t *testing.T) {
	nodes, links, competencies := psSampleInput()
	report := buildProgramSuggestions(psRunID, psProgramID, "Прикладная математика", "program",
		nodes, links, competencies, psGeneratedAt)
	if report.ProgramID == nil || *report.ProgramID != psProgramID {
		t.Fatalf("program_id: %+v", report.ProgramID)
	}
	if report.ProgramTitle == nil || *report.ProgramTitle != "Прикладная математика" {
		t.Fatalf("program_title: %+v", report.ProgramTitle)
	}
	if !report.GeneratedAt.Equal(psGeneratedAt) {
		t.Fatalf("generated_at = %v, want %v", report.GeneratedAt, psGeneratedAt)
	}
	if report.Disclaimer != programSuggestionDisclaimer {
		t.Fatalf("disclaimer = %q", report.Disclaimer)
	}
	if len(report.Proposals) != 5 {
		t.Fatalf("предложения: %+v", report.Proposals)
	}
	// Приоритет: сначала high, затем medium (unmapped раньше unconfirmed),
	// затем low (uncovered раньше conflicting).
	wantPriorities := []string{
		suggestionPriorityHigh,
		suggestionPriorityMedium,
		suggestionPriorityMedium,
		suggestionPriorityLow,
		suggestionPriorityLow,
	}
	wantKinds := []string{
		suggestionKindUnmappedActivity,
		suggestionKindUnmappedActivity,
		suggestionKindUnconfirmedCompetency,
		suggestionKindUncoveredCompetency,
		suggestionKindConflictingLinks,
	}
	for i, proposal := range report.Proposals {
		if proposal.Priority != wantPriorities[i] || proposal.Kind != wantKinds[i] {
			t.Fatalf("proposal[%d] = %s/%s, want %s/%s", i, proposal.Kind, proposal.Priority, wantKinds[i], wantPriorities[i])
		}
		if proposal.Status != suggestionStatusProposal {
			t.Fatalf("статус предложения должен быть proposal: %+v", proposal)
		}
		if proposal.Rationale == "" {
			t.Fatalf("пустое rationale: %+v", proposal)
		}
	}
	// task без approved-связи — high и с target.node_id.
	taskProposal := psFindProposal(report.Proposals, suggestionKindUnmappedActivity, psNodeTask)
	if taskProposal == nil || taskProposal.Priority != suggestionPriorityHigh {
		t.Fatalf("непокрытая задача: %+v", taskProposal)
	}
	if taskProposal.Target.CompetencyID != nil || taskProposal.Target.CompetencyTitle != nil {
		t.Fatalf("у предложения об узле не должно быть полей компетенции: %+v", taskProposal.Target)
	}
	if !strings.Contains(taskProposal.Rationale, "Собирать требования") {
		t.Fatalf("rationale должен называть узел: %q", taskProposal.Rationale)
	}
	// action без approved-связи — medium.
	actionProposal := psFindProposal(report.Proposals, suggestionKindUnmappedActivity, psNodeAction)
	if actionProposal == nil || actionProposal.Priority != suggestionPriorityMedium {
		t.Fatalf("непокрытое действие: %+v", actionProposal)
	}
	// Компетенция только с предложениями — medium и с подписью компетенции.
	unconfirmed := psFindProposal(report.Proposals, suggestionKindUnconfirmedCompetency, psCompetencyU)
	if unconfirmed == nil || unconfirmed.Priority != suggestionPriorityMedium {
		t.Fatalf("неподтверждённая компетенция: %+v", unconfirmed)
	}
	if unconfirmed.Target.CompetencyTitle == nil || *unconfirmed.Target.CompetencyTitle != "Коммуникация" {
		t.Fatalf("подпись компетенции: %+v", unconfirmed.Target)
	}
	if !strings.Contains(unconfirmed.Rationale, "предложенные связи (1)") {
		t.Fatalf("счётчик предложенных связей должен быть уникальным: %q", unconfirmed.Rationale)
	}
	// Компетенция без связей — low.
	uncovered := psFindProposal(report.Proposals, suggestionKindUncoveredCompetency, psCompetencyC)
	if uncovered == nil || uncovered.Priority != suggestionPriorityLow {
		t.Fatalf("компетенция без связей: %+v", uncovered)
	}
	// Узел с approved и rejected связями к разным компетенциям — low.
	conflicting := psFindProposal(report.Proposals, suggestionKindConflictingLinks, psNodeConflict)
	if conflicting == nil || conflicting.Priority != suggestionPriorityLow {
		t.Fatalf("конфликтующие связи: %+v", conflicting)
	}
	if !strings.Contains(conflicting.Rationale, "Анализ данных") || !strings.Contains(conflicting.Rationale, "Дизайн") {
		t.Fatalf("rationale конфликта должен называть компетенции: %q", conflicting.Rationale)
	}
	// Формулировки нейтральные: без «пробела», «недостатка» и «слабости».
	for _, proposal := range report.Proposals {
		lower := strings.ToLower(proposal.Rationale)
		for _, forbidden := range []string{"пробел", "недостат", "слаб"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("в rationale недопустимая формулировка %q: %q", forbidden, proposal.Rationale)
			}
		}
	}
	if !strings.Contains(report.Summary, "Предложений: 5") {
		t.Fatalf("summary = %q", report.Summary)
	}
}

func TestBuildProgramSuggestionsActivityMode(t *testing.T) {
	nodes, links, competencies := psSampleInput()
	report := buildProgramSuggestions(psRunID, "", "", "activity", nodes, links, competencies, psGeneratedAt)
	if report.ProgramID != nil || report.ProgramTitle != nil {
		t.Fatalf("в режиме activity программы нет: %+v", report)
	}
	if report.Proposals == nil || len(report.Proposals) != 0 {
		t.Fatalf("в режиме activity предложений нет: %+v", report.Proposals)
	}
	if !strings.Contains(report.Summary, "без сопоставления с образовательной программой") {
		t.Fatalf("summary = %q", report.Summary)
	}
	if report.Disclaimer != programSuggestionDisclaimer {
		t.Fatalf("disclaimer = %q", report.Disclaimer)
	}
}

func TestBuildProgramSuggestionsNoLinks(t *testing.T) {
	_, _, competencies := psSampleInput()
	report := buildProgramSuggestions(psRunID, psProgramID, "Прикладная математика", "program",
		nil, nil, competencies, psGeneratedAt)
	if report.ProgramID == nil || *report.ProgramID != psProgramID {
		t.Fatalf("программа должна быть видна: %+v", report)
	}
	if len(report.Proposals) != 0 || report.Proposals == nil {
		t.Fatalf("без связей proposals пуст: %+v", report.Proposals)
	}
	if !strings.Contains(report.Summary, "Связей элементов деятельности") {
		t.Fatalf("summary = %q", report.Summary)
	}
}

func TestBuildProgramSuggestionsNoOpenCandidates(t *testing.T) {
	// Все задачи и действия подтверждены, все компетенции связаны — предложений нет.
	nodes := []programSuggestionNode{
		{ID: psNodeMapped, Title: "Строить отчёт", Type: domain.NodeTask},
	}
	links := []programSuggestionLink{
		{NodeID: psNodeMapped, CompetencyID: psCompetencyA, CompetencyTitle: "Анализ данных", Status: domain.ProgramLinkApproved},
	}
	competencies := []programSuggestionCompetency{{ID: psCompetencyA, Title: "Анализ данных"}}
	report := buildProgramSuggestions(psRunID, psProgramID, "Программа", "program", nodes, links, competencies, psGeneratedAt)
	if len(report.Proposals) != 0 {
		t.Fatalf("открытых кандидатов нет: %+v", report.Proposals)
	}
	if !strings.Contains(report.Summary, "Открытых кандидатов не обнаружено") {
		t.Fatalf("summary = %q", report.Summary)
	}
}

func TestBuildProgramSuggestionsSameCompetencyIsNotConflict(t *testing.T) {
	// approved и rejected одной и той же компетенции — не конфликт разных
	// компетенций; такой узел не попадает в conflicting_links.
	nodes := []programSuggestionNode{
		{ID: psNodeMapped, Title: "Строить отчёт", Type: domain.NodeTask},
	}
	links := []programSuggestionLink{
		{NodeID: psNodeMapped, CompetencyID: psCompetencyA, CompetencyTitle: "Анализ данных", Status: domain.ProgramLinkApproved},
		{NodeID: psNodeMapped, CompetencyID: psCompetencyA, CompetencyTitle: "Анализ данных", Status: domain.ProgramLinkRejected},
	}
	competencies := []programSuggestionCompetency{{ID: psCompetencyA, Title: "Анализ данных"}}
	report := buildProgramSuggestions(psRunID, psProgramID, "Программа", "program", nodes, links, competencies, psGeneratedAt)
	if len(report.Proposals) != 0 {
		t.Fatalf("одна компетенция не даёт конфликта: %+v", report.Proposals)
	}
}

func TestBuildProgramSuggestionsProposedAndRejectedIsNotUnconfirmed(t *testing.T) {
	// «Только proposed-связи» понимается буквально: если у компетенции уже
	// есть и отклонённая связь, отдельного предложения unconfirmed_competency
	// не возникает (по отклонённой связи решение уже принято).
	nodes := []programSuggestionNode{
		{ID: psNodeMapped, Title: "Строить отчёт", Type: domain.NodeFunction},
	}
	links := []programSuggestionLink{
		{NodeID: psNodeMapped, CompetencyID: psCompetencyU, CompetencyTitle: "Коммуникация", Status: domain.ProgramLinkProposed},
		{NodeID: psNodeMapped, CompetencyID: psCompetencyU, CompetencyTitle: "Коммуникация", Status: domain.ProgramLinkRejected},
	}
	competencies := []programSuggestionCompetency{{ID: psCompetencyU, Title: "Коммуникация"}}
	report := buildProgramSuggestions(psRunID, psProgramID, "Программа", "program", nodes, links, competencies, psGeneratedAt)
	if len(report.Proposals) != 0 {
		t.Fatalf("proposed+rejected не даёт unconfirmed: %+v", report.Proposals)
	}
}

func TestBuildProgramSuggestionsDeterministic(t *testing.T) {
	nodes, links, competencies := psSampleInput()
	first := buildProgramSuggestions(psRunID, psProgramID, "Программа", "program", nodes, links, competencies, psGeneratedAt)
	second := buildProgramSuggestions(psRunID, psProgramID, "Программа", "program", nodes, links, competencies, psGeneratedAt)
	firstJSON, err := json.Marshal(first.Proposals)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second.Proposals)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("порядок предложений недетерминирован:\n%s\n%s", firstJSON, secondJSON)
	}
}

// --- HTTP-контракт на pgxmock ---

func TestProgramSuggestionsHTTPWithPostgresMock(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(psRunID).
		WillReturnRows(runCardsGetRunRow(psRunID, ""))
	pool.ExpectQuery("FROM runs r").WithArgs(psRunID).
		WillReturnRows(pgxmock.NewRows([]string{"result_kind", "program_id", "program_title"}).
			AddRow("program", psProgramID, "Прикладная математика"))
	pool.ExpectQuery("FROM activity_program_links l").WithArgs(psRunID, psProgramID).
		WillReturnRows(pgxmock.NewRows([]string{"node", "competency", "competency_title", "status"}).
			AddRow(psNodeMapped, psCompetencyA, "Анализ данных", domain.ProgramLinkApproved).
			AddRow(psNodeMapped, psCompetencyU, "Коммуникация", domain.ProgramLinkProposed).
			AddRow(psNodeConflict, psCompetencyA, "Анализ данных", domain.ProgramLinkApproved).
			AddRow(psNodeConflict, psCompetencyR, "Дизайн", domain.ProgramLinkRejected))
	pool.ExpectQuery("FROM nodes").WithArgs(psRunID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "title", "type"}).
			AddRow(psNodeTask, "Собирать требования", domain.NodeTask).
			AddRow(psNodeAction, "Проверять гипотезы", domain.NodeAction).
			AddRow(psNodeMapped, "Строить отчёт", domain.NodeTask).
			AddRow(psNodeConflict, "Согласовывать макет", domain.NodeAction))
	pool.ExpectQuery("FROM program_competencies").WithArgs(psProgramID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "title"}).
			AddRow(psCompetencyA, "Анализ данных").
			AddRow(psCompetencyU, "Коммуникация").
			AddRow(psCompetencyR, "Дизайн").
			AddRow(psCompetencyC, "Проектирование"))
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+psRunID+"/program-suggestions", "specialist", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		RunID        string            `json:"run_id"`
		ProgramID    *string           `json:"program_id"`
		ProgramTitle *string           `json:"program_title"`
		GeneratedAt  string            `json:"generated_at"`
		Proposals    []programProposal `json:"proposals"`
		Summary      string            `json:"summary"`
		Disclaimer   string            `json:"disclaimer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	if body.RunID != psRunID || body.ProgramID == nil || *body.ProgramID != psProgramID {
		t.Fatalf("шапка ответа: %s", rec.Body.String())
	}
	if body.ProgramTitle == nil || *body.ProgramTitle != "Прикладная математика" {
		t.Fatalf("название программы: %s", rec.Body.String())
	}
	if body.GeneratedAt == "" || body.Disclaimer != programSuggestionDisclaimer {
		t.Fatalf("generated_at/disclaimer: %s", rec.Body.String())
	}
	if len(body.Proposals) != 5 {
		t.Fatalf("предложения: %s", rec.Body.String())
	}
	for _, proposal := range body.Proposals {
		if proposal.Status != suggestionStatusProposal || proposal.Rationale == "" {
			t.Fatalf("форма предложения: %+v", proposal)
		}
	}
	for _, key := range []string{"run_id", "program_id", "program_title", "generated_at", "proposals", "summary", "disclaimer"} {
		if _, exists := jsonObject(t, rec.Body.Bytes())[key]; !exists {
			t.Fatalf("в ответе нет поля %q: %s", key, rec.Body.String())
		}
	}
}

func TestProgramSuggestionsHTTPActivityMode(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(psRunID).
		WillReturnRows(runCardsGetRunRow(psRunID, ""))
	pool.ExpectQuery("FROM runs r").WithArgs(psRunID).
		WillReturnRows(pgxmock.NewRows([]string{"result_kind", "program_id", "program_title"}).
			AddRow("activity", "", ""))
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+psRunID+"/program-suggestions", "user", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		ProgramID *string           `json:"program_id"`
		Proposals []json.RawMessage `json:"proposals"`
		Summary   string            `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.ProgramID != nil || len(body.Proposals) != 0 || body.Summary == "" {
		t.Fatalf("пустой режим activity: %s", rec.Body.String())
	}
	// proposals должен быть массивом, а не null.
	if !strings.Contains(rec.Body.String(), `"proposals":[`) {
		t.Fatalf("proposals должен быть массивом: %s", rec.Body.String())
	}
}

func TestProgramSuggestionsHTTPNoLinks(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(psRunID).
		WillReturnRows(runCardsGetRunRow(psRunID, ""))
	pool.ExpectQuery("FROM runs r").WithArgs(psRunID).
		WillReturnRows(pgxmock.NewRows([]string{"result_kind", "program_id", "program_title"}).
			AddRow("program", psProgramID, "Программа"))
	pool.ExpectQuery("FROM activity_program_links l").WithArgs(psRunID, psProgramID).
		WillReturnRows(pgxmock.NewRows([]string{"node", "competency", "competency_title", "status"}))
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+psRunID+"/program-suggestions", "user", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body programSuggestions
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.ProgramID == nil || len(body.Proposals) != 0 {
		t.Fatalf("без связей: %s", rec.Body.String())
	}
	if !strings.Contains(body.Summary, "Связей элементов деятельности") {
		t.Fatalf("summary = %q", body.Summary)
	}
}

func TestProgramSuggestionsMissingRun(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(psRunID).WillReturnError(pgx.ErrNoRows)
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+psRunID+"/program-suggestions", "user", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d (%s), want 404", rec.Code, rec.Body.String())
	}
}

func TestProgramSuggestionsRequireAuthAndAccess(t *testing.T) {
	store := newMemStore()
	store.seedRun(domain.Run{ID: psRunID, Profession: "Аналитик данных", CreatedBy: "test-user"})
	foreign := "99999999-9999-4999-8999-999999999999"
	store.seedRun(domain.Run{ID: foreign, Profession: "Аналитик данных", CreatedBy: "someone-else"})
	handler := NewServer(store).Routes()
	path := "/api/v1/runs/" + psRunID + "/program-suggestions"
	foreignPath := "/api/v1/runs/" + foreign + "/program-suggestions"

	if rec := do(t, handler, http.MethodGet, path, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("без токена: status = %d, want 401", rec.Code)
	}
	for _, role := range []string{"user", "specialist"} {
		if rec := do(t, handler, http.MethodGet, foreignPath, role, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("чужой прогон (%s): status = %d (%s), want 403", role, rec.Code, rec.Body.String())
		}
	}
	if rec := do(t, handler, http.MethodGet, foreignPath, "admin", ""); rec.Code != http.StatusNotImplemented {
		t.Fatalf("чужой прогон (admin): status = %d, want 501", rec.Code)
	}
	for _, role := range []string{"user", "specialist", "admin"} {
		if rec := do(t, handler, http.MethodGet, path, role, ""); rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s на in-memory store: status = %d (%s), want 501", role, rec.Code, rec.Body.String())
		}
	}
}
