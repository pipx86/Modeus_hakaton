package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

// Сравнение двух исследований (run-to-run comparison) по их сохранённым деревьям.
//
// Контракт:
//
//	GET /api/v1/runs/{id}/compare?against={other_run_id}
//
// `{id}` — «голова» сравнения (head, новое/текущее состояние), параметр
// `against` — «база» (base, состояние, с которым сравнивают). Направление
// совпадает с snapshot diff: added = узлы, которые есть только у `{id}`;
// removed = узлы, которые есть только у `against`.
//
// Сравниваются ТЕКУЩИЕ деревья исследований (то же, что отдаёт
// GET /api/v1/runs/{id}/tree), поэтому запрос только читает БД и ничего не
// мутирует. Матчинг узлов двухпроходный (см. run_compare_match.go):
//
//  1. по node.id — как snapshot diff (для прогонов, переиспользующих id);
//  2. оставшиеся unmatched-узлы — семантически по
//     type + нормализованное title + нормализованный путь предков. Матчатся
//     только однозначные пары; при неоднозначности узлы остаются
//     added/removed. Это нужно потому, что nodes.id — глобальный PK, и два
//     независимых прогона одной профессии никогда не делят идентификаторы.
//
// changed перечисляет изменённые поля в фиксированном порядке title,
// description, type, parent_id, evidence. Для semantic-пар evidence
// сравнивается без технических UUID прогона (ref_id/quote/status), parent_id —
// по нормализованному пути предков (перемещение даёт added/removed, а не
// changed). Узлы и подтверждения возвращаются как есть, поэтому у каждого
// evidence сохранены source_id/ref_id/quote/status — по ним UI открывает
// исходную цитату.
//
// Ответ:
//
//	{
//	  "run_id": "…",
//	  "against_run_id": "…",
//	  "profession": "Data Analyst",
//	  "same_profession": true,           // профессии совпали (см. ниже)
//	  "match": {"by_id": 0, "by_title": 12, "unmatched": 2},
//	  "added":   [domain.Node, …],       // узлы только у {id}, отсортированы по id
//	  "removed": [domain.Node, …],       // узлы только у against, отсортированы по id
//	  "changed": [{"id": "…", "before": domain.Node, "after": domain.Node,
//	               "fields": ["title", "evidence"]}],
//	  "summary": {"added": 0, "removed": 0, "changed": 1},
//	  "story": "Узлов добавлено: 0, удалено: 0, изменено: 1. Изменён узел «Отчёт»: подтверждения."
//	}
//
// `match.by_id` — сколько узлов сматчено по uuid, `match.by_title` — сколько
// семантически (по типу/названию/пути предков), `match.unmatched` — сколько
// узлов осталось несопоставленными (= added + removed). Для changed из
// semantic-пары `id` — идентификатор head-узла (`after.id`), а `before.id` —
// идентификатор узла базового прогона.
//
// `story` — детерминированное человекочитаемое описание diff'а на русском
// языке; строится общим с snapshot diff helper'ом buildComparisonStory
// (comparison_story.go): количества всегда совпадают с `summary`, порядок
// подробностей фиксирован, длина ограничена. Для двух одинаковых деревьев —
// «Изменений нет: деревья идентичны.».
//
// Требования к исследованиям:
//   - оба существуют (иначе 404);
//   - относятся к одной профессии: нормализованное runs.profession совпадает
//     ИЛИ совпадает нормализованное название корневого role-узла деревьев
//     (иначе 409 RUN_PROFESSION_MISMATCH). Второе условие позволяет сравнивать
//     один и тот же профессиональный результат, даже если прогоны назвали
//     профессию по-разному («Аналитик данных» / «Data Analyst»);
//   - у обоих есть сохранённое дерево: статус исследования сам по себе не
//     блокирует сравнение, важно наличие дерева (иначе 409 RUN_TREE_MISSING,
//     чтобы не возвращать выдуманные данные).
//
// Коды ответа:
//   - 200 — сравнение построено (в том числе для идентичных деревьев — пустой diff);
//   - 400 — параметр against отсутствует/не UUID или совпадает с {id};
//   - 401 — нет/невалидный токен (middleware);
//   - 403 — роль не входит в набор чтения деревьев;
//   - 404 — исследование {id} или against не найдено (включая невалидный UUID в пути);
//   - 409 — разные профессии или у исследования нет дерева;
//   - 500 — ошибка БД;
//   - 501 — хранилище не поддерживает сравнение (in-memory Store).
//
// Права: как у GET /api/v1/runs/{id}/tree — admin, teacher, rop, guest.
// Роль student намеренно не добавлена: сравнение раскрывает содержимое деревьев,
// а студент не входит в матрицу чтения деревьев.
var (
	// ErrRunProfessionMismatch — исследования относятся к разным профессиям.
	ErrRunProfessionMismatch = errors.New("RUN_PROFESSION_MISMATCH")
	// ErrRunTreeMissing — у исследования нет сохранённого дерева.
	ErrRunTreeMissing = errors.New("RUN_TREE_MISSING")
)

