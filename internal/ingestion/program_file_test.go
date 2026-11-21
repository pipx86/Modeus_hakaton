package ingestion

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestParseProgramFileCSVSemicolonWithBOM(t *testing.T) {
	input := "\ufeffkind;code;title;parent_code;taxonomy_codes\n" +
		"program;P1;Программа;;\n" +
		"competency;K1;Проектирование;;\"T1;T2\"\n" +
		"знание;K1;Знать ГОСТ;K1;T1\n" +
		"умение;K1;Анализировать;K1;\n" +
		"навык;K1;Документировать;K1;\n"
	program, warnings, err := ParseProgramFile("program.csv", strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseProgramFile: %v", err)
	}
	if program.Code != "P1" || program.Title != "Программа" || len(program.Competencies) != 1 {
		t.Fatalf("программа: %+v", program)
	}
	competency := program.Competencies[0]
	if competency.Code != "K1" || competency.Title != "Проектирование" {
		t.Fatalf("компетенция: %+v", competency)
	}
	if len(competency.TaxonomyCodes) != 2 || competency.TaxonomyCodes[0] != "T1" || competency.TaxonomyCodes[1] != "T2" {
		t.Fatalf("таксономия: %+v", competency.TaxonomyCodes)
	}
	if len(competency.KSA) != 3 {
		t.Fatalf("KSA: %+v", competency.KSA)
	}
	if competency.KSA[0].Kind != "knowledge" || competency.KSA[1].Kind != "skill" || competency.KSA[2].Kind != "ability" {
		t.Fatalf("виды KSA: %+v", competency.KSA)
	}
	if len(competency.KSA[0].TaxonomyCodes) != 1 || competency.KSA[0].TaxonomyCodes[0] != "T1" {
		t.Fatalf("таксономия KSA: %+v", competency.KSA[0])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "type") {
		t.Fatalf("ожидали предупреждение о пустом type: %+v", warnings)
	}
}

func TestParseProgramFileCSVCommaAndOrganization(t *testing.T) {
	input := "kind,code,title,parent_code,taxonomy_codes,organization,type\n" +
		"program,P1,Программа,,,Вуз,\n" +
		"competency,K1,Компетенция,,T1;T2,,ПК\n"
	program, warnings, err := ParseProgramFile("program.csv", strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseProgramFile: %v", err)
	}
	if program.Organization != "Вуз" || len(program.Competencies) != 1 {
		t.Fatalf("программа: %+v", program)
	}
	competency := program.Competencies[0]
	if competency.Type != "professional" {
		t.Fatalf("type: %q", competency.Type)
	}
	if len(competency.TaxonomyCodes) != 2 {
		t.Fatalf("таксономия: %+v", competency.TaxonomyCodes)
	}
	if len(warnings) != 0 {
		t.Fatalf("неожиданные предупреждения: %+v", warnings)
	}
}

func TestParseProgramFileCSVKindKSA(t *testing.T) {
	input := "kind,code,title,parent_code,taxonomy_codes,type\n" +
		"program,P1,Программа,,,\n" +
		"competency,K1,Компетенция,,,\n" +
		"ksa,skill,Уметь,K1,,, \n"
	program, warnings, err := ParseProgramFile("program.csv", strings.NewReader(input))
	if err != nil {
		t.Fatalf("kind=ksa: %v", err)
	}
	if len(program.Competencies) != 1 || len(program.Competencies[0].KSA) != 1 {
		t.Fatalf("KSA: %+v", program.Competencies)
	}
	if program.Competencies[0].KSA[0].Kind != "skill" {
		t.Fatalf("вид KSA: %+v", program.Competencies[0].KSA[0])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "type") {
		t.Fatalf("ожидали предупреждение о пустом type: %+v", warnings)
	}
}

