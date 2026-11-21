package fgos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/llm"
)

// testLogger отбрасывает логи: тесты деления чанков иначе засоряют вывод
// предупреждениями slog.
var testLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

const (
	pageOneText   = "УК-1. Способен осуществлять поиск и критический анализ информации.\nЗнать: методы системного анализа.\n"
	pageTwoText   = "ОПК-2. Способен разрабатывать программное обеспечение.\nЗнать: основы алгоритмизации.\n"
	pageThreeText = "ПК-3. Способен проектировать базы данных.\nЗнать: модели данных.\n"
)

const quoteOne = "Способен осуществлять поиск и критический анализ информации"
const quoteTwo = "Способен разрабатывать программное обеспечение"

// fakeLLM — llm.ChatClient без сети. Отвечает заранее заданными строками по
// порядку вызовов, через reply может решать по тексту запроса; block эмулирует
// зависшую модель до отмены контекста.
type fakeLLM struct {
	mu        sync.Mutex
	answers   []string
	err       error
	block     bool
	reply     func(user string) (string, error)
	users     []string
	systems   []string
	maxTokens int
}

func (f *fakeLLM) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	var user, system string
	for _, message := range messages {
		switch message.Role {
		case llm.RoleUser:
			user = message.Content
		case llm.RoleSystem:
			system = message.Content
		}
	}
	f.mu.Lock()
	index := len(f.users)
	f.users = append(f.users, user)
	f.systems = append(f.systems, system)
	block := f.block
	callErr := f.err
	reply := f.reply
	answer := ""
	if index < len(f.answers) {
		answer = f.answers[index]
	} else if len(f.answers) > 0 {
		answer = f.answers[len(f.answers)-1]
	}
	f.mu.Unlock()

	if block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if reply != nil {
		return reply(user)
	}
	if callErr != nil {
		return "", callErr
	}
	return answer, nil
}

// SetMaxResponseTokens реализует fgos.MaxResponseTokensSetter: адаптер должен
// передать лимит ответа, если клиент это поддерживает.
func (f *fakeLLM) SetMaxResponseTokens(tokens int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxTokens = tokens
}

func (f *fakeLLM) responseTokens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxTokens
}

func (f *fakeLLM) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.users...)
}

