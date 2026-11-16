package analyst

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm/validate"
)

// ErrQuoteMismatch marks an entity whose quote is not a verbatim fragment of
// the transcript.
var ErrQuoteMismatch = errors.New("analyst: quote is not verbatim")

// ErrNoEntities marks an empty extraction, which cannot support a role model.
var ErrNoEntities = errors.New("analyst: model returned no entities")

// ValidateEntities enforces the mandatory evidence fields and checks that every
// quote is a verbatim transcript fragment after whitespace normalization.
func ValidateEntities(out agent.AnalystOutput, transcript string) error {
	if len(out.Entities) == 0 {
		return ErrNoEntities
	}
	for i := range out.Entities {
		entity := &out.Entities[i]
		if err := entity.Validate(); err != nil {
			return fmt.Errorf("analyst: entity %q: %w", entity.ID, err)
		}
		for _, evidence := range entity.Evidence {
			if !validate.ContainsQuote(transcript, evidence.Quote) {
				return fmt.Errorf("%w: entity %q quote %q", ErrQuoteMismatch, entity.ID, evidence.Quote)
			}
		}
	}
	return nil
}

// NormalizeEntities trims field values and fills missing identifiers with
// deterministic content hashes so deduplication and evidence stay stable.
func NormalizeEntities(entities []agent.ExtractedEntity) []agent.ExtractedEntity {
	result := make([]agent.ExtractedEntity, 0, len(entities))
	for _, entity := range entities {
		entity.Title = strings.TrimSpace(entity.Title)
		entity.Description = strings.TrimSpace(entity.Description)
		entity.ID = strings.TrimSpace(entity.ID)
		if entity.ID == "" {
			entity.ID = syntheticID("entity", string(entity.Type)+"|"+normalizeTitle(entity.Title))
		}
		for i := range entity.Evidence {
			evidence := &entity.Evidence[i]
			evidence.ID = strings.TrimSpace(evidence.ID)
			evidence.SourceID = strings.TrimSpace(evidence.SourceID)
			evidence.RefID = strings.TrimSpace(evidence.RefID)
			evidence.Quote = strings.TrimSpace(evidence.Quote)
			if evidence.ID == "" {
				evidence.ID = syntheticID("ev", evidence.SourceID+"|"+evidence.RefID+"|"+normalizeQuote(evidence.Quote))
			}
		}
		result = append(result, entity)
	}
	return result
}

// ToNodes converts extracted entities to plain domain nodes.
func ToNodes(entities []agent.ExtractedEntity) []domain.Node {
	nodes := make([]domain.Node, 0, len(entities))
	for _, entity := range entities {
		node := entity.Node
		node.Evidence = append([]domain.Evidence(nil), entity.Evidence...)
		if entity.ParentID != nil {
			parent := *entity.ParentID
			node.ParentID = &parent
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func syntheticID(prefix, value string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + value))
	return prefix + "-" + hex.EncodeToString(sum[:6])
}

// mergeEvidence appends src evidence to dst, dropping duplicates by source,
// ref id and normalized quote.
func mergeEvidence(dst, src []domain.Evidence) []domain.Evidence {
	seen := make(map[string]bool, len(dst)+len(src))
	result := make([]domain.Evidence, 0, len(dst)+len(src))
	for _, list := range [][]domain.Evidence{dst, src} {
		for _, evidence := range list {
			key := evidence.SourceID + "\x00" + evidence.RefID + "\x00" + normalizeQuote(evidence.Quote)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, evidence)
		}
	}
	return result
}

func normalizeQuote(quote string) string {
	return strings.Join(strings.Fields(quote), " ")
}
