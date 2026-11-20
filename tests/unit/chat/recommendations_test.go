package chat_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/analyst"
	"activity-descriptor/internal/agent/interviewer"
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

const interviewAnswer = "Формирую SQL-отчёты и проверяю данные каждый день."

func TestFallbackRecommendationsSelectGroundedTemplates(t *testing.T) {
	session := domain.InterviewSession{
		Profession: "Аналитик данных",
		Questions: []domain.Question{
			{ID: "role", Level: domain.QuestionRole},
			{ID: "function", Level: domain.QuestionFunction},
			{ID: "task", Level: domain.QuestionTask},
			{ID: "action", Level: domain.QuestionAction},
			{ID: "vague", Level: domain.QuestionTask},
		},
		Answers: []domain.Answer{
			{QuestionID: "role", Text: "Отвечаю за подготовку отчётов для команды продаж."},
			{QuestionID: "function", Text: "Собираю данные и готовлю аналитические отчёты."},
			{QuestionID: "task", Text: "Проверяю SQL-запросы перед выпуском отчёта."},
			{QuestionID: "action", Text: "Использую скрипт для автоматизации ежедневной проверки."},
			{QuestionID: "vague", Text: "что-то делаю", Vague: true},
		},
	}
	got := interviewer.FallbackRecommendationsMessage(session)
	for _, want := range []string{"Резервные рекомендации", "Аналитик данных", "границы ответственности", "регулярность", "входные данные", "чек-лист", "полноты, точности", "автоматизируйте", "критерии приёмки", "обратной связи"} {
		if !strings.Contains(got, want) {
			t.Errorf("нет рекомендации %q в %q", want, got)
		}
	}
	if strings.Contains(got, "что-то делаю") || strings.Contains(got, "ИИ подготовил") {
		t.Fatalf("необоснованный или ложный текст: %q", got)
	}
	if again := interviewer.FallbackRecommendationsMessage(session); got != again {
		t.Fatal("повторный вызов изменил список или порядок рекомендаций")
	}
}

func TestFallbackRecommendationsSparseProfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session domain.InterviewSession
		want    string
		reject  string
	}{
		{"нет данных", domain.InterviewSession{}, "Опишите конкретную роль", "Для роли"},
		{"только профессия", domain.InterviewSession{Profession: "Повар"}, "Для роли «Повар»", "работы с данными"},
		{"общий ответ", domain.InterviewSession{Profession: "Повар", Questions: []domain.Question{{ID: "q", Level: domain.QuestionTask}}, Answers: []domain.Answer{{QuestionID: "q", Text: "что-то делаю", Vague: true}}}, "недостаточно для более точных", "«что-то делаю»"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := interviewer.FallbackRecommendationsMessage(tc.session)
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.reject) {
				t.Fatalf("неверный выбор при скудных данных: %q", got)
			}
		})
	}
}

func TestFallbackRecommendationsTopicSelection(t *testing.T) {
	for _, tc := range []struct {
		answer string
		want   string
		reject string
	}{
		{"Настраиваю скрипты автоматизации для развёртывания сервиса.", "автоматизируйте", "полноты, точности"},
		{"Сверяю документ с регламентом перед передачей результата.", "версию и источник требований", "автоматизируйте"},
		{"Планирую задачи по срокам и приоритету команды.", "зависимые шаги", "полноты, точности"},
		{"Собираю обратную связь клиента после выполнения задачи.", "заинтересованными людьми", "автоматизируйте"},
	} {
		t.Run(tc.answer, func(t *testing.T) {
			session := domain.InterviewSession{
				Profession: "DevOps-инженер",
				Questions:  []domain.Question{{ID: "q", Level: domain.QuestionTask}},
				Answers:    []domain.Answer{{QuestionID: "q", Text: tc.answer}},
			}
			got := interviewer.FallbackRecommendationsMessage(session)
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.reject) {
				t.Fatalf("неверные шаблоны: %q", got)
			}
		})
	}
}

type scriptedAnalyst struct {
	response        string
	err             error
	waitForDeadline bool
	calls           *atomic.Int32
}

