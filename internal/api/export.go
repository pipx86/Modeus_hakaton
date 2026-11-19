package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
)

// Экспорт завершённого исследования в удобный для демонстрации вид.
//
// Контракт:
//
//	GET /api/v1/runs/{id}/export?format=md|json|csv
//
// format=md отдаёт Markdown-отчёт для человека: профессию,
// статус, идентификатор прогона, иерархию role → function → task → action,
// подтверждения (цитата, source_id, ref_id, статус), разрешённые конфликты,
// финальное сообщение с рекомендациями/курсами (если оно доступно) и время
// генерации отчёта. format=json отдаёт ту же информацию структурой
// (schema=run-export/v1): run, дерево, плоский список узлов, evidence,
// conflicts, final_message и метаданные источников.
//
// Экспорт доступен ролям admin, teacher, rop, student и guest.
// Без токена сервер возвращает 401.
//
// Коды: 400 - неизвестный format; 404 - прогон не найден; 409 - прогон ещё не
// завершён (RUN_NOT_FINISHED) или завершён, но дерево не сохранено
// (RUN_HAS_NO_TREE); 500 - ошибка хранилища; 503/504 - временная
// недоступность/таймаут апстрима (пробрасываются mapStoreError).
//
// Конфликты, источники и финальное сообщение - вспомогательные данные:
// конфликты и метаданные источников читаются best-effort и не ломают экспорт
// при ошибке (в отчёт попадёт пустой список, ошибка пишется в лог). Финальное
// сообщение берётся из чата интервью (последнее сообщение агента) и доступно,
// пока живёт in-memory сессия чата; после перезапуска сервера оно пустое.
// Секреты, конфигурация и полные тексты источников в экспорт не попадают.

// exportSchemaVersion идентифицирует структуру JSON-экспорта.
const exportSchemaVersion = "run-export/v1"

// exportFormat - значение query-параметра format.
type exportFormat string

const (
	exportFormatLegacy   exportFormat = "legacy"
	exportFormatMarkdown exportFormat = "md"
	exportFormatJSON     exportFormat = "json"
	exportFormatCSV      exportFormat = "csv"
)

// synthesisConflictsReader - узкий читающий интерфейс конфликтов синтезатора.
// Его реализует *PGStore; в отличие от synthesisReviewReader (review-эндпоинты)
// экспорту не нужны сообщения памяти агента, поэтому интерфейс уже.
type synthesisConflictsReader interface {
	listSynthesisConflicts(ctx context.Context, runID string) ([]domain.Conflict, error)
}

// runSourcesReader - узкий читающий интерфейс метаданных источников прогона.
// Его реализует *PGStore; in-memory хранилище его не реализует, поэтому для
// демо-стенда секция источников просто отсутствует.
type runSourcesReader interface {
	ListRunSources(ctx context.Context, runID string) ([]domain.Source, error)
}

// runExport - документ JSON-экспорта (format=json).
type runExport struct {
	Schema       string            `json:"schema"`
	GeneratedAt  time.Time         `json:"generated_at"`
	Run          *domain.Run       `json:"run"`
	Tree         []runExportNode   `json:"tree"`
	Nodes        []domain.Node     `json:"nodes"`
	Evidence     []domain.Evidence `json:"evidence"`
	Conflicts    []domain.Conflict `json:"conflicts"`
	FinalMessage string            `json:"final_message"`
	Sources      []domain.Source   `json:"sources,omitempty"`
}

// runExportNode - узел иерархии экспорта: поля domain.Node плюс вложенные дети.
type runExportNode struct {
	domain.Node
	Children []runExportNode `json:"children"`
}

