package ingestion

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// maxProgramFileSize ограничивает файл программы теми же 20 МБ, что и
// остальные загрузчики, чтобы импорт не занимал память бесконечно.
const maxProgramFileSize = 20 << 20

// ProgramFileErrors - список проблем импорта с номерами строк. Парсер не
// останавливается на первой ошибке: администратор должен увидеть все
// некорректные строки за один раз.
type ProgramFileErrors struct {
	Problems []string
}

func (e *ProgramFileErrors) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return "файл программы не прошёл проверку"
	}
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	return fmt.Sprintf("файл программы содержит %d ошибок: %s", len(e.Problems), strings.Join(e.Problems, "; "))
}

// Details возвращает копию проблем для JSON-ответа 422.
func (e *ProgramFileErrors) Details() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.Problems...)
}

func newProgramFileErrors(problems ...string) error {
	filtered := make([]string, 0, len(problems))
	for _, problem := range problems {
		if strings.TrimSpace(problem) != "" {
			filtered = append(filtered, problem)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &ProgramFileErrors{Problems: filtered}
}

// ProgramSourceFormat возвращает метку формата для хранения в черновике.
// Значения совпадают с расширениями, которые понимает ParseProgramFile.
func ProgramSourceFormat(name string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".json":
		return "json"
	case ".txt", ".md":
		return "text"
	case ".csv":
		return "csv"
	case ".xlsx":
		return "xlsx"
	default:
		return strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(name))), ".")
	}
}

// ParseProgramFile разбирает программу из JSON, размеченного текста, CSV или
// XLSX. Возвращаются программа, предупреждения (например, KSA без родителя) и
// ошибка со списком проблем. Пустые строки игнорируются.
func ParseProgramFile(name string, r io.Reader) (EducationProgram, []string, error) {
	program := EducationProgram{}
	warnings := []string{}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
	switch ext {
	case ".json":
		parsed, err := ReadEducation(r)
		if err != nil {
			return program, warnings, newProgramFileErrors(err.Error())
		}
		return finalizeImportedProgram(parsed, false)
	case ".txt", ".md":
		data, err := readProgramFile(r)
		if err != nil {
			return program, warnings, err
		}
		parsed, err := ReadEducationText(string(data), programTitleFromFileName(name))
		if err != nil {
			return program, warnings, newProgramFileErrors(err.Error())
		}
		return finalizeImportedProgram(parsed, false)
	case ".csv":
		return parseProgramCSV(r)
	case ".xlsx":
		return parseProgramXLSX(r)
	case "":
		return program, warnings, newProgramFileErrors("не удалось определить формат файла: используйте .json, .txt, .md, .csv или .xlsx")
	default:
		return program, warnings, newProgramFileErrors(fmt.Sprintf("формат %q не поддерживается: сохраните программу как CSV или XLSX", strings.TrimPrefix(ext, ".")))
	}
}

func readProgramFile(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxProgramFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать файл программы: %w", err)
	}
	if len(data) > maxProgramFileSize {
		return nil, fmt.Errorf("файл программы больше %d МБ", maxProgramFileSize>>20)
	}
	return data, nil
}

func programTitleFromFileName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimSpace(base)
	if base == "" || base == "." {
		return "Учебная программа"
	}
	return base
}

func finalizeImportedProgram(program EducationProgram, requireCode bool) (EducationProgram, []string, error) {
	program = normalizeEducationProgram(program)
	warnings := missingTypeWarnings(program)
	if !requireCode && program.Code == "" {
		warnings = append(warnings, "программа: не указан code - при импорте будет использован key")
	}
	if problems := validateEducationProgram(program, requireCode); len(problems) > 0 {
		return program, warnings, &ProgramFileErrors{Problems: problems}
	}
	return program, warnings, nil
}

