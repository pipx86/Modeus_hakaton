package fgos

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ksaJSON собирает ответ модели для одного кода компетенции. Русские виды и
// лишние пробелы в коде проверяются отдельными тестами.
func ksaJSON(code string, items ...[2]string) string {
	var builder strings.Builder
	builder.WriteString(`{"competencies":[{"code":"`)
	builder.WriteString(code)
	builder.WriteString(`","ksa":[`)
	for index, item := range items {
		if index > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(`{"kind":"`)
		builder.WriteString(item[0])
		builder.WriteString(`","title":"`)
		builder.WriteString(item[1])
		builder.WriteString(`"}`)
	}
	builder.WriteString(`]}]}`)
	return builder.String()
}

func ksaPages(long bool) []Page {
	text := "ОПК-2. Способен разрабатывать программное обеспечение."
	if long {
		// Две страницы по 3500 символов дают два чанка по 6000 символов.
		text = strings.Repeat("знание ", 500)
	}
	return []Page{
		{Number: 1, Text: text},
		{Number: 2, Text: text},
	}
}

func findKSAEntry(t *testing.T, entries []KSAEntry, code string) KSAEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Code == code {
			return entry
		}
	}
	t.Fatalf("нет записи KSA для %s: %+v", code, entries)
	return KSAEntry{}
}

