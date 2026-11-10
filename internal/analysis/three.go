package analysis

import (
	"fmt"
	"strings"

	"activity-descriptor/internal/domain"
)

func BuildActivityTree(profession string, levels []map[string]any, reports []GapReport) ([]domain.Node, error) {
	if profession == "" {
		return nil, fmt.Errorf("profession is required")
	}

	nodes := make([]domain.Node, 0, len(levels)+len(reports)*2+1)

	rootID := slugify("role-" + profession)
	nodes = append(nodes, domain.Node{
		ID:       rootID,
		Type:     domain.NodeRole,
		Title:    titleCase(profession),
		ParentID: nil,
		Evidence: []domain.Evidence{},
	})

	for i, lv := range levels {
		exp, _ := lv["experience"].(string)
		skillsRaw, _ := lv["skills"].([]string)
		if exp == "" || len(skillsRaw) == 0 {
			continue
		}

		funcID := slugify(fmt.Sprintf("fn-%s-%d", exp, i))
		title := fmt.Sprintf("%s-level requirements", titleCase(exp))
		parentRef := rootID

		ev := make([]domain.Evidence, 0, len(skillsRaw))
		for j, sk := range skillsRaw {
			ev = append(ev, domain.Evidence{
				ID:       slugify(fmt.Sprintf("ev-fn%d-s%d", i, j)),
				SourceID: "taxonomy:" + sk,
				Quote:    sk,
			})
		}

		nodes = append(nodes, domain.Node{
			ID:       funcID,
			Type:     domain.NodeFunction,
			Title:    title,
			ParentID: &parentRef,
			Evidence: ev,
		})

		for k, sk := range skillsRaw {
			taskID := slugify(fmt.Sprintf("task-%s-%d-%d", exp, i, k))
			funcRef := funcID
			nodes = append(nodes, domain.Node{
				ID:       taskID,
				Type:     domain.NodeTask,
				Title:    sk,
				ParentID: &funcRef,
				Evidence: []domain.Evidence{{
					ID:       slugify(fmt.Sprintf("ev-task%d-k%d", i, k)),
					SourceID: "market:" + sk,
					Quote:    fmt.Sprintf("demand signal for %s at %s level", sk, exp),
				}},
			})
		}
	}

	for ri, rep := range reports {
		for _, rec := range rep.Recommendations {
			actID := slugify(fmt.Sprintf("act-%d-%s", ri, rec.Skill))
			parentForRec := rootID
			nodes = append(nodes, domain.Node{
				ID:       actID,
				Type:     domain.NodeAction,
				Title:    fmt.Sprintf("Learn %s (priority %.2f)", rec.Skill, rec.Priority),
				ParentID: &parentForRec,
				Evidence: []domain.Evidence{{
					ID:       slugify(fmt.Sprintf("ev-act%d-%s", ri, rec.Skill)),
					SourceID: "gap:" + rec.Skill,
					Quote:    rec.Reason,
				}},
			})
		}
	}

	return nodes, nil
}

func slugify(s string) string {
	b := make([]byte, 0, len(s))
	prevDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b = append(b, byte(r))
			prevDash = false
		default:
			if !prevDash {
				b = append(b, '-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(string(b), "-") //
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
