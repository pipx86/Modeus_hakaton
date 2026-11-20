package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"activity-descriptor/internal/domain"
)

// Reference — ручной эталон пилота: согласованное дерево деятельности
// «роль → функция → задача → действие» с обоснованием каждого узла.
// Эталон не запускает пайплайн, а служит точкой отсчёта для CompareTrees.
type Reference struct {
	Profession string          `json:"profession"`
	Version    string          `json:"version"`
	ReviewedBy string          `json:"reviewed_by,omitempty"`
	ReviewedAt string          `json:"reviewed_at,omitempty"`
	Note       string          `json:"note,omitempty"`
	Nodes      []ReferenceNode `json:"nodes"`
}

// ReferenceNode — один узел эталона. ParentID пуст только у корня.
// Ambiguous отмечает спорное место: если такого узла нет в генерации,
// это не провал, а отдельная строка AmbiguousMissing в отчёте.
type ReferenceNode struct {
	ID        string            `json:"id"`
	Type      domain.NodeType   `json:"type"`
	ParentID  string            `json:"parent_id,omitempty"`
	Title     string            `json:"title"`
	Sources   []ReferenceSource `json:"sources"`
	Ambiguous bool              `json:"ambiguous,omitempty"`
	Note      string            `json:"note,omitempty"`
}

// ReferenceSource — обоснование узла: источник и дословная цитата.
type ReferenceSource struct {
	SourceID   string            `json:"source_id"`
	SourceType domain.SourceType `json:"source_type"`
	Quote      string            `json:"quote"`
	Reason     string            `json:"reason,omitempty"`
}

// GeneratedNode — минимальный срез сгенерированного дерева для сравнения.
type GeneratedNode struct {
	ID       string          `json:"id"`
	Type     domain.NodeType `json:"type"`
	ParentID string          `json:"parent_id,omitempty"`
	Title    string          `json:"title"`
}

// ReferenceReport — результат сравнения. Matched, Missing, Extra,
// LevelMismatch и AmbiguousMissing содержат id узлов; LevelMismatch имеет вид
// "id: ожидался task, получен action". Precision/Recall/F1 считаются по
// обязательным (не ambiguous) узлам; лишние узлы генерации снижают Precision,
// а спорные узлы в метрики не входят.
type ReferenceReport struct {
	Matched          []string `json:"matched,omitempty"`
	Missing          []string `json:"missing,omitempty"`
	Extra            []string `json:"extra,omitempty"`
	LevelMismatch    []string `json:"level_mismatch,omitempty"`
	AmbiguousMissing []string `json:"ambiguous_missing,omitempty"`
	Precision        float64  `json:"precision"`
	Recall           float64  `json:"recall"`
	F1               float64  `json:"f1"`
	// Error заполняется, когда сравнивать нечего (nil или пустой эталон).
	Error string `json:"error,omitempty"`
}

// LoadReference читает и валидирует ручной эталон из JSON-файла.
func LoadReference(path string) (*Reference, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: чтение эталона: %w", err)
	}
	var ref Reference
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ref); err != nil {
		return nil, fmt.Errorf("eval: разбор эталона %s: %w", path, err)
	}
	if err := ref.Validate(); err != nil {
		return nil, fmt.Errorf("eval: эталон %s: %w", path, err)
	}
	return &ref, nil
}