func normalizeEducationProgram(program EducationProgram) EducationProgram {
	program.Title = strings.TrimSpace(program.Title)
	program.Organization = strings.TrimSpace(program.Organization)
	program.Code = strings.TrimSpace(program.Code)
	for i := range program.Competencies {
		competency := &program.Competencies[i]
		competency.Code = strings.TrimSpace(competency.Code)
		competency.Title = strings.TrimSpace(competency.Title)
		if compType, ok := normalizeCompetencyType(competency.Type); ok {
			competency.Type = compType
		} else {
			competency.Type = strings.ToLower(strings.TrimSpace(competency.Type))
		}
		competency.TaxonomyCodes = normalizeTaxonomyCodes(competency.TaxonomyCodes)
		if competency.KSA == nil {
			competency.KSA = []ProgramKSA{}
		}
		for j := range competency.KSA {
			item := &competency.KSA[j]
			if kind, ok := canonicalKSAKind(item.Kind); ok {
				item.Kind = kind
			} else {
				item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
			}
			item.Title = strings.TrimSpace(item.Title)
			item.TaxonomyCodes = normalizeTaxonomyCodes(item.TaxonomyCodes)
		}
	}
	return program
}

// normalizeTaxonomyCodes обрезает пробелы, поддерживает значение из нескольких
// кодов через ";" и убирает пустые/повторяющиеся коды.
func normalizeTaxonomyCodes(codes []string) []string {
	out := make([]string, 0, len(codes))
	seen := make(map[string]bool, len(codes))
	for _, raw := range codes {
		for _, part := range strings.Split(raw, ";") {
			code := strings.TrimSpace(part)
			if code == "" || seen[code] {
				continue
			}
			seen[code] = true
			out = append(out, code)
		}
	}
	return out
}

func validateEducationProgram(program EducationProgram, requireCode bool) []string {
	problems := make([]string, 0, 4)
	if requireCode && program.Code == "" {
		problems = append(problems, "программа: обязателен code")
	}
	if program.Title == "" {
		problems = append(problems, "программа: обязательно название")
	}
	if len(program.Competencies) == 0 {
		problems = append(problems, "программа: нужна хотя бы одна компетенция")
	}
	seen := make(map[string]bool, len(program.Competencies))
	for i, competency := range program.Competencies {
		label := fmt.Sprintf("компетенция %d", i+1)
		if competency.Code != "" {
			label += fmt.Sprintf(" (%s)", competency.Code)
		}
		if competency.Code == "" {
			problems = append(problems, label+": обязателен code")
		}
		if competency.Title == "" {
			problems = append(problems, label+": обязательно название")
		}
		if _, ok := normalizeCompetencyType(competency.Type); !ok {
			problems = append(problems, label+": неизвестный type "+quoteValue(competency.Type))
		}
		if competency.Code != "" {
			if seen[competency.Code] {
				problems = append(problems, label+": повтор кода компетенции")
			}
			seen[competency.Code] = true
		}
		ksaSeen := make(map[string]bool, len(competency.KSA))
		for j, item := range competency.KSA {
			itemLabel := fmt.Sprintf("%s, KSA %d", label, j+1)
			if _, ok := canonicalKSAKind(item.Kind); !ok {
				problems = append(problems, itemLabel+": неизвестный вид "+item.Kind)
			}
			if item.Title == "" {
				problems = append(problems, itemLabel+": обязательно название")
				continue
			}
			key := item.Kind + "\x00" + item.Title
			if ksaSeen[key] {
				problems = append(problems, itemLabel+": повтор KSA")
			}
			ksaSeen[key] = true
		}
	}
	return problems
}

// canonicalKSAKind приводит английские и русские названия видов KSA к
// значениям, которые ожидает SaveEducation/ReadEducation.
func canonicalKSAKind(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "knowledge", "знание", "знания":
		return "knowledge", true
	case "skill", "умение", "умения":
		return "skill", true
	case "ability", "навык", "навыки", "способность":
		return "ability", true
	default:
		return "", false
	}
}

// normalizeCompetencyType переводит тип компетенции в каноническое значение
// колонки program_competencies.type. Пустая строка допустима.
func normalizeCompetencyType(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.Join(strings.Fields(normalized), "_")
	switch normalized {
	case "":
		return "", true
	case "universal", "ук", "универсальная", "универсальные", "универсальная_компетенция":
		return "universal", true
	case "general_professional", "generalprofessional", "опк", "общепрофессиональная", "общепрофессиональные", "общепрофессиональная_компетенция":
		return "general_professional", true
	case "professional", "пк", "профессиональная", "профессиональные", "профессиональная_компетенция":
		return "professional", true
	case "other", "иное", "другое", "прочие", "прочее":
		return "other", true
	default:
		return "", false
	}
}