// runComparer — узкий интерфейс, который нужен HTTP-хендлеру. Его реализует
// *PGStore; в unit-тестах подменяется заглушкой, а для in-memory Store
// интерфейс не выполняется, поэтому хендлер отвечает 501.
type runComparer interface {
	compareRuns(runID, againstRunID string) (runCompareResponse, error)
}

// runCompareMatch — метаданные сопоставления узлов двух прогонов, чтобы
// frontend мог объяснить, откуда взялись added/removed/changed (особенно когда
// узлы двух прогонов имеют разные uuid).
type runCompareMatch struct {
	// ByID — число пар, сматченных по node.id (узлы базового прогона).
	ByID int `json:"by_id"`
	// ByTitle — число пар, сматченных семантически (type + title + путь предков).
	ByTitle int `json:"by_title"`
	// Unmatched — сколько узлов не удалось сопоставить (= added + removed).
	Unmatched int `json:"unmatched"`
}

// runCompareResponse — DTO run-to-run сравнения. Изменённые узлы и summary
// намеренно переиспользуют типы snapshot diff (snapshotNodeChange /
// snapshotDiffSummary), чтобы frontend обрабатывал оба ответа одним кодом.
type runCompareResponse struct {
	RunID        string `json:"run_id"`
	AgainstRunID string `json:"against_run_id"`
	Profession   string `json:"profession"`
	// SameProfession — true, если профессии совпали нормализованно по
	// runs.profession или по названию корневого role-узла деревьев.
	SameProfession bool                 `json:"same_profession"`
	Match          runCompareMatch      `json:"match"`
	Added          []domain.Node        `json:"added"`
	Removed        []domain.Node        `json:"removed"`
	Changed        []snapshotNodeChange `json:"changed"`
	Summary        snapshotDiffSummary  `json:"summary"`
	// Story — одна строка человекочитаемого описания diff'а на русском языке
	// (см. buildComparisonStory). Заполняется хендлером.
	Story string `json:"story"`
}

// compareRuns реализует контракт runComparer на PostgreSQL.
//
// Метод только читает состояние: GetRun проверяет существование обоих
// исследований, GetTree — наличие деревьев; дальше работает чистая
// buildRunCompareResponse. Никаких записей в БД нет, возвращаемые узлы — копии
// прочитанных данных.
func (p *PGStore) compareRuns(runID, againstRunID string) (runCompareResponse, error) {
	run, err := p.GetRun(runID)
	if err != nil {
		return runCompareResponse{}, err
	}
	against, err := p.GetRun(againstRunID)
	if err != nil {
		return runCompareResponse{}, err
	}
	headNodes, err := p.GetTree(runID)
	if err != nil {
		return runCompareResponse{}, err
	}
	baseNodes, err := p.GetTree(againstRunID)
	if err != nil {
		return runCompareResponse{}, err
	}
	return buildRunCompareResponse(run, against, headNodes, baseNodes)
}