// Validate проверяет структуру эталона: один корень-роль, допустимые уровни
// role → function → task → action, существующих родителей, обоснование с
// цитатой у каждого узла и отсутствие одинаковых названий у одного родителя.
func (r *Reference) Validate() error {
	if r == nil {
		return fmt.Errorf("эталон не передан")
	}
	if strings.TrimSpace(r.Profession) == "" {
		return fmt.Errorf("не заполнена профессия (profession)")
	}
	if strings.TrimSpace(r.Version) == "" {
		return fmt.Errorf("не заполнена версия эталона (version)")
	}
	if len(r.Nodes) == 0 {
		return fmt.Errorf("в эталоне нет узлов (nodes)")
	}

	byID := make(map[string]*ReferenceNode, len(r.Nodes))
	for i := range r.Nodes {
		node := &r.Nodes[i]
		if strings.TrimSpace(node.ID) == "" {
			return fmt.Errorf("узел #%d без id", i+1)
		}
		if _, duplicate := byID[node.ID]; duplicate {
			return fmt.Errorf("повторяющийся id узла %q", node.ID)
		}
		if strings.TrimSpace(node.Title) == "" {
			return fmt.Errorf("узел %q без названия (title)", node.ID)
		}
		if !node.Type.Valid() {
			return fmt.Errorf("узел %q имеет неизвестный тип %q", node.ID, node.Type)
		}
		if len(node.Sources) == 0 {
			return fmt.Errorf("узел %q без источников (sources)", node.ID)
		}
		hasQuote := false
		for j, source := range node.Sources {
			if strings.TrimSpace(source.SourceID) == "" {
				return fmt.Errorf("узел %q, источник #%d: пустой source_id", node.ID, j+1)
			}
			if !validSourceType(string(source.SourceType)) {
				return fmt.Errorf("узел %q, источник %q: неизвестный source_type %q",
					node.ID, source.SourceID, source.SourceType)
			}
			if strings.TrimSpace(source.Quote) != "" {
				hasQuote = true
			}
		}
		if !hasQuote {
			return fmt.Errorf("узел %q: ни один источник не содержит непустой цитаты (quote)", node.ID)
		}
		byID[node.ID] = node
	}

	var roots []string
	for i := range r.Nodes {
		node := &r.Nodes[i]
		if strings.TrimSpace(node.ParentID) == "" {
			roots = append(roots, node.ID)
			continue
		}
		parent, ok := byID[node.ParentID]
		if !ok {
			return fmt.Errorf("узел %q ссылается на несуществующего родителя %q", node.ID, node.ParentID)
		}
		if !validTreeParent(node.Type, parent.Type) {
			return fmt.Errorf("узел %q (%s) не может быть дочерним для %q (%s)",
				node.ID, node.Type, parent.ID, parent.Type)
		}
	}
	switch len(roots) {
	case 0:
		return fmt.Errorf("в эталоне нет корня: у всех узлов заполнен parent_id")
	case 1:
	default:
		return fmt.Errorf("в эталоне должен быть ровно один корень, найдено %d: %s",
			len(roots), strings.Join(roots, ", "))
	}
	root := byID[roots[0]]
	if root.Type != domain.NodeRole {
		return fmt.Errorf("корневой узел %q должен иметь тип role, а не %q", root.ID, root.Type)
	}

	seenTitles := make(map[string]string, len(r.Nodes))
	for i := range r.Nodes {
		node := &r.Nodes[i]
		key := node.ParentID + "\x00" + normalizeReferenceTitle(node.Title)
		if previous, duplicate := seenTitles[key]; duplicate {
			parent := "корень"
			if strings.TrimSpace(node.ParentID) != "" {
				parent = node.ParentID
			}
			return fmt.Errorf("у одного родителя (%s) два узла с одинаковым названием %q: %q и %q",
				parent, node.Title, previous, node.ID)
		}
		seenTitles[key] = node.ID
	}

	// Обход от корня ловит циклы (action → action) и ветки без корня.
	children := make(map[string][]string, len(r.Nodes))
	for i := range r.Nodes {
		node := &r.Nodes[i]
		if strings.TrimSpace(node.ParentID) != "" {
			children[node.ParentID] = append(children[node.ParentID], node.ID)
		}
	}
	visited := map[string]bool{root.ID: true}
	queue := []string{root.ID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, childID := range children[current] {
			visited[childID] = true
			queue = append(queue, childID)
		}
	}
	if len(visited) != len(r.Nodes) {
		for i := range r.Nodes {
			if !visited[r.Nodes[i].ID] {
				return fmt.Errorf("узел %q не достижим от корня %q (цикл или отдельная ветка)",
					r.Nodes[i].ID, root.ID)
			}
		}
	}
	return nil
}

