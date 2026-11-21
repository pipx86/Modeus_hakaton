package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
)

// Карточка 32 плана MVP: предложения по учебным изменениям на основе
// сопоставления деятельности с образовательной программой.
//
// Контракт:
//
//	GET /api/v1/runs/{id}/program-suggestions
//
// Ответ:
//
//	{
//	  "run_id": "…",
//	  "program_id": "…" | null,
//	  "program_title": "…" | null,
//	  "generated_at": "2026-11-21T10:00:00Z",
//	  "proposals": [{
//	    "id": "unmapped_activity:<node_id>",
//	    "kind": "unmapped_activity",
//	    "target": {"node_id": "…"},
//	    "rationale": "…",
//	    "priority": "high",
//	    "status": "proposal"
//	  }],
//	  "summary": "…",
//	  "disclaimer": "предложения системы, решение принимает методист; …"
//	}
//
// Виды предложений:
//   - unmapped_activity — у узла task/action нет ни одной approved-связи;
//   - unconfirmed_competency — у компетенции есть proposed-связи, но нет
//     ни одной approved;
//   - uncovered_competency — у компетенции нет ни одной связи с деятельностью;
//   - conflicting_links — у узла есть и approved, и rejected связи к разным
//     компетенциям.
//
// Отчёт ничего не утверждает и не переписывает программу: статус каждого
// предложения всегда proposal, а финальное решение остаётся за методистом.
// Формулировки намеренно нейтральные: «кандидат», «стоит проверить», без слов
// «пробел» и «недостаток программы».
//
// Если исследование выполнено не в режиме program или связей с программой ещё
// нет, ответ всё равно 200: proposals пуст, summary объясняет почему.
//
// Коды ответа:
//   - 200 — предложения построены (в том числе пустой список);
//   - 401 — нет/невалидный токен (middleware);
//   - 403 — роль не входит в набор или прогон принадлежит другому пользователю;
//   - 404 — исследование не найдено;
//   - 500 — ошибка БД;
//   - 501 — хранилище не поддерживает отчёт (in-memory Store).

// Виды предложений (kind).
const (
	suggestionKindUnmappedActivity      = "unmapped_activity"
	suggestionKindUnconfirmedCompetency = "unconfirmed_competency"
	suggestionKindUncoveredCompetency   = "uncovered_competency"
	suggestionKindConflictingLinks      = "conflicting_links"
)

// Приоритеты предложений (priority).
const (
	suggestionPriorityHigh   = "high"
	suggestionPriorityMedium = "medium"
	suggestionPriorityLow    = "low"
)

// suggestionStatusProposal — единственный статус, который отдаёт система.
const suggestionStatusProposal = "proposal"

// programSuggestionDisclaimer — обязательная оговорка карточки 32. Формулировка
// фиксированная: интерфейс и тесты сверяют её дословно.
const programSuggestionDisclaimer = "предложения системы, решение принимает методист; система не утверждает и не переписывает программу"

// programSuggestionTarget — на что указывает предложение. У предложений об
// узле заполнен node_id, у предложений о компетенции — competency_id и
// competency_title; остальные поля отсутствуют.
type programSuggestionTarget struct {
	NodeID          *string `json:"node_id,omitempty"`
	CompetencyID    *string `json:"competency_id,omitempty"`
	CompetencyTitle *string `json:"competency_title,omitempty"`
}

// programProposal — одно предложение системы.
type programProposal struct {
	ID        string                  `json:"id"`
	Kind      string                  `json:"kind"`
	Target    programSuggestionTarget `json:"target"`
	Rationale string                  `json:"rationale"`
	Priority  string                  `json:"priority"`
	Status    string                  `json:"status"`
}

// programSuggestions — ответ GET /runs/{id}/program-suggestions. Summary —
// аддитивное человекочитаемое пояснение (в том числе для пустого списка),
// которого нет в исходном контракте, но который нужен интерфейсу, чтобы
// объяснить пустое состояние.
type programSuggestions struct {
	RunID        string            `json:"run_id"`
	ProgramID    *string           `json:"program_id"`
	ProgramTitle *string           `json:"program_title"`
	GeneratedAt  time.Time         `json:"generated_at"`
	Proposals    []programProposal `json:"proposals"`
	Summary      string            `json:"summary"`
	Disclaimer   string            `json:"disclaimer"`
}

