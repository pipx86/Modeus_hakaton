package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
)

// Карточки результата исследования, которые читают интерфейс и методист:
//
//   - карточка 10 — состав использованных материалов:
//     GET /api/v1/runs/{id}/sources;
//   - карточка 14 — какие ответы респондента стали основаниями решений:
//     GET /api/v1/runs/{id}/interviews/{interview_id}/evidence;
//   - карточка 24 — отчёт о покрытии образовательной программы:
//     GET /api/v1/runs/{id}/program-coverage.
//
// Все три ответа — агрегаты только для чтения: они ничего не пишут, не меняют
// статусы проверки и не создают новых сущностей. Доступ к прогону тот же, что у
// интервью (s.allowInterviewAccess): администратор или владелец исследования.
// Ошибки отдаются существующими хелперами writeErr/writeJSON.

// runSourceCard — один использованный материал исследования (карточка 10).
// EvidenceCount — сколько обоснований узлов именно этого прогона ссылаются на
// источник: материал, привязанный к прогону, но ни разу не процитированный,
// честно показывает ноль.
type runSourceCard struct {
	ID            string            `json:"id"`
	Type          domain.SourceType `json:"type"`
	Title         string            `json:"title"`
	SourceURL     string            `json:"source_url"`
	CreatedAt     time.Time         `json:"created_at"`
	EvidenceCount int               `json:"evidence_count"`
}

// interviewEvidenceBasis — обоснование узла, выросшее из ответа интервью
// (карточка 14). Связь идёт через evidence.ref_id = run_chat_messages.id,
// поэтому в ответ попадают только обоснования, привязанные к ответам этого
// респондента, а не все цитаты прогона.
type interviewEvidenceBasis struct {
	EvidenceID  string                `json:"evidence_id"`
	NodeID      string                `json:"node_id"`
	NodeTitle   string                `json:"node_title"`
	NodeType    domain.NodeType       `json:"node_type"`
	Quote       string                `json:"quote"`
	SourceID    string                `json:"source_id"`
	SourceTitle string                `json:"source_title"`
	Status      domain.EvidenceStatus `json:"status"`
}

// Статусы покрытия узла в отчёте о программе (карточка 24).
const (
	// coverageConfirmed — у узла есть хотя бы одна подтверждённая связь.
	coverageConfirmed = "confirmed"
	// coverageProposed — подтверждённых связей нет, но есть предложения ядра.
	coverageProposed = "proposed"
	// coverageNone — покрытия нет: связей нет либо все они отклонены.
	coverageNone = "none"
)

// programCoverageLink — связь узла с компетенцией программы в составе отчёта.
// Reason сохраняется, чтобы методист видел, почему связь была предложена.
// NodeID нужен только для группировки по узлам и в JSON не отдаётся: связь уже
// лежит внутри своего узла.
type programCoverageLink struct {
	LinkID          string                   `json:"link_id"`
	NodeID          string                   `json:"-"`
	CompetencyID    string                   `json:"competency_id"`
	CompetencyTitle string                   `json:"competency_title"`
	Status          domain.ProgramLinkStatus `json:"status"`
	Reason          string                   `json:"reason"`
}

// programCoverageNode — узел дерева и его связи с программой.
type programCoverageNode struct {
	NodeID    string                `json:"node_id"`
	NodeTitle string                `json:"node_title"`
	NodeType  domain.NodeType       `json:"node_type"`
	Status    string                `json:"status"`
	Links     []programCoverageLink `json:"links"`
}

// programCoverageCompetency — компетенция программы с числом подтверждённых
// связей. В отчёт попадают только approved-связи, поэтому непокрытые
// компетенции здесь не появляются: их видно по узлам со статусом none.
type programCoverageCompetency struct {
	CompetencyID    string `json:"competency_id"`
	CompetencyTitle string `json:"competency_title"`
	LinksCount      int    `json:"links_count"`
}

// programCoverageCounts — распределение узлов по статусам покрытия.
// Всегда confirmed + proposed + none == total.
type programCoverageCounts struct {
	Confirmed int `json:"confirmed"`
	Proposed  int `json:"proposed"`
	None      int `json:"none"`
	Total     int `json:"total"`
}

