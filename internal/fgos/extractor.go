package fgos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/validate"
)

// competencyCodePattern — допустимый код компетенции ФГОС: две–четыре
// заглавные русские буквы, дефис, номер (УК-1, ОПК-2, ПК-3).
var competencyCodePattern = regexp.MustCompile(`^[А-ЯЁ]{2,4}-\d+$`)

const (
	// maxSplitDepth — не более четырёх уровней деления чанка: 1 запрос может
	// превратиться в 1+2+4+8+16 = 31 при полном отказе модели.
	maxSplitDepth = 4
	// minSplitChars — меньше этого чанк не делим: ответ на такой запрос уже
	// небольшой, а стоимость растёт.
	minSplitChars = 1500
	// maxWarnings ограничивает список предупреждений, чтобы черновик не разросся
	// на документе, где модель вернула сотни невалидных пунктов.
	maxWarnings = 200
	// responseCharsPerToken — грубая оценка: сколько символов ответа приходится
	// на один токен при локальной проверке размера ответа.
	responseCharsPerToken = 4
)

// promptSegment — часть страницы, которую реально видит модель. При аварийном
// делении страница может попасть в два сегмента с одним номером; для проверки
// цитат используется полный текст страницы из chunk.pages.
type promptSegment struct {
	number int
	text   string
}

// chunk — часть документа, отправляемая модели одним запросом. Страница
// никогда не разрывается на этапе базового чанкинга; при аварийном делении
// текст режется по символам, но полные страницы сохраняются в pages.
type chunk struct {
	pages    []Page
	segments []promptSegment
	chars    int
}

// extractionState — общий результат разбора документа: программа, карта
// дедупликации, предупреждения и причина последней неудачи. Обновляется только
// из главного потока, поэтому гонок нет даже при параллельном разборе чанков.
type extractionState struct {
	program     *ingestion.EducationProgram
	indexByCode map[string]int
	warnings    []string
	warningSet  map[string]bool
	lastReason  string
}

func newExtractionState(program *ingestion.EducationProgram) *extractionState {
	return &extractionState{
		program:     program,
		indexByCode: make(map[string]int),
		warnings:    []string{},
		warningSet:  make(map[string]bool),
	}
}

func (s *extractionState) addWarning(warning string) {
	if warning == "" || s.warningSet[warning] {
		return
	}
	if len(s.warnings) >= maxWarnings {
		return
	}
	s.warningSet[warning] = true
	s.warnings = append(s.warnings, warning)
}

// apply переносит результат одного чанка в общий state в порядке фрагментов:
// так предупреждения, дедуп и причина последней неудачи остаются
// детерминированными при любом порядке завершения параллельных воркеров.
func (s *extractionState) apply(outcome *chunkOutcome) {
	if s == nil || outcome == nil {
		return
	}
	for _, warning := range outcome.warnings {
		s.addWarning(warning)
	}
	for _, competency := range outcome.competencies {
		for _, conflict := range mergeCompetency(s.program, s.indexByCode, competency) {
			s.addWarning(fmt.Sprintf("фрагмент %d: %s", outcome.fragment+1, conflict))
		}
	}
	if outcome.lastReason != "" {
		s.lastReason = outcome.lastReason
	}
}

// chunkOutcome — локальный результат одного чанка. Его пишет ровно один
// воркер, поэтому поля не нуждаются в блокировке; слияние выполняет
// extractionState.apply после завершения пула.
type chunkOutcome struct {
	fragment     int
	competencies []ingestion.ProgramCompetency
	warnings     []string
	warningSet   map[string]bool
	lastReason   string
}

func (o *chunkOutcome) addWarning(warning string) {
	if warning == "" {
		return
	}
	if o.warningSet == nil {
		o.warningSet = make(map[string]bool)
	}
	if o.warningSet[warning] {
		return
	}
	o.warningSet[warning] = true
	o.warnings = append(o.warnings, warning)
}

func (o *chunkOutcome) fail(reason string) {
	if reason != "" {
		o.lastReason = reason
	}
}

