package api

import "activity-descriptor/internal/domain"

// A model occasionally copies the right definition ID but emits a stale or
// off-by-one version in its link. Correct that reference only when this
// response contains exactly one version for the ID; ambiguous references stay
// invalid and are rejected by ValidateSkillModel.
func normalizeSkillExtractionVersions(model *domain.SkillModel) {
	versionsByID := map[string]map[int]struct{}{}
	for _, definition := range model.Definitions {
		if versionsByID[definition.ID] == nil {
			versionsByID[definition.ID] = map[int]struct{}{}
		}
		versionsByID[definition.ID][definition.Version] = struct{}{}
	}
	for i := range model.Links {
		versions := versionsByID[model.Links[i].SkillRef.ID]
		if _, exact := versions[model.Links[i].Version]; exact || len(versions) != 1 {
			continue
		}
		for version := range versions {
			model.Links[i].Version = version
		}
	}
}
