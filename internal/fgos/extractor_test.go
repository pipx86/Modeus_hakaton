package fgos

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/llm"
)

const (
	pageOneText   = "УК-1. Способен осуществлять поиск и критический анализ информации.\nЗнать: методы системного анализа.\n"
	pageTwoText   = "ОПК-2. Способен разрабатывать программное обеспечение.\nЗнать: основы алгоритмизации.\n"
	pageThreeText = "ПК-3. Способен проектировать базы данных.\nЗнать: модели данных.\n"
)

const quoteOne = "Способен осуществлять поиск и критический анализ информации"
const quoteTwo = "Способен разрабатывать программное обеспечение"

// fakeLLM — llm.ChatClient без сети. Отвечает заранее заданными строками по
// порядку вызовов; block эмулирует зависшую модель до отмены контекста.
type fakeLLM struct {
	mu      sync.Mutex
	answers []string
	err     error
	block   bool
	users   []string
	systems []string
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
	if callErr != nil {
		return "", callErr
	}
	return answer, nil
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
	if len(warnings) != 0 {
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

func TestExtractLLMErrorIsWrapped(t *testing.T) {
	client := &fakeLLM{err: errors.New("upstream 500")}
	_, _, err := NewExtractor(client).Extract(context.Background(), []Page{{Number: 1, Text: pageOneText}})
	if err == nil || !strings.Contains(err.Error(), "LLM не вернул ответ") {
		t.Fatalf("ожидали ошибку модели, получили %v", err)
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