func missingTypeWarnings(program EducationProgram) []string {
	warnings := make([]string, 0)
	for _, competency := range program.Competencies {
		if competency.Type == "" {
			warnings = append(warnings, fmt.Sprintf("компетенция %s: не указан type (УК/ОПК/ПК/иное)", quoteValue(competency.Code)))
		}
	}
	return warnings
}

// parseProgramCSV читает плоскую таблицу: шапка с колонками kind, code, title,
// parent_code, taxonomy_codes. Для строки kind=program организация берётся из
// необязательной колонки organization, а если её нет - из parent_code.
func parseProgramCSV(r io.Reader) (EducationProgram, []string, error) {
	program := EducationProgram{}
	warnings := []string{}
	data, err := readProgramFile(r)
	if err != nil {
		return program, warnings, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(data) {
		return program, warnings, newProgramFileErrors("CSV: файл должен быть в UTF-8")
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = detectCSVDelimiter(data)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return program, warnings, newProgramFileErrors("CSV: файл пуст")
	}
	if err != nil {
		return program, warnings, newProgramFileErrors("CSV: не удалось прочитать шапку: " + err.Error())
	}
	columns := mapProgramColumns(header)
	problems := make([]string, 0, 8)
	if missing := missingProgramColumns(columns, true); len(missing) > 0 {
		return program, warnings, newProgramFileErrors("CSV: в шапке нужны колонки " + strings.Join(missing, ", ") + "; получено: " + describeHeader(header))
	}

	type pendingKSA struct {
		line     int
		parent   string
		kind     string
		title    string
		taxonomy []string
	}
	pending := make([]pendingKSA, 0, 8)
	competencyIndex := map[string]int{}
	programLine := 0

	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			line := 0
			var parseErr *csv.ParseError
			if errors.As(readErr, &parseErr) {
				line = parseErr.Line
			}
			problems = append(problems, rowProblem(line, "CSV: "+readErr.Error()))
			break
		}
		line := 0
		if len(record) > 0 {
			line, _ = reader.FieldPos(0)
		}
		if blankRecord(record) {
			continue
		}
		kindRaw := csvField(record, columns.kind)
		rowKind, ksaKind := classifyProgramKind(kindRaw)
		switch rowKind {
		case "program":
			if programLine != 0 {
				problems = append(problems, rowProblem(line, fmt.Sprintf("повтор строки program: программа уже задана в строке %d", programLine)))
				continue
			}
			programLine = line
			program.Code = strings.TrimSpace(csvField(record, columns.code))
			program.Title = strings.TrimSpace(csvField(record, columns.title))
			organization := ""
			if columns.organization >= 0 {
				organization = csvField(record, columns.organization)
			}
			if strings.TrimSpace(organization) == "" {
				organization = csvField(record, columns.parent)
			}
			program.Organization = strings.TrimSpace(organization)
		case "competency":
			code := strings.TrimSpace(csvField(record, columns.code))
			title := strings.TrimSpace(csvField(record, columns.title))
			if code == "" {
				problems = append(problems, rowProblem(line, "у компетенции обязателен code"))
				continue
			}
			if title == "" {
				problems = append(problems, rowProblem(line, "у компетенции обязательно название"))
				continue
			}
			if _, exists := competencyIndex[code]; exists {
				problems = append(problems, rowProblem(line, "повтор кода компетенции "+quoteValue(code)))
				continue
			}
			compType, ok := normalizeCompetencyType(csvField(record, columns.competencyType))
			if !ok {
				problems = append(problems, rowProblem(line, "неизвестный type компетенции "+quoteValue(csvField(record, columns.competencyType))+": допустимы universal|general_professional|professional|other или УК|ОПК|ПК|иное"))
				continue
			}
			if compType == "" {
				warnings = append(warnings, rowProblem(line, fmt.Sprintf("у компетенции %s не указан type (УК/ОПК/ПК/иное)", quoteValue(code))))
			}
			competencyIndex[code] = len(program.Competencies)
			program.Competencies = append(program.Competencies, ProgramCompetency{
				Code:          code,
				Title:         title,
				Type:          compType,
				TaxonomyCodes: normalizeTaxonomyCodes([]string{csvField(record, columns.taxonomy)}),
			})
		case "ksa":
			title := strings.TrimSpace(csvField(record, columns.title))
			if title == "" {
				problems = append(problems, rowProblem(line, "у KSA обязательно название"))
				continue
			}
			if ksaKind == "" {
				ksaKind = resolveKSAKind(csvField(record, columns.ksaKind), csvField(record, columns.code))
			}
			if ksaKind == "" {
				problems = append(problems, rowProblem(line, "неизвестный вид KSA: ожидается knowledge|skill|ability или знание|умение|навык"))
				continue
			}
			pending = append(pending, pendingKSA{
				line:     line,
				parent:   strings.TrimSpace(csvField(record, columns.parent)),
				kind:     ksaKind,
				title:    title,
				taxonomy: normalizeTaxonomyCodes([]string{csvField(record, columns.taxonomy)}),
			})
		default:
			problems = append(problems, rowProblem(line, "неизвестный kind "+quoteValue(kindRaw)+": ожидается program, competency, ksa или вид KSA"))
		}
	}

	for _, item := range pending {
		index, ok := competencyIndex[item.parent]
		if !ok {
			warnings = append(warnings, rowProblem(item.line, fmt.Sprintf("KSA %s пропущена: компетенция %s не найдена", quoteValue(item.title), quoteValue(item.parent))))
			continue
		}
		competency := &program.Competencies[index]
		if hasKSA(competency.KSA, item.kind, item.title) {
			warnings = append(warnings, rowProblem(item.line, fmt.Sprintf("KSA %s уже добавлена, повтор пропущен", quoteValue(item.title))))
			continue
		}
		competency.KSA = append(competency.KSA, ProgramKSA{Kind: item.kind, Title: item.title, TaxonomyCodes: item.taxonomy})
	}

	if programLine == 0 {
		problems = append(problems, "нет строки kind=program")
	} else {
		if program.Code == "" {
			problems = append(problems, rowProblem(programLine, "у программы обязателен code"))
		}
		if program.Title == "" {
			problems = append(problems, rowProblem(programLine, "у программы обязательно название"))
		}
	}
	if len(program.Competencies) == 0 {
		problems = append(problems, "нет ни одной строки kind=competency")
	}
	if len(problems) > 0 {
		return program, warnings, &ProgramFileErrors{Problems: problems}
	}
	return normalizeEducationProgram(program), warnings, nil
}