func (c scriptedAnalyst) Chat(ctx context.Context, _ []llm.Message) (string, error) {
	if c.calls != nil {
		c.calls.Add(1)
	}
	if c.waitForDeadline {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return c.response, c.err
}

func analysisFinalizer(model llm.ChatClient, timeout time.Duration) chat.Finalizer {
	return func(ctx context.Context, _, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		worker, err := analyst.New(model, analyst.WithMaxRepairs(0))
		if err != nil {
			return domain.RunFailed, fmt.Errorf("%w: %v", chat.ErrAnalysisUnavailable, err)
		}
		transcript := make([]string, 0, len(session.Answers))
		for _, answer := range session.Answers {
			transcript = append(transcript, answer.Text)
		}
		out, err := worker.Analyze(ctx, agent.AnalystInput{Profession: profession, Transcript: strings.Join(transcript, "\n")})
		if err != nil {
			return domain.RunFailed, fmt.Errorf("%w: %v", chat.ErrAnalysisUnavailable, err)
		}
		if len(out.Entities) == 0 {
			return domain.RunFailed, fmt.Errorf("%w: empty analysis", chat.ErrAnalysisUnavailable)
		}
		return domain.RunDone, nil
	}
}

func TestRecommendationsThroughRunMessagesAPI(t *testing.T) {
	valid := `{"entities":[{"id":"role-1","type":"role","title":"Аналитик данных","evidence":[{"id":"ev-1","source_id":"interview-transcript","ref_id":"q-1","quote":"Формирую SQL-отчёты и проверяю данные каждый день."}]}]}`
	for _, tc := range []struct {
		name     string
		model    llm.ChatClient
		finalize chat.Finalizer
		timeout  time.Duration
		want     domain.RunStatus
		fallback bool
	}{
		{name: "успешный ответ ИИ", model: scriptedAnalyst{response: valid}, want: domain.RunDone},
		{name: "провайдер недоступен", model: scriptedAnalyst{err: errors.New("provider unavailable")}, want: domain.RunFailed, fallback: true},
		{name: "таймаут", model: scriptedAnalyst{waitForDeadline: true}, timeout: 5 * time.Millisecond, want: domain.RunFailed, fallback: true},
		{name: "пустой ответ", model: scriptedAnalyst{response: ""}, want: domain.RunFailed, fallback: true},
		{name: "некорректный JSON", model: scriptedAnalyst{response: "{"}, want: domain.RunFailed, fallback: true},
		{name: "ответ вне схемы", model: scriptedAnalyst{response: `{"entities":[]}`}, want: domain.RunFailed, fallback: true},
		{name: "иная ошибка", finalize: func(context.Context, string, string, domain.InterviewSession) (domain.RunStatus, error) {
			return domain.RunFailed, errors.New("save failed")
		}, want: domain.RunFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			if model, ok := tc.model.(scriptedAnalyst); ok {
				model.calls = &calls
				tc.model = model
			}
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				t.Fatal(err)
			}
			auth.SetSecretForTests(string(secret))
			token, err := auth.Issue("recommendation-user", auth.RoleUser, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			store := api.NewMemoryStore()
			finished := make(chan domain.RunStatus, 1)
			service := chat.NewService(interviewerFixture{}, func(runID string, status domain.RunStatus) {
				_ = store.SetStatus(runID, status)
				finished <- status
			})
			finalizer := tc.finalize
			if finalizer == nil {
				finalizer = analysisFinalizer(tc.model, tc.timeout)
			}
			service.SetFinalizer(finalizer)
			handler := api.NewServerWithChat(store, service).Routes()
			request := func(method, path, body string, authorized bool) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				if authorized {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}
			created := request(http.MethodPost, "/api/v1/runs", `{"profession":"Аналитик данных","type":"chat"}`, true)
			if created.Code != http.StatusCreated {
				t.Fatalf("создание исследования: %d %s", created.Code, created.Body.String())
			}
			var run domain.Run
			if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/runs/" + run.ID + "/messages"
			read := func() []chat.Message {
				rec := request(http.MethodGet, path, "", true)
				if rec.Code != http.StatusOK {
					t.Fatalf("чтение сообщений: %d %s", rec.Code, rec.Body.String())
				}
				var messages []chat.Message
				if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil {
					t.Fatal(err)
				}
				return messages
			}
			waitMessages := func(want int) []chat.Message {
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					messages := read()
					if len(messages) >= want {
						return messages
					}
					time.Sleep(5 * time.Millisecond)
				}
				t.Fatalf("сообщений меньше %d", want)
				return nil
			}
			for answer := range 6 {
				waitMessages(2 + answer*2)
				posted := request(http.MethodPost, path, `{"content":"`+interviewAnswer+`"}`, true)
				if posted.Code != http.StatusCreated {
					t.Fatalf("ответ %d: %d %s", answer, posted.Code, posted.Body.String())
				}
			}
			select {
			case status := <-finished:
				if status != tc.want {
					t.Fatalf("статус %s, ожидался %s", status, tc.want)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("исследование не завершилось")
			}
			messages := read()
			last := messages[len(messages)-1]
			if tc.fallback {
				if last.Role != "agent" || !strings.Contains(last.Content, "Ответы сохранены") || strings.Contains(last.Content, "Резервные рекомендации") || len(messages) != 14 {
					t.Fatalf("нет содержательного резервного сообщения: %+v", last)
				}
			} else if tc.want == domain.RunDone {
				if !strings.Contains(last.Content, "Интервью завершено") || strings.Contains(last.Content, "Резервные рекомендации") {
					t.Fatalf("успешный анализ заменён резервным: %+v", last)
				}
			} else if strings.Contains(last.Content, "Резервные рекомендации") {
				t.Fatal("сбой хранилища ошибочно показан как сбой ИИ")
			}
			if again := read(); len(again) != len(messages) || again[len(again)-1].ID != last.ID {
				t.Fatal("повторное чтение создало дубли сообщений")
			}
			if rec := request(http.MethodPost, path, `{"content":"повтор"}`, true); rec.Code != http.StatusConflict {
				t.Fatalf("повторный ответ: %d", rec.Code)
			}
			if rec := request(http.MethodGet, path, "", false); rec.Code != http.StatusUnauthorized {
				t.Fatalf("неавторизованное чтение: %d", rec.Code)
			}
			if tc.model != nil && calls.Load() != 1 {
				t.Fatalf("ИИ вызван %d раз вместо одного", calls.Load())
			}
		})
	}
}
