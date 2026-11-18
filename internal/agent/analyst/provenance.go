package analyst

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm/validate"
)

// InterviewSourceID is the synthetic source id the analyst prompt uses for the
// interview transcript. The transcript is not a database source at analysis
// time (the finalizer creates it afterwards), so the model gets a stable id to
// cite. Entities whose evidence references it are classified as Interview.
const InterviewSourceID = "interview-transcript"

// provenanceClass maps a source type to the synthesizer slot its evidence
// belongs to. Standard and Vacancy keep their own slot; everything else
// (interview, document, unknown) is treated as Interview so the conservative
// default of the finalizer is preserved.
func provenanceClass(sourceType domain.SourceType) domain.SourceType {
	switch sourceType {
	case domain.SourceStandard:
		return domain.SourceStandard
	case domain.SourceVacancy:
		return domain.SourceVacancy
	default:
		return domain.SourceInterview
	}
}

// provenanceOrder is the deterministic order in which provenance groups are
// emitted. It mirrors the source priority order interview > standard > vacancy.
var provenanceOrder = []domain.SourceType{domain.SourceInterview, domain.SourceStandard, domain.SourceVacancy}

// sourceTypesByID indexes the declared source types of the analyst input.
func sourceTypesByID(sources []domain.Source) map[string]domain.SourceType {
	types := make(map[string]domain.SourceType, len(sources))
	for _, source := range sources {
		types[source.ID] = source.Type
	}
	return types
}

// evidenceProvenance reports the slot of one evidence item. Unknown ids are
// Interview: missing metadata must never fabricate a standard/vacancy node.
func evidenceProvenance(sourceID string, types map[string]domain.SourceType) domain.SourceType {
	sourceType, ok := types[sourceID]
	if !ok {
		return domain.SourceInterview
	}
	return provenanceClass(sourceType)
}

// splitEntitiesByProvenance splits every entity into one entity per provenance
// class of its evidence. An entity extracted from both a standard and a vacancy
// source (for example because the model put two quotes into one entity) becomes
// two entities with the same id/title/parent and disjoint evidence, so the
// finalizer can route each to the right synthesizer slot instead of silently
// collapsing the mixed evidence into Interview.
func splitEntitiesByProvenance(entities []agent.ExtractedEntity, sources []domain.Source) []provenanceGroup {
	types := sourceTypesByID(sources)
	grouped := make(map[domain.SourceType][]agent.ExtractedEntity, len(provenanceOrder))
	for _, entity := range entities {
		byClass := make(map[domain.SourceType][]domain.Evidence, len(provenanceOrder))
		for _, evidence := range entity.Evidence {
			class := evidenceProvenance(evidence.SourceID, types)
			byClass[class] = append(byClass[class], evidence)
		}
		for _, class := range provenanceOrder {
			evidence := byClass[class]
			if len(evidence) == 0 {
				continue
			}
			copy := entity
			copy.Evidence = evidence
			grouped[class] = append(grouped[class], copy)
		}
	}
	groups := make([]provenanceGroup, 0, len(grouped))
	for _, class := range provenanceOrder {
		if len(grouped[class]) == 0 {
			continue
		}
		groups = append(groups, provenanceGroup{class: class, entities: grouped[class]})
	}
	return groups
}

// provenanceGroup is one batch of single-provenance entities. Deduplication and
// taxonomy linking run per group so entities from different source types never
// share evidence by accident.
type provenanceGroup struct {
	class    domain.SourceType
	entities []agent.ExtractedEntity
}

// sourceContents indexes every text the analyst may quote: the interview
// transcript plus the declared standard/vacancy sources.
func sourceContents(in agent.AnalystInput) map[string]domain.Source {
	contents := make(map[string]domain.Source, len(in.Sources)+1)
	contents[InterviewSourceID] = domain.Source{
		ID:      InterviewSourceID,
		Type:    domain.SourceInterview,
		Title:   "Транскрипт интервью",
		Content: in.Transcript,
	}
	for _, source := range in.Sources {
		// An interview source usually has no persisted content at analysis time
		// (the transcript itself is the content). Treat the transcript as its
		// text so declared interview evidence keeps its own source id instead of
		// being rewritten to the synthetic transcript source.
		if source.Type == domain.SourceInterview && strings.TrimSpace(source.Content) == "" {
			source.Content = in.Transcript
		}
		contents[source.ID] = source
	}
	return contents
}

// ResolveEvidenceSources attributes every evidence quote to the source that
// actually contains it.
//
// The model is asked to cite the source id it quoted from, but live models
// sometimes fall back to an invented id. A quote that is not found in the
// declared source is therefore re-attributed to the single best matching source
// (highest trust priority, then lowest id). Evidence that only occurs in the
// transcript is normalized to InterviewSourceID. A quote found nowhere is a
// validation error and triggers the normal repair loop.
func ResolveEvidenceSources(out *agent.AnalystOutput, in agent.AnalystInput) error {
	if out == nil {
		return errors.New("analyst: nil output")
	}
	contents := sourceContents(in)
	for i := range out.Entities {
		entity := &out.Entities[i]
		for j := range entity.Evidence {
			evidence := &entity.Evidence[j]
			if err := resolveEvidence(evidence, contents); err != nil {
				return fmt.Errorf("%w: entity %q: %v", ErrQuoteMismatch, entity.ID, err)
			}
		}
	}
	return nil
}

func resolveEvidence(evidence *domain.Evidence, contents map[string]domain.Source) error {
	declared, declaredKnown := contents[evidence.SourceID]
	if declaredKnown && validate.ContainsQuote(declared.Content, evidence.Quote) {
		return nil
	}

	bestID := ""
	bestPriority := -1
	ids := make([]string, 0, len(contents))
	for id := range contents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		source := contents[id]
		if !validate.ContainsQuote(source.Content, evidence.Quote) {
			continue
		}
		priority := source.EffectivePriority()
		if priority > bestPriority {
			bestID, bestPriority = id, priority
		}
	}
	if bestID != "" {
		evidence.SourceID = bestID
		return nil
	}
	if validate.ContainsQuote(contents[InterviewSourceID].Content, evidence.Quote) {
		// The quote is an interview fragment. A known source id would make the
		// finalizer classify it as standard/vacancy, so normalize it.
		if declaredKnown && declared.Type != domain.SourceInterview {
			evidence.SourceID = InterviewSourceID
		} else if strings.TrimSpace(evidence.SourceID) == "" {
			evidence.SourceID = InterviewSourceID
		}
		return nil
	}
	return errors.New("quote is not present in the transcript or any provided source")
}

// evidenceSource resolves the declared source of an evidence item. It reports
// false when the id is unknown, which the caller treats as Interview.
func evidenceSource(contents map[string]domain.Source, sourceID string) (domain.Source, bool) {
	source, ok := contents[sourceID]
	return source, ok
}