func parseProgramXLSX(r io.Reader) (EducationProgram, []string, error) {
	program := EducationProgram{}
	warnings := []string{}
	data, err := readProgramFile(r)
	if err != nil {
		return program, warnings, err
	}
	file, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return program, warnings, newProgramFileErrors("XLSX: не удалось открыть файл: " + err.Error())
	}
	defer file.Close()

	programSheet, okProgram := findSheet(file.GetSheetList(), "Программа", "Program", "Programs")
	competenciesSheet, okCompetencies := findSheet(file.GetSheetList(), "Компетенции", "Competencies", "Competences")
	ksaSheet, okKSA := findSheet(file.GetSheetList(), "KSA", "Знания, умения и навыки", "ЗУН")
	if !okProgram || !okCompetencies || !okKSA {
		missing := make([]string, 0, 3)
		if !okProgram {
			missing = append(missing, "«Программа»")
		}
		if !okCompetencies {
			missing = append(missing, "«Компетенции»")
		}
		if !okKSA {
			missing = append(missing, "«KSA»")
		}
		return program, warnings, newProgramFileErrors("XLSX: не найдены листы " + strings.Join(missing, ", ") + "; нужны листы «Программа», «Компетенции» и «KSA»")
	}

	problems := make([]string, 0, 8)
	programRows, err := file.GetRows(programSheet)
	if err != nil {
		return program, warnings, newProgramFileErrors(sheetProblem("Программа", 0, "не удалось прочитать лист: "+err.Error()))
	}
	programLine := 0
	if headerRow := firstNonBlankRow(programRows); headerRow >= 0 {
		header := programRows[headerRow]
		codeColumn := findColumn(header, "code", "код")
		titleColumn := findColumn(header, "title", "название", "наименование")
		organizationColumn := findColumn(header, "organization", "organisation", "организация", "вуз", "университет")
		if codeColumn < 0 || titleColumn < 0 {
			problems = append(problems, sheetProblem("Программа", headerRow+1, "в шапке нужны колонки code и title; получено: "+describeHeader(header)))
		} else {
			for i := headerRow + 1; i < len(programRows); i++ {
				row := programRows[i]
				if blankRecord(row) {
					continue
				}
				if programLine != 0 {
					problems = append(problems, sheetProblem("Программа", i+1, fmt.Sprintf("ожидается одна строка программы; лишняя строка после %d", programLine)))
					continue
				}
				programLine = i + 1
				program.Code = strings.TrimSpace(cell(row, codeColumn))
				program.Title = strings.TrimSpace(cell(row, titleColumn))
				program.Organization = strings.TrimSpace(cell(row, organizationColumn))
			}
		}
	}
	if programLine == 0 {
		problems = append(problems, "XLSX: на листе «Программа» нет строки программы")
	} else {
		if program.Code == "" {
			problems = append(problems, sheetProblem("Программа", programLine, "у программы обязателен code"))
		}
		if program.Title == "" {
			problems = append(problems, sheetProblem("Программа", programLine, "у программы обязательно название"))
		}
	}

	competencyRows, err := file.GetRows(competenciesSheet)
	if err != nil {
		return program, warnings, newProgramFileErrors(sheetProblem("Компетенции", 0, "не удалось прочитать лист: "+err.Error()))
	}
	competencyIndex := map[string]int{}
	headerRow := firstNonBlankRow(competencyRows)
	if headerRow < 0 {
		problems = append(problems, "XLSX: лист «Компетенции» пуст")
	} else {
		header := competencyRows[headerRow]
		codeColumn := findColumn(header, "code", "код")
		titleColumn := findColumn(header, "title", "название", "наименование")
		taxonomyColumn := findColumn(header, "taxonomy_codes", "taxonomy", "таксономия", "коды_таксономии")
		typeColumn := findColumn(header, "type", "тип", "вид_компетенции", "категория")
		if codeColumn < 0 || titleColumn < 0 {
			problems = append(problems, sheetProblem("Компетенции", headerRow+1, "в шапке нужны колонки code и title; получено: "+describeHeader(header)))
		} else {
			for i := headerRow + 1; i < len(competencyRows); i++ {
				row := competencyRows[i]
				if blankRecord(row) {
					continue
				}
				line := i + 1
				code := strings.TrimSpace(cell(row, codeColumn))
				title := strings.TrimSpace(cell(row, titleColumn))
				if code == "" {
					problems = append(problems, sheetProblem("Компетенции", line, "у компетенции обязателен code"))
					continue
				}
				if title == "" {
					problems = append(problems, sheetProblem("Компетенции", line, "у компетенции обязательно название"))
					continue
				}
				if _, exists := competencyIndex[code]; exists {
					problems = append(problems, sheetProblem("Компетенции", line, "повтор кода компетенции "+quoteValue(code)))
					continue
				}
				compType, ok := normalizeCompetencyType(cell(row, typeColumn))
				if !ok {
					problems = append(problems, sheetProblem("Компетенции", line, "неизвестный type компетенции "+quoteValue(cell(row, typeColumn))+": допустимы universal|general_professional|professional|other или УК|ОПК|ПК|иное"))
					continue
				}
				if compType == "" {
					warnings = append(warnings, sheetProblem("Компетенции", line, fmt.Sprintf("у компетенции %s не указан type (УК/ОПК/ПК/иное)", quoteValue(code))))
				}
				competencyIndex[code] = len(program.Competencies)
				program.Competencies = append(program.Competencies, ProgramCompetency{
					Code:          code,
					Title:         title,
					Type:          compType,
					TaxonomyCodes: normalizeTaxonomyCodes([]string{cell(row, taxonomyColumn)}),
				})
			}
		}
	}
	if len(program.Competencies) == 0 {
		problems = append(problems, "XLSX: нет ни одной компетенции")
	}

	ksaRows, err := file.GetRows(ksaSheet)
	if err != nil {
		return program, warnings, newProgramFileErrors(sheetProblem("KSA", 0, "не удалось прочитать лист: "+err.Error()))
	}
	headerRow = firstNonBlankRow(ksaRows)
	if headerRow >= 0 {
		header := ksaRows[headerRow]
		kindColumn := findColumn(header, "kind", "вид", "тип", "ksa_kind", "вид_знания")
		titleColumn := findColumn(header, "title", "название", "наименование")
		parentColumn := findColumn(header, "parent_code", "parent", "родитель", "код_компетенции", "компетенция")
		taxonomyColumn := findColumn(header, "taxonomy_codes", "taxonomy", "таксономия", "коды_таксономии")
		codeColumn := findColumn(header, "code", "код")
		if kindColumn < 0 || titleColumn < 0 || parentColumn < 0 {
			problems = append(problems, sheetProblem("KSA", headerRow+1, "в шапке нужны колонки kind, title и parent_code; получено: "+describeHeader(header)))
		} else {
			type pendingKSA struct {
				line     int
				parent   string
				kind     string
				title    string
				taxonomy []string
			}
			pending := make([]pendingKSA, 0, 8)
			for i := headerRow + 1; i < len(ksaRows); i++ {
				row := ksaRows[i]
				if blankRecord(row) {
					continue
				}
				line := i + 1
				title := strings.TrimSpace(cell(row, titleColumn))
				if title == "" {
					problems = append(problems, sheetProblem("KSA", line, "у KSA обязательно название"))
					continue
				}
				kind := resolveKSAKind(cell(row, kindColumn), cell(row, codeColumn))
				if kind == "" {
					problems = append(problems, sheetProblem("KSA", line, "неизвестный вид KSA: ожидается knowledge|skill|ability или знание|умение|навык"))
					continue
				}
				pending = append(pending, pendingKSA{
					line:     line,
					parent:   strings.TrimSpace(cell(row, parentColumn)),
					kind:     kind,
					title:    title,
					taxonomy: normalizeTaxonomyCodes([]string{cell(row, taxonomyColumn)}),
				})
			}
			for _, item := range pending {
				index, ok := competencyIndex[item.parent]
				if !ok {
					warnings = append(warnings, sheetProblem("KSA", item.line, fmt.Sprintf("KSA %s пропущена: компетенция %s не найдена", quoteValue(item.title), quoteValue(item.parent))))
					continue
				}
				competency := &program.Competencies[index]
				if hasKSA(competency.KSA, item.kind, item.title) {
					warnings = append(warnings, sheetProblem("KSA", item.line, fmt.Sprintf("KSA %s уже добавлена, повтор пропущен", quoteValue(item.title))))
					continue
				}
				competency.KSA = append(competency.KSA, ProgramKSA{Kind: item.kind, Title: item.title, TaxonomyCodes: item.taxonomy})
			}
		}
	}

	if len(problems) > 0 {
		return program, warnings, &ProgramFileErrors{Problems: problems}
	}
	return normalizeEducationProgram(program), warnings, nil
}