// handleExportRun godoc
// @Summary      Export final results of a completed run
// @Description  Renders the finalized competency tree of a `done` run as a Markdown report (default) or as a structured JSON document (`schema=run-export/v1`). Markdown includes profession, status, run id, role → function → task → action hierarchy, evidence quotes with source_id/ref_id/status, resolved conflicts, the final chat message with recommendations/courses when available and the generation timestamp. JSON returns the run, nested tree, flat node list, evidence, conflicts and final message. Backed by PostgreSQL: conflicts and source metadata are best-effort enrichment.
// @Tags         runs
// @Produce      text/markdown
// @Produce      json
// @Produce      text/csv
// @Param        id      path   string  true   "Run ID"
// @Param        format  query  string  false  "md, json or csv"
// @Success      200  {string}  string  "Markdown report or JSON export document"
// @Failure      400  {object}  errorBody
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      409  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      503  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/export [get]
func (s *Server) handleExportRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent, auth.RoleGuest) {
		return
	}
	id := r.PathValue("id")

	// Формат проверяем до обращения к хранилищу: неизвестный format - это 400
	// независимо от того, существует ли прогон.
	format, err := parseExportFormat(r.URL.Query(), r.Header.Get("Accept"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	run, err := s.store.GetRun(id)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("export_get_run_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	if run.Status != domain.RunDone {
		writeErr(w, http.StatusConflict, "RUN_NOT_FINISHED", "run is not finished yet")
		return
	}

	nodes, err := s.store.GetTree(run.ID)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("export_get_tree_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	if len(nodes) == 0 {
		// Прогон формально done, но результата нет: это конфликт состояния, а
		// не отсутствие ресурса, поэтому 409 с явным кодом.
		writeErr(w, http.StatusConflict, "RUN_HAS_NO_TREE", "run has no saved tree yet")
		return
	}

	if format == exportFormatLegacy {
		// Backward compatibility: without an explicit format this endpoint has
		// always returned a bare JSON array of domain.Node values.
		payload, marshalErr := json.Marshal(nodes)
		if marshalErr != nil {
			logging.Logger.Error("export_marshal_error", "run_id", id, "error", marshalErr)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "failed to encode export")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", exportFilename(run.ID, "json")))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(append(payload, '\n'))
		return
	}

	if format == exportFormatCSV {
		writeRunCSV(w, run.ID, nodes)
		return
	}

	document := s.buildRunExport(r.Context(), run, nodes)

	var (
		payload     []byte
		contentType string
		extension   string
	)
	switch format {
	case exportFormatMarkdown:
		payload = []byte(renderRunExportMarkdown(document))
		contentType = "text/markdown; charset=utf-8"
		extension = "md"
	case exportFormatJSON:
		encoded, marshalErr := json.MarshalIndent(document, "", "  ")
		if marshalErr != nil {
			logging.Logger.Error("export_marshal_error", "run_id", id, "error", marshalErr)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "failed to encode export")
			return
		}
		payload = append(encoded, '\n')
		contentType = "application/json; charset=utf-8"
		extension = "json"
	default:
		// Недостижимо: parseExportFormat проверяет список форматов.
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "format must be one of: md, json, csv")
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", exportFilename(run.ID, extension)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

// parseExportFormat разбирает и валидирует ?format=. Пустое значение означает
// формат по умолчанию md. Регистр и пробелы вокруг значения нормализуются,
// любое другое значение - ошибка для 400.
func parseExportFormat(values url.Values, accept string) (exportFormat, error) {
	raw := strings.ToLower(strings.TrimSpace(values.Get("format")))
	if raw == "" {
		// Backward compatibility: the legacy /export endpoint returned a bare
		// JSON array of nodes. New formats are explicit: ?format=md|json|csv.
		if strings.Contains(strings.ToLower(accept), "text/markdown") {
			return exportFormatMarkdown, nil
		}
		return exportFormatLegacy, nil
	}
	switch raw {
	case string(exportFormatMarkdown):
		return exportFormatMarkdown, nil
	case string(exportFormatJSON):
		return exportFormatJSON, nil
	case string(exportFormatCSV):
		return exportFormatCSV, nil
	default:
		return "", fmt.Errorf("format must be one of: md, json, csv")
	}
}

// buildRunExport собирает документ экспорта из прогона и его дерева.
// Вспомогательные данные (конфликты, источники, финальное сообщение) читаются
// best-effort: их ошибки логируются, но не ломают экспорт основного результата.
func (s *Server) buildRunExport(ctx context.Context, run *domain.Run, nodes []domain.Node) *runExport {
	document := &runExport{
		Schema:      exportSchemaVersion,
		GeneratedAt: time.Now().UTC(),
		Run:         run,
		Tree:        buildExportTree(nodes),
		Nodes:       make([]domain.Node, 0, len(nodes)),
		Evidence:    make([]domain.Evidence, 0),
		Conflicts:   make([]domain.Conflict, 0),
	}

	document.Nodes = append(document.Nodes, nodes...)
	for index := range document.Nodes {
		if document.Nodes[index].Evidence == nil {
			document.Nodes[index].Evidence = []domain.Evidence{}
		}
	}

	// Плоский список evidence: цитаты хранятся внутри узлов дерева, поэтому
	// собираем их в порядке узлов и восстанавливаем node_id для каждой.
	for _, node := range nodes {
		for _, evidence := range node.Evidence {
			if evidence.NodeID == "" {
				evidence.NodeID = node.ID
			}
			document.Evidence = append(document.Evidence, evidence)
		}
	}

	document.Conflicts = s.exportConflicts(ctx, run.ID)
	document.Sources = s.exportSources(ctx, run.ID)
	document.FinalMessage = s.exportFinalMessage(run.ID)
	return document
}

// exportConflicts читает разрешённые синтезатором конфликты. In-memory
// хранилище их не поддерживает, поэтому возвращается пустой список; ошибка
// PostgreSQL не фатальна для экспорта.
func (s *Server) exportConflicts(ctx context.Context, runID string) []domain.Conflict {
	reader, ok := s.store.(synthesisConflictsReader)
	if !ok {
		return []domain.Conflict{}
	}
	items, err := reader.listSynthesisConflicts(ctx, runID)
	if err != nil {
		logging.Logger.Warn("export_conflicts_read_failed", "run_id", runID, "error", err)
		return []domain.Conflict{}
	}
	if items == nil {
		return []domain.Conflict{}
	}
	return items
}

// exportSources читает метаданные источников прогона. Полный content
// источника намеренно обнуляется: в экспорт попадают только id/тип/заголовок/
// URL, чтобы не выносить сырые тексты и потенциальные PII за пределы API.
// In-memory хранилище не поддерживает ListRunSources; ошибка PostgreSQL не
// фатальна для экспорта.
func (s *Server) exportSources(ctx context.Context, runID string) []domain.Source {
	reader, ok := s.store.(runSourcesReader)
	if !ok {
		return nil
	}
	items, err := reader.ListRunSources(ctx, runID)
	if err != nil {
		logging.Logger.Warn("export_sources_read_failed", "run_id", runID, "error", err)
		return nil
	}
	out := make([]domain.Source, 0, len(items))
	for _, item := range items {
		item.Content = ""
		out = append(out, item)
	}
	return out
}

// exportFinalMessage возвращает последнее сообщение агента в чате интервью -
// это финальное сообщение с рекомендациями и бесплатными курсами. История
// читается из chat.Service, который возвращает сохранённый диалог даже после
// перезапуска backend; для batch-прогонов без чата сообщение недоступно и
// возвращается пустая строка.
func (s *Server) exportFinalMessage(runID string) string {
	if s.chat == nil {
		return ""
	}
	messages, err := s.chat.Messages(runID)
	if err != nil {
		if !errors.Is(err, chat.ErrNotFound) {
			logging.Logger.Warn("export_chat_messages_failed", "run_id", runID, "error", err)
		}
		return ""
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "agent" && strings.TrimSpace(messages[index].Content) != "" {
			return messages[index].Content
		}
	}
	return ""
}

// buildExportTree превращает плоский список узлов в иерархию. Узлы с
// отсутствующим/несуществующим/циклическим parent_id становятся корнями, чтобы
// ни один узел не потерялся в отчёте; внутри одного родителя узлы сортируются
// role → function → task → action, затем по title и id.
func buildExportTree(nodes []domain.Node) []runExportNode {
	if len(nodes) == 0 {
		return []runExportNode{}
	}

	known := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		known[node.ID] = true
	}

	children := make(map[string][]domain.Node, len(nodes))
	roots := make([]domain.Node, 0, 1)
	for _, node := range nodes {
		parent := node.ParentID
		if parent == nil || *parent == node.ID || !known[*parent] {
			roots = append(roots, node)
			continue
		}
		children[*parent] = append(children[*parent], node)
	}
	sortExportNodes(roots)
	for parentID := range children {
		sortExportNodes(children[parentID])
	}

	emitted := make(map[string]bool, len(nodes))
	const maxDepth = 64
	var build func(node domain.Node, depth int) runExportNode
	build = func(node domain.Node, depth int) runExportNode {
		emitted[node.ID] = true
		evidence := node.Evidence
		if evidence == nil {
			evidence = []domain.Evidence{}
		}
		out := runExportNode{Node: node, Children: []runExportNode{}}
		out.Node.Evidence = evidence
		if depth >= maxDepth {
			return out
		}
		for _, child := range children[node.ID] {
			if emitted[child.ID] {
				continue
			}
			out.Children = append(out.Children, build(child, depth+1))
		}
		return out
	}

	tree := make([]runExportNode, 0, len(roots))
	for _, root := range roots {
		if emitted[root.ID] {
			continue
		}
		tree = append(tree, build(root, 0))
	}

	// Циклы без корня: такие узлы не достижимы сверху, добавляем их корнями.
	remaining := make([]domain.Node, 0)
	for _, node := range nodes {
		if !emitted[node.ID] {
			remaining = append(remaining, node)
		}
	}
	sortExportNodes(remaining)
	for _, node := range remaining {
		if emitted[node.ID] {
			continue
		}
		tree = append(tree, build(node, 0))
	}
	return tree
}

