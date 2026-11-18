package api

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"activity-descriptor/internal/domain"
)

// «История» сравнения деревьев — детерминированное человекочитаемое описание
// diff'а, одинаковое для обоих сравнительных эндпоинтов:
//
//   - GET /api/v1/runs/{id}/snapshots/{snapshot_id}/diff?against={base_snapshot_id}
//     (поля story и story_lines);
//   - GET /api/v1/runs/{id}/compare?against={other_run_id} (поле story).
//
// Текст на русском языке (UI проекта русскоязычный) и строится только из
// структурированного diff'а, поэтому:
//   - counts в заголовке всегда совпадают с len(added)/len(removed)/len(changed),
//     то есть с полем summary того же ответа;
//   - порядок детерминирован: категории added → removed → changed, внутри
//     категории — по id узла, поля изменения — в фиксированном порядке
//     (title, description, type, parent_id, evidence);
//   - длина ограничена (см. comparisonStory*), поэтому гигантские деревья не
//     раздувают ответ.
//
// Формат:
//
//	story      — заголовок с количествами и не более чем comparisonStoryOneLineDetails
//	             подробностями, одна строка;
//	story_lines — заголовок + до comparisonStoryMaxLines подробностей, по одной
//	             на строку; если подробностей больше, последняя строка —
//	             «Ещё изменений: N.».
//
// Пустой diff даёт «Изменений нет: деревья идентичны.» (и в story, и в
// единственной строке story_lines). Изменение только подтверждений (fields =
// ["evidence"]) — не пустой diff: это изменение узла с полем «подтверждения».
const (
	// comparisonStoryTitleLimit — сколько рун названия узла показываем в истории.
	comparisonStoryTitleLimit = 80
	// comparisonStoryMaxRunes — верхняя граница длины одной строки story.
	comparisonStoryMaxRunes = 800
	// comparisonStoryOneLineDetails — сколько подробностей попадает в story.
	comparisonStoryOneLineDetails = 5
	// comparisonStoryMaxLines — сколько подробностей попадает в story_lines.
	comparisonStoryMaxLines = 20
	// comparisonStoryEmptyText — история сравнения двух одинаковых деревьев.
	comparisonStoryEmptyText = "Изменений нет: деревья идентичны."
	// comparisonStoryOverflowFormat — строка про опущенные подробности.
	comparisonStoryOverflowFormat = "Ещё изменений: %d."
)

// comparisonFieldOrder — канонический порядок полей узла в diff (совпадает с
// snapshotChangedFields), поэтому и в истории поля перечисляются так же.
var comparisonFieldOrder = []string{"title", "description", "type", "parent_id", "evidence"}

// comparisonFieldLabels — русские названия полей узла для истории.
var comparisonFieldLabels = map[string]string{
	"title":       "название",
	"description": "описание",
	"type":        "тип",
	"parent_id":   "родительский узел",
	"evidence":    "подтверждения",
}

// buildComparisonStory строит story и story_lines из результата сравнения
// двух деревьев. Функция чистая: не мутирует переданные срезы и не зависит от
// порядка элементов на входе.
func buildComparisonStory(added, removed []domain.Node, changed []snapshotNodeChange) (string, []string) {
	addedNodes := sortedNodesByID(added)
	removedNodes := sortedNodesByID(removed)
	changedNodes := sortedChangesByID(changed)

	details := make([]string, 0, len(addedNodes)+len(removedNodes)+len(changedNodes))
	for _, node := range addedNodes {
		details = append(details, fmt.Sprintf("Добавлен узел «%s».", comparisonNodeTitle(node)))
	}
	for _, node := range removedNodes {
		details = append(details, fmt.Sprintf("Удалён узел «%s».", comparisonNodeTitle(node)))
	}
	for _, change := range changedNodes {
		details = append(details, comparisonChangedDetail(change))
	}

	if len(details) == 0 {
		return comparisonStoryEmptyText, []string{comparisonStoryEmptyText}
	}

	header := fmt.Sprintf("Узлов добавлено: %d, удалено: %d, изменено: %d.",
		len(addedNodes), len(removedNodes), len(changedNodes))

	lines := make([]string, 0, 1+comparisonMinInt(len(details), comparisonStoryMaxLines)+1)
	lines = append(lines, header)
	for index, detail := range details {
		if index >= comparisonStoryMaxLines {
			lines = append(lines, fmt.Sprintf(comparisonStoryOverflowFormat, len(details)-index))
			break
		}
		lines = append(lines, detail)
	}

	return comparisonOneLineStory(header, details), lines
}