// buildRunCompareResponse — чистая часть сравнения: проверяет требования к
// исследованиям и строит DTO из уже прочитанных деревьев. Вынесена из
// compareRuns, чтобы правила (одинаковая профессия, наличие деревьев,
// направление head/against, два прохода матчинга) проверялись unit-тестами
// без БД.
//
// run — «голова» сравнения, against — «база»; матчинг и changed считаются
// buildRunCompareResult (run_compare_match.go).
func buildRunCompareResponse(run, against *domain.Run, headNodes, baseNodes []domain.Node) (runCompareResponse, error) {
	if run == nil || against == nil {
		return runCompareResponse{}, ErrNotFound
	}
	// Профессия проверяется до деревьев: если runs.profession совпадает, дерево
	// для этой проверки не нужно. Если профессия не совпала, вторым шансом
	// является содержимое дерева — одинаковый корневой role-узел.
	if !runCompareSameProfession(run, against, headNodes, baseNodes) {
		return runCompareResponse{}, ErrRunProfessionMismatch
	}
	// Статус (pending/running/done) не блокирует сравнение: источником истины
	// считается сохранённое дерево. Пустое дерево = его нет, иначе ответ был бы
	// выдуманным (все узлы одного исследования — «удалённые»).
	if len(headNodes) == 0 {
		return runCompareResponse{}, fmt.Errorf("%w: у исследования %s нет дерева", ErrRunTreeMissing, run.ID)
	}
	if len(baseNodes) == 0 {
		return runCompareResponse{}, fmt.Errorf("%w: у исследования %s нет дерева", ErrRunTreeMissing, against.ID)
	}
	result := buildRunCompareResult(baseNodes, headNodes)
	return runCompareResponse{
		RunID:          run.ID,
		AgainstRunID:   against.ID,
		Profession:     run.Profession,
		SameProfession: true,
		Match:          result.Match,
		Added:          result.Added,
		Removed:        result.Removed,
		Changed:        result.Changed,
		Summary: snapshotDiffSummary{
			Added:   len(result.Added),
			Removed: len(result.Removed),
			Changed: len(result.Changed),
		},
	}, nil
}

// runCompareError переводит ошибки compareRuns в HTTP-ответы.
func runCompareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование не найдено")
	case errors.Is(err, ErrRunProfessionMismatch):
		writeErr(w, http.StatusConflict, "RUN_PROFESSION_MISMATCH", "исследования относятся к разным профессиям")
	case errors.Is(err, ErrRunTreeMissing):
		writeErr(w, http.StatusConflict, "RUN_TREE_MISSING", "у исследования нет сохранённого дерева")
	default:
		if mapStoreError(w, err) {
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сравнить исследования")
	}
}

// handleCompareRuns godoc
// @Summary      Compare two research runs
// @Description  Compares the current saved trees of two research runs of the same profession. The path run `{id}` is the head (newer) side, the `against` query parameter is the base. Nodes are matched first by node id and then semantically by type + normalized title + normalized ancestor-title path (node ids are globally unique in PostgreSQL, so independent runs never share them); only unambiguous semantic pairs are matched, otherwise nodes are reported as added/removed. A semantic pair is changed when title/description/type/evidence differ or (for id-matched pairs) any diff field differs. Response includes `same_profession` and `match` counters (by_id, by_title, unmatched), summary counts and a deterministic Russian human-readable `story`. Every returned node keeps its evidence with source_id/ref_id/quote/status so the UI can jump to the source quote. The handler only reads data.
// @Tags         runs
// @Produce      json
// @Param        id       path   string  true  "Head run ID"
// @Param        against  query  string  true  "Base run ID"
// @Success      200  {object}  runCompareResponse
// @Failure      400  {object}  errorBody  "against is missing, not a UUID or equals id"
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody  "run not found"
// @Failure      409  {object}  errorBody  "different professions or a run has no tree"
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody  "store does not support comparison"
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/compare [get]
func (s *Server) handleCompareRuns(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent, auth.RoleGuest) {
		return
	}
	// Параметры проверяются до обращения к хранилищу: невалидный запрос — это
	// 400/404 при любом бэкенде, в том числе когда сравнение не поддерживается.
	against := strings.TrimSpace(r.URL.Query().Get("against"))
	if against == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "параметр against обязателен")
		return
	}
	if !validUUID(against) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "against должен быть UUID исследования")
		return
	}
	runID := strings.TrimSpace(r.PathValue("id"))
	if runID != "" && strings.EqualFold(runID, against) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "against должен отличаться от id")
		return
	}
	if !validUUID(runID) {
		// Невалидный id в пути — это отсутствующий ресурс, а не ошибка формата
		// запроса: так же ведёт себя GET /api/v1/runs/{id}/tree.
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование не найдено")
		return
	}
	comparer, ok := s.store.(runComparer)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для сравнения исследований требуется PostgreSQL")
		return
	}
	result, err := comparer.compareRuns(runID, against)
	if err != nil {
		runCompareError(w, err)
		return
	}
	// Story строится на HTTP-слое общим helper'ом со snapshot diff: хранилище
	// отдаёт структурированный diff, человекочитаемое описание — представление.
	result.Story, _ = buildComparisonStory(result.Added, result.Removed, result.Changed)
	writeJSON(w, http.StatusOK, result)
}
