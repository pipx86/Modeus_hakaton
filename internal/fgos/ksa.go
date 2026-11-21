package fgos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/llm"
)

const (
	// KSAPromptVersion — версия промпта извлечения KSA из ОПОП/РПД, встроенная
	// в бинарь (prompts/fgosksa/<version>/system.md).
	KSAPromptVersion = "v1"
	// KSAMaxCharsPerChunk — целевой размер чанка при извлечении KSA. ОПОП и
	// РПД обычно содержат длинные разделы «Знать/Уметь/Владеть», поэтому
	// чанк совпадает с базовым чанком ФГОС и не превышает 6000 символов.
	KSAMaxCharsPerChunk = 6000
)

// Ошибки извлечения KSA. Вызывающий код матчит их через errors.Is, чтобы
// выбрать HTTP-код: 422 — KSA не нашлись или документ не разобрался,
// 503 — модель недоступна или не уложилась в таймаут.
var (
	// ErrNoKSA — в документе не нашлось ни одного валидного KSA.
	ErrNoKSA = errors.New("fgos: не удалось извлечь KSA")
	// ErrKSAUnavailable — модель не ответила ни на один чанк: это сбой
	// зависимости, а не отсутствие KSA в документе.
	ErrKSAUnavailable = errors.New("fgos: LLM недоступна при извлечении KSA")
)

// KSAEntry — знания, умения и навыки одной компетенции, извлечённые из
// документа ОПОП/РПД. Code нормализован (верхний регистр, дефис без пробелов),
// сами формулировки компетенций в структуру не входят.
type KSAEntry struct {
	Code string
	KSA  []ingestion.ProgramKSA
}

// KSAExtractor — необязательное расширение DocumentExtractor: извлечение KSA
// из уже полученных страниц. Реализуется *Extractor и, по встраиванию,
// ocr.FGOSExtractor. API проверяет поддержку через type assertion: старые
// реализации DocumentExtractor продолжают работать, а эндпоинт KSA отвечает
// 422, если адаптер не умеет извлекать KSA.
type KSAExtractor interface {
	ExtractKSA(ctx context.Context, pages []Page) ([]KSAEntry, []string, error)
}

// ksaChunk — часть документа, отправляемая модели одним запросом. Страница
// может быть разрезана на несколько сегментов с одним номером, если она
// длиннее лимита: ОПОП/РПД после конвертации из DOCX иногда приходят одной
// огромной «страницей».
type ksaChunk struct {
	segments []promptSegment
	chars    int
}

// ksaChunkOutcome — локальный результат одного чанка. Пишет его ровно один
// воркер, поэтому поля не нуждаются в блокировке; слияние выполняет
// ksaState.apply после завершения пула.
type ksaChunkOutcome struct {
	fragment         int
	entries          []KSAEntry
	warnings         []string
	warningSet       map[string]bool
	lastReason       string
	responded        bool
	transportFailure bool
}

