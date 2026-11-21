package fgos

import (
	"reflect"
	"testing"

	"activity-descriptor/internal/ingestion"
)

func testKSAProgram() *ingestion.EducationProgram {
	return &ingestion.EducationProgram{
		Title: "Программа",
		Competencies: []ingestion.ProgramCompetency{
			{
				Code:  "УК-1",
				Title: "Способен осуществлять поиск и критический анализ информации",
				Type:  "universal",
				KSA: []ingestion.ProgramKSA{
					{Kind: "knowledge", Title: "методы системного анализа"},
				},
			},
			{
				Code:  "ОПК-2",
				Title: "Способен разрабатывать программное обеспечение",
				Type:  "general_professional",
			},
		},
	}
}

func programCompetency(t *testing.T, program *ingestion.EducationProgram, code string) ingestion.ProgramCompetency {
	t.Helper()
	for _, competency := range program.Competencies {
		if competency.Code == code {
			return competency
		}
	}
	t.Fatalf("нет компетенции %s: %+v", code, program.Competencies)
	return ingestion.ProgramCompetency{}
}

func TestMergeKSAAddsOnlyNewAndKeepsFormulations(t *testing.T) {
	program := testKSAProgram()
	before := program.Competencies[0]
	result := MergeKSA(program, []KSAEntry{
		{Code: "УК-1", KSA: []ingestion.ProgramKSA{
			{Kind: "knowledge", Title: "методы системного анализа"},   // уже есть
			{Kind: "knowledge", Title: "методы критического анализа"}, // новая
		}},
		{Code: "ОПК-2", KSA: []ingestion.ProgramKSA{
			{Kind: "skill", Title: "разрабатывать алгоритмы"},
			{Kind: "ability", Title: "навыками отладки"},
		}},
	})
	if result.Added != 3 || result.AlreadyPresent != 1 {
		t.Fatalf("счётчики: %+v", result)
	}
	if len(result.UnknownCodes) != 0 {
		t.Fatalf("неизвестные коды: %+v", result.UnknownCodes)
	}
	uk := programCompetency(t, program, "УК-1")
	if len(uk.KSA) != 2 {
		t.Fatalf("KSA УК-1: %+v", uk.KSA)
	}
	if uk.Title != before.Title || uk.Type != before.Type {
		t.Fatalf("формулировка/тип компетенции изменились: %+v", uk)
	}
	if uk.KSA[1].Title != "методы критического анализа" {
		t.Fatalf("порядок KSA: %+v", uk.KSA)
	}
	opk := programCompetency(t, program, "ОПК-2")
	if len(opk.KSA) != 2 || opk.KSA[0].Kind != "skill" || opk.KSA[1].Kind != "ability" {
		t.Fatalf("KSA ОПК-2: %+v", opk.KSA)
	}
	if opk.Title != "Способен разрабатывать программное обеспечение" {
		t.Fatalf("формулировка ОПК-2 изменилась: %q", opk.Title)
	}
}

func TestMergeKSAUnknownCodes(t *testing.T) {
	program := testKSAProgram()
	result := MergeKSA(program, []KSAEntry{
		{Code: "ПК-9", KSA: []ingestion.ProgramKSA{{Kind: "skill", Title: "чужое"}}},
		{Code: "пк-10", KSA: []ingestion.ProgramKSA{{Kind: "knowledge", Title: "ещё чужое"}}},
		{Code: "УК-1", KSA: []ingestion.ProgramKSA{{Kind: "skill", Title: "своё"}}},
		{Code: "ПК-9", KSA: []ingestion.ProgramKSA{{Kind: "knowledge", Title: "повтор кода"}}},
	})
	if result.Added != 1 {
		t.Fatalf("added = %d, ожидали 1", result.Added)
	}
	want := []string{"ПК-10", "ПК-9"}
	if !reflect.DeepEqual(result.UnknownCodes, want) {
		t.Fatalf("unknown_codes = %+v, ожидали %+v", result.UnknownCodes, want)
	}
	if len(program.Competencies) != 2 {
		t.Fatalf("новые компетенции не должны создаваться: %+v", program.Competencies)
	}
}