// nodeTypeRank задаёт порядок типов в иерархии: role → function → task → action.
func nodeTypeRank(nodeType domain.NodeType) int {
	switch nodeType {
	case domain.NodeRole:
		return 0
	case domain.NodeFunction:
		return 1
	case domain.NodeTask:
		return 2
	case domain.NodeAction:
		return 3
	default:
		return 4
	}
}

// sortExportNodes упорядочивает узлы детерминированно: тип, title, id.
func sortExportNodes(nodes []domain.Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		ri, rj := nodeTypeRank(nodes[i].Type), nodeTypeRank(nodes[j].Type)
		if ri != rj {
			return ri < rj
		}
		if nodes[i].Title != nodes[j].Title {
			return nodes[i].Title < nodes[j].Title
		}
		return nodes[i].ID < nodes[j].ID
	})
}

// renderRunExportMarkdown рендерит Markdown-отчёт для пользователя.
func renderRunExportMarkdown(document *runExport) string {
	var builder strings.Builder
	run := document.Run

	builder.WriteString("# Run export")
	if run != nil && strings.TrimSpace(run.Profession) != "" {
		builder.WriteString(": ")
		builder.WriteString(markdownInline(run.Profession))
	}
	builder.WriteString("\n\n")

	if run != nil {
		fmt.Fprintf(&builder, "- Run ID: %s\n", markdownCode(run.ID))
		fmt.Fprintf(&builder, "- Profession: %s\n", markdownText(run.Profession))
		fmt.Fprintf(&builder, "- Status: %s\n", markdownText(string(run.Status)))
		if run.Stage != "" {
			fmt.Fprintf(&builder, "- Stage: %s\n", markdownText(string(run.Stage)))
		}
		if run.Type != "" {
			fmt.Fprintf(&builder, "- Type: %s\n", markdownText(string(run.Type)))
		}
		if run.StartedAt != nil {
			fmt.Fprintf(&builder, "- Started at: %s\n", run.StartedAt.UTC().Format(time.RFC3339))
		}
		if run.FinishedAt != nil {
			fmt.Fprintf(&builder, "- Finished at: %s\n", run.FinishedAt.UTC().Format(time.RFC3339))
		}
	}
	fmt.Fprintf(&builder, "- Generated at: %s\n", document.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&builder, "- Nodes: %d\n", len(document.Nodes))
	fmt.Fprintf(&builder, "- Evidence: %d\n", len(document.Evidence))
	fmt.Fprintf(&builder, "- Conflicts: %d\n\n", len(document.Conflicts))

	writeMarkdownTreeSection(&builder, document.Tree)
	writeMarkdownEvidenceSection(&builder, document)
	writeMarkdownConflictsSection(&builder, document.Conflicts)
	writeMarkdownSourcesSection(&builder, document.Sources)

	if message := strings.TrimSpace(document.FinalMessage); message != "" {
		builder.WriteString("## Final message (recommendations / courses)\n\n")
		builder.WriteString(markdownLiteral.Replace(message))
		builder.WriteString("\n")
	}

	return strings.TrimRight(builder.String(), "\n") + "\n"
}