// programSuggestionNode — узел дерева, нужный для поиска непокрытых элементов
// деятельности.
type programSuggestionNode struct {
	ID    string
	Title string
	Type  domain.NodeType
}

// programSuggestionLink — связь узла с компетенцией вместе с подписями.
type programSuggestionLink struct {
	NodeID          string
	CompetencyID    string
	CompetencyTitle string
	Status          domain.ProgramLinkStatus
}

// programSuggestionCompetency — компетенция программы. Список читается
// целиком, чтобы предложения были и по компетенциям без единой связи.
type programSuggestionCompetency struct {
	ID    string
	Title string
}

const programSuggestionsRunQuery = `SELECT COALESCE(r.result_kind, 'activity'),
       COALESCE(r.program_id::text, ''), COALESCE(p.title, '')
FROM runs r
LEFT JOIN education_programs p ON p.id = r.program_id
WHERE r.id = $1`

const programSuggestionsLinksQuery = `SELECT l.node_id::text, l.competency_id::text, c.title, l.status
FROM activity_program_links l
JOIN program_competencies c ON c.id = l.competency_id
WHERE l.run_id = $1 AND l.program_id = $2
AND l.snapshot_id IS NOT DISTINCT FROM (SELECT id FROM snapshots WHERE run_id=$1 ORDER BY version DESC LIMIT 1)
ORDER BY l.created_at, l.id`

const programSuggestionsNodesQuery = `SELECT id::text, title, type
FROM nodes
WHERE run_id = $1
ORDER BY level, id`

const programSuggestionsCompetenciesQuery = `SELECT id::text, title
FROM program_competencies
WHERE program_id = $1
ORDER BY title, id`

// stringPointer возвращает копию строки для JSON-поля-указателя.
func stringPointer(value string) *string {
	return &value
}

// suggestionPriorityRank задаёт порядок вывода: сначала высокий приоритет.
func suggestionPriorityRank(priority string) int {
	switch priority {
	case suggestionPriorityHigh:
		return 0
	case suggestionPriorityMedium:
		return 1
	case suggestionPriorityLow:
		return 2
	default:
		return 3
	}
}

// suggestionKindRank фиксирует порядок видов внутри одного приоритета.
func suggestionKindRank(kind string) int {
	switch kind {
	case suggestionKindUnmappedActivity:
		return 0
	case suggestionKindUnconfirmedCompetency:
		return 1
	case suggestionKindUncoveredCompetency:
		return 2
	case suggestionKindConflictingLinks:
		return 3
	default:
		return 4
	}
}

// sortProgramProposals делает порядок предложений детерминированным:
// приоритет, вид, затем id (id включает id цели).
func sortProgramProposals(proposals []programProposal) {
	sort.SliceStable(proposals, func(i, j int) bool {
		left, right := proposals[i], proposals[j]
		if suggestionPriorityRank(left.Priority) != suggestionPriorityRank(right.Priority) {
			return suggestionPriorityRank(left.Priority) < suggestionPriorityRank(right.Priority)
		}
		if suggestionKindRank(left.Kind) != suggestionKindRank(right.Kind) {
			return suggestionKindRank(left.Kind) < suggestionKindRank(right.Kind)
		}
		return left.ID < right.ID
	})
}

// suggestionNodeRationale объясняет, почему задача или действие попали в
// отчёт. Формулировка констатирует отсутствие подтверждённой связи и
// предлагает рассмотреть добавление, не оценивая программу.
func suggestionNodeRationale(node programSuggestionNode, proposed int) string {
	if node.Type == domain.NodeAction {
		if proposed > 0 {
			return fmt.Sprintf("У действия «%s» нет подтверждённых связей с компетенциями программы (предложений без решения: %d); кандидат — рассмотреть добавление в программу.", node.Title, proposed)
		}
		return fmt.Sprintf("У действия «%s» нет ни одной связи с компетенциями программы; кандидат — рассмотреть добавление в программу.", node.Title)
	}
	if proposed > 0 {
		return fmt.Sprintf("У задачи «%s» нет подтверждённых связей с компетенциями программы (предложений без решения: %d); кандидат — рассмотреть добавление в программу.", node.Title, proposed)
	}
	return fmt.Sprintf("У задачи «%s» нет ни одной связи с компетенциями программы; кандидат — рассмотреть добавление в программу.", node.Title)
}

