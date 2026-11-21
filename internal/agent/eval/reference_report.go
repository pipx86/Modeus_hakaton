package eval

import (
	"fmt"
	"strings"
)

// MinReferenceRecall — блокирующий порог recall из README эталона: на эталоне
// из 10 обязательных узлов допускается пропуск двух, но не развал дерева.
const MinReferenceRecall = 0.8

// CheckReferenceThresholds проверяет блокирующие пороги ручного эталона:
// отсутствие ошибки сравнения, пустой LevelMismatch и recall не ниже
// MinReferenceRecall. Precision и F1 намеренно не входят: precision гейтится
// относительно baseline, который для каждого эталона фиксируется отдельно.
// Пустой результат означает, что пороги выполнены.
func CheckReferenceThresholds(report ReferenceReport) []string {
	if report.Error != "" {
		// Метрики при ошибке сравнения нулевые, поэтому отдельно про recall
		// не сообщаем: чинить нужно конфигурацию эталона, а не дерево.
		return []string{"сравнение не выполнено: " + report.Error}
	}
	violations := make([]string, 0, 2)
	if len(report.LevelMismatch) > 0 {
		violations = append(violations,
			"level_mismatch: "+strings.Join(report.LevelMismatch, "; "))
	}
	if report.Recall < MinReferenceRecall {
		violations = append(violations,
			fmt.Sprintf("recall = %.2f, нужно >= %.2f", report.Recall, MinReferenceRecall))
	}
	return violations
}

// FormatReferenceReport печатает человекочитаемое сравнение дерева кейса с
// ручным эталоном: метрики, missing/extra, нарушения уровней, спорные узлы и
// состояние блокирующих порогов. JSON-формат отдаёт сам ReferenceReport.
func FormatReferenceReport(caseName string, ref *Reference, report ReferenceReport) string {
	var builder strings.Builder
	builder.WriteString("Сравнение с ручным эталоном\n")
	fmt.Fprintf(&builder, "Кейс: %s\n", displayValue(caseName, "не указан"))
	if ref != nil {
		fmt.Fprintf(&builder, "Эталон: «%s», версия %s", ref.Profession, ref.Version)
		if reviewer := strings.TrimSpace(ref.ReviewedBy); reviewer != "" {
			fmt.Fprintf(&builder, ", проверил: %s", reviewer)
		}
		if reviewedAt := strings.TrimSpace(ref.ReviewedAt); reviewedAt != "" {
			fmt.Fprintf(&builder, ", дата: %s", reviewedAt)
		}
		builder.WriteString("\n")
	}

	if report.Error != "" {
		fmt.Fprintf(&builder, "Ошибка: %s\n", report.Error)
		return builder.String()
	}

	fmt.Fprintf(&builder, "precision: %.2f\n", report.Precision)
	fmt.Fprintf(&builder, "recall: %.2f\n", report.Recall)
	fmt.Fprintf(&builder, "f1: %.2f\n", report.F1)
	fmt.Fprintf(&builder, "matched: %d\n", len(report.Matched))
	writeReferenceIDs(&builder, "missing", report.Missing)
	writeReferenceIDs(&builder, "extra", report.Extra)
	if len(report.LevelMismatch) == 0 {
		builder.WriteString("level_mismatch: нет\n")
	} else {
		fmt.Fprintf(&builder, "level_mismatch (%d):\n", len(report.LevelMismatch))
		for _, mismatch := range report.LevelMismatch {
			fmt.Fprintf(&builder, "  - %s\n", mismatch)
		}
	}

	builder.WriteString("спорные узлы:\n")
	ambiguous := ambiguousReferenceNodes(ref)
	if len(ambiguous) == 0 {
		builder.WriteString("  нет\n")
	}
	for _, node := range ambiguous {
		fmt.Fprintf(&builder, "  - %s (%s): %s", node.ID, referenceNodeStatus(node.ID, report), node.Title)
		if note := strings.TrimSpace(node.Note); note != "" {
			fmt.Fprintf(&builder, " — %s", note)
		}
		builder.WriteString("\n")
	}

	if violations := CheckReferenceThresholds(report); len(violations) == 0 {
		builder.WriteString("пороги: выполнены\n")
	} else {
		builder.WriteString("пороги: не выполнены\n")
		for _, violation := range violations {
			fmt.Fprintf(&builder, "  - %s\n", violation)
		}
	}
	return builder.String()
}

// writeReferenceIDs печатает список id или «нет».
func writeReferenceIDs(builder *strings.Builder, label string, ids []string) {
	if len(ids) == 0 {
		fmt.Fprintf(builder, "%s: нет\n", label)
		return
	}
	fmt.Fprintf(builder, "%s (%d): %s\n", label, len(ids), strings.Join(ids, ", "))
}

// ambiguousReferenceNodes возвращает спорные узлы в порядке эталона.
func ambiguousReferenceNodes(ref *Reference) []ReferenceNode {
	if ref == nil {
		return nil
	}
	nodes := make([]ReferenceNode, 0)
	for _, node := range ref.Nodes {
		if node.Ambiguous {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// referenceNodeStatus подписывает спорный узел по результату сравнения.
func referenceNodeStatus(id string, report ReferenceReport) string {
	switch {
	case containsReferenceID(report.Matched, id):
		return "найден"
	case containsReferenceID(report.AmbiguousMissing, id):
		return "не найден"
	case hasLevelMismatch(report.LevelMismatch, id):
		return "нарушен уровень"
	default:
		return "не найден"
	}
}

func containsReferenceID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func hasLevelMismatch(mismatches []string, id string) bool {
	prefix := id + ":"
	for _, mismatch := range mismatches {
		if strings.HasPrefix(mismatch, prefix) {
			return true
		}
	}
	return false
}

func displayValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