func (o *ksaChunkOutcome) addWarning(warning string) {
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

// failTransport фиксирует ошибку обращения к модели: если ни один чанк не
// ответил, вызывающий код должен получить 503, а не «в документе нет KSA».
func (o *ksaChunkOutcome) failTransport(reason string) {
	o.transportFailure = true
	if reason != "" {
		o.lastReason = reason
	}
}

// setReason фиксирует причину, при которой модель ответила, но валидных KSA
// не вернула.
func (o *ksaChunkOutcome) setReason(reason string) {
	if reason != "" {
		o.lastReason = reason
	}
}

// ksaState — общий результат разбора документа. Обновляется только из
// главного потока, поэтому гонок нет даже при параллельном разборе чанков.
type ksaState struct {
	entries          []KSAEntry
	indexByCode      map[string]int
	seen             map[string]bool
	warnings         []string
	warningSet       map[string]bool
	lastReason       string
	responded        bool
	transportFailure bool
}

func newKSAState() *ksaState {
	return &ksaState{
		entries:     []KSAEntry{},
		indexByCode: make(map[string]int),
		seen:        make(map[string]bool),
		warnings:    []string{},
		warningSet:  make(map[string]bool),
	}
}

func (s *ksaState) addWarning(warning string) {
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
// порядок записей и предупреждений детерминирован независимо от порядка
// завершения параллельных воркеров. Дубли (code, kind, title) отбрасываются.
func (s *ksaState) apply(outcome *ksaChunkOutcome) {
	if s == nil || outcome == nil {
		return
	}
	for _, warning := range outcome.warnings {
		s.addWarning(fmt.Sprintf("фрагмент %d: %s", outcome.fragment+1, warning))
	}
	for _, entry := range outcome.entries {
		if entry.Code == "" || len(entry.KSA) == 0 {
			continue
		}
		index, exists := s.indexByCode[entry.Code]
		if !exists {
			index = len(s.entries)
			s.indexByCode[entry.Code] = index
			s.entries = append(s.entries, KSAEntry{Code: entry.Code})
		}
		target := &s.entries[index]
		for _, item := range entry.KSA {
			key := entry.Code + "\x00" + item.Kind + "\x00" + item.Title
			if s.seen[key] {
				continue
			}
			s.seen[key] = true
			target.KSA = append(target.KSA, item)
		}
	}
	if outcome.responded {
		s.responded = true
	}
	if outcome.transportFailure {
		s.transportFailure = true
	}
	if outcome.lastReason != "" {
		s.lastReason = outcome.lastReason
	}
}

// result возвращает только записи с непустым KSA. Пустой срез вместо nil
// удобен вызывающему коду и JSON-ответу.
func (s *ksaState) result() []KSAEntry {
	result := make([]KSAEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		if len(entry.KSA) == 0 {
			continue
		}
		result = append(result, entry)
	}
	return result
}

// failureError возвращает ошибку «ничего не извлеклось» с причиной последней
// неудачи. Если модель не ответила ни на один чанк, ошибка оборачивает
// ErrKSAUnavailable: это 503, а не 422.
func (s *ksaState) failureError() error {
	if s == nil {
		return ErrNoKSA
	}
	if s.transportFailure && !s.responded {
		if s.lastReason == "" {
			return ErrKSAUnavailable
		}
		return fmt.Errorf("%w: последняя ошибка — %s", ErrKSAUnavailable, s.lastReason)
	}
	if s.lastReason == "" {
		return ErrNoKSA
	}
	return fmt.Errorf("%w: последняя ошибка — %s", ErrNoKSA, s.lastReason)
}

// ExtractKSA извлекает знания, умения и навыки из страниц ОПОП/РПД.
//
// Промпт не выбирает раздел документа: в отличие от ФГОС, KSA в ОПОП/РПД
// могут быть в любой части программы, поэтому модели уходят все непустые
// страницы. Документ режется на чанки по KSAMaxCharsPerChunk; страница длиннее
// чанка делится на сегменты с тем же номером. Чанки обрабатываются пулом
// воркеров (Concurrency, как у Extract), результаты собираются в порядке
// страниц и дедуплицируются по (code, kind, title).
//
// Невалидные пункты (код не по шаблону, неизвестный kind, пустой title)
// отбрасываются с предупреждением. Если модель не ответила ни на один чанк,
// возвращается ошибка, оборачивающая ErrKSAUnavailable; если ответила, но
// валидных KSA нет — ошибка, оборачивающая ErrNoKSA, с причиной последней
// неудачи.
func (e *Extractor) ExtractKSA(ctx context.Context, pages []Page) ([]KSAEntry, []string, error) {
	if e == nil || e.Client == nil {
		return []KSAEntry{}, []string{}, ErrNoClient
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
		return []KSAEntry{}, []string{}, fmt.Errorf("%w: страницы не содержат текста", ErrNoTextLayer)
	}

	state := newKSAState()
	for _, page := range filled {
		if page.OCR {
			state.addWarning(OCRPageWarning(page.Number))
		}
	}

	system, err := KSASystemPrompt()
	if err != nil {
		return []KSAEntry{}, state.warnings, err
	}

	timeout := e.timeout()
	extractCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	e.applyResponseTokenLimit()

	chunks := chunkKSAPages(filled, KSAMaxCharsPerChunk)
	concurrency := e.concurrency()
	if concurrency > len(chunks) {
		concurrency = len(chunks)
	}
	e.logger().Info("fgos_ksa_extract_cycle",
		"pages", len(filled),
		"chunks", len(chunks),
		"concurrency", concurrency,
		"max_chars_per_chunk", KSAMaxCharsPerChunk)

	if concurrency <= 1 {
		// Последовательный режим сохранён для concurrency=1 и документов из
		// одного чанка: порядок предупреждений и записей прежний.
		for index, part := range chunks {
			if ctxErr := extractCtx.Err(); ctxErr != nil {
				return state.result(), state.warnings, timeoutError(ctxErr, timeout, state.lastReason)
			}
			outcome := ksaChunkOutcome{fragment: index}
			e.extractKSAChunk(extractCtx, system, part, &outcome)
			state.apply(&outcome)
		}
	} else {
		e.extractKSAChunksParallel(extractCtx, system, chunks, concurrency, state)
	}

	if ctxErr := extractCtx.Err(); ctxErr != nil {
		return state.result(), state.warnings, timeoutError(ctxErr, timeout, state.lastReason)
	}
	entries := state.result()
	if len(entries) == 0 {
		return entries, state.warnings, state.failureError()
	}
	return entries, state.warnings, nil
}

// extractKSAChunksParallel разбирает чанки пулом воркеров. Каждый чанк пишет
// результат в свою ячейку outcomes, поэтому порядок записей и предупреждений
// детерминирован независимо от порядка завершения запросов.
func (e *Extractor) extractKSAChunksParallel(ctx context.Context, system string, chunks []ksaChunk, concurrency int, state *ksaState) {
	outcomes := make([]ksaChunkOutcome, len(chunks))
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
			e.extractKSAChunk(ctx, system, chunks[index], &outcomes[index])
		}(index)
	}
	wait.Wait()
	for index := range outcomes {
		state.apply(&outcomes[index])
	}
}