// buildProgramSuggestions собирает предложения из уже прочитанных строк.
// Функция чистая: в базу не ходит, не мутирует входные срезы и не зависит от
// порядка связей (счётчики уникальны, предложения сортируются).
func buildProgramSuggestions(
	runID, programID, programTitle, resultKind string,
	nodes []programSuggestionNode,
	links []programSuggestionLink,
	competencies []programSuggestionCompetency,
	generatedAt time.Time,
) programSuggestions {
	report := programSuggestions{
		RunID:       runID,
		GeneratedAt: generatedAt,
		Proposals:   []programProposal{},
		Disclaimer:  programSuggestionDisclaimer,
	}
	programMode := resultKind == string(domain.ResultKindProgram) && programID != ""
	if programMode {
		report.ProgramID = stringPointer(programID)
		report.ProgramTitle = stringPointer(programTitle)
	}
	if !programMode {
		report.Summary = "Исследование выполнено в режиме описания деятельности, без сопоставления с образовательной программой — предложений нет."
		return report
	}
	if len(links) == 0 {
		report.Summary = "Связей элементов деятельности с компетенциями программы пока нет — предложений нет."
		return report
	}

	type competencyState struct {
		title    string
		proposed int
		approved int
		rejected int
	}
	states := make(map[string]*competencyState, len(competencies))
	for _, competency := range competencies {
		states[competency.ID] = &competencyState{title: competency.Title}
	}
	nodeApproved := make(map[string]map[string]struct{})
	nodeRejected := make(map[string]map[string]struct{})
	proposedByNode := make(map[string]int)
	seenLinks := make(map[string]struct{}, len(links))
	for _, link := range links {
		// Связи версионируются снимками, поэтому одна и та же пара
		// «узел-компетенция-статус» может встретиться несколько раз. Для
		// предложений важна уникальность кандидата, а не число ревизий.
		key := link.NodeID + "\x1f" + link.CompetencyID + "\x1f" + string(link.Status)
		if _, duplicate := seenLinks[key]; duplicate {
			continue
		}
		seenLinks[key] = struct{}{}
		state, exists := states[link.CompetencyID]
		if !exists {
			state = &competencyState{title: link.CompetencyTitle}
			states[link.CompetencyID] = state
		}
		if state.title == "" {
			state.title = link.CompetencyTitle
		}
		switch link.Status {
		case domain.ProgramLinkProposed:
			state.proposed++
			proposedByNode[link.NodeID]++
		case domain.ProgramLinkApproved:
			state.approved++
			if nodeApproved[link.NodeID] == nil {
				nodeApproved[link.NodeID] = make(map[string]struct{})
			}
			nodeApproved[link.NodeID][link.CompetencyID] = struct{}{}
		case domain.ProgramLinkRejected:
			state.rejected++
			if nodeRejected[link.NodeID] == nil {
				nodeRejected[link.NodeID] = make(map[string]struct{})
			}
			nodeRejected[link.NodeID][link.CompetencyID] = struct{}{}
		}
	}

	// 1. Элементы деятельности уровня task/action без единой approved-связи.
	for _, node := range nodes {
		if node.Type != domain.NodeTask && node.Type != domain.NodeAction {
			continue
		}
		if len(nodeApproved[node.ID]) > 0 {
			continue
		}
		priority := suggestionPriorityMedium
		if node.Type == domain.NodeTask {
			priority = suggestionPriorityHigh
		}
		report.Proposals = append(report.Proposals, programProposal{
			ID:   suggestionKindUnmappedActivity + ":" + node.ID,
			Kind: suggestionKindUnmappedActivity,
			Target: programSuggestionTarget{
				NodeID: stringPointer(node.ID),
			},
			Rationale: suggestionNodeRationale(node, proposedByNode[node.ID]),
			Priority:  priority,
			Status:    suggestionStatusProposal,
		})
	}

	// 2–3. Состояние каждой компетенции: неподтверждённые предложения и
	// компетенции без связей. Карта обходится один раз, порядок затем
	// восстанавливается сортировкой предложений.
	for competencyID, state := range states {
		switch {
		case state.proposed > 0 && state.approved == 0 && state.rejected == 0:
			report.Proposals = append(report.Proposals, programProposal{
				ID:   suggestionKindUnconfirmedCompetency + ":" + competencyID,
				Kind: suggestionKindUnconfirmedCompetency,
				Target: programSuggestionTarget{
					CompetencyID:    stringPointer(competencyID),
					CompetencyTitle: stringPointer(state.title),
				},
				Rationale: fmt.Sprintf("У компетенции «%s» есть предложенные связи (%d), но ни одна не подтверждена; стоит проверить и подтвердить или отклонить.", state.title, state.proposed),
				Priority:  suggestionPriorityMedium,
				Status:    suggestionStatusProposal,
			})
		case state.proposed+state.approved+state.rejected == 0:
			report.Proposals = append(report.Proposals, programProposal{
				ID:   suggestionKindUncoveredCompetency + ":" + competencyID,
				Kind: suggestionKindUncoveredCompetency,
				Target: programSuggestionTarget{
					CompetencyID:    stringPointer(competencyID),
					CompetencyTitle: stringPointer(state.title),
				},
				Rationale: fmt.Sprintf("Компетенция «%s» не связана ни с одним элементом деятельности этого исследования; стоит проверить, насколько она актуальна для программы.", state.title),
				Priority:  suggestionPriorityLow,
				Status:    suggestionStatusProposal,
			})
		}
	}

	// 4. Узлы, у которых одновременно есть и подтверждённые, и отклонённые
	// связи к разным компетенциям: решение по ним стоит пересмотреть.
	nodesByID := make(map[string]programSuggestionNode, len(nodes))
	for _, node := range nodes {
		nodesByID[node.ID] = node
	}
	for nodeID, approved := range nodeApproved {
		rejected := nodeRejected[nodeID]
		if len(approved) == 0 || len(rejected) == 0 {
			continue
		}
		conflict := false
		for approvedID := range approved {
			for rejectedID := range rejected {
				if approvedID != rejectedID {
					conflict = true
				}
			}
		}
		if !conflict {
			continue
		}
		node, exists := nodesByID[nodeID]
		if !exists {
			node = programSuggestionNode{ID: nodeID}
		}
		approvedTitles := make([]string, 0, len(approved))
		for competencyID := range approved {
			approvedTitles = append(approvedTitles, states[competencyID].title)
		}
		rejectedTitles := make([]string, 0, len(rejected))
		for competencyID := range rejected {
			rejectedTitles = append(rejectedTitles, states[competencyID].title)
		}
		sort.Strings(approvedTitles)
		sort.Strings(rejectedTitles)
		report.Proposals = append(report.Proposals, programProposal{
			ID:   suggestionKindConflictingLinks + ":" + nodeID,
			Kind: suggestionKindConflictingLinks,
			Target: programSuggestionTarget{
				NodeID: stringPointer(nodeID),
			},
			Rationale: fmt.Sprintf("У элемента «%s» есть подтверждённые связи (%s) и отклонённые связи (%s) с разными компетенциями; стоит пересмотреть, какая связь верна.",
				node.Title, joinSuggestionTitles(approvedTitles), joinSuggestionTitles(rejectedTitles)),
			Priority: suggestionPriorityLow,
			Status:   suggestionStatusProposal,
		})
	}

	sortProgramProposals(report.Proposals)
	report.Summary = programSuggestionsSummary(report.Proposals)
	return report
}

