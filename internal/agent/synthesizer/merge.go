package synthesizer

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

// ErrInvalidInput marks source nodes that violate the evidence or hierarchy
// contract before merging.
var ErrInvalidInput = errors.New("synthesizer: invalid input")

// sourcedNode is a node together with the source it came from.
type sourcedNode struct {
	source domain.Source
	node   domain.Node
}

// groupNodes validates every input node and groups nodes by canonical id.
// Source priorities come from the declared sources (or the source type when a
// source is not declared).
//
// Every node keeps the declared source its own evidence points at, so a
// competing value names the material it really came from even when several
// sources share one type (two standards, two vacancies). Without that, all
// values of a slot would be attributed to the first source of the type and the
// expert could not trace the losing value back to its origin.
func groupNodes(in agent.SynthesizerInput) (map[string][]sourcedNode, []string, error) {
	groups := make(map[string][]sourcedNode)
	ids := make([]string, 0)
	declared := declaredSourcesByID(in.Sources)

	appendSource := func(sourceType domain.SourceType, nodes []domain.Node) error {
		slot := findSource(in.Sources, sourceType)
		for _, node := range nodes {
			if strings.TrimSpace(node.ID) == "" {
				return fmt.Errorf("%w: node without id from %s", ErrInvalidInput, sourceType)
			}
			if !node.Type.Valid() {
				return fmt.Errorf("%w: node %q has invalid type %q", ErrInvalidInput, node.ID, node.Type)
			}
			if err := node.Validate(); err != nil {
				return fmt.Errorf("%w: node %q from %s: %v", ErrInvalidInput, node.ID, sourceType, err)
			}
			if _, seen := groups[node.ID]; !seen {
				ids = append(ids, node.ID)
			}
			groups[node.ID] = append(groups[node.ID], sourcedNode{source: nodeSource(slot, node, declared), node: node})
		}
		return nil
	}
	if err := appendSource(domain.SourceInterview, in.Interview); err != nil {
		return nil, nil, err
	}
	if err := appendSource(domain.SourceStandard, in.Standard); err != nil {
		return nil, nil, err
	}
	if err := appendSource(domain.SourceVacancy, in.Vacancy); err != nil {
		return nil, nil, err
	}
	sort.Strings(ids)
	return groups, ids, nil
}

// declaredSourcesByID indexes the declared source metadata by id.
func declaredSourcesByID(sources []domain.Source) map[string]domain.Source {
	declared := make(map[string]domain.Source, len(sources))
	for _, source := range sources {
		if source.ID == "" {
			continue
		}
		declared[source.ID] = source
	}
	return declared
}

// nodeSource returns the declared source that actually backs the node. The
// first evidence item referring to a declared source of the node's slot type
// wins; otherwise the slot source is used, so a node whose provenance cannot be
// resolved still gets a stable, non-invented basis.
func nodeSource(slot domain.Source, node domain.Node, declared map[string]domain.Source) domain.Source {
	for _, evidence := range node.Evidence {
		source, ok := declared[evidence.SourceID]
		if ok && source.Type == slot.Type {
			return source
		}
	}
	return slot
}

// Merge combines nodes from all three sources, resolves conflicts by source
// priority interview > standard > vacancy and connects the result into one tree.
// Every output node carries evidence aggregated across sources.
func Merge(in agent.SynthesizerInput) (agent.SynthesizerOutput, error) {
	groups, ids, err := groupNodes(in)
	if err != nil {
		return agent.SynthesizerOutput{}, err
	}
	if len(ids) == 0 {
		return agent.SynthesizerOutput{}, fmt.Errorf("%w: no nodes from any source", ErrInvalidInput)
	}

	conflicts := DetectConflicts(groups, ids)
	tree, err := EnsureTree(mergeGroups(groups, ids, conflicts), in.Profession)
	if err != nil {
		return agent.SynthesizerOutput{}, err
	}
	for _, node := range tree {
		if err := node.Validate(); err != nil {
			return agent.SynthesizerOutput{}, fmt.Errorf("%w: merged node %q: %v", ErrInvalidInput, node.ID, err)
		}
	}
	return agent.SynthesizerOutput{
		Tree:      tree,
		Conflicts: conflicts,
		Messages:  ConflictMessages(conflicts),
	}, nil
}