// extractKSAChunk разбирает один чанк. В отличие от Extract здесь нет
// рекурсивного деления: чанк уже ограничен 6000 символами, а сбой одного
// фрагмента не должен отбрасывать KSA из остальных — он попадает в
// предупреждения, а итоговая ошибка возвращается только если ничего не
// извлеклось.
func (e *Extractor) extractKSAChunk(ctx context.Context, system string, part ksaChunk, outcome *ksaChunkOutcome) {
	if ctx.Err() != nil {
		return
	}
	e.logger().Debug("fgos_ksa_chunk_request",
		"fragment", outcome.fragment+1,
		"chunk_chars", part.chars,
		"concurrency", e.concurrency())

	answer, callErr := e.Client.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: ksaUserPrompt(part)},
	})
	if callErr != nil {
		if ctx.Err() != nil {
			// Истёк общий бюджет разбора: причину вернёт вызывающий код
			// через timeoutError.
			return
		}
		outcome.failTransport("LLM: " + callErr.Error())
		outcome.addWarning("не удалось получить ответ модели: " + callErr.Error())
		return
	}
	outcome.responded = true

	response, parseErr := parseResponse(answer)
	if parseErr != nil {
		reason := "невалидный JSON: " + parseErr.Error()
		outcome.setReason(reason)
		outcome.addWarning(reason)
		return
	}
	before := len(outcome.entries)
	for _, raw := range response.Competencies {
		entry, itemWarnings := validateKSACompetency(raw)
		for _, warning := range itemWarnings {
			outcome.addWarning(warning)
		}
		if len(entry.KSA) == 0 {
			continue
		}
		outcome.entries = append(outcome.entries, entry)
	}
	if len(outcome.entries) == before {
		reason := "модель не вернула KSA"
		if len(response.Competencies) > 0 {
			reason = "все пункты ответа отбракованы при валидации"
		}
		outcome.setReason(reason)
	}
}