// programColumns хранит индексы колонок CSV; -1 означает, что колонки нет.
type programColumns struct {
	kind           int
	code           int
	title          int
	parent         int
	taxonomy       int
	organization   int
	ksaKind        int
	competencyType int
}

func mapProgramColumns(header []string) programColumns {
	return programColumns{
		kind:           findColumn(header, "kind", "вид", "тип"),
		code:           findColumn(header, "code", "код"),
		title:          findColumn(header, "title", "название", "наименование"),
		parent:         findColumn(header, "parent_code", "parent", "parentcode", "родитель", "код_компетенции", "компетенция"),
		taxonomy:       findColumn(header, "taxonomy_codes", "taxonomy", "таксономия", "коды_таксономии"),
		organization:   findColumn(header, "organization", "organisation", "организация", "вуз", "университет"),
		ksaKind:        findColumn(header, "ksa_kind", "knowledge_kind", "вид_знания", "подвид"),
		competencyType: findColumn(header, "type", "тип_компетенции", "вид_компетенции", "категория"),
	}
}

func missingProgramColumns(columns programColumns, withKind bool) []string {
	missing := make([]string, 0, 3)
	if withKind && columns.kind < 0 {
		missing = append(missing, "kind")
	}
	if columns.code < 0 {
		missing = append(missing, "code")
	}
	if columns.title < 0 {
		missing = append(missing, "title")
	}
	return missing
}