// CompareTrees сопоставляет сгенерированное дерево с эталоном по
// нормализованным названиям. При одинаковых названиях приоритет у узла с тем
// же родителем, затем у совпадающего типа. Пустой эталон возвращает ошибку в
// ReferenceReport.Error и нулевые метрики вместо деления на ноль.
func CompareTrees(ref *Reference, generated []GeneratedNode) ReferenceReport {
	var report ReferenceReport
	if ref == nil {
		report.Error = "эталон не передан"
		return report
	}
	if len(ref.Nodes) == 0 {
		report.Error = "эталон пуст: в нём нет узлов"
		return report
	}

	refByID := make(map[string]*ReferenceNode, len(ref.Nodes))
	for i := range ref.Nodes {
		refByID[ref.Nodes[i].ID] = &ref.Nodes[i]
	}
	refParentTitle := make(map[string]string, len(ref.Nodes))
	for i := range ref.Nodes {
		node := &ref.Nodes[i]
		if parent, ok := refByID[node.ParentID]; ok {
			refParentTitle[node.ID] = normalizeReferenceTitle(parent.Title)
		}
	}

	entries := make([]generatedEntry, len(generated))
	byID := make(map[string]int, len(generated))
	for i, node := range generated {
		entries[i] = generatedEntry{node: node, title: normalizeReferenceTitle(node.Title)}
		byID[node.ID] = i
	}
	for i := range entries {
		if parentIndex, ok := byID[entries[i].node.ParentID]; ok {
			entries[i].parentTitle = normalizeReferenceTitle(entries[parentIndex].node.Title)
		}
	}
	used := make([]bool, len(entries))

	required, truePositive, falsePositive := 0, 0, 0
	for i := range ref.Nodes {
		node := &ref.Nodes[i]
		if !node.Ambiguous {
			required++
		}
		index := bestCandidate(entries, used, normalizeReferenceTitle(node.Title),
			refParentTitle[node.ID], node.Type)
		if index < 0 {
			if node.Ambiguous {
				report.AmbiguousMissing = append(report.AmbiguousMissing, node.ID)
			} else {
				report.Missing = append(report.Missing, node.ID)
			}
			continue
		}
		used[index] = true
		if entries[index].node.Type == node.Type {
			report.Matched = append(report.Matched, node.ID)
			if !node.Ambiguous {
				truePositive++
			}
			continue
		}
		report.LevelMismatch = append(report.LevelMismatch,
			fmt.Sprintf("%s: ожидался %s, получен %s", node.ID, node.Type, entries[index].node.Type))
		if !node.Ambiguous {
			falsePositive++
		}
	}
	for i := range entries {
		if used[i] {
			continue
		}
		report.Extra = append(report.Extra, entries[i].node.ID)
		falsePositive++
	}

	if truePositive+falsePositive > 0 {
		report.Precision = float64(truePositive) / float64(truePositive+falsePositive)
	}
	if required > 0 {
		report.Recall = float64(truePositive) / float64(required)
	}
	if report.Precision+report.Recall > 0 {
		report.F1 = 2 * report.Precision * report.Recall / (report.Precision + report.Recall)
	}
	return report
}

type generatedEntry struct {
	node        GeneratedNode
	title       string
	parentTitle string
}

// bestCandidate ищет свободный узел генерации с тем же названием. Родитель
// важнее типа: так дубль названия не «переедет» под чужой родитель.
func bestCandidate(entries []generatedEntry, used []bool, title, parentTitle string, wantType domain.NodeType) int {
	best, bestScore := -1, -1
	for i := range entries {
		if used[i] || entries[i].title != title {
			continue
		}
		score := 0
		if entries[i].parentTitle == parentTitle {
			score += 2
		}
		if entries[i].node.Type == wantType {
			score++
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

// normalizeReferenceTitle приводит названия к сравнимому виду: trim, нижний
// регистр, «ё» → «е», схлопывание пробелов.
func normalizeReferenceTitle(title string) string {
	normalized := strings.ToLower(strings.TrimSpace(title))
	normalized = strings.ReplaceAll(normalized, "ё", "е")
	return strings.Join(strings.Fields(normalized), " ")
}
