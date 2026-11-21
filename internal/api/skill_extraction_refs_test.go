package api

import (
	"activity-descriptor/internal/domain"
	"testing"
)

func TestNormalizeSkillExtractionVersionsOnlyWhenUnambiguous(t *testing.T) {
	model := domain.SkillModel{
		Definitions: []domain.SkillDefinition{
			{ID: "single", Version: 1},
			{ID: "multiple", Version: 1},
			{ID: "multiple", Version: 2},
		},
		Links: []domain.SkillNodeLink{
			{SkillRef: domain.SkillRef{ID: "single", Version: 9}},
			{SkillRef: domain.SkillRef{ID: "multiple", Version: 9}},
			{SkillRef: domain.SkillRef{ID: "missing", Version: 9}},
			{SkillRef: domain.SkillRef{ID: "multiple", Version: 2}},
		},
	}

	normalizeSkillExtractionVersions(&model)

	if model.Links[0].Version != 1 {
		t.Fatalf("unique definition version = %d, want 1", model.Links[0].Version)
	}
	if model.Links[1].Version != 9 || model.Links[2].Version != 9 {
		t.Fatalf("ambiguous or unknown version was guessed: %+v", model.Links)
	}
	if model.Links[3].Version != 2 {
		t.Fatalf("exact existing version changed: %d", model.Links[3].Version)
	}
}
