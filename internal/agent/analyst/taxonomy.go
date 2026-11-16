package analyst

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

//go:embed assets/taxonomy.json
var defaultTaxonomyJSON []byte

// ErrTaxonomyInvalid marks a malformed or inconsistent taxonomy fixture.
var ErrTaxonomyInvalid = errors.New("analyst: invalid taxonomy")

// TaxonomyEntry is one canonical role-model element.
type TaxonomyEntry struct {
	ID       string          `json:"id"`
	Type     domain.NodeType `json:"type"`
	Title    string          `json:"title"`
	ParentID *string         `json:"parent_id,omitempty"`
	Aliases  []string        `json:"aliases,omitempty"`
}

// Taxonomy is a curated role model used to link extracted entities to
// canonical functions, tasks and actions.
type Taxonomy struct {
	Version string          `json:"version"`
	Entries []TaxonomyEntry `json:"entries"`
}

// DefaultTaxonomy loads the taxonomy fixture embedded in the binary.
func DefaultTaxonomy() (Taxonomy, error) {
	return LoadTaxonomy(defaultTaxonomyJSON)
}

// LoadTaxonomy parses and validates taxonomy JSON.
func LoadTaxonomy(data []byte) (Taxonomy, error) {
	var taxonomy Taxonomy
	if err := json.Unmarshal(data, &taxonomy); err != nil {
		return Taxonomy{}, fmt.Errorf("%w: %v", ErrTaxonomyInvalid, err)
	}
	if err := taxonomy.Validate(); err != nil {
		return Taxonomy{}, err
	}
	return taxonomy, nil
}

// Validate checks unique ids, valid node types, existing parents and the strict
// role → function → task → action hierarchy.
func (t Taxonomy) Validate() error {
	if strings.TrimSpace(t.Version) == "" {
		return fmt.Errorf("%w: empty version", ErrTaxonomyInvalid)
	}
	if len(t.Entries) == 0 {
		return fmt.Errorf("%w: no entries", ErrTaxonomyInvalid)
	}
	byID := make(map[string]TaxonomyEntry, len(t.Entries))
	for _, entry := range t.Entries {
		if strings.TrimSpace(entry.ID) == "" {
			return fmt.Errorf("%w: entry without id", ErrTaxonomyInvalid)
		}
		if !entry.Type.Valid() {
			return fmt.Errorf("%w: entry %q has invalid type %q", ErrTaxonomyInvalid, entry.ID, entry.Type)
		}
		if strings.TrimSpace(entry.Title) == "" {
			return fmt.Errorf("%w: entry %q has empty title", ErrTaxonomyInvalid, entry.ID)
		}
		if _, duplicate := byID[entry.ID]; duplicate {
			return fmt.Errorf("%w: duplicate id %q", ErrTaxonomyInvalid, entry.ID)
		}
		byID[entry.ID] = entry
	}
	for _, entry := range t.Entries {
		if entry.ParentID == nil {
			if entry.Type != domain.NodeRole {
				return fmt.Errorf("%w: non-root entry %q has no parent", ErrTaxonomyInvalid, entry.ID)
			}
			continue
		}
		parent, ok := byID[*entry.ParentID]
		if !ok {
			return fmt.Errorf("%w: entry %q references missing parent %q", ErrTaxonomyInvalid, entry.ID, *entry.ParentID)
		}
		if !validParentType(entry.Type, parent.Type) {
			return fmt.Errorf("%w: %s %q cannot have %s parent %q",
				ErrTaxonomyInvalid, entry.Type, entry.ID, parent.Type, parent.ID)
		}
	}
	return nil
}

// Lookup finds the taxonomy entry by normalized title or alias.
func (t Taxonomy) Lookup(title string) (TaxonomyEntry, bool) {
	normalized := normalizeTitle(title)
	if normalized == "" {
		return TaxonomyEntry{}, false
	}
	for _, entry := range t.Entries {
		if normalizeTitle(entry.Title) == normalized {
			return entry, true
		}
		for _, alias := range entry.Aliases {
			if normalizeTitle(alias) == normalized {
				return entry, true
			}
		}
	}
	return TaxonomyEntry{}, false
}

// LookupID returns the taxonomy entry with the given id.
func (t Taxonomy) LookupID(id string) (TaxonomyEntry, bool) {
	for _, entry := range t.Entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return TaxonomyEntry{}, false
}

// Link aligns nodes with taxonomy ids and adds missing ancestors whose evidence
// is aggregated from their descendants. Nodes without a taxonomy match are kept
// unchanged. The result is ordered role → function → task → action.
func (t Taxonomy) Link(nodes []domain.Node) []domain.Node {
	byID := make(map[string]domain.Node, len(nodes)+len(t.Entries))
	order := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if entry, ok := t.Lookup(node.Title); ok {
			node.ID = entry.ID
			node.Type = entry.Type
			node.ParentID = entry.ParentID
		}
		if existing, ok := byID[node.ID]; ok {
			existing.Evidence = mergeEvidence(existing.Evidence, node.Evidence)
			byID[node.ID] = existing
			continue
		}
		byID[node.ID] = node
		order = append(order, node.ID)
	}

	// Aggregate descendant evidence for every taxonomy ancestor and create
	// missing ancestors so the tree stays connected.
	ancestorEvidence := make(map[string][]domain.Evidence)
	ancestorOrder := make([]string, 0)
	for _, node := range byID {
		entry, ok := t.LookupID(node.ID)
		if !ok {
			continue
		}
		for parentID := entry.ParentID; parentID != nil; {
			ancestorEvidence[*parentID] = mergeEvidence(ancestorEvidence[*parentID], node.Evidence)
			if _, seen := byID[*parentID]; !seen {
				ancestorOrder = append(ancestorOrder, *parentID)
			}
			parent, ok := t.LookupID(*parentID)
			if !ok {
				break
			}
			parentID = parent.ParentID
		}
	}
	for _, id := range ancestorOrder {
		if _, ok := byID[id]; ok {
			continue
		}
		entry, ok := t.LookupID(id)
		if !ok {
			continue
		}
		byID[id] = domain.Node{
			ID:       entry.ID,
			Type:     entry.Type,
			Title:    entry.Title,
			ParentID: entry.ParentID,
			Evidence: ancestorEvidence[id],
		}
	}

	result := make([]domain.Node, 0, len(byID))
	seen := make(map[string]bool, len(byID))
	for _, id := range order {
		result = append(result, byID[id])
		seen[id] = true
	}
	remaining := make([]string, 0, len(byID))
	for id := range byID {
		if !seen[id] {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	for _, id := range remaining {
		result = append(result, byID[id])
	}
	sort.SliceStable(result, func(i, j int) bool {
		return typeRank(result[i].Type) < typeRank(result[j].Type)
	})
	return result
}

// NodesToEntities wraps linked nodes back into the analyst output shape.
func NodesToEntities(nodes []domain.Node) []agent.ExtractedEntity {
	entities := make([]agent.ExtractedEntity, 0, len(nodes))
	for _, node := range nodes {
		entities = append(entities, agent.ExtractedEntity{Node: node})
	}
	return entities
}

func validParentType(child, parent domain.NodeType) bool {
	switch child {
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