func classifyProgramKind(raw string) (rowKind, ksaKind string) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "program", "программа":
		return "program", ""
	case "competency", "competence", "компетенция":
		return "competency", ""
	case "ksa", "зун":
		return "ksa", ""
	default:
		if kind, ok := canonicalKSAKind(raw); ok {
			return "ksa", kind
		}
		return "", ""
	}
}

func resolveKSAKind(values ...string) string {
	for _, value := range values {
		if kind, ok := canonicalKSAKind(value); ok {
			return kind
		}
	}
	return ""
}

func hasKSA(items []ProgramKSA, kind, title string) bool {
	for _, item := range items {
		if item.Kind == kind && item.Title == title {
			return true
		}
	}
	return false
}

func detectCSVDelimiter(data []byte) rune {
	firstLine := data
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		firstLine = data[:index]
	}
	semicolons := countOutsideQuotes(firstLine, ';')
	commas := countOutsideQuotes(firstLine, ',')
	if semicolons == 0 && commas == 0 {
		return ';'
	}
	if semicolons >= commas {
		return ';'
	}
	return ','
}

func countOutsideQuotes(line []byte, target byte) int {
	count := 0
	quoted := false
	for _, char := range line {
		switch char {
		case '"':
			quoted = !quoted
		case target:
			if !quoted {
				count++
			}
		}
	}
	return count
}

