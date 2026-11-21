package fgos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/validate"
)

// competencyCodePattern — допустимый код компетенции ФГОС: две–четыре
// заглавные русские буквы, дефис, номер (УК-1, ОПК-2, ПК-3).
var competencyCodePattern = regexp.MustCompile(`^[А-ЯЁ]{2,4}-\d+$`)

// maxWarnings ограничивает список предупреждений, чтобы черновик не разросся
// на документе, где модель вернула сотни невалидных пунктов.
const maxWarnings = 200

// chunk — часть документа, отправляемая модели одним запросом. Страница никогда
// не разрывается между чанками: цитата всегда проверяется по целой странице.
type chunk struct {
	pages []Page
	chars int
}

// rawResponse — ответ модели до валидации.
type rawResponse struct {
	Competencies []rawCompetency `json:"competencies"`
}

type rawCompetency struct {
	Code      string        `json:"code"`
	Title     string        `json:"title"`
	Type      string        `json:"type"`
	KSA       []rawKSA      `json:"ksa"`
	Citations []rawCitation `json:"citations"`
}

type rawKSA struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

type rawCitation struct {
	Page  int    `json:"page"`
	Quote string `json:"quote"`
}

// Extract извлекает компетенции и KSA из страниц документа.
//
// Возвращаются программа (без title — его заполняет вызывающий), предупреждения
// о каждом отброшенном пункте и ошибка. Если ни одного валидного пункта не
// осталось, возвращается ErrNoCompetencies. Превышение MaxPages и Timeout —
// ошибки с понятным текстом.
func (e *Extractor) Extract(ctx context.Context, pages []Page) (ingestion.EducationProgram, []string, error) {
	program := ingestion.EducationProgram{}
	warnings := []string{}
	if e == nil || e.Client == nil {
		return program, warnings, ErrNoClient
	}
	if ctx == nil {
		ctx = context.Background()
	}

	filled := make([]Page, 0, len(pages))
	for _, page := range pages {
		if strings.TrimSpace(page.Text) == "" {
			continue
		}
		filled = append(filled, page)
	}
	if len(filled) == 0 {
		return program, warnings, fmt.Errorf("%w: страницы не содержат текста", ErrNoTextLayer)
	}
	if limit := e.maxPages(); len(filled) > limit {
		return program, warnings, fmt.Errorf(
			"fgos: документ содержит %d страниц, лимит %d: разделите документ или увеличьте MaxPages",
			len(filled), limit)
	}
	system, err := SystemPrompt()
	if err != nil {
		return program, warnings, err
	}

	timeout := e.timeout()
	extractCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	indexByCode := make(map[string]int)
	for index, part := range chunkPages(filled, e.maxCharsPerChunk()) {
		if ctxErr := extractCtx.Err(); ctxErr != nil {
			return program, warnings, contextError(ctxErr, timeout)
		}
		answer, callErr := e.Client.Chat(extractCtx, []llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: userPrompt(part)},
		})
		if callErr != nil {
			if ctxErr := extractCtx.Err(); ctxErr != nil {
				return program, warnings, contextError(ctxErr, timeout)
			}
			return program, warnings, fmt.Errorf("fgos: LLM не вернул ответ для фрагмента %d: %w", index+1, callErr)
		}
		response, parseErr := parseResponse(answer)
		if parseErr != nil {
			warnings = appendWarning(warnings, fmt.Sprintf("фрагмент %d: %v", index+1, parseErr))
			continue
		}
		for _, raw := range response.Competencies {
			competency, itemWarnings, ok := validateCompetency(raw, part.pages)
			for _, warning := range itemWarnings {
				warnings = appendWarning(warnings, fmt.Sprintf("фрагмент %d: %s", index+1, warning))
			}
			if !ok {
				continue
			}
			mergeCompetency(&program, indexByCode, competency, &warnings)
		}
	}
	if len(program.Competencies) == 0 {
		return program, warnings, ErrNoCompetencies
	}
	return program, warnings, nil
}

// chunkPages режет страницы на чанки по limit символов, не разрывая страницу.
// Страница длиннее лимита становится отдельным чанком.
func chunkPages(pages []Page, limit int) []chunk {
	if limit <= 0 {
		limit = DefaultMaxCharsPerChunk
	}
	result := make([]chunk, 0, len(pages))
	current := chunk{}
	for _, page := range pages {
		pageChars := len([]rune(page.Text))
		if len(current.pages) > 0 && current.chars+pageChars > limit {
			result = append(result, current)
			current = chunk{}
		}
		current.pages = append(current.pages, page)
		current.chars += pageChars
	}
	if len(current.pages) > 0 {
		result = append(result, current)
	}
	return result
}

