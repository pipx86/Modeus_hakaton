package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

type programLinkTarget struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Description     string `json:"description,omitempty"`
	CompetencyID    string `json:"competency_id"`
	CompetencyTitle string `json:"competency_title"`
	Kind            string `json:"kind,omitempty"`
}
type programLinkSuggestion struct {
	NodeID       string `json:"node_id"`
	CompetencyID string `json:"competency_id"`
	KsaID        string `json:"ksa_id,omitempty"`
	Reason       string `json:"reason"`
}

func (s *Server) handleGenerateProgramLinks(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	p, ok := s.store.(*PGStore)
	if !ok || p.Pool == nil {
		writeErr(w, 503, "SERVICE_UNAVAILABLE", "сопоставление требует PostgreSQL")
		return
	}
	if s.programMatcher == nil {
		writeErr(w, 503, "MATCHER_UNAVAILABLE", "модель для сопоставления не настроена")
		return
	}
	runID := r.PathValue("id")
	run, err := p.GetRun(runID)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	if run.ResultKind != string(domain.ResultKindProgram) || run.ProgramID == nil || !validUUID(*run.ProgramID) {
		writeErr(w, 422, "PROGRAM_NOT_SELECTED", "для этого исследования не выбрана образовательная программа")
		return
	}
	programID := *run.ProgramID
	rows, err := p.Pool.Query(r.Context(), `SELECT c.id::text,c.title,COALESCE(c.description,''),c.id::text,c.title,'' FROM program_competencies c WHERE c.program_id=$1
UNION ALL SELECT k.id::text,k.title,COALESCE(k.description,''),c.id::text,c.title,k.kind FROM competency_ksa k JOIN program_competencies c ON c.id=k.competency_id WHERE c.program_id=$1 ORDER BY 2,1`, programID)
	if err != nil {
		writeErr(w, 500, "INTERNAL", "не удалось прочитать цели образовательной программы")
		return
	}
	targets := []programLinkTarget{}
	targetByID := map[string]programLinkTarget{}
	for rows.Next() {
		var t programLinkTarget
		if err = rows.Scan(&t.ID, &t.Title, &t.Description, &t.CompetencyID, &t.CompetencyTitle, &t.Kind); err != nil {
			rows.Close()
			writeErr(w, 500, "INTERNAL", "не удалось прочитать цели программы")
			return
		}
		targets = append(targets, t)
		targetByID[t.ID] = t
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		writeErr(w, 500, "INTERNAL", "не удалось прочитать цели программы")
		return
	}
	rows.Close()
	if len(targets) == 0 {
		writeErr(w, 422, "PROGRAM_EMPTY", "в программе нет компетенций и ЗУН для сопоставления")
		return
	}
	snapshot, err := p.createSnapshot(r.Context(), runID)
	if err != nil {
		snapshotError(w, err)
		return
	}
	tree, err := parseSnapshotTree(snapshot.Tree)
	if err != nil {
		writeErr(w, 500, "SNAPSHOT_INVALID", "не удалось прочитать снимок дерева")
		return
	}
	allowed := map[string]domain.Node{}
	inputNodes := make([]domain.Node, 0, len(tree))
	for _, node := range tree {
		filtered := node
		filtered.Evidence = make([]domain.Evidence, 0, len(node.Evidence))
		for _, evidence := range node.Evidence {
			if evidence.Status != domain.EvidenceRejected {
				filtered.Evidence = append(filtered.Evidence, evidence)
			}
		}
		if len(filtered.Evidence) > 0 {
			allowed[node.ID] = filtered
			inputNodes = append(inputNodes, filtered)
		}
	}
	if len(inputNodes) == 0 {
		writeErr(w, 422, "TREE_WITHOUT_EVIDENCE", "в дереве нет оснований, доступных для сопоставления")
		return
	}
	payload, _ := json.Marshal(struct {
		Nodes   []domain.Node       `json:"activity_nodes"`
		Targets []programLinkTarget `json:"program_targets"`
	}{inputNodes, targets})
	answer, err := s.programMatcher.Chat(r.Context(), []llm.Message{{Role: llm.RoleSystem, Content: `Ты помощник методиста. Сопоставляй деятельность только с переданными компетенциями и ЗУН выбранной образовательной программы. Используй только переданные ID. Учитывай точные цитаты и claim_origin; отклонённые основания игнорируй, неклассифицированные явно отмечай в причине как требующие проверки. Не сопоставляй по совпадению слов: объясни содержательную связь действия/задачи/функции с компетенцией или ЗУН. Не придумывай основание. Верни только JSON вида {"suggestions":[{"node_id":"...","competency_id":"...","ksa_id":"...","reason":"..."}]}. Для одной компетенции выбирай не больше одного наиболее релевантного ЗУН; не больше 3 разных компетенций на узел. Если связи нет, верни пустой suggestions.`}, {Role: llm.RoleUser, Content: string(payload)}})
	if err != nil {
		writeErr(w, 502, "MATCHER_ERROR", "не удалось сформировать предложения сопоставления")
		return
	}
	var result struct {
		Suggestions []programLinkSuggestion `json:"suggestions"`
	}
	decoder := json.NewDecoder(strings.NewReader(answer))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeErr(w, 502, "MATCHER_INVALID_OUTPUT", "модель вернула ответ вне согласованного формата")
		return
	}
	seen := map[string]bool{}
	perNode := map[string]int{}
	for _, item := range result.Suggestions {
		node, nodeOK := allowed[item.NodeID]
		target, targetOK := targetByID[item.CompetencyID]
		key := item.NodeID + "/" + item.CompetencyID
		if !nodeOK || !targetOK || target.Kind != "" || seen[key] || perNode[item.NodeID] >= 3 || strings.TrimSpace(item.Reason) == "" || len(item.Reason) > 2000 {
			writeErr(w, 502, "MATCHER_INVALID_OUTPUT", "предложение содержит неизвестный узел, компетенцию или необоснованное объяснение")
			return
		}
		if item.KsaID != "" {
			ksa, ok := targetByID[item.KsaID]
			if !ok || ksa.Kind == "" || ksa.CompetencyID != item.CompetencyID {
				writeErr(w, 502, "MATCHER_INVALID_OUTPUT", "предложение содержит ЗУН, не относящийся к указанной компетенции")
				return
			}
		}
		_ = node
		seen[key] = true
		perNode[item.NodeID]++
	}
	if len(result.Suggestions) > 300 {
		writeErr(w, 502, "MATCHER_INVALID_OUTPUT", "слишком много предложений сопоставления")
		return
	}
	tx, err := p.Pool.Begin(r.Context())
	if err != nil {
		writeErr(w, 500, "INTERNAL", "не удалось сохранить предложения")
		return
	}
	defer tx.Rollback(context.Background())
	for _, item := range result.Suggestions {
		_, err = tx.Exec(r.Context(), `INSERT INTO activity_program_links(run_id,snapshot_id,node_id,program_id,competency_id,ksa_id,status,reason,source)
VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,'proposed',$7,'system') ON CONFLICT DO NOTHING`, runID, snapshot.ID.String(), item.NodeID, programID, item.CompetencyID, item.KsaID, strings.TrimSpace(item.Reason))
		if err != nil {
			writeErr(w, 500, "INTERNAL", "не удалось сохранить предложения сопоставления")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeErr(w, 500, "INTERNAL", "не удалось сохранить предложения сопоставления")
		return
	}
	links, err := p.ListProgramLinks(runID, "")
	if err != nil {
		writeReviewError(w, err, "не удалось загрузить предложения сопоставления")
		return
	}
	writeJSON(w, 200, links)
}