func TestParseProgramFileCSVRussianTypes(t *testing.T) {
	for raw, want := range map[string]string{
		"УК":   "universal",
		"ОПК":  "general_professional",
		"ПК":   "professional",
		"иное": "other",
	} {
		input := "kind;code;title;parent_code;taxonomy_codes;type\n" +
			"program;P1;Программа;;;\n" +
			"competency;K1;Компетенция;;;" + raw + "\n"
		program, _, err := ParseProgramFile("program.csv", strings.NewReader(input))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if len(program.Competencies) != 1 || program.Competencies[0].Type != want {
			t.Fatalf("%s: ожидали %q, получили %+v", raw, want, program.Competencies)
		}
	}
}

func TestParseProgramFileCSVRowErrorsAndWarnings(t *testing.T) {
	input := "kind;code;title;parent_code;taxonomy_codes;type\n" +
		"\n" +
		"program;;;;;\n" +
		"competency;K1;Первая;;;\n" +
		"competency;K1;Дубль;;;\n" +
		"knowledge;K1;Знать;K9;;\n" +
		"other;K1;Неизвестно;K1;;\n"
	program, warnings, err := ParseProgramFile("program.csv", strings.NewReader(input))
	if err == nil {
		t.Fatalf("ожидали ошибки, получили %+v", program)
	}
	var problems *ProgramFileErrors
	if !errors.As(err, &problems) {
		t.Fatalf("ожидали *ProgramFileErrors, получили %T: %v", err, err)
	}
	joined := strings.Join(problems.Details(), "\n")
	for _, fragment := range []string{"строка 3", "обязателен code", "строка 5", "повтор кода компетенции", "строка 7", "неизвестный kind"} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("в ошибках нет %q:\n%s", fragment, joined)
		}
	}
	if len(problems.Details()) < 4 {
		t.Fatalf("ожидали все ошибки строк, получили: %+v", problems.Details())
	}
	warningsText := strings.Join(warnings, "\n")
	if !strings.Contains(warningsText, "K9") {
		t.Fatalf("ожидали предупреждение о KSA без родителя: %+v", warnings)
	}
	if !strings.Contains(warningsText, "type") {
		t.Fatalf("ожидали предупреждение о пустом type: %+v", warnings)
	}
}

func TestParseProgramFileCSVOrphanKSADropped(t *testing.T) {
	input := "kind,code,title,parent_code,taxonomy_codes,type\n" +
		"program,P1,Программа,,,\n" +
		"competency,K1,Компетенция,,,ПК\n" +
		"knowledge,K1,Знать,MISSING,T1,\n"
	program, warnings, err := ParseProgramFile("program.csv", strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseProgramFile: %v", err)
	}
	if len(program.Competencies) != 1 || len(program.Competencies[0].KSA) != 0 {
		t.Fatalf("KSA без родителя должна быть отброшена: %+v", program.Competencies)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "MISSING") {
		t.Fatalf("предупреждения: %+v", warnings)
	}
}

func TestParseProgramFileCSVHeaderAndEncodingErrors(t *testing.T) {
	if _, _, err := ParseProgramFile("program.csv", strings.NewReader("foo;bar\n1;2\n")); err == nil || !strings.Contains(err.Error(), "шапке") {
		t.Fatalf("ожидали ошибку шапки, получили %v", err)
	}
	invalidUTF8 := append([]byte("kind;code;title\nprogram;"), 0xFF, '\n')
	if _, _, err := ParseProgramFile("program.csv", bytes.NewReader(invalidUTF8)); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("ожидали ошибку UTF-8, получили %v", err)
	}
}