// userPrompt собирает запрос по одному чанку: страницы с номерами и схема
// ответа. Текст страницы идёт целиком, без пересказа.
func userPrompt(part chunk) string {
	var builder strings.Builder
	builder.WriteString("Фрагмент ФГОС. Страницы: ")
	builder.WriteString(pageRange(part.pages))
	builder.WriteString(".\n")
	builder.WriteString("Извлекай компетенции только из текста ниже. page в citations обязан быть одной из этих страниц.\n\n")
	for _, page := range part.pages {
		fmt.Fprintf(&builder, "=== Страница %d ===\n%s\n\n", page.Number, page.Text)
	}
	builder.WriteString(`Верни JSON ровно по схеме: {"competencies":[{"code":"УК-1","title":"...","type":"universal",` +
		`"ksa":[{"kind":"knowledge","title":"..."}],"citations":[{"page":3,"quote":"дословный фрагмент"}]}]}`)
	return builder.String()
}

func pageRange(pages []Page) string {
	if len(pages) == 0 {
		return "—"
	}
	if len(pages) == 1 {
		return strconv.Itoa(pages[0].Number)
	}
	contiguous := true
	for index := 1; index < len(pages); index++ {
		if pages[index].Number != pages[index-1].Number+1 {
			contiguous = false
			break
		}
	}
	if contiguous {
		return fmt.Sprintf("%d–%d", pages[0].Number, pages[len(pages)-1].Number)
	}
	numbers := make([]string, len(pages))
	for index, page := range pages {
		numbers[index] = strconv.Itoa(page.Number)
	}
	return strings.Join(numbers, ", ")
}

// parseResponse извлекает JSON-объект из ответа модели, даже если тот обёрнут в
// markdown-блок, и разбирает его по схеме.
func parseResponse(answer string) (rawResponse, error) {
	object, err := firstJSONObject(answer)
	if err != nil {
		return rawResponse{}, err
	}
	var response rawResponse
	if err := json.Unmarshal([]byte(object), &response); err != nil {
		return rawResponse{}, fmt.Errorf("ответ модели не соответствует JSON-схеме: %v", err)
	}
	return response, nil
}

// firstJSONObject возвращает первый сбалансированный JSON-объект строки. Скобки
// внутри строковых значений не учитываются, поэтому цитаты не ломают разбор.
func firstJSONObject(answer string) (string, error) {
	start := strings.Index(answer, "{")
	if start < 0 {
		return "", errors.New("в ответе модели нет JSON-объекта")
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(answer); index++ {
		char := answer[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return answer[start : index+1], nil
			}
		}
	}
	return "", errors.New("в ответе модели незавершённый JSON-объект")
}

// validateCompetency проверяет один пункт ответа. Невалидный пункт
// отбрасывается, а причина попадает в предупреждения.
func validateCompetency(raw rawCompetency, pages []Page) (ingestion.ProgramCompetency, []string, bool) {
	competency := ingestion.ProgramCompetency{}
	warnings := []string{}
	code := strings.TrimSpace(raw.Code)
	if !competencyCodePattern.MatchString(code) {
		return competency, append(warnings, fmt.Sprintf(
			"компетенция с кодом %q отброшена: код должен соответствовать шаблону УК-1/ОПК-2/ПК-3", code)), false
	}
	title := strings.TrimSpace(raw.Title)
	if title == "" {
		return competency, append(warnings, fmt.Sprintf("компетенция %s отброшена: пустой title", code)), false
	}
	competencyType, ok := normalizeCompetencyType(raw.Type)
	if !ok {
		return competency, append(warnings, fmt.Sprintf(
			"компетенция %s отброшена: неизвестный type %q", code, strings.TrimSpace(raw.Type))), false
	}
	competency = ingestion.ProgramCompetency{Code: code, Title: title, Type: competencyType}
	for _, item := range raw.KSA {
		kind, kindOK := normalizeKSAKind(item.Kind)
		itemTitle := strings.TrimSpace(item.Title)
		if !kindOK {
			warnings = append(warnings, fmt.Sprintf("компетенция %s: KSA с видом %q отброшено",
				code, strings.TrimSpace(item.Kind)))
			continue
		}
		if itemTitle == "" {
			warnings = append(warnings, fmt.Sprintf("компетенция %s: KSA без title отброшено", code))
			continue
		}
		competency.KSA = append(competency.KSA, ingestion.ProgramKSA{Kind: kind, Title: itemTitle})
	}
	citations := 0
	for _, citation := range raw.Citations {
		page, found := findPage(pages, citation.Page)
		if !found {
			warnings = append(warnings, fmt.Sprintf(
				"компетенция %s: цитата отброшена — страница %d вне фрагмента", code, citation.Page))
			continue
		}
		if !validate.ContainsQuote(page.Text, citation.Quote) {
			warnings = append(warnings, fmt.Sprintf(
				"компетенция %s: цитата отброшена — не найдена дословно на странице %d", code, citation.Page))
			continue
		}
		citations++
	}
	if citations == 0 {
		return competency, append(warnings, fmt.Sprintf(
			"компетенция %s отброшена: нет ни одной дословной цитаты на указанной странице", code)), false
	}
	return competency, warnings, true
}

