package steps

import "activity-descriptor/internal/domain"

// Provenance classification is shared by the chat finalizer and the batch AI
// chain. Both paths must route an evidence-backed node into the same
// synthesizer slot, otherwise the deterministic merge sees a single Interview
// list in one path and silently drops the losing source value instead of
// recording a conflict (карточки 9 и 17 плана MVP).

// SourceTypesByID indexes the declared type of every source by id.
func SourceTypesByID(sources []domain.Source) map[string]domain.SourceType {
	types := make(map[string]domain.SourceType, len(sources))
	for _, source := range sources {
		types[source.ID] = source.Type
	}
	return types
}

// ClassifyNodeProvenance reports the single non-interview source type shared by
// all evidence of the node. Mixed, unknown, interview and document provenance,
// and an evidence-less node, are reported as interview: missing metadata must
// never fabricate a standard/vacancy node.
func ClassifyNodeProvenance(node domain.Node, sourceTypes map[string]domain.SourceType) domain.SourceType {
	if len(node.Evidence) == 0 {
		return domain.SourceInterview
	}
	provenance := domain.SourceType("")
	for _, evidence := range node.Evidence {
		sourceType, ok := sourceTypes[evidence.SourceID]
		if !ok {
			return domain.SourceInterview
		}
		if sourceType != domain.SourceStandard && sourceType != domain.SourceVacancy {
			return domain.SourceInterview
		}
		if provenance == "" {
			provenance = sourceType
			continue
		}
		if provenance != sourceType {
			return domain.SourceInterview
		}
	}
	return provenance
}

// PartitionNodesByProvenance routes nodes into the three synthesizer input
// slots. An entity belongs to Standard (or Vacancy) only when every evidence
// item is backed by a source of that type. Everything else falls back to
// Interview, which keeps the interview-only path working when provenance cannot
// be established. Node ids, hierarchy and evidence are passed through unchanged:
// partitioning only chooses a slot.
func PartitionNodesByProvenance(nodes []domain.Node, sources []domain.Source) (interview, standard, vacancy []domain.Node) {
	if len(nodes) == 0 {
		return nil, nil, nil
	}
	sourceTypes := SourceTypesByID(sources)
	for _, node := range nodes {
		switch ClassifyNodeProvenance(node, sourceTypes) {
		case domain.SourceStandard:
			standard = append(standard, node)
		case domain.SourceVacancy:
			vacancy = append(vacancy, node)
		default:
			interview = append(interview, node)
		}
	}
	return interview, standard, vacancy
}