// comparisonOneLineStory склеивает заголовок и подробности в одну строку,
// укладываясь в comparisonStoryMaxRunes и comparisonStoryOneLineDetails.
func comparisonOneLineStory(header string, details []string) string {
	var builder strings.Builder
	builder.WriteString(header)

	// Резервируем место под строку «Ещё изменений: N.», чтобы не обрезать её.
	const overflowReserve = 40
	budget := comparisonStoryMaxRunes - overflowReserve
	included := 0
	for _, detail := range details {
		if included >= comparisonStoryOneLineDetails {
			break
		}
		if utf8.RuneCountInString(builder.String())+1+utf8.RuneCountInString(detail) > budget {
			break
		}
		builder.WriteString(" ")
		builder.WriteString(detail)
		included++
	}
	if included < len(details) {
		overflow := fmt.Sprintf(comparisonStoryOverflowFormat, len(details)-included)
		if utf8.RuneCountInString(builder.String())+1+utf8.RuneCountInString(overflow) <= comparisonStoryMaxRunes {
			builder.WriteString(" ")
			builder.WriteString(overflow)
		}
	}

	story := builder.String()
	// Защита от неожиданно длинного заголовка/подробности: правило выше держит
	// строку в бюджете, но контракт «не длиннее comparisonStoryMaxRunes» важнее.
	if utf8.RuneCountInString(story) > comparisonStoryMaxRunes {
		story = string([]rune(story)[:comparisonStoryMaxRunes])
	}
	return story
}

// comparisonChangedDetail описывает одно изменённое поле/поля узла.
func comparisonChangedDetail(change snapshotNodeChange) string {
	labels := comparisonChangedFieldLabels(change.Fields)
	if labels == "" {
		return fmt.Sprintf("Изменён узел «%s»: изменения.", comparisonNodeTitle(change.After))
	}
	return fmt.Sprintf("Изменён узел «%s»: %s.", comparisonNodeTitle(change.After), labels)
}

// comparisonChangedFieldLabels переводит имена изменённых полей в русские
// названия в каноническом порядке. Неизвестные поля (на случай расширения
// diff'а) добавляются в конце по алфавиту — так ответ остаётся детерминированным.
func comparisonChangedFieldLabels(fields []string) string {
	present := make(map[string]bool, len(fields))
	for _, field := range fields {
		present[field] = true
	}
	labels := make([]string, 0, len(fields))
	for _, field := range comparisonFieldOrder {
		if present[field] {
			labels = append(labels, comparisonFieldLabels[field])
			delete(present, field)
		}
	}
	unknown := make([]string, 0, len(present))
	for field := range present {
		unknown = append(unknown, field)
	}
	sort.Strings(unknown)
	labels = append(labels, unknown...)
	return strings.Join(labels, ", ")
}

// comparisonNodeTitle приводит название узла к виду, пригодному для одной
// строки: схлопывает пробелы/переводы строк, пустое название заменяет
// заглушкой и ограничивает длину.
func comparisonNodeTitle(node domain.Node) string {
	title := strings.Join(strings.Fields(node.Title), " ")
	if title == "" {
		return "без названия"
	}
	if utf8.RuneCountInString(title) > comparisonStoryTitleLimit {
		title = string([]rune(title)[:comparisonStoryTitleLimit]) + "…"
	}
	return title
}

// sortedNodesByID возвращает копию среза, отсортированную по id: порядок
// истории не должен зависеть от порядка обхода дерева/БД.
func sortedNodesByID(nodes []domain.Node) []domain.Node {
	result := make([]domain.Node, len(nodes))
	copy(result, nodes)
	sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// sortedChangesByID возвращает копию среза изменений, отсортированную по id.
func sortedChangesByID(changes []snapshotNodeChange) []snapshotNodeChange {
	result := make([]snapshotNodeChange, len(changes))
	copy(result, changes)
	sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func comparisonMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