// validateKSACompetency проверяет один пункт ответа модели. Код нормализуется
// и сверяется с шаблоном УК-1/ОПК-2/ПК-3; неизвестный kind и пустой title
// отбрасываются с предупреждением, остальные KSA сохраняются в порядке ответа.
func validateKSACompetency(raw rawCompetency) (KSAEntry, []string) {
	warnings := []string{}
	code := normalizeKSACode(raw.Code)
	if !competencyCodePattern.MatchString(code) {
		return KSAEntry{}, append(warnings, fmt.Sprintf(
			"компетенция с кодом %q отброшена: код должен соответствовать шаблону УК-1/ОПК-2/ПК-3",
			strings.TrimSpace(raw.Code)))
	}
	entry := KSAEntry{Code: code}
	for _, item := range raw.KSA {
		kind, kindOK := normalizeKSAKind(item.Kind)
		if !kindOK {
			warnings = append(warnings, fmt.Sprintf("компетенция %s: KSA с видом %q отброшено",
				code, strings.TrimSpace(item.Kind)))
			continue
		}
		title := strings.TrimSpace(item.Title)
		if title == "" {
			warnings = append(warnings, fmt.Sprintf("компетенция %s: KSA без title отброшено", code))
			continue
		}
		entry.KSA = append(entry.KSA, ingestion.ProgramKSA{Kind: kind, Title: title})
	}
	return entry, warnings
}

// normalizeKSACode приводит код компетенции к виду ОПК-2: верхний регистр,
// дефис вместо тире, без пробелов внутри. Коды не по шаблону отсеиваются позже
// через competencyCodePattern.
func normalizeKSACode(raw string) string {
	code := strings.TrimSpace(raw)
	code = strings.ToUpper(code)
	code = strings.NewReplacer(
		"–", "-", // en dash
		"—", "-", // em dash
		"−", "-", // minus sign
		"‐", "-", // hyphen
		"‑", "-", // non-breaking hyphen
	).Replace(code)
	code = strings.Join(strings.Fields(code), "")
	return code
}

// chunkKSAPages режет страницы на чанки по limit символов. Страница длиннее
// лимита делится на несколько сегментов с тем же номером, чтобы запрос не
// превышал лимит даже на DOCX, где весь текст иногда попадает в одну страницу.
func chunkKSAPages(pages []Page, limit int) []ksaChunk {
	if limit <= 0 {
		limit = KSAMaxCharsPerChunk
	}
	result := make([]ksaChunk, 0, len(pages))
	current := ksaChunk{}
	flush := func() {
		if len(current.segments) == 0 {
			return
		}
		result = append(result, current)
		current = ksaChunk{}
	}
	for _, page := range pages {
		runes := []rune(page.Text)
		if len(runes) == 0 {
			continue
		}
		// Обычная страница не разрывается между чанками: так номера страниц в
		// запросе остаются осмысленными. Режется только страница, которая сама
		// длиннее лимита.
		if len(runes) > limit {
			flush()
			for start := 0; start < len(runes); start += limit {
				end := start + limit
				if end > len(runes) {
					end = len(runes)
				}
				result = append(result, ksaChunk{
					segments: []promptSegment{{number: page.Number, text: string(runes[start:end])}},
					chars:    end - start,
				})
			}
			continue
		}
		if current.chars > 0 && current.chars+len(runes) > limit {
			flush()
		}
		current.segments = append(current.segments, promptSegment{number: page.Number, text: page.Text})
		current.chars += len(runes)
		if current.chars >= limit {
			flush()
		}
	}
	flush()
	return result
}

// ksaUserPrompt собирает запрос по одному чанку: страницы с номерами и схему
// ответа. В модель уходит только видимая часть страницы (сегменты).
func ksaUserPrompt(part ksaChunk) string {
	var builder strings.Builder
	builder.WriteString("Фрагмент ОПОП/РПД. Страницы: ")
	builder.WriteString(pageRange(segmentPageNumbers(part.segments)))
	builder.WriteString(".\n")
	builder.WriteString("Извлекай знания, умения и навыки только из текста ниже. ")
	builder.WriteString("Не включай формулировки компетенций: только KSA с кодом компетенции.\n\n")
	for _, segment := range part.segments {
		fmt.Fprintf(&builder, "=== Страница %d ===\n%s\n\n", segment.number, segment.text)
	}
	builder.WriteString(`Верни JSON ровно по схеме: {"competencies":[{"code":"ОПК-2","ksa":[{"kind":"knowledge","title":"..."}]}]}`)
	return builder.String()
}

var _ KSAExtractor = (*Extractor)(nil)