// writeMarkdownTreeSection пишет иерархию узлов вложенным списком.
func writeMarkdownTreeSection(builder *strings.Builder, tree []runExportNode) {
	builder.WriteString("## Competency tree\n\n")
	if len(tree) == 0 {
		builder.WriteString("The run has no saved tree.\n\n")
		return
	}
	builder.WriteString("Hierarchy: role → function → task → action. Node type and id are shown for every item.\n\n")
	writeMarkdownTree(builder, tree, 0)
	builder.WriteString("\n")
}

func writeMarkdownTree(builder *strings.Builder, nodes []runExportNode, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, node := range nodes {
		suffix := ""
		if len(node.Evidence) > 0 {
			suffix = fmt.Sprintf(" - evidence: %d", len(node.Evidence))
		}
		fmt.Fprintf(builder, "%s- %s (%s) %s%s\n",
			indent, markdownText(node.Title), markdownText(string(node.Type)), markdownCode(node.ID), suffix)
		writeMarkdownTree(builder, node.Children, depth+1)
	}
}

// writeMarkdownEvidenceSection пишет таблицу подтверждений: цитата, источник,
// ref_id и статус.
func writeMarkdownEvidenceSection(builder *strings.Builder, document *runExport) {
	builder.WriteString("## Evidence\n\n")
	if len(document.Evidence) == 0 {
		builder.WriteString("No evidence quotes are attached to the tree.\n\n")
		return
	}

	titles := make(map[string]string, len(document.Nodes))
	for _, node := range document.Nodes {
		if _, exists := titles[node.ID]; !exists {
			titles[node.ID] = node.Title
		}
	}
	sources := make(map[string]domain.Source, len(document.Sources))
	for _, source := range document.Sources {
		sources[source.ID] = source
	}

	builder.WriteString("| Node | Quote | Source | Ref ID | Status |\n")
	builder.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, evidence := range document.Evidence {
		nodeLabel := markdownCode(evidence.NodeID)
		if title := titles[evidence.NodeID]; strings.TrimSpace(title) != "" {
			nodeLabel = fmt.Sprintf("%s - %s", markdownCode(evidence.NodeID), markdownCell(title))
		}
		status := string(evidence.Status)
		if status == "" {
			status = string(domain.EvidencePending)
		}
		fmt.Fprintf(builder, "| %s | %s | %s | %s | %s |\n",
			nodeLabel,
			markdownCellOrDash(evidence.Quote),
			markdownSourceLabel(evidence.SourceID, sources),
			markdownCode(evidence.RefID),
			markdownCellOrDash(status),
		)
	}
	builder.WriteString("\n")
}