func TestParseProgramFileJSONAndText(t *testing.T) {
	program, warnings, err := ParseProgramFile("program.json", strings.NewReader(`{"code":"P1","title":"Программа","competencies":[{"code":"K1","title":"Компетенция","type":"ПК"}]}`))
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if program.Competencies[0].Type != "professional" || len(warnings) != 0 {
		t.Fatalf("JSON: %+v %+v", program, warnings)
	}
	if _, _, err = ParseProgramFile("program.json", strings.NewReader(`{"code":"P1","title":"Программа","competencies":[{"code":"K1","title":"Компетенция","type":"bad"}]}`)); err == nil {
		t.Fatal("неизвестный type JSON принят")
	}

	text := "Компетенция: K1 | Анализ | ОПК\nЗнание: Стандарт"
	program, _, err = ParseProgramFile("uchebnaya-programma.md", strings.NewReader(text))
	if err != nil {
		t.Fatalf("текст: %v", err)
	}
	if program.Title != "uchebnaya-programma" || program.Competencies[0].Type != "general_professional" {
		t.Fatalf("текст: %+v", program)
	}
}

func TestParseProgramFileXLSX(t *testing.T) {
	file := excelize.NewFile()
	defer file.Close()
	file.SetSheetName(file.GetSheetName(0), "Программа")
	if _, err := file.NewSheet("Компетенции"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.NewSheet("KSA"); err != nil {
		t.Fatal(err)
	}
	mustRow := func(sheet, cell string, values ...any) {
		t.Helper()
		if err := file.SetSheetRow(sheet, cell, &values); err != nil {
			t.Fatal(err)
		}
	}
	mustRow("Программа", "A1", " code ", " title ", " organization ")
	mustRow("Программа", "A2", "P1", "Программа", "Вуз")
	mustRow("Компетенции", "A1", "Code", "Title", "Type", "taxonomy_codes")
	mustRow("Компетенции", "A2", "K1", "Компетенция", "УК", "T1;T2")
	mustRow("Компетенции", "A3", "K2", "Без типа", "", "")
	mustRow("KSA", "A1", "kind", "title", "parent_code", "taxonomy_codes")
	mustRow("KSA", "A2", "знание", "Знать", "K1", "T1")
	mustRow("KSA", "A3", "умение", "Уметь", "MISSING", "")
	var buffer bytes.Buffer
	if _, err := file.WriteTo(&buffer); err != nil {
		t.Fatal(err)
	}

	program, warnings, err := ParseProgramFile("program.xlsx", &buffer)
	if err != nil {
		t.Fatalf("XLSX: %v", err)
	}
	if program.Code != "P1" || program.Organization != "Вуз" || len(program.Competencies) != 2 {
		t.Fatalf("программа: %+v", program)
	}
	if program.Competencies[0].Type != "universal" || len(program.Competencies[0].KSA) != 1 {
		t.Fatalf("компетенция K1: %+v", program.Competencies[0])
	}
	if program.Competencies[1].Type != "" {
		t.Fatalf("компетенция K2: %+v", program.Competencies[1])
	}
	warningsText := strings.Join(warnings, "\n")
	for _, fragment := range []string{"Компетенции", "type", "KSA", "MISSING"} {
		if !strings.Contains(warningsText, fragment) {
			t.Fatalf("в предупреждениях нет %q: %+v", fragment, warnings)
		}
	}
}

func TestParseProgramFileXLSXMissingSheets(t *testing.T) {
	file := excelize.NewFile()
	defer file.Close()
	var buffer bytes.Buffer
	if _, err := file.WriteTo(&buffer); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseProgramFile("program.xlsx", &buffer); err == nil || !strings.Contains(err.Error(), "не найдены листы") {
		t.Fatalf("ожидали ошибку о листах, получили %v", err)
	}
}

func TestProgramSourceFormatAndUnsupported(t *testing.T) {
	for name, want := range map[string]string{
		"a.CSV":  "csv",
		"a.xlsx": "xlsx",
		"a.json": "json",
		"a.md":   "text",
		"a.txt":  "text",
	} {
		if got := ProgramSourceFormat(name); got != want {
			t.Fatalf("%s: %q, ожидали %q", name, got, want)
		}
	}
	if _, _, err := ParseProgramFile("program.pdf", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "не поддерживается") {
		t.Fatalf("ожидали ошибку формата, получили %v", err)
	}
}
