package domain

import (
	"fmt"
	"reflect"
	"strings"
)

// SkillModel is a versioned dictionary scoped to a research, never a person score.
type SkillModel struct {
	Revision    int               `json:"revision"`
	Tools       []SkillTool       `json:"tools"`
	Definitions []SkillDefinition `json:"definitions"`
	Links       []SkillNodeLink   `json:"links"`
}
type SkillTool struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Aliases []string `json:"aliases"`
}
type SkillToolUse struct {
	ToolID   string `json:"tool_id"`
	Usage    string `json:"usage"`
	Required bool   `json:"required"`
	Version  string `json:"version,omitempty"`
}
type SkillDefinition struct {
	ID         string         `json:"id"`
	Version    int            `json:"version"`
	Title      string         `json:"title"`
	Kind       string         `json:"kind"`
	Action     string         `json:"action"`
	Object     string         `json:"object"`
	Conditions string         `json:"conditions"`
	Result     string         `json:"result"`
	Criterion  string         `json:"criterion"`
	Autonomy   string         `json:"autonomy"`
	Scope      string         `json:"scope"`
	Tools      []SkillToolUse `json:"tools"`
}
type SkillRef struct {
	ID      string `json:"definition_id"`
	Version int    `json:"version"`
}
type SkillNodeLink struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	SkillRef
	EvidenceIDs []string   `json:"evidence_ids"`
	Evidence    []Evidence `json:"evidence"`
	Stale       bool       `json:"stale,omitempty"`
	Basis       string     `json:"basis"`
	Reason      string     `json:"reason"`
	Status      string     `json:"status"`
}

func SkillKey(id string, version int) string { return fmt.Sprintf("%s@%d", id, version) }
func SkillText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(s, "ё", "е"))), " ")
}
func EmptySkillModel() SkillModel {
	return SkillModel{Tools: []SkillTool{
		{"python", "Python", "language", []string{"Питон"}},
		{"sql", "SQL", "language", []string{}},
		{"postgresql", "PostgreSQL", "database", []string{"Postgres", "Постгрес"}},
		{"java", "Java", "language", []string{}},
		{"docker", "Docker", "platform", []string{}},
		{"kubernetes", "Kubernetes", "platform", []string{"k8s"}},
	}, Definitions: []SkillDefinition{}, Links: []SkillNodeLink{}}
}

func NormalizeSkillModel(m *SkillModel) {
	if m.Tools == nil {
		m.Tools = []SkillTool{}
	}
	if m.Definitions == nil {
		m.Definitions = []SkillDefinition{}
	}
	if m.Links == nil {
		m.Links = []SkillNodeLink{}
	}
	for i := range m.Tools {
		if m.Tools[i].Aliases == nil {
			m.Tools[i].Aliases = []string{}
		}
	}
	for i := range m.Definitions {
		if m.Definitions[i].Tools == nil {
			m.Definitions[i].Tools = []SkillToolUse{}
		}
	}
	for i := range m.Links {
		if m.Links[i].EvidenceIDs == nil {
			m.Links[i].EvidenceIDs = []string{}
		}
		if m.Links[i].Evidence == nil {
			m.Links[i].Evidence = []Evidence{}
		}
	}
}

func (d SkillDefinition) Gaps() []string {
	gaps := []string{}
	for _, f := range []struct{ v, n string }{{d.Action, "действие"}, {d.Object, "объект"}, {d.Conditions, "условия"}, {d.Result, "результат"}, {d.Criterion, "критерий проверки"}} {
		if SkillFieldMissing(f.v) {
			gaps = append(gaps, f.n)
		}
	}
	if d.Kind == "proficiency" {
		if SkillFieldMissing(d.Autonomy) {
			gaps = append(gaps, "самостоятельность")
		}
		if SkillFieldMissing(d.Scope) {
			gaps = append(gaps, "диапазон ситуаций и устойчивость")
		}
	}
	return gaps
}