// failureError возвращает ошибку «ничего не извлеклось» с причиной последней
// неудачи: администратор должен видеть 413 или невалидный JSON, а не общий
// текст.
func (s *extractionState) failureError() error {
	if s == nil || s.lastReason == "" {
		return ErrNoCompetencies
	}
	return fmt.Errorf("%w: последняя ошибка — %s", ErrNoCompetencies, s.lastReason)
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
// Если включён SectionOnly (по умолчанию; env FGOS_SECTION_ONLY), сначала
// отбираются только страницы раздела компетенций с окном SectionWindow
// (env FGOS_SECTION_WINDOW). MaxPages проверяется по отобранным страницам:
// общие положения не должны упираться в лимит, если модель их не увидит.
//
// Возвращаются программа (без title — его заполняет вызывающий), предупреждения
// о каждом отброшенном пункте и ошибка. Если ни одного валидного пункта не
// осталось, ошибка оборачивает ErrNoCompetencies и содержит причину последней
// неудачи. Превышение MaxPages и Timeout — отдельные ошибки с понятным текстом.
//
// При отказе модели (413/context length/таймаут) или невалидном/пустом JSON
// чанк автоматически делится пополам (не более четырёх уровней и не меньше
// 1500 символов), результаты подзапросов объединяются.
func (e *Extractor) Extract(ctx context.Context, pages []Page) (ingestion.EducationProgram, []string, error) {
	program := ingestion.EducationProgram{}
	if e == nil || e.Client == nil {
		return program, []string{}, ErrNoClient
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
		return program, []string{}, fmt.Errorf("%w: страницы не содержат текста", ErrNoTextLayer)
	}
	// Предупреждения об OCR добавляются до разбора: они относятся к документу,
	// а не к конкретному чанку, и должны попасть в черновик даже при ошибке
	// лимита страниц.
	state := newExtractionState(&program)
	for _, page := range filled {
		if page.OCR {
			state.addWarning(OCRPageWarning(page.Number))
		}
	}

	// Отбор раздела компетенций выполняется до чанкования и до проверки
	// MaxPages: страницы общих положений не должны упираться в лимит, если
	// модель их всё равно не увидит.
	selected := filled
	sectionOnly := e.sectionOnly()
	window := e.sectionWindow()
	if sectionOnly {
		selectionWarnings := []string{}
		selected, selectionWarnings = SelectCompetencyPagesWithOptions(filled, SelectOptions{Window: window})
		for _, warning := range selectionWarnings {
			state.addWarning(warning)
		}
		if len(selected) < len(filled) {
			state.addWarning(SectionProcessedWarning(len(selected), len(filled)))
		}
	}
	e.logger().Info("fgos_section_selected",
		"pages_total", len(filled),
		"pages_selected", len(selected),
		"window", window,
		"section_only", sectionOnly)

	if limit := e.maxPages(); len(selected) > limit {
		return program, state.warnings, fmt.Errorf(
			"fgos: документ содержит %d страниц, лимит %d: разделите документ или увеличьте MaxPages",
			len(selected), limit)
	}
	system, err := SystemPrompt()
	if err != nil {
		return program, state.warnings, err
	}

	timeout := e.timeout()
	extractCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	e.applyResponseTokenLimit()

	chunks := chunkPages(selected, e.maxCharsPerChunk())
	concurrency := e.concurrency()
	if concurrency > len(chunks) {
		concurrency = len(chunks)
	}
	e.logger().Info("fgos_extract_cycle",
		"pages", len(selected),
		"pages_total", len(filled),
		"chunks", len(chunks),
		"concurrency", concurrency,
		"max_chars_per_chunk", e.maxCharsPerChunk())

	if concurrency <= 1 {
		// Последовательный режим сохранён для concurrency=1 и для документов из
		// одного чанка: поведение и порядок предупреждений прежние.
		for index, part := range chunks {
			if ctxErr := extractCtx.Err(); ctxErr != nil {
				return program, state.warnings, timeoutError(ctxErr, timeout, state.lastReason)
			}
			outcome := chunkOutcome{fragment: index}
			e.extractChunk(extractCtx, system, part, 0, index, &outcome)
			state.apply(&outcome)
		}
	} else {
		e.extractChunksParallel(extractCtx, system, chunks, concurrency, state)
	}

	if ctxErr := extractCtx.Err(); ctxErr != nil {
		return program, state.warnings, timeoutError(ctxErr, timeout, state.lastReason)
	}
	if len(program.Competencies) == 0 {
		return program, state.warnings, state.failureError()
	}
	return program, state.warnings, nil
}

// extractChunksParallel разбирает чанки пулом воркеров. Каждый чанк пишет
// результат в свою ячейку outcomes, поэтому порядок страниц, дедуп и
// предупреждения детерминированы независимо от порядка завершения запросов.
// Аварийное деление чанка остаётся последовательным внутри его воркера и не
// блокирует остальные чанки.
func (e *Extractor) extractChunksParallel(ctx context.Context, system string, chunks []chunk, concurrency int, state *extractionState) {
	outcomes := make([]chunkOutcome, len(chunks))
	sem := make(chan struct{}, concurrency)
	var wait sync.WaitGroup
launch:
	for index := range chunks {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break launch
		}
		outcomes[index].fragment = index
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			defer func() { <-sem }()
			e.extractChunk(ctx, system, chunks[index], 0, index, &outcomes[index])
		}(index)
	}
	wait.Wait()
	for index := range outcomes {
		state.apply(&outcomes[index])
	}
}