// joinSuggestionTitles соединяет названия в одну строку; пустые названия не
// должны давать «()» в объяснении.
func joinSuggestionTitles(titles []string) string {
	cleaned := make([]string, 0, len(titles))
	for _, title := range titles {
		if title != "" {
			cleaned = append(cleaned, title)
		}
	}
	return strings.Join(cleaned, ", ")
}

// programSuggestionsSummary считает предложения по приоритетам и объясняет
// пустой список. Сводка нейтральна и не оценивает программу.
func programSuggestionsSummary(proposals []programProposal) string {
	if len(proposals) == 0 {
		return "Открытых кандидатов не обнаружено: у элементов деятельности есть подтверждённые связи, а у компетенций нет связей, требующих проверки."
	}
	high, medium, low := 0, 0, 0
	for _, proposal := range proposals {
		switch proposal.Priority {
		case suggestionPriorityHigh:
			high++
		case suggestionPriorityMedium:
			medium++
		case suggestionPriorityLow:
			low++
		}
	}
	return fmt.Sprintf("Предложений: %d. Высокий приоритет: %d, средний: %d, низкий: %d. Решение принимает методист.",
		len(proposals), high, medium, low)
}

// RunProgramSuggestions строит предложения по программе для исследования
// (карточка 32). Отчёт читает сохранённые связи activity_program_links и
// справочник компетенций; он ничего не меняет и не создаёт связей.
func (p *PGStore) RunProgramSuggestions(runID string) (*programSuggestions, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	var resultKind, programID, programTitle string
	if err := p.Pool.QueryRow(ctx, programSuggestionsRunQuery, runID).Scan(&resultKind, &programID, &programTitle); err != nil {
		return nil, translate(err)
	}
	generatedAt := time.Now().UTC()
	if resultKind != string(domain.ResultKindProgram) || programID == "" {
		report := buildProgramSuggestions(runID, programID, programTitle, resultKind, nil, nil, nil, generatedAt)
		return &report, nil
	}
	links, err := p.programSuggestionLinks(ctx, runID, programID)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		report := buildProgramSuggestions(runID, programID, programTitle, resultKind, nil, nil, nil, generatedAt)
		return &report, nil
	}
	nodes, err := p.programSuggestionNodes(ctx, runID)
	if err != nil {
		return nil, err
	}
	competencies, err := p.programSuggestionCompetencies(ctx, programID)
	if err != nil {
		return nil, err
	}
	report := buildProgramSuggestions(runID, programID, programTitle, resultKind, nodes, links, competencies, generatedAt)
	return &report, nil
}