// programCoverage — ответ GET /runs/{id}/program-coverage (карточка 24).
// ProgramID/ProgramTitle равны null, если у прогона не выбран режим program:
// интерфейс в этом случае покажет пустое состояние, но ответ остаётся 200.
type programCoverage struct {
	RunID        string                      `json:"run_id"`
	ProgramID    *string                     `json:"program_id"`
	ProgramTitle *string                     `json:"program_title"`
	Nodes        []programCoverageNode       `json:"nodes"`
	Competencies []programCoverageCompetency `json:"competencies"`
	Counts       programCoverageCounts       `json:"counts"`
}

// coverageTreeNode — строка nodes, нужная отчёту о покрытии.
type coverageTreeNode struct {
	ID    string
	Title string
	Type  domain.NodeType
}

// SQL карточек вынесен в константы: по ним же находят запросы pgxmock-тесты,
// поэтому текст не должен разъезжаться между кодом и тестом.
const runSourceCardsQuery = `SELECT s.id::text, s.type, s.title, COALESCE(s.source_url, ''), s.created_at,
       (SELECT count(*)
        FROM evidence e JOIN nodes n ON n.id = e.node_id
        WHERE n.run_id = $1 AND e.source_id = s.id)
FROM run_sources rs
JOIN sources s ON s.id = rs.source_id
WHERE rs.run_id = $1
ORDER BY rs.created_at, s.id`

const interviewEvidenceQuery = `SELECT e.id::text, n.id::text, n.title, n.type, e.quote,
       s.id::text, s.title, COALESCE(e.status, 'pending')
FROM evidence e
JOIN nodes n ON n.id = e.node_id
JOIN sources s ON s.id = e.source_id
JOIN run_chat_messages m ON m.id = e.ref_id
WHERE n.run_id = $1 AND m.run_id = $1 AND m.interview_id = $2::uuid
ORDER BY m.sequence, e.id`

const programCoverageRunQuery = `SELECT COALESCE(r.program_id::text, ''), COALESCE(p.title, '')
FROM runs r
LEFT JOIN education_programs p ON p.id = r.program_id
WHERE r.id = $1`

const programCoverageNodesQuery = `SELECT id::text, title, type
FROM nodes
WHERE run_id = $1
ORDER BY level, id`

const programCoverageLinksQuery = `SELECT l.id::text, l.node_id::text, l.competency_id::text, c.title, l.status, l.reason
FROM activity_program_links l
JOIN program_competencies c ON c.id = l.competency_id
WHERE l.run_id = $1
AND l.snapshot_id IS NOT DISTINCT FROM (SELECT id FROM snapshots WHERE run_id=$1 ORDER BY version DESC LIMIT 1)
ORDER BY l.created_at, l.id`

// ListRunSourceCards читает материалы прогона вместе с числом обоснований,
// которые на них ссылаются (карточка 10). Источник — run_sources: один и тот же
// материал может быть привязан к нескольким исследованиям.
func (p *PGStore) ListRunSourceCards(runID string) ([]runSourceCard, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, runSourceCardsQuery, runID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	cards := []runSourceCard{}
	for rows.Next() {
		var card runSourceCard
		var evidenceCount int64
		if err := rows.Scan(&card.ID, &card.Type, &card.Title, &card.SourceURL, &card.CreatedAt, &evidenceCount); err != nil {
			return nil, err
		}
		card.EvidenceCount = int(evidenceCount)
		cards = append(cards, card)
	}
	return cards, rows.Err()
}