// extractChunk пытается разобрать один чанк. При ошибке модели или невалидном
// ответе чанк делится пополам и разбор повторяется рекурсивно; результаты
// складываются в локальный outcome, который затем применяется к общему state в
// порядке фрагментов.
func (e *Extractor) extractChunk(ctx context.Context, system string, part chunk, depth, fragment int, outcome *chunkOutcome) {
	if ctx.Err() != nil {
		return
	}
	e.logger().Debug("fgos_chunk_request",
		"fragment", fragment+1,
		"depth", depth,
		"chunk_chars", part.chars,
		"concurrency", e.concurrency())

	answer, callErr := e.Client.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: userPrompt(part)},
	})
	if callErr != nil {
		if ctx.Err() != nil {
			// Истёк общий бюджет разбора: деление уже не поможет, причину
			// вернёт вызывающий код через timeoutError.
			return
		}
		e.splitOrFail(ctx, system, part, depth, fragment, outcome, "LLM: "+callErr.Error())
		return
	}
	if limit := e.maxResponseChars(); limit > 0 && len([]rune(answer)) > limit {
		e.splitOrFail(ctx, system, part, depth, fragment, outcome,
			fmt.Sprintf("ответ модели больше %d символов при лимите %d токенов", limit, e.maxResponseTokens()))
		return
	}

	response, parseErr := parseResponse(answer)
	if parseErr != nil {
		e.splitOrFail(ctx, system, part, depth, fragment, outcome, "невалидный JSON: "+parseErr.Error())
		return
	}
	valid := 0
	for _, raw := range response.Competencies {
		competency, itemWarnings, ok := validateCompetency(raw, part.pages)
		for _, warning := range itemWarnings {
			outcome.addWarning(fmt.Sprintf("фрагмент %d: %s", fragment+1, warning))
		}
		if !ok {
			continue
		}
		outcome.competencies = append(outcome.competencies, competency)
		valid++
	}
	if valid == 0 {
		reason := "модель вернула пустой список компетенций"
		if len(response.Competencies) > 0 {
			reason = "все пункты ответа отбракованы при валидации"
		}
		e.splitOrFail(ctx, system, part, depth, fragment, outcome, reason)
		return
	}
}

// splitOrFail делит чанк пополам и повторяет разбор или фиксирует причину
// неудачи, если деление невозможно (достигнут лимит глубины/размера).
func (e *Extractor) splitOrFail(ctx context.Context, system string, part chunk, depth, fragment int, outcome *chunkOutcome, reason string) {
	outcome.fail(reason)
	if depth >= maxSplitDepth || part.chars <= minSplitChars {
		outcome.addWarning(fmt.Sprintf("фрагмент %d: не удалось разобрать чанк (%d символов): %s", fragment+1, part.chars, reason))
		e.logger().Warn("fgos_chunk_failed",
			"fragment", fragment+1,
			"depth", depth,
			"chunk_chars", part.chars,
			"reason", reason)
		return
	}
	left, right, ok := splitChunk(part)
	if !ok {
		outcome.addWarning(fmt.Sprintf("фрагмент %d: не удалось разделить чанк (%d символов): %s", fragment+1, part.chars, reason))
		e.logger().Warn("fgos_chunk_failed",
			"fragment", fragment+1,
			"depth", depth,
			"chunk_chars", part.chars,
			"reason", reason)
		return
	}
	outcome.addWarning(SplitChunkWarning)
	e.logger().Warn("fgos_chunk_split",
		"fragment", fragment+1,
		"depth", depth+1,
		"chunk_chars", part.chars,
		"left_chars", left.chars,
		"right_chars", right.chars,
		"reason", reason)
	e.extractChunk(ctx, system, left, depth+1, fragment, outcome)
	if ctx.Err() != nil {
		return
	}
	e.extractChunk(ctx, system, right, depth+1, fragment, outcome)
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
		if len(current.segments) > 0 && current.chars+pageChars > limit {
			result = append(result, current)
			current = chunk{}
		}
		current.pages = append(current.pages, page)
		current.segments = append(current.segments, promptSegment{number: page.Number, text: page.Text})
		current.chars += pageChars
	}
	if len(current.segments) > 0 {
		result = append(result, current)
	}
	return result
}