// writeMarkdownConflictsSection пишет разрешённые конфликты синтезатора.
func writeMarkdownConflictsSection(builder *strings.Builder, conflicts []domain.Conflict) {
	builder.WriteString("## Conflicts\n\n")
	if len(conflicts) == 0 {
		builder.WriteString("No source conflicts were resolved for this run.\n\n")
		return
	}
	for _, conflict := range conflicts {
		fmt.Fprintf(builder, "- Node %s, field %s (conflict %s)\n",
			markdownCode(conflict.NodeID), markdownCode(conflict.Field), markdownCode(conflict.ID))
		fmt.Fprintf(builder, "  - Winner: %s (source %s, %s, priority %d)\n",
			markdownCellOrDash(conflict.Winner.Value),
			markdownCode(conflict.Winner.SourceID),
			markdownText(string(conflict.Winner.SourceType)),
			conflict.Winner.Priority,
		)
		if len(conflict.Values) > 0 {
			values := make([]string, 0, len(conflict.Values))
			for _, value := range conflict.Values {
				values = append(values, fmt.Sprintf("%s (%s, priority %d)",
					markdownCellOrDash(value.Value), markdownText(string(value.SourceType)), value.Priority))
			}
			fmt.Fprintf(builder, "  - Competing values: %s\n", strings.Join(values, "; "))
		}
		fmt.Fprintf(builder, "  - Reason: %s\n", markdownText(conflict.Reason))
	}
	builder.WriteString("\n")
}