func TestExtractKSAMultiChunkMergesAndNormalizes(t *testing.T) {
	client := &fakeLLM{answers: []string{
		// Код с пробелами и длинным тире, русские виды KSA.
		ksaJSON("опк — 2",
			[2]string{"знание", " основные алгоритмы "},
			[2]string{"умение", "разрабатывать алгоритмы"},
		),
		ksaJSON("ПК-3",
			[2]string{"владение", "навыками отладки"},
		),
	}}
	extractor := NewExtractor(client)
	extractor.Concurrency = 1

	entries, warnings, err := extractor.ExtractKSA(context.Background(), ksaPages(true))
	if err != nil {
		t.Fatalf("ExtractKSA: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("неожидали предупреждений: %v", warnings)
	}
	if len(entries) != 2 {
		t.Fatalf("ожидали две компетенции: %+v", entries)
	}
	first := findKSAEntry(t, entries, "ОПК-2")
	if len(first.KSA) != 2 {
		t.Fatalf("KSA ОПК-2: %+v", first.KSA)
	}
	if first.KSA[0].Kind != "knowledge" || first.KSA[0].Title != "основные алгоритмы" {
		t.Fatalf("нормализация знания: %+v", first.KSA[0])
	}
	if first.KSA[1].Kind != "skill" {
		t.Fatalf("умение должно маппиться в skill: %+v", first.KSA[1])
	}
	second := findKSAEntry(t, entries, "ПК-3")
	if len(second.KSA) != 1 || second.KSA[0].Kind != "ability" {
		t.Fatalf("владение должно маппиться в ability: %+v", second.KSA)
	}
	calls := client.calls()
	if len(calls) != 2 {
		t.Fatalf("ожидали два чанка, получили %d", len(calls))
	}
	if !strings.Contains(calls[0], "=== Страница 1 ===") || strings.Contains(calls[0], "=== Страница 2 ===") {
		t.Fatalf("первый чанк должен содержать только первую страницу: %s", calls[0])
	}
	if !strings.Contains(calls[1], "=== Страница 2 ===") {
		t.Fatalf("второй чанк должен содержать вторую страницу: %s", calls[1])
	}
	systems := client.systems
	if len(systems) == 0 || !strings.Contains(systems[0], "ОПОП/РПД") || !strings.Contains(systems[0], `"kind"`) {
		t.Fatalf("системный промпт KSA не передан: %q", systems)
	}
}

func TestExtractKSADeduplicatesAcrossChunks(t *testing.T) {
	client := &fakeLLM{answers: []string{
		ksaJSON("ОПК-2",
			[2]string{"knowledge", "алгоритмы"},
			[2]string{"skill", "писать код"},
		),
		ksaJSON("ОПК-2",
			[2]string{"knowledge", "алгоритмы"},
			[2]string{"skill", "писать код"},
			[2]string{"ability", "навыками отладки"},
		),
	}}
	extractor := NewExtractor(client)
	extractor.Concurrency = 1

	entries, _, err := extractor.ExtractKSA(context.Background(), ksaPages(true))
	if err != nil {
		t.Fatalf("ExtractKSA: %v", err)
	}
	if len(entries) != 1 || len(entries[0].KSA) != 3 {
		t.Fatalf("дубли должны схлопнуться: %+v", entries)
	}
	if entries[0].KSA[0].Title != "алгоритмы" || entries[0].KSA[2].Title != "навыками отладки" {
		t.Fatalf("порядок KSA: %+v", entries[0].KSA)
	}
}

func TestExtractKSASplitsOversizedPage(t *testing.T) {
	client := &fakeLLM{answers: []string{
		ksaJSON("УК-1", [2]string{"knowledge", "методы анализа"}),
		ksaJSON("УК-1", [2]string{"skill", "анализировать"}),
	}}
	extractor := NewExtractor(client)
	extractor.Concurrency = 1
	page := Page{Number: 7, Text: strings.Repeat("текст ", KSAMaxCharsPerChunk/6+1)}

	entries, _, err := extractor.ExtractKSA(context.Background(), []Page{page})
	if err != nil {
		t.Fatalf("ExtractKSA: %v", err)
	}
	if len(entries) != 1 || len(entries[0].KSA) != 2 {
		t.Fatalf("страница длиннее чанка должна разбираться частями: %+v", entries)
	}
	calls := client.calls()
	if len(calls) != 2 {
		t.Fatalf("ожидали два чанка для длинной страницы, получили %d", len(calls))
	}
	for _, call := range calls {
		if !strings.Contains(call, "=== Страница 7 ===") {
			t.Fatalf("сегменты должны сохранять номер страницы: %s", call)
		}
	}
}

func TestExtractKSAFiltersInvalidItems(t *testing.T) {
	answer := `{"competencies":[` +
		`{"code":"ОПК-2","ksa":[
			{"kind":"knowledge","title":"валидное знание"},
			{"kind":"mystery","title":"неизвестный вид"},
			{"kind":"skill","title":"   "}
		]},` +
		`{"code":"OPK-2","ksa":[{"kind":"knowledge","title":"латиница"}]},` +
		`{"code":"ПК-3","ksa":[{"kind":"unknown","title":"только мусор"}]}` +
		`]}`
	client := &fakeLLM{answers: []string{answer}}
	extractor := NewExtractor(client)
	extractor.Concurrency = 1

	entries, warnings, err := extractor.ExtractKSA(context.Background(), []Page{{Number: 1, Text: "текст"}})
	if err != nil {
		t.Fatalf("ExtractKSA: %v", err)
	}
	if len(entries) != 1 || entries[0].Code != "ОПК-2" || len(entries[0].KSA) != 1 {
		t.Fatalf("должна остаться одна валидная запись: %+v", entries)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"mystery", "KSA без title", "OPK-2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("нет предупреждения %q в %v", want, warnings)
		}
	}
}