func findPage(pages []Page, number int) (Page, bool) {
	for _, page := range pages {
		if page.Number == number {
			return page, true
		}
	}
	return Page{}, false
}

// mergeCompetency дедуплицирует компетенции по коду: KSA объединяются, при
// конфликте формулировок остаётся первая и добавляется предупреждение.
func mergeCompetency(program *ingestion.EducationProgram, indexByCode map[string]int, incoming ingestion.ProgramCompetency, warnings *[]string) {
	index, exists := indexByCode[incoming.Code]
	if !exists {
		indexByCode[incoming.Code] = len(program.Competencies)
		program.Competencies = append(program.Competencies, incoming)
		return
	}
	existing := &program.Competencies[index]
	if existing.Title != incoming.Title {
		*warnings = appendWarning(*warnings, fmt.Sprintf(
			"компетенция %s: формулировка %q отличается от %q; оставлена первая",
			incoming.Code, incoming.Title, existing.Title))
	}
	if existing.Type != incoming.Type {
		*warnings = appendWarning(*warnings, fmt.Sprintf(
			"компетенция %s: тип %q отличается от %q; оставлен первый",
			incoming.Code, incoming.Type, existing.Type))
	}
	seen := make(map[string]bool, len(existing.KSA)+len(incoming.KSA))
	for _, item := range existing.KSA {
		seen[item.Kind+"\x00"+item.Title] = true
	}
	for _, item := range incoming.KSA {
		key := item.Kind + "\x00" + item.Title
		if seen[key] {
			continue
		}
		seen[key] = true
		existing.KSA = append(existing.KSA, item)
	}
}

func appendWarning(warnings []string, warning string) []string {
	if len(warnings) >= maxWarnings {
		return warnings
	}
	return append(warnings, warning)
}

// contextError превращает отмену контекста в понятную ошибку адаптера.
func contextError(ctxErr error, timeout time.Duration) error {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return fmt.Errorf("%w (%s): %w", ErrTimeout, timeout, ctxErr)
	}
	return fmt.Errorf("fgos: извлечение прервано: %w", ctxErr)
}

// normalizeCompetencyType переводит тип компетенции в каноническое значение
// колонки program_competencies.type. Пустой и неизвестный тип недопустимы.
func normalizeCompetencyType(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.Join(strings.Fields(normalized), "_")
	switch normalized {
	case "universal", "ук", "универсальная", "универсальные":
		return "universal", true
	case "general_professional", "generalprofessional", "опк",
		"общепрофессиональная", "общепрофессиональные":
		return "general_professional", true
	case "professional", "пк", "профессиональная", "профессиональные":
		return "professional", true
	case "other", "иное", "другое", "прочие", "прочее":
		return "other", true
	default:
		return "", false
	}
}

// normalizeKSAKind переводит вид KSA в каноническое значение knowledge/skill/
// ability. Русские названия из документа тоже принимаются.
func normalizeKSAKind(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.Join(strings.Fields(normalized), "_")
	switch normalized {
	case "knowledge", "знание", "знания":
		return "knowledge", true
	case "skill", "умение", "умения":
		return "skill", true
	case "ability", "навык", "навыки", "владение":
		return "ability", true
	default:
		return "", false
	}
}