// mergeGroups keeps one node per canonical id: the value of the highest-priority
// source wins title and description, while evidence from every source is kept.
func mergeGroups(groups map[string][]sourcedNode, ids []string, conflicts []domain.Conflict) []domain.Node {
	conflictFields := make(map[string]map[string]domain.Conflict, len(conflicts))
	for _, conflict := range conflicts {
		fields, ok := conflictFields[conflict.NodeID]
		if !ok {
			fields = make(map[string]domain.Conflict)
			conflictFields[conflict.NodeID] = fields
		}
		fields[conflict.Field] = conflict
	}

	merged := make([]domain.Node, 0, len(ids))
	for _, id := range ids {
		items := groups[id]
		best := items[0]
		for _, item := range items[1:] {
			if item.source.EffectivePriority() > best.source.EffectivePriority() {
				best = item
			}
		}
		node := best.node
		node.Evidence = unionEvidence(items)
		if fields := conflictFields[id]; fields != nil {
			if conflict, ok := fields["title"]; ok {
				node.Title = conflict.Winner.Value
			}
			if conflict, ok := fields["description"]; ok {
				node.Description = conflict.Winner.Value
			}
		}
		merged = append(merged, node)
	}
	return merged
}

func findSource(sources []domain.Source, sourceType domain.SourceType) domain.Source {
	for _, source := range sources {
		if source.Type == sourceType {
			return source
		}
	}
	return domain.Source{
		ID:    "src-" + string(sourceType),
		Type:  sourceType,
		Title: string(sourceType),
	}
}

func unionEvidence(items []sourcedNode) []domain.Evidence {
	seen := make(map[string]bool)
	var result []domain.Evidence
	for _, item := range items {
		for _, evidence := range item.node.Evidence {
			key := evidence.SourceID + "\x00" + evidence.RefID + "\x00" + strings.Join(strings.Fields(evidence.Quote), " ")
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, evidence)
		}
	}
	return result
}