// writeMarkdownSourcesSection пишет метаданные источников прогона, если они
// доступны (PostgreSQL). Полный content источника намеренно не экспортируется.
func writeMarkdownSourcesSection(builder *strings.Builder, sources []domain.Source) {
	if len(sources) == 0 {
		return
	}
	builder.WriteString("## Sources\n\n")
	builder.WriteString("| ID | Type | Title | URL |\n")
	builder.WriteString("| --- | --- | --- | --- |\n")
	for _, source := range sources {
		fmt.Fprintf(builder, "| %s | %s | %s | %s |\n",
			markdownCode(source.ID),
			markdownCellOrDash(string(source.Type)),
			markdownCellOrDash(source.Title),
			markdownCellOrDash(source.SourceURL),
		)
	}
	builder.WriteString("\n")
}

// markdownSourceLabel показывает источник подтверждения: id и, если метаданные
// доступны, тип и заголовок.
func markdownSourceLabel(sourceID string, sources map[string]domain.Source) string {
	label := markdownCode(sourceID)
	source, ok := sources[sourceID]
	if !ok {
		return label
	}
	parts := make([]string, 0, 2)
	if source.Type != "" {
		parts = append(parts, markdownCell(string(source.Type)))
	}
	if strings.TrimSpace(source.Title) != "" {
		parts = append(parts, markdownCell(source.Title))
	}
	if len(parts) == 0 {
		return label
	}
	return fmt.Sprintf("%s (%s)", label, strings.Join(parts, " - "))
}

// markdownInline убирает переводы строк, чтобы значение не ломало структуру
// Markdown, и обрезает пробелы по краям.
func markdownInline(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.TrimSpace(value)
}

// Символы в пользовательском тексте не должны добавлять разметку в отчёт.
var markdownLiteral = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;",
	"\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_",
	"[", "\\[", "]", "\\]", "!", "\\!", "~", "\\~",
)

// markdownCell экранирует значение для ячейки таблицы Markdown.
func markdownCell(value string) string {
	return strings.ReplaceAll(markdownLiteral.Replace(markdownInline(value)), "|", `\|`)
}

func markdownCellOrDash(value string) string {
	cell := markdownCell(value)
	if cell == "" {
		return "-"
	}
	return cell
}

func markdownText(value string) string {
	text := markdownInline(value)
	if text == "" {
		return "-"
	}
	return markdownLiteral.Replace(text)
}

// markdownCode оформляет идентификатор как inline-код; пустое значение
// отображается прочерком.
func markdownCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return "`" + strings.ReplaceAll(value, "`", "'") + "`"
}

// exportFilename строит безопасное имя файла для Content-Disposition:
// только латиница, цифры, дефис и подчёркивание.
func exportFilename(runID, extension string) string {
	return "run-" + exportSafeName(runID) + "." + extension
}

func exportSafeName(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'), r == '-', r == '_':
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	safe := strings.Trim(builder.String(), "-_")
	if safe == "" {
		return "unknown"
	}
	if len(safe) > 64 {
		safe = safe[:64]
	}
	return safe
}