// splitChunk делит чанк примерно пополам по символам. Если граница попадает
// внутрь страницы, страница режется на два сегмента с одним номером: модель
// видит половину текста, но цитата всё равно проверяется по полному тексту
// страницы, сохранённому в pages.
func splitChunk(part chunk) (chunk, chunk, bool) {
	if part.chars < 2 || len(part.segments) == 0 {
		return chunk{}, chunk{}, false
	}
	target := part.chars / 2
	left := chunk{}
	right := chunk{}
	remaining := target
	for _, segment := range part.segments {
		runes := []rune(segment.text)
		chars := len(runes)
		if remaining > 0 {
			if chars <= remaining {
				left.segments = append(left.segments, segment)
				left.chars += chars
				remaining -= chars
				continue
			}
			left.segments = append(left.segments, promptSegment{number: segment.number, text: string(runes[:remaining])})
			right.segments = append(right.segments, promptSegment{number: segment.number, text: string(runes[remaining:])})
			left.chars += remaining
			right.chars += chars - remaining
			remaining = 0
			continue
		}
		right.segments = append(right.segments, segment)
		right.chars += chars
	}
	if left.chars == 0 || right.chars == 0 {
		return chunk{}, chunk{}, false
	}
	left.pages = pagesForSegments(part.pages, left.segments)
	right.pages = pagesForSegments(part.pages, right.segments)
	return left, right, true
}

// pagesForSegments возвращает полные страницы, которых касаются сегменты.
func pagesForSegments(pages []Page, segments []promptSegment) []Page {
	byNumber := make(map[int]Page, len(pages))
	for _, page := range pages {
		byNumber[page.Number] = page
	}
	seen := make(map[int]bool, len(segments))
	result := make([]Page, 0, len(segments))
	for _, segment := range segments {
		if seen[segment.number] {
			continue
		}
		seen[segment.number] = true
		if page, ok := byNumber[segment.number]; ok {
			result = append(result, page)
		}
	}
	return result
}

// userPrompt собирает запрос по одному чанку: страницы с номерами и схема
// ответа. В модель уходит только видимая часть страницы (сегменты).
func userPrompt(part chunk) string {
	var builder strings.Builder
	builder.WriteString("Фрагмент ФГОС. Страницы: ")
	builder.WriteString(pageRange(segmentPageNumbers(part.segments)))
	builder.WriteString(".\n")
	builder.WriteString("Извлекай компетенции только из текста ниже. page в citations обязан быть одной из этих страниц.\n\n")
	for _, segment := range part.segments {
		fmt.Fprintf(&builder, "=== Страница %d ===\n%s\n\n", segment.number, segment.text)
	}
	builder.WriteString(`Верни JSON ровно по схеме: {"competencies":[{"code":"УК-1","title":"...","type":"universal",` +
		`"ksa":[{"kind":"knowledge","title":"..."}],"citations":[{"page":3,"quote":"дословный фрагмент"}]}]}`)
	return builder.String()
}

func segmentPageNumbers(segments []promptSegment) []int {
	numbers := make([]int, 0, len(segments))
	seen := make(map[int]bool, len(segments))
	for _, segment := range segments {
		if seen[segment.number] {
			continue
		}
		seen[segment.number] = true
		numbers = append(numbers, segment.number)
	}
	return numbers
}

func pageRange(numbers []int) string {
	if len(numbers) == 0 {
		return "—"
	}
	if len(numbers) == 1 {
		return strconv.Itoa(numbers[0])
	}
	contiguous := true
	for index := 1; index < len(numbers); index++ {
		if numbers[index] != numbers[index-1]+1 {
			contiguous = false
			break
		}
	}
	if contiguous {
		return fmt.Sprintf("%d–%d", numbers[0], numbers[len(numbers)-1])
	}
	values := make([]string, len(numbers))
	for index, number := range numbers {
		values[index] = strconv.Itoa(number)
	}
	return strings.Join(values, ", ")
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
// конфликте формулировок остаётся первая и возвращается предупреждение.
func mergeCompetency(program *ingestion.EducationProgram, indexByCode map[string]int, incoming ingestion.ProgramCompetency) []string {
	index, exists := indexByCode[incoming.Code]
	if !exists {
		indexByCode[incoming.Code] = len(program.Competencies)
		program.Competencies = append(program.Competencies, incoming)
		return nil
	}
	existing := &program.Competencies[index]
	warnings := []string{}
	if existing.Title != incoming.Title {
		warnings = append(warnings, fmt.Sprintf(
			"компетенция %s: формулировка %q отличается от %q; оставлена первая",
			incoming.Code, incoming.Title, existing.Title))
	}
	if existing.Type != incoming.Type {
		warnings = append(warnings, fmt.Sprintf(
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
	return warnings
}

// timeoutError превращает истёкший общий бюджет в ошибку адаптера и добавляет
// причину последней неудачи, если она была.
func timeoutError(ctxErr error, timeout time.Duration, lastReason string) error {
	base := contextError(ctxErr, timeout)
	if lastReason == "" {
		return base
	}
	return fmt.Errorf("%w: последняя ошибка — %s", base, lastReason)
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