// EnsureTree connects nodes into one role → function → task → action hierarchy.
// Missing ancestors are generated and their evidence is aggregated from their
// descendants, so no node ends up without evidence.
func EnsureTree(nodes []domain.Node, profession string) ([]domain.Node, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("%w: no nodes", ErrInvalidInput)
	}
	byID := make(map[string]domain.Node, len(nodes))
	for _, node := range nodes {
		if _, duplicate := byID[node.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate merged node %q", ErrInvalidInput, node.ID)
		}
		byID[node.ID] = node
	}

	idsByType := func(nodeType domain.NodeType) []string {
		ids := make([]string, 0)
		for id, node := range byID {
			if node.Type == nodeType {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		return ids
	}
	newNode := func(nodeType domain.NodeType, title string) string {
		base := "node-" + string(nodeType)
		id := base
		for suffix := 2; ; suffix++ {
			if _, taken := byID[id]; !taken {
				break
			}
			id = fmt.Sprintf("%s-%d", base, suffix)
		}
		byID[id] = domain.Node{ID: id, Type: nodeType, Title: title}
		return id
	}
	setParent := func(id string, parentID *string) {
		node := byID[id]
		node.ParentID = parentID
		byID[id] = node
	}
	hasParentOfType := func(node domain.Node, wanted domain.NodeType) bool {
		if node.ParentID == nil {
			return false
		}
		parent, ok := byID[*node.ParentID]
		return ok && parent.Type == wanted
	}

	// 1. Roles: the first role becomes the root, extra roles stay top-level.
	roleIDs := idsByType(domain.NodeRole)
	rootID := ""
	if len(roleIDs) == 0 {
		rootID = newNode(domain.NodeRole, roleTitle(profession))
	} else {
		rootID = roleIDs[0]
		for _, id := range roleIDs[1:] {
			setParent(id, nil)
		}
	}
	setParent(rootID, nil)

	// 2. Functions attach to the root role.
	for _, id := range idsByType(domain.NodeFunction) {
		if hasParentOfType(byID[id], domain.NodeRole) {
			continue
		}
		root := rootID
		setParent(id, &root)
	}
	firstFunction := func() string {
		if ids := idsByType(domain.NodeFunction); len(ids) > 0 {
			return ids[0]
		}
		id := newNode(domain.NodeFunction, "Функции роли")
		root := rootID
		setParent(id, &root)
		return id
	}

	// 3. Tasks attach to a function, generating one when needed.
	for _, id := range idsByType(domain.NodeTask) {
		if hasParentOfType(byID[id], domain.NodeFunction) {
			continue
		}
		parentID := firstFunction()
		setParent(id, &parentID)
	}
	firstTask := func() string {
		if ids := idsByType(domain.NodeTask); len(ids) > 0 {
			return ids[0]
		}
		functionID := firstFunction()
		id := newNode(domain.NodeTask, "Задачи")
		setParent(id, &functionID)
		return id
	}

	// 4. Actions attach to a task (or to another action).
	for _, id := range idsByType(domain.NodeAction) {
		node := byID[id]
		if node.ParentID != nil {
			if parent, ok := byID[*node.ParentID]; ok &&
				(parent.Type == domain.NodeTask || parent.Type == domain.NodeAction) {
				continue
			}
		}
		parentID := firstTask()
		setParent(id, &parentID)
	}

	// 5. Aggregate descendant evidence into evidence-less generated parents.
	allIDs := make([]string, 0, len(byID))
	for id := range byID {
		allIDs = append(allIDs, id)
	}
	sort.Strings(allIDs)
	for changed := true; changed; {
		changed = false
		for _, id := range allIDs {
			node := byID[id]
			if len(node.Evidence) > 0 {
				continue
			}
			for _, childID := range allIDs {
				child := byID[childID]
				if child.ParentID != nil && *child.ParentID == id && len(child.Evidence) > 0 {
					node.Evidence = append(node.Evidence, child.Evidence...)
				}
			}
			if len(node.Evidence) > 0 {
				byID[id] = node
				changed = true
			}
		}
	}

	for _, id := range allIDs {
		node := byID[id]
		if len(node.Evidence) == 0 {
			return nil, fmt.Errorf("%w: node %q has no evidence", ErrInvalidInput, id)
		}
		if err := node.Validate(); err != nil {
			return nil, fmt.Errorf("%w: node %q: %v", ErrInvalidInput, id, err)
		}
		if node.ParentID == nil {
			continue
		}
		parent, ok := byID[*node.ParentID]
		if !ok {
			return nil, fmt.Errorf("%w: node %q references missing parent %q", ErrInvalidInput, id, *node.ParentID)
		}
		if !validParentType(node.Type, parent.Type) {
			return nil, fmt.Errorf("%w: %s node %q cannot be a child of %s %q",
				ErrInvalidInput, node.Type, id, parent.Type, parent.ID)
		}
	}

	sort.SliceStable(allIDs, func(i, j int) bool {
		left, right := byID[allIDs[i]], byID[allIDs[j]]
		if typeRank(left.Type) != typeRank(right.Type) {
			return typeRank(left.Type) < typeRank(right.Type)
		}
		return left.ID < right.ID
	})
	tree := make([]domain.Node, 0, len(allIDs))
	tree = append(tree, byID[rootID])
	for _, id := range allIDs {
		if id == rootID {
			continue
		}
		tree = append(tree, byID[id])
	}
	return tree, nil
}

func roleTitle(profession string) string {
	if strings.TrimSpace(profession) == "" {
		return "Роль"
	}
	return strings.TrimSpace(profession)
}

func typeRank(nodeType domain.NodeType) int {
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

func validParentType(child, parent domain.NodeType) bool {
	switch child {
	case domain.NodeRole:
		return false
	case domain.NodeFunction:
		return parent == domain.NodeRole
	case domain.NodeTask:
		return parent == domain.NodeFunction
	case domain.NodeAction:
		return parent == domain.NodeTask || parent == domain.NodeAction
	default:
		return false
	}
}