// A statement that information was not supplied cannot satisfy a requirement.
func SkillFieldMissing(value string) bool {
	text := SkillText(value)
	if text == "" {
		return true
	}
	for _, phrase := range []string{"не обсуждал", "не уточнен", "не уточнял", "не указан", "не сообщ", "не установл", "не описан", "не определен"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// ValidateSkillModel preserves all accepted definition versions and checks links.
// Tools cannot silently change meaning once they have been stored.
func ValidateSkillModel(next, previous SkillModel, nodes []Node) error {
	if len(next.Tools) > 500 || len(next.Definitions) > 2000 || len(next.Links) > 5000 {
		return fmt.Errorf("превышен размер словаря")
	}
	tools := map[string]SkillTool{}
	aliases := map[string]string{}
	for _, tool := range next.Tools {
		if strings.TrimSpace(tool.ID) == "" || len(tool.ID) > 100 || strings.TrimSpace(tool.Name) == "" || len(tool.Name) > 300 || len(tool.Aliases) > 50 {
			return fmt.Errorf("нужны ID и название инструмента")
		}
		if _, ok := tools[tool.ID]; ok {
			return fmt.Errorf("повтор ID инструмента")
		}
		switch tool.Kind {
		case "language", "database", "framework", "library", "platform", "application", "other":
		default:
			return fmt.Errorf("неизвестный тип инструмента")
		}
		for _, alias := range append([]string{tool.Name}, tool.Aliases...) {
			key := SkillText(alias)
			if key == "" || len(key) > 300 {
				return fmt.Errorf("пустой или слишком длинный синоним")
			}
			if id, ok := aliases[key]; ok && id != tool.ID {
				return fmt.Errorf("синоним %q неоднозначен", alias)
			}
			aliases[key] = tool.ID
		}
		tools[tool.ID] = tool
	}
	for _, tool := range previous.Tools {
		if current, ok := tools[tool.ID]; !ok || current.Name != tool.Name || current.Kind != tool.Kind {
			return fmt.Errorf("нельзя удалить или переопределить инструмент %s", tool.Name)
		}
	}
	defs := map[string]SkillDefinition{}
	maxVersion := map[string]int{}
	for _, d := range previous.Definitions {
		maxVersion[d.ID] = max(maxVersion[d.ID], d.Version)
	}
	for _, d := range next.Definitions {
		key := SkillKey(d.ID, d.Version)
		if strings.TrimSpace(d.ID) == "" || len(d.ID) > 100 || d.Version < 1 || d.Version > 2000 || strings.TrimSpace(d.Title) == "" {
			return fmt.Errorf("неверный ID, версия или название ЗУН")
		}
		if _, ok := defs[key]; ok {
			return fmt.Errorf("повтор версии ЗУН")
		}
		switch d.Kind {
		case "knowledge", "skill", "proficiency":
		default:
			return fmt.Errorf("неизвестный вид ЗУН")
		}
		for _, v := range []string{d.Title, d.Action, d.Object, d.Conditions, d.Result, d.Criterion, d.Autonomy, d.Scope} {
			if len([]rune(v)) > 2000 {
				return fmt.Errorf("поле ЗУН длиннее 2000 символов")
			}
		}
		seen := map[string]bool{}
		for _, use := range d.Tools {
			if _, ok := tools[use.ToolID]; !ok || strings.TrimSpace(use.Usage) == "" || seen[use.ToolID] || len(use.Usage) > 2000 || len(use.Version) > 100 {
				return fmt.Errorf("неверная связь с инструментом")
			}
			seen[use.ToolID] = true
		}
		defs[key] = d
		maxVersion[d.ID] = max(maxVersion[d.ID], d.Version)
	}
	for id, version := range maxVersion {
		for v := 1; v <= version; v++ {
			if _, ok := defs[SkillKey(id, v)]; !ok {
				return fmt.Errorf("версии ЗУН должны идти подряд без пропусков")
			}
		}
	}
	for _, old := range previous.Definitions {
		if current, ok := defs[SkillKey(old.ID, old.Version)]; !ok || !reflect.DeepEqual(old, current) {
			return fmt.Errorf("сохранённая версия ЗУН неизменяема; создайте следующую версию")
		}
	}
	nodeMap := map[string]Node{}
	oldLinks := map[string]SkillNodeLink{}
	for _, link := range previous.Links {
		oldLinks[link.ID] = link
	}
	for _, n := range nodes {
		nodeMap[n.ID] = n
	}
	seen := map[string]bool{}
	for _, link := range next.Links {
		d, ok := defs[SkillKey(link.SkillRef.ID, link.Version)]
		if !ok {
			return fmt.Errorf("версия ЗУН не найдена")
		}
		n, ok := nodeMap[link.NodeID]
		old, known := oldLinks[link.ID]
		historical := known && old.NodeID == link.NodeID && old.SkillRef == link.SkillRef && reflect.DeepEqual(old.EvidenceIDs, link.EvidenceIDs)
		if !ok && !historical {
			return fmt.Errorf("узел не принадлежит исследованию")
		}
		if link.ID == "" || len(link.ID) > 100 || seen[link.ID] || strings.TrimSpace(link.Reason) == "" || len(link.Reason) > 4000 {
			return fmt.Errorf("неверный ID или основание связи")
		}
		seen[link.ID] = true
		switch link.Basis {
		case "requirement", "interview", "interpretation":
		default:
			return fmt.Errorf("укажите вид основания")
		}
		switch link.Status {
		case "proposed", "approved", "rejected":
		default:
			return fmt.Errorf("неверный статус связи")
		}
		evidence := map[string]Evidence{}
		for _, e := range n.Evidence {
			evidence[e.ID] = e
		}
		if len(link.EvidenceIDs) == 0 {
			return fmt.Errorf("нужно хотя бы одно основание")
		}
		used := map[string]bool{}
		for _, id := range link.EvidenceIDs {
			e, ok := evidence[id]
			if ((!ok || e.Status == EvidenceRejected) && !historical) || used[id] {
				return fmt.Errorf("основание отсутствует, повторяется или отклонено")
			}
			used[id] = true
		}
		if link.Status == "approved" && len(d.Gaps()) > 0 {
			return fmt.Errorf("для подтверждения заполните: %s", strings.Join(d.Gaps(), ", "))
		}
	}
	return nil
}

type SkillComparison struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons"`
}

// CompareSkills compares agreed requirements; it never certifies competence.
func CompareSkills(expected, actual SkillDefinition) SkillComparison {
	r := SkillComparison{"full", []string{}}
	if SkillText(expected.Action) != SkillText(actual.Action) || SkillText(expected.Object) != SkillText(actual.Object) {
		return SkillComparison{"not_established", []string{"действие или объект различаются; эквивалентность не установлена"}}
	}
	if len(expected.Gaps()) > 0 || len(actual.Gaps()) > 0 {
		return SkillComparison{"not_established", []string{"определение неполное"}}
	}
	for _, f := range []struct{ a, b, n string }{{expected.Kind, actual.Kind, "вид ЗУН"}, {expected.Conditions, actual.Conditions, "условия"}, {expected.Result, actual.Result, "результат"}, {expected.Criterion, actual.Criterion, "критерий"}, {expected.Autonomy, actual.Autonomy, "самостоятельность"}, {expected.Scope, actual.Scope, "диапазон ситуаций"}} {
		if SkillText(f.a) != SkillText(f.b) {
			r.Status = "partial"
			r.Reasons = append(r.Reasons, "различается: "+f.n)
		}
	}
	for _, required := range expected.Tools {
		if !required.Required {
			continue
		}
		found := false
		for _, use := range actual.Tools {
			if use.Required && required.ToolID == use.ToolID && (required.Version == "" || required.Version == use.Version) && SkillText(required.Usage) == SkillText(use.Usage) {
				found = true
			}
		}
		if !found {
			r.Status = "partial"
			r.Reasons = append(r.Reasons, "не совпадает обязательное применение инструмента: "+required.ToolID)
		}
	}
	if r.Status == "full" {
		r.Reasons = append(r.Reasons, "совпадают согласованные поля; это соответствие описаний, а не оценка человека")
	}
	return r
}