func TestMergeKSADuplicatesCountAsAlreadyPresent(t *testing.T) {
	program := &ingestion.EducationProgram{
		Competencies: []ingestion.ProgramCompetency{{Code: "УК-1", Title: "Компетенция"}},
	}
	result := MergeKSA(program, []KSAEntry{
		{Code: "УК-1", KSA: []ingestion.ProgramKSA{
			{Kind: "knowledge", Title: "знание"},
			{Kind: "knowledge", Title: "знание"},
		}},
		{Code: "УК-1", KSA: []ingestion.ProgramKSA{
			{Kind: "knowledge", Title: "знание"},
			{Kind: "skill", Title: "умение"},
		}},
	})
	if result.Added != 2 || result.AlreadyPresent != 2 {
		t.Fatalf("счётчики: %+v", result)
	}
	if len(program.Competencies[0].KSA) != 2 {
		t.Fatalf("KSA: %+v", program.Competencies[0].KSA)
	}
}

func TestMergeKSAIdempotent(t *testing.T) {
	program := testKSAProgram()
	entries := []KSAEntry{{Code: "УК-1", KSA: []ingestion.ProgramKSA{
		{Kind: "knowledge", Title: "методы системного анализа"},
		{Kind: "skill", Title: "анализировать информацию"},
	}}}

	first := MergeKSA(program, entries)
	if first.Added != 1 || first.AlreadyPresent != 1 {
		t.Fatalf("первый вызов: %+v", first)
	}
	second := MergeKSA(program, entries)
	if second.Added != 0 || second.AlreadyPresent != 2 {
		t.Fatalf("повторный вызов должен быть идемпотентным: %+v", second)
	}
	if len(program.Competencies[0].KSA) != 2 {
		t.Fatalf("KSA размножились: %+v", program.Competencies[0].KSA)
	}
}

func TestMergeKSAEmptyEntries(t *testing.T) {
	program := testKSAProgram()
	before := *program
	result := MergeKSA(program, nil)
	if result.Added != 0 || result.AlreadyPresent != 0 {
		t.Fatalf("счётчики: %+v", result)
	}
	if result.UnknownCodes == nil || len(result.UnknownCodes) != 0 {
		t.Fatalf("unknown_codes должен быть пустым массивом: %+v", result.UnknownCodes)
	}
	if !reflect.DeepEqual(*program, before) {
		t.Fatalf("программа изменилась: %+v", program)
	}
	result = MergeKSA(program, []KSAEntry{})
	if result.Added != 0 || result.UnknownCodes == nil {
		t.Fatalf("пустой список: %+v", result)
	}
}

func TestMergeKSANormalizesCodeAndKind(t *testing.T) {
	program := testKSAProgram()
	result := MergeKSA(program, []KSAEntry{
		{Code: " опк – 2 ", KSA: []ingestion.ProgramKSA{
			{Kind: "знание", Title: "основы алгоритмизации"},
		}},
	})
	if result.Added != 1 || len(result.UnknownCodes) != 0 {
		t.Fatalf("слияние по нормализованному коду: %+v", result)
	}
	opk := programCompetency(t, program, "ОПК-2")
	if len(opk.KSA) != 1 || opk.KSA[0].Kind != "knowledge" {
		t.Fatalf("нормализация kind: %+v", opk.KSA)
	}
}

func TestMergeKSASkipsInvalidItems(t *testing.T) {
	program := testKSAProgram()
	result := MergeKSA(program, []KSAEntry{
		{Code: "УК-1", KSA: []ingestion.ProgramKSA{
			{Kind: "mystery", Title: "неизвестный вид"},
			{Kind: "knowledge", Title: "   "},
		}},
		{Code: "UK-1", KSA: []ingestion.ProgramKSA{
			{Kind: "knowledge", Title: "латинский код"},
		}},
	})
	if result.Added != 0 || result.AlreadyPresent != 0 {
		t.Fatalf("счётчики: %+v", result)
	}
	if len(result.UnknownCodes) != 0 {
		t.Fatalf("невалидные коды не должны попадать в unknown_codes: %+v", result.UnknownCodes)
	}
	if len(program.Competencies[0].KSA) != 1 {
		t.Fatalf("KSA изменились: %+v", program.Competencies[0].KSA)
	}
}

func TestMergeKSANilProgram(t *testing.T) {
	result := MergeKSA(nil, []KSAEntry{
		{Code: "УК-1", KSA: []ingestion.ProgramKSA{{Kind: "knowledge", Title: "x"}}},
		{Code: "УК-1", KSA: []ingestion.ProgramKSA{{Kind: "skill", Title: "y"}}},
	})
	if result.Added != 0 || result.AlreadyPresent != 0 {
		t.Fatalf("счётчики: %+v", result)
	}
	if !reflect.DeepEqual(result.UnknownCodes, []string{"УК-1"}) {
		t.Fatalf("unknown_codes: %+v", result.UnknownCodes)
	}
}