// programSuggestionLinks читает связи исследования с выбранной программой.
func (p *PGStore) programSuggestionLinks(ctx context.Context, runID, programID string) ([]programSuggestionLink, error) {
	rows, err := p.Pool.Query(ctx, programSuggestionsLinksQuery, runID, programID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	links := []programSuggestionLink{}
	for rows.Next() {
		var link programSuggestionLink
		if err := rows.Scan(&link.NodeID, &link.CompetencyID, &link.CompetencyTitle, &link.Status); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// programSuggestionNodes читает узлы дерева исследования.
func (p *PGStore) programSuggestionNodes(ctx context.Context, runID string) ([]programSuggestionNode, error) {
	rows, err := p.Pool.Query(ctx, programSuggestionsNodesQuery, runID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	nodes := []programSuggestionNode{}
	for rows.Next() {
		var node programSuggestionNode
		if err := rows.Scan(&node.ID, &node.Title, &node.Type); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

// programSuggestionCompetencies читает все компетенции программы.
func (p *PGStore) programSuggestionCompetencies(ctx context.Context, programID string) ([]programSuggestionCompetency, error) {
	rows, err := p.Pool.Query(ctx, programSuggestionsCompetenciesQuery, programID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	competencies := []programSuggestionCompetency{}
	for rows.Next() {
		var competency programSuggestionCompetency
		if err := rows.Scan(&competency.ID, &competency.Title); err != nil {
			return nil, err
		}
		competencies = append(competencies, competency)
	}
	return competencies, rows.Err()
}

// writeProgramSuggestionsError переводит ошибки отчёта в HTTP-ответы.
func writeProgramSuggestionsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование не найдено")
	case errors.Is(err, ErrServiceUnavailable):
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для предложений по программе требуется PostgreSQL")
	default:
		logging.Logger.Error("program_suggestions_failed", "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось подготовить предложения по программе")
	}
}

// handleProgramSuggestions godoc
// @Summary      Program-related suggestions based on activity mapping
// @Description  Builds read-only proposals for the methodologist from saved activity-to-program links: tasks/actions without an approved link (unmapped_activity), competencies with proposals but no approval (unconfirmed_competency), competencies without any activity link (uncovered_competency), and nodes with approved and rejected links to different competencies (conflicting_links). Priorities: high for task-level unmapped, medium for other unmapped and unconfirmed, low for uncovered and conflicting. Every proposal has status "proposal"; the system never approves or rewrites the program. When the run is not in program mode or has no links yet, the response is still 200 with an empty proposals array and an explanatory summary.
// @Tags         review
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {object}  programSuggestions
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/program-suggestions [get]
func (s *Server) handleProgramSuggestions(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	report, err := pg.RunProgramSuggestions(runID)
	if err != nil {
		writeProgramSuggestionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
