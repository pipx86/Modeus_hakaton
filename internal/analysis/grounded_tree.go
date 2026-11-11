package analysis

import (
	"crypto/rand"
	"fmt"
	"strings"
	"unicode/utf8"

	"activity-descriptor/internal/domain"
	"github.com/jackc/pgx/v5/pgtype"
)

// BuildGroundedTree строит предварительное дерево по реально найденным фрагментам вакансий.
// Это автоматическая заготовка: содержание узлов требует проверки человеком.
func BuildGroundedTree(profession string, vacancies []domain.VacancyRecord, skills [][]string) ([]domain.Node, error) {
	profession = strings.TrimSpace(profession)
	if profession == "" {
		return nil, fmt.Errorf("профессия не задана")
	}
	type finding struct{ skill, source, quote string }
	var findings []finding
	seen := map[string]bool{}
	for i, vacancy := range vacancies {
		if i >= len(skills) {
			break
		}
		var source pgtype.UUID
		if source.Scan(vacancy.SourceID) != nil || !source.Valid {
			continue
		}
		for _, skill := range skills[i] {
			key := strings.ToLower(strings.TrimSpace(skill))
			if key == "" || seen[key] {
				continue
			}
			quote := excerpt(vacancy.Description, key)
			if quote == "" {
				continue
			}
			seen[key] = true
			findings = append(findings, finding{skill: skill, source: vacancy.SourceID, quote: quote})
		}
	}
	if len(findings) == 0 {
		return nil, fmt.Errorf("нет навыков с подтверждением в сохранённых вакансиях")
	}
	newID := func() (string, error) {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
	}
	newNode := func(kind domain.NodeType, title string, parent *string, f finding) (domain.Node, error) {
		id, err := newID()
		if err != nil {
			return domain.Node{}, err
		}
		evID, err := newID()
		if err != nil {
			return domain.Node{}, err
		}
		return domain.Node{ID: id, Type: kind, Title: title, ParentID: parent, Evidence: []domain.Evidence{{ID: evID, SourceID: f.source, Quote: f.quote}}}, nil
	}
	root, err := newNode(domain.NodeRole, profession, nil, findings[0])
	if err != nil {
		return nil, err
	}
	fn, err := newNode(domain.NodeFunction, "Работа с профессиональными навыками", &root.ID, findings[0])
	if err != nil {
		return nil, err
	}
	nodes := []domain.Node{root, fn}
	for _, f := range findings {
		task, err := newNode(domain.NodeTask, "Применять "+f.skill, &fn.ID, f)
		if err != nil {
			return nil, err
		}
		action, err := newNode(domain.NodeAction, "Использовать "+f.skill, &task.ID, f)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, task, action)
	}
	if err := domain.ValidateTree(nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

// excerpt возвращает только часть оригинального текста, содержащую найденный навык.
func excerpt(text, skill string) string {
	index := strings.Index(strings.ToLower(text), strings.ToLower(skill))
	if index < 0 || index >= len(text) || !utf8.ValidString(text[:index]) {
		return ""
	}
	start := utf8.RuneCountInString(text[:index])
	runes := []rune(text)
	left := start - 100
	if left < 0 {
		left = 0
	}
	right := start + utf8.RuneCountInString(skill) + 200
	if right > len(runes) {
		right = len(runes)
	}
	return strings.TrimSpace(string(runes[left:right]))
}