func TestExtractKSAInvalidJSONReturnsReason(t *testing.T) {
	client := &fakeLLM{answers: []string{"это не JSON"}}
	extractor := NewExtractor(client)
	extractor.Concurrency = 1

	entries, warnings, err := extractor.ExtractKSA(context.Background(), []Page{{Number: 1, Text: "текст"}})
	if !errors.Is(err, ErrNoKSA) {
		t.Fatalf("ожидали ErrNoKSA, получили %v", err)
	}
	if errors.Is(err, ErrKSAUnavailable) {
		t.Fatalf("битый JSON не должен считаться недоступной моделью: %v", err)
	}
	if !strings.Contains(err.Error(), "невалидный JSON") {
		t.Fatalf("ошибка должна содержать причину: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("записей быть не должно: %+v", entries)
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "невалидный JSON") {
		t.Fatalf("ожидали предупреждение о JSON: %v", warnings)
	}
}

func TestExtractKSAUnavailableLLM(t *testing.T) {
	client := &fakeLLM{err: errors.New("upstream 500")}
	extractor := NewExtractor(client)
	extractor.Concurrency = 1

	_, warnings, err := extractor.ExtractKSA(context.Background(), []Page{{Number: 1, Text: "текст"}})
	if !errors.Is(err, ErrKSAUnavailable) {
		t.Fatalf("ожидали ErrKSAUnavailable, получили %v", err)
	}
	if !strings.Contains(err.Error(), "upstream 500") {
		t.Fatalf("ошибка должна содержать причину: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("сбой модели должен попасть в предупреждения")
	}
}

func TestExtractKSAEmptyResponse(t *testing.T) {
	client := &fakeLLM{answers: []string{`{"competencies":[]}`}}
	extractor := NewExtractor(client)
	extractor.Concurrency = 1

	entries, _, err := extractor.ExtractKSA(context.Background(), []Page{{Number: 1, Text: "текст"}})
	if !errors.Is(err, ErrNoKSA) {
		t.Fatalf("ожидали ErrNoKSA, получили %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("записей быть не должно: %+v", entries)
	}
	if !strings.Contains(err.Error(), "не вернула KSA") {
		t.Fatalf("причина пустого ответа: %v", err)
	}
}

func TestExtractKSAGuards(t *testing.T) {
	if _, _, err := NewExtractor(nil).ExtractKSA(context.Background(), []Page{{Number: 1, Text: "текст"}}); !errors.Is(err, ErrNoClient) {
		t.Fatalf("без клиента: %v", err)
	}
	client := &fakeLLM{answers: []string{ksaJSON("УК-1", [2]string{"knowledge", "x"})}}
	_, _, err := NewExtractor(client).ExtractKSA(context.Background(), []Page{{Number: 1, Text: "   "}})
	if !errors.Is(err, ErrNoTextLayer) {
		t.Fatalf("без текста: %v", err)
	}
}

func TestExtractKSAWarnsAboutOCR(t *testing.T) {
	client := &fakeLLM{answers: []string{ksaJSON("УК-1", [2]string{"knowledge", "методы"})}}
	extractor := NewExtractor(client)
	extractor.Concurrency = 1

	_, warnings, err := extractor.ExtractKSA(context.Background(), []Page{{Number: 1, Text: "текст", OCR: true}})
	if err != nil {
		t.Fatalf("ExtractKSA: %v", err)
	}
	if !hasString(warnings, OCRPageWarning(1)) {
		t.Fatalf("нет предупреждения OCR: %v", warnings)
	}
}

func TestExtractKSAConcurrentChunksKeepPageOrder(t *testing.T) {
	client := &fakeLLM{reply: func(user string) (string, error) {
		if strings.Contains(user, "=== Страница 1 ===") {
			return ksaJSON("УК-1", [2]string{"knowledge", "первая"}), nil
		}
		return ksaJSON("ОПК-2", [2]string{"knowledge", "вторая"}), nil
	}}
	extractor := NewExtractor(client)
	extractor.Concurrency = 2
	pages := []Page{
		{Number: 1, Text: strings.Repeat("а", 3500)},
		{Number: 2, Text: strings.Repeat("б", 3500)},
	}

	entries, _, err := extractor.ExtractKSA(context.Background(), pages)
	if err != nil {
		t.Fatalf("ExtractKSA: %v", err)
	}
	if len(entries) != 2 || entries[0].Code != "УК-1" || entries[1].Code != "ОПК-2" {
		t.Fatalf("порядок записей должен совпадать с порядком страниц: %+v", entries)
	}
}

func TestChunkKSAPagesRespectsLimit(t *testing.T) {
	pages := []Page{
		{Number: 1, Text: strings.Repeat("а", 5000)},
		{Number: 2, Text: strings.Repeat("б", 5000)},
	}
	chunks := chunkKSAPages(pages, KSAMaxCharsPerChunk)
	if len(chunks) != 2 {
		t.Fatalf("ожидали два чанка: %+v", chunks)
	}
	for _, chunk := range chunks {
		if chunk.chars > KSAMaxCharsPerChunk {
			t.Fatalf("чанк больше лимита: %d", chunk.chars)
		}
	}
}

func TestKSASystemPromptEmbedded(t *testing.T) {
	prompt, err := KSASystemPrompt()
	if err != nil {
		t.Fatalf("KSASystemPrompt: %v", err)
	}
	for _, want := range []string{"ОПОП/РПД", `"competencies"`, `"kind"`, "ability"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("промпт не содержит %q", want)
		}
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