// ListInterviewEvidence читает обоснования, которые выросли из ответов одного
// интервью (карточка 14). Сообщение обязано принадлежать и интервью, и прогону:
// иначе ответы другого респондента или другого исследования попали бы в карточку.
func (p *PGStore) ListInterviewEvidence(runID, interviewID string) ([]interviewEvidenceBasis, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) || !validUUID(interviewID) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, interviewEvidenceQuery, runID, interviewID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	items := []interviewEvidenceBasis{}
	for rows.Next() {
		var item interviewEvidenceBasis
		if err := rows.Scan(&item.EvidenceID, &item.NodeID, &item.NodeTitle, &item.NodeType,
			&item.Quote, &item.SourceID, &item.SourceTitle, &item.Status); err != nil {
			return nil, err
		}
		if item.Status == "" {
			item.Status = domain.EvidencePending
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// RunProgramCoverage строит отчёт о покрытии образовательной программы
// (карточка 24). Отчёт читается, а не вычисляется на лету из дерева: связи уже
// сохранены в activity_program_links, поэтому повторный запуск не меняет цифры.
// Если программа не выбрана или связей ещё нет, метод всё равно возвращает
// отчёт с нулевым покрытием — это не ошибка, а пустое состояние интерфейса.
func (p *PGStore) RunProgramCoverage(runID string) (*programCoverage, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	var programID, programTitle string
	if err := p.Pool.QueryRow(ctx, programCoverageRunQuery, runID).Scan(&programID, &programTitle); err != nil {
		return nil, translate(err)
	}
	nodes, err := p.coverageNodes(ctx, runID)
	if err != nil {
		return nil, err
	}
	links, err := p.coverageLinks(ctx, runID)
	if err != nil {
		return nil, err
	}
	report := buildProgramCoverage(runID, programID, programTitle, nodes, links)
	return &report, nil
}

// coverageNodes читает все узлы дерева прогона в порядке дерева.
func (p *PGStore) coverageNodes(ctx context.Context, runID string) ([]coverageTreeNode, error) {
	rows, err := p.Pool.Query(ctx, programCoverageNodesQuery, runID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	nodes := []coverageTreeNode{}
	for rows.Next() {
		var node coverageTreeNode
		if err := rows.Scan(&node.ID, &node.Title, &node.Type); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

// coverageLinks читает связи прогона с компетенциями программ вместе с
// названиями компетенций: отчёт не должен дозапрашивать справочник.
func (p *PGStore) coverageLinks(ctx context.Context, runID string) ([]programCoverageLink, error) {
	rows, err := p.Pool.Query(ctx, programCoverageLinksQuery, runID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	links := []programCoverageLink{}
	for rows.Next() {
		var link programCoverageLink
		if err := rows.Scan(&link.LinkID, &link.NodeID, &link.CompetencyID, &link.CompetencyTitle,
			&link.Status, &link.Reason); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// buildProgramCoverage собирает отчёт из уже прочитанных строк. Функция чистая:
// в базу не ходит, не мутирует входные срезы и не зависит от порядка связей,
// кроме заданного SQL (связи сохраняют порядок created_at, id).
func buildProgramCoverage(runID, programID, programTitle string, nodes []coverageTreeNode, links []programCoverageLink) programCoverage {
	linksByNode := make(map[string][]programCoverageLink, len(nodes))
	for _, link := range links {
		linksByNode[link.NodeID] = append(linksByNode[link.NodeID], link)
	}
	report := programCoverage{
		RunID:        runID,
		ProgramID:    optionalCoverageString(programID),
		ProgramTitle: optionalCoverageString(programTitle),
		Nodes:        make([]programCoverageNode, 0, len(nodes)),
		Competencies: coverageCompetencySummary(links),
	}
	for _, node := range nodes {
		nodeLinks := linksByNode[node.ID]
		if nodeLinks == nil {
			nodeLinks = []programCoverageLink{}
		}
		report.Nodes = append(report.Nodes, programCoverageNode{
			NodeID:    node.ID,
			NodeTitle: node.Title,
			NodeType:  node.Type,
			Status:    coverageNodeStatus(nodeLinks),
			Links:     nodeLinks,
		})
	}
	report.Counts = coverageCounts(report.Nodes)
	return report
}

// coverageNodeStatus классифицирует узел: подтверждённая связь важнее
// предложенной, а отклонённые связи покрытием не считаются. Узел только с
// отклонёнными связями попадает в none, но сами связи остаются в links, чтобы
// методист видел, что предложение было и почему оно не подошло.
func coverageNodeStatus(links []programCoverageLink) string {
	proposed := false
	for _, link := range links {
		switch link.Status {
		case domain.ProgramLinkApproved:
			return coverageConfirmed
		case domain.ProgramLinkProposed:
			proposed = true
		}
	}
	if proposed {
		return coverageProposed
	}
	return coverageNone
}

// coverageCompetencySummary группирует подтверждённые связи по компетенциям.
// Предложения и отклонения в сводку не попадают: покрытием программы считается
// только то, что методист подтвердил. Порядок детерминирован — сначала самые
// покрытые компетенции, затем по названию и id.
func coverageCompetencySummary(links []programCoverageLink) []programCoverageCompetency {
	type aggregate struct {
		title string
		count int
	}
	byCompetency := make(map[string]*aggregate, len(links))
	for _, link := range links {
		if link.Status != domain.ProgramLinkApproved {
			continue
		}
		item, exists := byCompetency[link.CompetencyID]
		if !exists {
			item = &aggregate{title: link.CompetencyTitle}
			byCompetency[link.CompetencyID] = item
		}
		item.count++
	}
	summary := make([]programCoverageCompetency, 0, len(byCompetency))
	for competencyID, item := range byCompetency {
		summary = append(summary, programCoverageCompetency{
			CompetencyID:    competencyID,
			CompetencyTitle: item.title,
			LinksCount:      item.count,
		})
	}
	sort.Slice(summary, func(i, j int) bool {
		if summary[i].LinksCount != summary[j].LinksCount {
			return summary[i].LinksCount > summary[j].LinksCount
		}
		if summary[i].CompetencyTitle != summary[j].CompetencyTitle {
			return summary[i].CompetencyTitle < summary[j].CompetencyTitle
		}
		return summary[i].CompetencyID < summary[j].CompetencyID
	})
	return summary
}

// coverageCounts считает распределение статусов. Инвариант
// confirmed + proposed + none == total проверяется в тестах.
func coverageCounts(nodes []programCoverageNode) programCoverageCounts {
	counts := programCoverageCounts{Total: len(nodes)}
	for _, node := range nodes {
		switch node.Status {
		case coverageConfirmed:
			counts.Confirmed++
		case coverageProposed:
			counts.Proposed++
		default:
			counts.None++
		}
	}
	return counts
}

// optionalCoverageString превращает пустую строку в null: JSON-контракт
// карточки 24 обещает null, а не "", когда программа не выбрана.
func optionalCoverageString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// writeRunCardsError переводит ошибки чтения карточек в HTTP-ответы.
func writeRunCardsError(w http.ResponseWriter, err error, message string) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", message)
	case errors.Is(err, ErrServiceUnavailable):
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "карточки требуют PostgreSQL")
	default:
		logging.Logger.Error("run_cards_store_failed", "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", message)
	}
}

// handleListRunSources godoc
// @Summary      Used materials of a research run
// @Description  Returns the sources attached to the run through run_sources together with the number of tree-node evidence items that quote each source. Materials attached to the run but never quoted honestly report zero. Read-only card 10 of the MVP plan.
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {array}   runSourceCard
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/sources [get]
func (s *Server) handleListRunSources(w http.ResponseWriter, r *http.Request) {
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
	cards, err := pg.ListRunSourceCards(runID)
	if err != nil {
		writeRunCardsError(w, err, "не удалось прочитать материалы исследования")
		return
	}
	writeJSON(w, http.StatusOK, cards)
}

// handleRunInterviewEvidence godoc
// @Summary      Evidence items born from one interview
// @Description  Returns the tree-node evidence items whose ref_id points at a message of exactly this interview of the run, so the methodologist sees which answers became the grounds of the tree. An interview that does not belong to the run is 404. Read-only card 14 of the MVP plan.
// @Tags         interviews
// @Produce      json
// @Param        id            path      string  true  "Run ID"
// @Param        interview_id  path      string  true  "Interview ID"
// @Success      200  {array}   interviewEvidenceBasis
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/interviews/{interview_id}/evidence [get]
func (s *Server) handleRunInterviewEvidence(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	runID := r.PathValue("id")
	interviewID := r.PathValue("interview_id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	// Интервью проверяем отдельным запросом: иначе чужое интервью вернуло бы
	// пустой список и выглядело бы как «ответов не было».
	if _, err := pg.GetRunInterview(r.Context(), runID, interviewID); err != nil {
		s.writeInterviewError(w, err, "интервью не найдено в этом исследовании")
		return
	}
	items, err := pg.ListInterviewEvidence(runID, interviewID)
	if err != nil {
		writeRunCardsError(w, err, "не удалось прочитать основания из ответов интервью")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// handleProgramCoverage godoc
// @Summary      Coverage of the educational program by the activity tree
// @Description  Groups the activity tree nodes by program coverage: confirmed when at least one link is approved, proposed when only proposals exist, none otherwise. The competencies array contains approved links only, grouped per competency. When the run has no selected program or no links at all the response is still 200 with counts.none equal to total and empty competencies, so the UI can show its empty state. Read-only card 24 of the MVP plan.
// @Tags         review
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {object}  programCoverage
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/program-coverage [get]
func (s *Server) handleProgramCoverage(w http.ResponseWriter, r *http.Request) {
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
	report, err := pg.RunProgramCoverage(runID)
	if err != nil {
		writeRunCardsError(w, err, "не удалось посчитать покрытие программы")
		return
	}
	writeJSON(w, http.StatusOK, report)
}