func answerFor(code, title, typ, kind, ksaTitle string, page int, quote string) string {
	return `{"competencies":[{"code":"` + code + `","title":"` + title + `","type":"` + typ +
		`","ksa":[{"kind":"` + kind + `","title":"` + ksaTitle + `"}],"citations":[{"page":` +
		itoa(page) + `,"quote":"` + quote + `"}]}]}`
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func TestExtractMultiChunkMergesResults(t *testing.T) {
	client := &fakeLLM{answers: []string{
		answerFor("УК-1", "Способен осуществлять поиск и критический анализ информации", "УК", "знание", "методы системного анализа", 1, quoteOne),
		answerFor("ОПК-2", "Способен разрабатывать программное обеспечение", "general_professional", "skill", "разрабатывать модули", 2, quoteTwo),
	}}
	extractor := NewExtractor(client)
	extractor.MaxCharsPerChunk = 100
	extractor.Concurrency = 1 // тест про границы чанков, а не про параллельность

	program, warnings, err := extractor.Extract(context.Background(), []Page{
		{Number: 1, Text: pageOneText},
		{Number: 2, Text: pageTwoText},
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("неожидали предупреждений: %v", warnings)
	}
	if len(program.Competencies) != 2 {
		t.Fatalf("компетенции: %+v", program.Competencies)
	}
	first, second := program.Competencies[0], program.Competencies[1]
	if first.Code != "УК-1" || first.Type != "universal" {
		t.Fatalf("первая компетенция: %+v", first)
	}
	if len(first.KSA) != 1 || first.KSA[0].Kind != "knowledge" || first.KSA[0].Title != "методы системного анализа" {
		t.Fatalf("KSA первой компетенции: %+v", first.KSA)
	}
	if second.Code != "ОПК-2" || second.Type != "general_professional" {
		t.Fatalf("вторая компетенция: %+v", second)
	}
	if second.KSA[0].Kind != "skill" {
		t.Fatalf("русский вид KSA должен маппиться: %+v", second.KSA)
	}
	calls := client.calls()
	if len(calls) != 2 {
		t.Fatalf("ожидали два чанка (два вызова модели), получили %d", len(calls))
	}
	if !strings.Contains(calls[0], "=== Страница 1 ===") || strings.Contains(calls[0], "=== Страница 2 ===") {
		t.Fatalf("первый чанк не должен содержать вторую страницу: %s", calls[0])
	}
	if !strings.Contains(calls[1], "=== Страница 2 ===") {
		t.Fatalf("второй чанк должен содержать вторую страницу: %s", calls[1])
	}
}

func TestExtractParsesCodeFence(t *testing.T) {
	answer := "Вот результат:\n```json\n" +
		answerFor("ПК-3", "Способен проектировать базы данных", "пк", "ability", "проектировать БД", 1, "Способен проектировать базы данных") +
		"\n```"
	client := &fakeLLM{answers: []string{answer}}
	program, warnings, err := NewExtractor(client).Extract(context.Background(), []Page{{Number: 1, Text: pageThreeText}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(warnings) != 0 || len(program.Competencies) != 1 {
		t.Fatalf("program=%+v warnings=%v", program, warnings)
	}
	if program.Competencies[0].Type != "professional" || program.Competencies[0].KSA[0].Kind != "ability" {
		t.Fatalf("нормализация типа/KSA: %+v", program.Competencies[0])
	}
}

func TestExtractRejectsInvalidItems(t *testing.T) {
	answer := `{"competencies":[` +
		// валидная компетенция с невалидным KSA: KSA отбрасывается, пункт остаётся
		`{"code":"УК-1","title":"Валидная компетенция","type":"universal",` +
		`"ksa":[{"kind":"неизвестно","title":"что-то"}],` +
		`"citations":[{"page":1,"quote":"` + quoteOne + `"}]},` +
		// неверный код
		`{"code":"UK-1","title":"Латиница в коде","type":"universal","citations":[{"page":1,"quote":"` + quoteOne + `"}]},` +
		// пустой title
		`{"code":"ОПК-2","title":"  ","type":"general_professional","citations":[{"page":1,"quote":"` + quoteOne + `"}]},` +
		// неизвестный тип
		`{"code":"ПК-3","title":"Странный тип","type":"mystery","citations":[{"page":1,"quote":"` + quoteOne + `"}]},` +
		// цитаты нет на странице
		`{"code":"ПК-4","title":"Выдуманная цитата","type":"professional","citations":[{"page":1,"quote":"этой фразы нет в документе"}]},` +
		// страница вне фрагмента
		`{"code":"ПК-5","title":"Чужая страница","type":"professional","citations":[{"page":9,"quote":"` + quoteOne + `"}]},` +
		// нет ни одной цитаты
		`{"code":"ПК-6","title":"Без цитат","type":"professional","citations":[]}` +
		`]}`
	client := &fakeLLM{answers: []string{answer}}
	program, warnings, err := NewExtractor(client).Extract(context.Background(), []Page{{Number: 1, Text: pageOneText}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(program.Competencies) != 1 {
		t.Fatalf("должна остаться одна валидная компетенция: %+v", program.Competencies)
	}
	if len(program.Competencies[0].KSA) != 0 {
		t.Fatalf("невалидный KSA должен отбрасываться: %+v", program.Competencies[0].KSA)
	}
	joined := strings.Join(warnings, "\n")
	for _, marker := range []string{
		"код должен соответствовать",
		"пустой title",
		"неизвестный type",
		"не найдена дословно на странице",
		"страница 9 вне фрагмента",
		"нет ни одной дословной цитаты",
		"KSA с видом",
	} {
		if !strings.Contains(joined, marker) {
			t.Errorf("в предупреждениях нет %q:\n%s", marker, joined)
		}
	}
}

func TestExtractDeduplicatesByCodeAndMergesKSA(t *testing.T) {
	client := &fakeLLM{answers: []string{
		`{"competencies":[{"code":"УК-1","title":"Первая формулировка","type":"universal",` +
			`"ksa":[{"kind":"knowledge","title":"первое знание"}],` +
			`"citations":[{"page":1,"quote":"` + quoteOne + `"}]}]}`,
		`{"competencies":[{"code":"УК-1","title":"Другая формулировка","type":"universal",` +
			`"ksa":[{"kind":"skill","title":"второе умение"}],` +
			`"citations":[{"page":2,"quote":"` + quoteTwo + `"}]}]}`,
	}}
	extractor := NewExtractor(client)
	extractor.MaxCharsPerChunk = 100
	extractor.Concurrency = 1 // порядок слияния проверяется отдельно для пула
	program, warnings, err := extractor.Extract(context.Background(), []Page{
		{Number: 1, Text: pageOneText},
		{Number: 2, Text: pageTwoText},
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(program.Competencies) != 1 {
		t.Fatalf("дедуп по коду: %+v", program.Competencies)
	}
	competency := program.Competencies[0]
	if competency.Title != "Первая формулировка" {
		t.Fatalf("при конфликте должна остаться первая формулировка: %q", competency.Title)
	}
	if len(competency.KSA) != 2 {
		t.Fatalf("KSA должны объединяться: %+v", competency.KSA)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "оставлена первая") {
		t.Fatalf("нет предупреждения о конфликте формулировок: %v", warnings)
	}
}

func TestExtractEmptyResultIsError(t *testing.T) {
	client := &fakeLLM{answers: []string{`{"competencies":[]}`}}
	_, warnings, err := NewExtractor(client).Extract(context.Background(), []Page{{Number: 1, Text: pageOneText}})
	if !errors.Is(err, ErrNoCompetencies) {
		t.Fatalf("ожидали ErrNoCompetencies, получили %v", err)
	}
	if !strings.Contains(err.Error(), "не удалось извлечь компетенции") {
		t.Fatalf("текст ошибки: %v", err)
	}
	if !strings.Contains(err.Error(), "пустой список компетенций") {
		t.Fatalf("ошибка должна содержать причину последней неудачи: %v", err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "пустой список компетенций") {
		t.Fatalf("предупреждения: %v", warnings)
	}
}

func TestExtractInvalidJSONReportsWarning(t *testing.T) {
	client := &fakeLLM{answers: []string{"извините, не могу"}}
	_, warnings, err := NewExtractor(client).Extract(context.Background(), []Page{{Number: 1, Text: pageOneText}})
	if !errors.Is(err, ErrNoCompetencies) {
		t.Fatalf("ожидали ErrNoCompetencies, получили %v", err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "нет JSON-объекта") {
		t.Fatalf("нет предупреждения о невалидном ответе: %v", warnings)
	}
	if !strings.Contains(err.Error(), "невалидный JSON") {
		t.Fatalf("ошибка должна содержать причину последней неудачи: %v", err)
	}
}

func TestExtractRespectsTimeout(t *testing.T) {
	client := &fakeLLM{block: true}
	extractor := NewExtractor(client)
	extractor.Timeout = 20 * time.Millisecond
	_, _, err := extractor.Extract(context.Background(), []Page{{Number: 1, Text: pageOneText}})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("ожидали ErrTimeout, получили %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка должна оборачивать context.DeadlineExceeded: %v", err)
	}
	if !strings.Contains(err.Error(), "таймаут") {
		t.Fatalf("понятный текст таймаута: %v", err)
	}
}

func TestExtractRespectsMaxPages(t *testing.T) {
	client := &fakeLLM{}
	extractor := NewExtractor(client)
	extractor.MaxPages = 2
	_, _, err := extractor.Extract(context.Background(), []Page{
		{Number: 1, Text: pageOneText},
		{Number: 2, Text: pageTwoText},
		{Number: 3, Text: pageThreeText},
	})
	if err == nil || !strings.Contains(err.Error(), "лимит") {
		t.Fatalf("ожидали ошибку про лимит страниц, получили %v", err)
	}
	if len(client.calls()) != 0 {
		t.Fatalf("при превышении MaxPages модель не должна вызываться: %v", client.calls())
	}
}

func TestExtractChunkDoesNotSplitPage(t *testing.T) {
	longPage := strings.Repeat("Длинная страница про компетенции. ", 10)
	client := &fakeLLM{answers: []string{
		answerFor("УК-1", "Длинная компетенция", "universal", "knowledge", "знание", 1, "Длинная страница про компетенции"),
		answerFor("ПК-3", "Вторая компетенция", "professional", "knowledge", "знание", 2, "Способен проектировать базы данных"),
	}}
	extractor := NewExtractor(client)
	extractor.MaxCharsPerChunk = 50
	extractor.Concurrency = 1 // проверяем порядок запросов, а не пул
	program, _, err := extractor.Extract(context.Background(), []Page{
		{Number: 1, Text: longPage},
		{Number: 2, Text: pageThreeText},
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(program.Competencies) != 2 {
		t.Fatalf("компетенции: %+v", program.Competencies)
	}
	calls := client.calls()
	if len(calls) != 2 {
		t.Fatalf("страница не должна разрываться: вызовов %d", len(calls))
	}
	if !strings.Contains(calls[0], longPage) {
		t.Fatal("первый чанк должен содержать страницу целиком")
	}
	if strings.Contains(calls[0], pageThreeText) {
		t.Fatal("вторая страница не должна попасть в первый чанк")
	}
}

func TestExtractWithoutClient(t *testing.T) {
	_, _, err := NewExtractor(nil).Extract(context.Background(), []Page{{Number: 1, Text: pageOneText}})
	if !errors.Is(err, ErrNoClient) {
		t.Fatalf("ожидали ErrNoClient, получили %v", err)
	}
}

func TestExtractEmptyPagesIsNoTextLayer(t *testing.T) {
	_, _, err := NewExtractor(&fakeLLM{}).Extract(context.Background(), []Page{{Number: 1, Text: "   "}})
	if !errors.Is(err, ErrNoTextLayer) {
		t.Fatalf("ожидали ErrNoTextLayer, получили %v", err)
	}
}

// TestExtractLLMErrorReasonInFinalError — частая ошибка провайдера (413, 500,
// context length) должна попадать в итоговую ошибку и предупреждения, а не
// заменяться общим «не удалось извлечь компетенции».
func TestExtractLLMErrorReasonInFinalError(t *testing.T) {
	client := &fakeLLM{err: errors.New("upstream returned status 413")}
	_, warnings, err := NewExtractor(client).Extract(context.Background(), []Page{{Number: 1, Text: pageOneText}})
	if !errors.Is(err, ErrNoCompetencies) {
		t.Fatalf("ожидали ErrNoCompetencies, получили %v", err)
	}
	if !strings.Contains(err.Error(), "413") {
		t.Fatalf("итоговая ошибка без причины последней неудачи: %v", err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "413") {
		t.Fatalf("в предупреждениях нет причины: %v", warnings)
	}
}

func TestSystemPromptEmbedded(t *testing.T) {
	prompt, err := SystemPrompt()
	if err != nil {
		t.Fatalf("SystemPrompt: %v", err)
	}
	for _, marker := range []string{"competencies", "citations", "page", "УК"} {
		if !strings.Contains(prompt, marker) {
			t.Errorf("в промпте нет %q", marker)
		}
	}
}

// hasWarning сообщает, есть ли предупреждение с точным текстом want.
func hasWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}

// promptTextChars считает размер текста страниц в пользовательском запросе:
// так фейковая модель решает, помещается ли чанк в её лимит.
func promptTextChars(prompt string) int {
	total := 0
	rest := prompt
	for {
		start := strings.Index(rest, "=== Страница ")
		if start < 0 {
			return total
		}
		lineEnd := strings.Index(rest[start:], "\n")
		if lineEnd < 0 {
			return total
		}
		textStart := start + lineEnd + 1
		end := strings.Index(rest[textStart:], "\n\n")
		if end < 0 {
			total += len([]rune(rest[textStart:]))
			return total
		}
		total += len([]rune(rest[textStart : textStart+end]))
		rest = rest[textStart+end+2:]
	}
}

// TestExtractSplitsChunkOnProviderError — первый запрос получает 413, после
// деления пополам обе половины отвечают валидным JSON; результаты объединяются.
func TestExtractSplitsChunkOnProviderError(t *testing.T) {
	pageText := strings.Repeat("Способен анализировать информацию. ", 200)
	var calls int32
	client := &fakeLLM{}
	client.reply = func(string) (string, error) {
		number := atomic.AddInt32(&calls, 1)
		if number == 1 {
			return "", errors.New("upstream returned status 413")
		}
		code, competencyType := "УК-1", "universal"
		if number == 3 {
			code, competencyType = "ОПК-2", "general_professional"
		}
		return answerFor(code, "Способен анализировать информацию", competencyType,
			"knowledge", "знание", 1, "Способен анализировать информацию"), nil
	}
	extractor := NewExtractor(client)
	extractor.MaxCharsPerChunk = 20000
	extractor.Logger = testLogger
	program, warnings, err := extractor.Extract(context.Background(), []Page{{Number: 1, Text: pageText}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got := len(client.calls()); got != 3 {
		t.Fatalf("ожидали 3 запроса (исходный + две половины), получили %d", got)
	}
	if len(program.Competencies) != 2 {
		t.Fatalf("результаты половин должны объединяться: %+v", program.Competencies)
	}
	if !hasWarning(warnings, SplitChunkWarning) {
		t.Fatalf("нет предупреждения %q: %v", SplitChunkWarning, warnings)
	}
}

// TestExtractSplitsRecursivelyUntilChunkFits — модель принимает только чанки до
// 4000 символов, поэтому 12k-чанк делится дважды, а результаты всех листьев
// объединяются по коду компетенции.
func TestExtractSplitsRecursivelyUntilChunkFits(t *testing.T) {
	pageText := strings.Repeat("Способен анализировать информацию. ", 320)
	var successes int32
	client := &fakeLLM{}
	client.reply = func(prompt string) (string, error) {
		if promptTextChars(prompt) > 4000 {
			return "", errors.New("upstream returned status 413")
		}
		number := atomic.AddInt32(&successes, 1)
		return fmt.Sprintf(`{"competencies":[{"code":"УК-1","title":"Способен анализировать информацию",`+
			`"type":"universal","ksa":[{"kind":"knowledge","title":"знание %d"}],`+
			`"citations":[{"page":1,"quote":"Способен анализировать информацию"}]}]}`, number), nil
	}
	extractor := NewExtractor(client)
	extractor.MaxCharsPerChunk = 20000
	extractor.Logger = testLogger
	program, warnings, err := extractor.Extract(context.Background(), []Page{{Number: 1, Text: pageText}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// 12k -> 2x6k (отказ) -> 4x3k (успех): 1 + 2 + 4 запроса.
	if got := len(client.calls()); got != 7 {
		t.Fatalf("ожидали 7 запросов, получили %d", got)
	}
	if len(program.Competencies) != 1 {
		t.Fatalf("компетенции: %+v", program.Competencies)
	}
	if len(program.Competencies[0].KSA) != 4 {
		t.Fatalf("KSA всех листьев должны объединиться: %+v", program.Competencies[0].KSA)
	}
	if !hasWarning(warnings, SplitChunkWarning) {
		t.Fatalf("нет предупреждения %q: %v", SplitChunkWarning, warnings)
	}
}

// TestExtractSplitDepthLimit — при постоянном отказе деление останавливается на
// четырёх уровнях (1+2+4+8+16 запросов), ошибка содержит 413.
func TestExtractSplitDepthLimit(t *testing.T) {
	pageText := strings.Repeat("Способен анализировать информацию. ", 900)
	client := &fakeLLM{err: errors.New("upstream returned status 413")}
	extractor := NewExtractor(client)
	extractor.MaxCharsPerChunk = 20000
	extractor.Logger = testLogger
	_, warnings, err := extractor.Extract(context.Background(), []Page{{Number: 1, Text: pageText}})
	if !errors.Is(err, ErrNoCompetencies) {
		t.Fatalf("ожидали ErrNoCompetencies, получили %v", err)
	}
	if !strings.Contains(err.Error(), "413") {
		t.Fatalf("ошибка без причины последней неудачи: %v", err)
	}
	if got := len(client.calls()); got != 31 {
		t.Fatalf("лимит глубины: ожидали 31 запрос (1+2+4+8+16), получили %d", got)
	}
	if !hasWarning(warnings, SplitChunkWarning) {
		t.Fatalf("нет предупреждения %q", SplitChunkWarning)
	}
}

// TestExtractOversizedResponseSplitsOrFails — локальный лимит ответа: слишком
// длинный ответ считается ошибкой, а маленький чанк уже не делится, поэтому
// причина попадает в итоговую ошибку.
func TestExtractOversizedResponseSplitsOrFails(t *testing.T) {
	longAnswer := `{"competencies":[` + strings.Repeat(" ", 100) + `]}`
	client := &fakeLLM{answers: []string{longAnswer}}
	extractor := NewExtractor(client)
	extractor.MaxResponseTokens = 10 // 40 символов
	_, warnings, err := extractor.Extract(context.Background(), []Page{{Number: 1, Text: pageOneText}})
	if !errors.Is(err, ErrNoCompetencies) {
		t.Fatalf("ожидали ErrNoCompetencies, получили %v", err)
	}
	if !strings.Contains(err.Error(), "ответ модели больше") {
		t.Fatalf("ошибка без причины превышения ответа: %v", err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "ответ модели больше") {
		t.Fatalf("предупреждения: %v", warnings)
	}
}

// TestExtractAppliesResponseTokenLimit — клиент, поддерживающий
// MaxResponseTokensSetter, получает лимит ответа из адаптера.
func TestExtractAppliesResponseTokenLimit(t *testing.T) {
	answer := answerFor("УК-1", "Способен анализировать информацию", "universal",
		"knowledge", "знание", 1, quoteOne)
	client := &fakeLLM{answers: []string{answer}}
	extractor := NewExtractor(client)
	extractor.Logger = testLogger
	if _, _, err := extractor.Extract(context.Background(), []Page{{Number: 1, Text: pageOneText}}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got := client.responseTokens(); got != DefaultMaxResponseTokens {
		t.Fatalf("клиент должен получить лимит ответа %d, получил %d", DefaultMaxResponseTokens, got)
	}
}

// TestNewExtractorDefaults — базовый чанк уменьшен до 6000 символов, чтобы
// провайдер с небольшим контекстом не отвечал 413 на первый же запрос.
func TestNewExtractorDefaults(t *testing.T) {
	t.Setenv("FGOS_CONCURRENCY", "")
	extractor := NewExtractor(&fakeLLM{})
	if extractor.MaxCharsPerChunk != 6000 {
		t.Fatalf("MaxCharsPerChunk = %d, ожидали 6000", extractor.MaxCharsPerChunk)
	}
	if extractor.MaxResponseTokens != DefaultMaxResponseTokens {
		t.Fatalf("MaxResponseTokens = %d, ожидали %d", extractor.MaxResponseTokens, DefaultMaxResponseTokens)
	}
	if extractor.Concurrency != DefaultConcurrency {
		t.Fatalf("Concurrency = %d, ожидали %d", extractor.Concurrency, DefaultConcurrency)
	}
	if extractor.Timeout != DefaultTimeout {
		t.Fatalf("Timeout = %s", extractor.Timeout)
	}
}

// TestExtractAddsOCRWarnings — страницы, распознанные OCR, отмечаются в
// предупреждениях черновика; предупреждение переживает нарезку на чанки.
func TestExtractAddsOCRWarnings(t *testing.T) {
	answer := answerFor("УК-1", "Способен анализировать информацию", "universal",
		"knowledge", "знание", 1, quoteOne)
	client := &fakeLLM{answers: []string{answer}}
	extractor := NewExtractor(client)
	extractor.Logger = testLogger
	_, warnings, err := extractor.Extract(context.Background(), []Page{
		{Number: 1, Text: pageOneText, OCR: true},
		{Number: 2, Text: pageTwoText},
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !hasWarning(warnings, OCRPageWarning(1)) {
		t.Fatalf("нет предупреждения об OCR страницы 1: %v", warnings)
	}
	if hasWarning(warnings, OCRPageWarning(2)) {
		t.Fatalf("страница 2 не распознана OCR: %v", warnings)
	}

	// Предупреждения об OCR возвращаются и при ошибке лимита страниц.
	limited := NewExtractor(&fakeLLM{})
	limited.MaxPages = 1
	_, warnings, err = limited.Extract(context.Background(), []Page{
		{Number: 1, Text: pageOneText, OCR: true},
		{Number: 2, Text: pageTwoText},
	})
	if err == nil {
		t.Fatal("ожидали ошибку лимита страниц")
	}
	if !hasWarning(warnings, OCRPageWarning(1)) {
		t.Fatalf("OCR-предупреждение потерялось при ошибке лимита: %v", warnings)
	}
}

// promptPage возвращает номер и текст первой страницы пользовательского
// запроса: фейковая модель строит ответ, привязанный к странице, а не к порядку
// вызовов (при параллельности он недетерминирован).
func promptPage(prompt string) (int, string, bool) {
	const marker = "=== Страница "
	start := strings.Index(prompt, marker)
	if start < 0 {
		return 0, "", false
	}
	rest := prompt[start+len(marker):]
	end := strings.Index(rest, " ===")
	if end < 0 {
		return 0, "", false
	}
	number, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil {
		return 0, "", false
	}
	lineEnd := strings.Index(rest[end:], "\n")
	if lineEnd < 0 {
		return 0, "", false
	}
	body := rest[end+lineEnd+1:]
	if stop := strings.Index(body, "\n\n"); stop >= 0 {
		body = body[:stop]
	}
	return number, body, true
}

// answerForPromptPage возвращает валидный JSON, привязанный к странице из
// запроса: код, заголовок и цитата взяты из её текста.
func answerForPromptPage(prompt string) (string, error) {
	number, text, ok := promptPage(prompt)
	if !ok {
		return "", errors.New("в запросе нет страницы")
	}
	quote := strings.TrimSpace(text)
	if runes := []rune(quote); len(runes) > 40 {
		quote = string(runes[:40])
	}
	quoteJSON, err := json.Marshal(quote)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"competencies":[{"code":"УК-%d","title":"Компетенция страницы %d",`+
		`"type":"universal","ksa":[{"kind":"knowledge","title":"знание %d"}],`+
		`"citations":[{"page":%d,"quote":%s}]}]}`, number, number, number, number, quoteJSON), nil
}

// testPages создаёт страницы с коротким уникальным текстом: при лимите 60
// символов каждая страница становится отдельным чанком.
func testPages(count int) []Page {
	pages := make([]Page, 0, count)
	for number := 1; number <= count; number++ {
		pages = append(pages, Page{
			Number: number,
			Text:   fmt.Sprintf("Способен выполнять деятельность %d на уровне стандарта.", number),
		})
	}
	return pages
}

func programCodes(program ingestion.EducationProgram) []string {
	codes := make([]string, 0, len(program.Competencies))
	for _, competency := range program.Competencies {
		codes = append(codes, competency.Code)
	}
	return codes
}

// TestConcurrencyFromEnv — FGOS_CONCURRENCY: 1 = последовательно, значение по
// умолчанию 3, больше 6 ограничивается.
func TestConcurrencyFromEnv(t *testing.T) {
	cases := []struct {
		value string
		want  int
	}{
		{"", DefaultConcurrency},
		{"1", 1},
		{"4", 4},
		{"6", MaxConcurrency},
		{"7", MaxConcurrency},
		{"100", MaxConcurrency},
		{"0", DefaultConcurrency},
		{"-3", DefaultConcurrency},
		{"abc", DefaultConcurrency},
	}
	for _, testCase := range cases {
		t.Run(fmt.Sprintf("value=%q", testCase.value), func(t *testing.T) {
			t.Setenv("FGOS_CONCURRENCY", testCase.value)
			extractor := NewExtractor(&fakeLLM{})
			if extractor.Concurrency != testCase.want {
				t.Fatalf("Concurrency = %d, ожидали %d", extractor.Concurrency, testCase.want)
			}
		})
	}

	extractor := &Extractor{Concurrency: 99}
	if got := extractor.concurrency(); got != MaxConcurrency {
		t.Fatalf("concurrency() = %d, ожидали %d", got, MaxConcurrency)
	}
	extractor.Concurrency = 0
	if got := extractor.concurrency(); got != DefaultConcurrency {
		t.Fatalf("concurrency() = %d, ожидали %d", got, DefaultConcurrency)
	}
}

// TestExtractProcessesAllChunksConcurrently — пул обрабатывает все чанки, а
// результаты собираются в порядке страниц, а не в порядке завершения запросов.
func TestExtractProcessesAllChunksConcurrently(t *testing.T) {
	client := &fakeLLM{}
	client.reply = answerForPromptPage
	extractor := NewExtractor(client)
	extractor.MaxCharsPerChunk = 60
	extractor.Concurrency = 3
	extractor.Logger = testLogger

	program, warnings, err := extractor.Extract(context.Background(), testPages(6))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got := len(client.calls()); got != 6 {
		t.Fatalf("пул должен обработать все 6 чанков, вызовов: %d", got)
	}
	want := []string{"УК-1", "УК-2", "УК-3", "УК-4", "УК-5", "УК-6"}
	if got := programCodes(program); !reflect.DeepEqual(got, want) {
		t.Fatalf("порядок результатов: %v, ожидали %v", got, want)
	}
	if len(warnings) != 0 {
		t.Fatalf("предупреждения: %v", warnings)
	}
}

// TestExtractOneChunkErrorDoesNotLoseOthers — ошибка одного чанка не отменяет
// остальные: пул продолжает работу, успешные результаты остаются.
func TestExtractOneChunkErrorDoesNotLoseOthers(t *testing.T) {
	client := &fakeLLM{}
	client.reply = func(prompt string) (string, error) {
		number, _, ok := promptPage(prompt)
		if !ok {
			return "", errors.New("в запросе нет страницы")
		}
		if number == 2 {
			return "", errors.New("upstream returned status 413")
		}
		return answerForPromptPage(prompt)
	}
	extractor := NewExtractor(client)
	extractor.MaxCharsPerChunk = 60
	extractor.Concurrency = 3
	extractor.Logger = testLogger

	program, warnings, err := extractor.Extract(context.Background(), testPages(4))
	if err != nil {
		t.Fatalf("ошибка одного чанка не должна терять остальные: %v", err)
	}
	want := []string{"УК-1", "УК-3", "УК-4"}
	if got := programCodes(program); !reflect.DeepEqual(got, want) {
		t.Fatalf("результаты: %v, ожидали %v", got, want)
	}
	if joined := strings.Join(warnings, "\n"); !strings.Contains(joined, "413") {
		t.Fatalf("нет предупреждения о причине ошибки: %v", warnings)
	}
}

// TestExtractConcurrencyOneIsSequential — concurrency=1 работает как раньше:
// чанки уходят в модель строго по порядку страниц.
func TestExtractConcurrencyOneIsSequential(t *testing.T) {
	client := &fakeLLM{}
	client.reply = answerForPromptPage
	extractor := NewExtractor(client)
	extractor.MaxCharsPerChunk = 60
	extractor.Concurrency = 1
	extractor.Logger = testLogger

	if _, _, err := extractor.Extract(context.Background(), testPages(3)); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	calls := client.calls()
	if len(calls) != 3 {
		t.Fatalf("ожидали 3 вызова, получили %d", len(calls))
	}
	for index, prompt := range calls {
		number, _, ok := promptPage(prompt)
		if !ok || number != index+1 {
			t.Fatalf("вызов %d относится к странице %d, ожидали %d", index+1, number, index+1)
		}
	}
}

// TestExtractConcurrencyOneMatchesParallel — детерминированность: результаты и
// предупреждения при concurrency=1 и concurrency=3 совпадают.
func TestExtractConcurrencyOneMatchesParallel(t *testing.T) {
	run := func(concurrency int) ([]string, []string) {
		client := &fakeLLM{}
		client.reply = func(prompt string) (string, error) {
			number, _, ok := promptPage(prompt)
			if !ok {
				return "", errors.New("в запросе нет страницы")
			}
			switch number {
			case 3:
				return "извините, не могу", nil
			case 5:
				return "", errors.New("upstream returned status 413")
			}
			return answerForPromptPage(prompt)
		}
		extractor := NewExtractor(client)
		extractor.MaxCharsPerChunk = 60
		extractor.Concurrency = concurrency
		extractor.Logger = testLogger
		program, warnings, err := extractor.Extract(context.Background(), testPages(6))
		if err != nil {
			t.Fatalf("Extract(concurrency=%d): %v", concurrency, err)
		}
		return programCodes(program), warnings
	}

	sequentialCodes, sequentialWarnings := run(1)
	parallelCodes, parallelWarnings := run(3)
	if !reflect.DeepEqual(sequentialCodes, parallelCodes) {
		t.Fatalf("порядок компетенций: %v != %v", sequentialCodes, parallelCodes)
	}
	if !reflect.DeepEqual(sequentialWarnings, parallelWarnings) {
		t.Fatalf("предупреждения недетерминированы:\nsequential=%v\nparallel=%v", sequentialWarnings, parallelWarnings)
	}
}