func normalizeHeader(value string) string {
	value = strings.TrimPrefix(value, "\ufeff")
	value = strings.ReplaceAll(value, "\u00a0", " ")
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), "_")
}

func findColumn(header []string, aliases ...string) int {
	normalized := make(map[string]int, len(header))
	for index, value := range header {
		name := normalizeHeader(value)
		if name == "" {
			continue
		}
		if _, exists := normalized[name]; !exists {
			normalized[name] = index
		}
	}
	for _, alias := range aliases {
		if index, ok := normalized[normalizeHeader(alias)]; ok {
			return index
		}
	}
	return -1
}

func describeHeader(header []string) string {
	parts := make([]string, 0, len(header))
	for _, value := range header {
		if name := strings.TrimSpace(value); name != "" {
			parts = append(parts, quoteValue(name))
		}
	}
	if len(parts) == 0 {
		return "пустая шапка"
	}
	return strings.Join(parts, ", ")
}

func csvField(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return record[index]
}

func cell(row []string, index int) string {
	return csvField(row, index)
}

func blankRecord(record []string) bool {
	for _, value := range record {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func rowProblem(line int, message string) string {
	if line > 0 {
		return fmt.Sprintf("строка %d: %s", line, message)
	}
	return message
}

func sheetProblem(sheet string, line int, message string) string {
	if line > 0 {
		return fmt.Sprintf("лист «%s», строка %d: %s", sheet, line, message)
	}
	return fmt.Sprintf("лист «%s»: %s", sheet, message)
}

func quoteValue(value string) string {
	return "\"" + strings.TrimSpace(value) + "\""
}

func findSheet(sheets []string, aliases ...string) (string, bool) {
	for _, sheet := range sheets {
		name := strings.TrimSpace(sheet)
		for _, alias := range aliases {
			if strings.EqualFold(name, alias) {
				return sheet, true
			}
		}
	}
	return "", false
}

func firstNonBlankRow(rows [][]string) int {
	for index, row := range rows {
		if !blankRecord(row) {
			return index
		}
	}
	return -1
}
