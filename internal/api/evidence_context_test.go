package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

// evidenceContextStub — заглушка хранилища контекста подтверждения.
type evidenceContextStub struct {
	Store
	item        evidenceContextItem
	messages    []chatTranscriptMessage
	err         error
	messagesErr error
	runID       string
	evidenceID  string
}

func (s *evidenceContextStub) getRunEvidenceContext(_ context.Context, runID, evidenceID string) (evidenceContextItem, error) {
	s.runID, s.evidenceID = runID, evidenceID
	return s.item, s.err
}

func (s *evidenceContextStub) listEvidenceTranscriptMessages(_ context.Context, _ string) ([]chatTranscriptMessage, error) {
	return s.messages, s.messagesErr
}

// evidenceContextUnsupportedStore не умеет отдавать контекст (501).
type evidenceContextUnsupportedStore struct{ Store }

// evidenceContextChatStub — chat-сервис с готовой историей для fallback-теста.
type evidenceContextChatStub struct {
	messages []chat.Message
	err      error
}

func (s *evidenceContextChatStub) Start(string, string) error { return nil }
func (s *evidenceContextChatStub) Messages(string) ([]chat.Message, error) {
	return s.messages, s.err
}
func (s *evidenceContextChatStub) Post(string, string) (chat.Message, error) {
	return chat.Message{}, nil
}
func (s *evidenceContextChatStub) Subscribe(string) (<-chan chat.Event, func(), error) {
	return nil, func() {}, nil
}

// transcriptMessages — короткий диалог для юнит-тестов сопоставления.
func transcriptMessages() []chatTranscriptMessage {
	return []chatTranscriptMessage{
		{ID: "msg-run-1", Role: "agent", Sequence: 1, Content: "Расскажите, чем вы занимаетесь?", CreatedAt: time.Unix(1, 0).UTC()},
		{ID: "msg-run-2", Role: "user", Sequence: 2, Content: "Каждый день пишу SQL-запросы и строю дашборды в Power BI", CreatedAt: time.Unix(2, 0).UTC()},
		{ID: "msg-run-3", Role: "agent", Sequence: 3, Content: "Какие инструменты используете?", CreatedAt: time.Unix(3, 0).UTC()},
		{ID: "msg-run-4", Role: "user", Sequence: 4, Content: "Использую PostgreSQL и Excel", CreatedAt: time.Unix(4, 0).UTC()},
	}
}

func TestMatchEvidenceToTranscriptRefStrategies(t *testing.T) {
	messages := transcriptMessages()
	tests := []struct {
		name         string
		refID        string
		quote        string
		wantMessage  string
		wantStrategy string
		wantContains bool
	}{
		{name: "message id", refID: "msg-run-4", quote: "PostgreSQL", wantMessage: "msg-run-4", wantStrategy: evidenceMatchStrategyMessageID, wantContains: true},
		{name: "message id other case", refID: "MSG-RUN-4", quote: "PostgreSQL", wantMessage: "msg-run-4", wantStrategy: evidenceMatchStrategyMessageID, wantContains: true},
		{name: "answer index", refID: "answer-2", quote: "PostgreSQL", wantMessage: "msg-run-4", wantStrategy: evidenceMatchStrategyAnswerIndex, wantContains: true},
		{name: "answer hyphen alias", refID: "a-1", quote: "SQL-запросы", wantMessage: "msg-run-2", wantStrategy: evidenceMatchStrategyAnswerIndex, wantContains: true},
		{name: "answer russian", refID: "ответ-1", quote: "SQL-запросы", wantMessage: "msg-run-2", wantStrategy: evidenceMatchStrategyAnswerIndex, wantContains: true},
		{name: "question index", refID: "q-2", quote: "инструменты", wantMessage: "msg-run-3", wantStrategy: evidenceMatchStrategyQuestionIndex, wantContains: true},
		{name: "sequence", refID: "msg-4", quote: "PostgreSQL", wantMessage: "msg-run-4", wantStrategy: evidenceMatchStrategySequence, wantContains: true},
		{name: "plain number", refID: "2", quote: "дашборды", wantMessage: "msg-run-2", wantStrategy: evidenceMatchStrategySequence, wantContains: true},
		{name: "quote fallback", refID: "unknown-ref", quote: "Использую PostgreSQL", wantMessage: "msg-run-4", wantStrategy: evidenceMatchStrategyQuote, wantContains: true},
		{name: "quote case insensitive", refID: "", quote: "POSTGRESQL И EXCEL", wantMessage: "msg-run-4", wantStrategy: evidenceMatchStrategyQuote, wantContains: true},
		{name: "ref resolves but quote elsewhere", refID: "answer-1", quote: "Использую PostgreSQL", wantMessage: "msg-run-4", wantStrategy: evidenceMatchStrategyQuote, wantContains: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			match, note := matchEvidenceToTranscript(test.refID, test.quote, messages)
			if match == nil {
				t.Fatalf("ожидали match, получили nil (note=%q)", note)
			}
			if match.MessageID != test.wantMessage || match.Strategy != test.wantStrategy || match.ContainsQuote != test.wantContains {
				t.Fatalf("match = %+v, ожидали message=%q strategy=%q contains=%v", match, test.wantMessage, test.wantStrategy, test.wantContains)
			}
			if note == "" {
				t.Fatal("note пуст")
			}
			// Детерминизм: повторный вызов возвращает то же самое.
			again, againNote := matchEvidenceToTranscript(test.refID, test.quote, messages)
			if !equalEvidenceMatch(again, match) || againNote != note {
				t.Fatalf("сопоставление не детерминировано: %+v/%q vs %+v/%q", again, againNote, match, note)
			}
		})
	}
}

func TestMatchEvidenceToTranscriptOffsets(t *testing.T) {
	messages := transcriptMessages()
	quote := "SQL-запросы"
	match, _ := matchEvidenceToTranscript("answer-1", quote, messages)
	if match == nil || !match.ContainsQuote || match.QuoteStart == 0 || match.QuoteEnd <= match.QuoteStart {
		t.Fatalf("ожидали точные смещения цитаты: %+v", match)
	}
	content := []rune(messages[1].Content)
	if got := string(content[match.QuoteStart:match.QuoteEnd]); got != quote {
		t.Fatalf("срез по смещениям = %q, ожидали %q", got, quote)
	}

	// Цитата с другими пробелами находится без точных смещений.
	messy := "Каждый   день пишу SQL-запросы"
	spaceMatch, _ := matchEvidenceToTranscript("", messy, messages)
	if spaceMatch == nil || !spaceMatch.ContainsQuote || spaceMatch.QuoteStart != 0 {
		t.Fatalf("нормализованное совпадение не должно давать смещения: %+v", spaceMatch)
	}
}

func TestMatchEvidenceToTranscriptNoMatch(t *testing.T) {
	messages := transcriptMessages()
	match, note := matchEvidenceToTranscript("answer-2", "совершенно другая цитата про маркетинг", messages)
	if match == nil {
		t.Fatalf("ref должен был сопоставиться с сообщением")
	}
	if match.ContainsQuote || match.Strategy != evidenceMatchStrategyAnswerIndex {
		t.Fatalf("ожидали ref-сообщение без цитаты: %+v", match)
	}
	if !strings.Contains(note, "цитата") {
		t.Fatalf("note должен объяснять отсутствие цитаты: %q", note)
	}

	none, note := matchEvidenceToTranscript("", "абракадабра", messages)
	if none != nil {
		t.Fatalf("ожидали nil для несопоставимой цитаты: %+v", none)
	}
	if note == "" {
		t.Fatal("note пуст для несопоставленной цитаты")
	}

	emptyMatch, emptyNote := matchEvidenceToTranscript("answer-1", "SQL", nil)
	if emptyMatch != nil || !strings.Contains(emptyNote, "сообщений") {
		t.Fatalf("пустой диалог: match=%+v note=%q", emptyMatch, emptyNote)
	}
}

func TestMatchEvidenceToTranscriptPrefersUserAndSequence(t *testing.T) {
	// Агент и пользователь содержат одну цитату; preference — реплика пользователя.
	messages := []chatTranscriptMessage{
		{ID: "a-1", Role: "agent", Sequence: 1, Content: "Вы говорили про SQL-запросы?"},
		{ID: "u-1", Role: "user", Sequence: 2, Content: "Да, пишу SQL-запросы ежедневно"},
	}
	match, _ := matchEvidenceToTranscript("", "SQL-запросы", messages)
	if match == nil || match.MessageID != "u-1" {
		t.Fatalf("ожидали реплику пользователя: %+v", match)
	}
	// Без реплики пользователя выбирается сообщение агента.
	match, _ = matchEvidenceToTranscript("", "SQL-запросы", messages[:1])
	if match == nil || match.MessageID != "a-1" {
		t.Fatalf("ожидали сообщение агента: %+v", match)
	}
}

func TestMatchEvidenceToTranscriptTokenOverlap(t *testing.T) {
	messages := []chatTranscriptMessage{
		{ID: "m-1", Role: "user", Sequence: 1, Content: "Ежедневно готовлю отчётность по продажам в Excel"},
		{ID: "m-2", Role: "user", Sequence: 2, Content: "Провожу собеседования и нанимаю сотрудников"},
	}
	match, _ := matchEvidenceToTranscript("", "отчётность по продажам готовлю", messages)
	if match == nil || match.MessageID != "m-1" || match.Strategy != evidenceMatchStrategyTokenOverlap || match.ContainsQuote {
		t.Fatalf("ожидали лексическое совпадение m-1: %+v", match)
	}
	if match.Score <= 0 || match.Score > 1 {
		t.Fatalf("score вне диапазона: %+v", match)
	}
}

func TestEvidenceContextHandlerAuth(t *testing.T) {
	stub := &evidenceContextStub{
		item:     evidenceContextItem{ID: "ev-1", Quote: "SQL-запросы", RefID: "answer-1"},
		messages: transcriptMessages(),
	}
	server := NewServer(stub)
	tests := []struct {
		role string
		want int
	}{
		{"admin", http.StatusOK},
		{"teacher", http.StatusOK},
		{"rop", http.StatusOK},
		{"student", http.StatusOK},
		{"guest", http.StatusOK},
		{"", http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run("role="+test.role, func(t *testing.T) {
			recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet,
				"/api/v1/runs/run-1/evidence/ev-1/context", test.role, "",
				map[string]string{"id": "run-1", "evidence_id": "ev-1"})
			if recorder.Code != test.want {
				t.Fatalf("роль %q: ожидали %d, получили %d (%s)", test.role, test.want, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestEvidenceContextHandlerSuccess(t *testing.T) {
	stub := &evidenceContextStub{
		item: evidenceContextItem{
			ID: "ev-1", NodeID: "node-1", NodeTitle: "Роль", SourceID: "src-1",
			SourceTitle: "Интервью", SourceURL: "https://example.org/interview",
			RefID: "answer-2", Quote: "PostgreSQL", Status: domain.EvidencePending,
		},
		messages: transcriptMessages(),
	}
	server := NewServer(stub)
	recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet,
		"/api/v1/runs/run-1/evidence/ev-1/context", "student", "",
		map[string]string{"id": "run-1", "evidence_id": "ev-1"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	if stub.runID != "run-1" || stub.evidenceID != "ev-1" {
		t.Fatalf("идентификаторы не переданы в хранилище: run=%q evidence=%q", stub.runID, stub.evidenceID)
	}
	response := decodeMentorJSON[evidenceContextResponse](t, recorder)
	if response.RunID != "run-1" || response.Evidence.ID != "ev-1" || response.Evidence.NodeTitle != "Роль" ||
		response.Evidence.SourceTitle != "Интервью" || response.Evidence.Status != domain.EvidencePending {
		t.Fatalf("контекст подтверждения потерян: %+v", response)
	}
	if len(response.Messages) != len(stub.messages) {
		t.Fatalf("сообщений %d, ожидали %d", len(response.Messages), len(stub.messages))
	}
	if response.Messages[1].Sequence != 2 || response.Messages[1].Role != "user" {
		t.Fatalf("sequence/role сообщения потеряны: %+v", response.Messages[1])
	}
	if response.Match == nil || response.Match.MessageID != "msg-run-4" ||
		response.Match.Strategy != evidenceMatchStrategyAnswerIndex || !response.Match.ContainsQuote {
		t.Fatalf("match = %+v", response.Match)
	}
	if response.Match.QuoteStart == 0 || response.Match.QuoteEnd <= response.Match.QuoteStart {
		t.Fatalf("ожидали смещения цитаты: %+v", response.Match)
	}
	if response.Note == "" {
		t.Fatal("note пуст")
	}
}

func TestEvidenceContextHandlerNoMatch(t *testing.T) {
	stub := &evidenceContextStub{
		item:     evidenceContextItem{ID: "ev-1", Quote: "цитата без совпадений", RefID: "answer-9"},
		messages: transcriptMessages(),
	}
	server := NewServer(stub)
	recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet,
		"/api/v1/runs/run-1/evidence/ev-1/context", "teacher", "",
		map[string]string{"id": "run-1", "evidence_id": "ev-1"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"match":null`) {
		t.Fatalf("match должен быть null: %s", recorder.Body.String())
	}
	response := decodeMentorJSON[evidenceContextResponse](t, recorder)
	if response.Match != nil || response.Note == "" || response.Messages == nil {
		t.Fatalf("неожиданный контекст: %+v", response)
	}
}

func TestEvidenceContextHandlerErrors(t *testing.T) {
	t.Run("missing path values", func(t *testing.T) {
		server := NewServer(&evidenceContextStub{})
		recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet, "/", "admin", "", map[string]string{"id": "run-1"})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("ожидали 400, получили %d (%s)", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("not found", func(t *testing.T) {
		server := NewServer(&evidenceContextStub{err: ErrNotFound})
		recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet, "/", "admin", "",
			map[string]string{"id": "run-1", "evidence_id": "ev-1"})
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("ожидали 404, получили %d (%s)", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("store error", func(t *testing.T) {
		server := NewServer(&evidenceContextStub{err: errors.New("boom")})
		recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet, "/", "admin", "",
			map[string]string{"id": "run-1", "evidence_id": "ev-1"})
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("ожидали 500, получили %d (%s)", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("messages error", func(t *testing.T) {
		server := NewServer(&evidenceContextStub{item: evidenceContextItem{ID: "ev-1"}, messagesErr: errors.New("boom")})
		recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet, "/", "admin", "",
			map[string]string{"id": "run-1", "evidence_id": "ev-1"})
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("ожидали 500, получили %d (%s)", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("unsupported store", func(t *testing.T) {
		server := NewServer(&evidenceContextUnsupportedStore{})
		recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet, "/", "admin", "",
			map[string]string{"id": "run-1", "evidence_id": "ev-1"})
		if recorder.Code != http.StatusNotImplemented {
			t.Fatalf("ожидали 501, получили %d (%s)", recorder.Code, recorder.Body.String())
		}
	})
}

// TestEvidenceContextChatFallback проверяет, что память+chat-сервис тоже
// отдают сообщения (in-memory demo): durable-таблицы там нет.
func TestEvidenceContextChatFallback(t *testing.T) {
	store := newMemStore()
	runID := zunSeedRun(t, store, "Аналитик данных",
		domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль"},
	)
	store.seedEvidence(domain.Evidence{
		ID: "ev-1", NodeID: "node-1", SourceID: "55555555-5555-5555-5555-555555555555",
		RefID: "answer-2", Quote: "Использую PostgreSQL", Status: domain.EvidencePending,
	}, runID)
	chatStub := &evidenceContextChatStub{messages: []chat.Message{
		{ID: "msg-1", Role: "agent", Sequence: 1, Content: "Какие инструменты используете?", CreatedAt: time.Unix(1, 0)},
		{ID: "msg-2", Role: "user", Sequence: 2, Content: "Использую PostgreSQL", CreatedAt: time.Unix(2, 0)},
	}}
	server := NewServerWithChat(store, chatStub)
	recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet, "/", "guest", "",
		map[string]string{"id": runID, "evidence_id": "ev-1"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	response := decodeMentorJSON[evidenceContextResponse](t, recorder)
	if len(response.Messages) != 2 || response.Messages[1].Sequence != 2 {
		t.Fatalf("сообщения чата не подхвачены: %+v", response.Messages)
	}
	if response.Match == nil || response.Match.MessageID != "msg-2" || !response.Match.ContainsQuote {
		t.Fatalf("match по answer-2 не найден: %+v", response.Match)
	}

	// Без chat-сервиса возвращается пустой список сообщений, а не ошибка.
	bare := newMemStore()
	bareRunID := zunSeedRun(t, bare, "Аналитик",
		domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль"})
	bare.seedEvidence(domain.Evidence{ID: "ev-1", NodeID: "node-1", SourceID: "src", Quote: "цитата", RefID: "answer-1"}, bareRunID)
	bareServer := NewServer(bare)
	noChat := doMentorHandler(t, bareServer.handleEvidenceContext, http.MethodGet, "/", "teacher", "",
		map[string]string{"id": bareRunID, "evidence_id": "ev-1"})
	if noChat.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", noChat.Code, noChat.Body.String())
	}
	bareResponse := decodeMentorJSON[evidenceContextResponse](t, noChat)
	if bareResponse.Messages == nil || len(bareResponse.Messages) != 0 || bareResponse.Match != nil {
		t.Fatalf("без чата ожидали пустые сообщения и match=null: %+v", bareResponse)
	}
}

// equalEvidenceMatch сравнивает два match-объекта (nil-safe).
func equalEvidenceMatch(left, right *evidenceTranscriptMatch) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

// TestEvidenceContextPostgres проверяет durable-историю чата после «рестарта»:
// сообщения читаются из run_chat_messages, цитата подсвечивается по sequence.
func TestEvidenceContextPostgres(t *testing.T) {
	pool := mentorFeatureTestPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	server := NewServer(store)

	source, err := store.CreateSource(domain.Source{Type: domain.SourceInterview, Title: "Evidence context: интервью",
		Content: "Я аналитик данных.\nИспользую PostgreSQL и Excel", SourceURL: "https://example.org/context"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun("Аналитик данных")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run.ID)
		pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1`, source.ID)
	})
	rootID := mentorFeatureTestUUID(t, pool)
	evidenceID := mentorFeatureTestUUID(t, pool)
	quote := "Использую PostgreSQL и Excel"
	nodes := []domain.Node{{
		ID: rootID, Type: domain.NodeRole, Title: "Аналитик данных",
		Evidence: []domain.Evidence{{ID: evidenceID, SourceID: source.ID, RefID: "answer-2", Quote: quote}},
	}}
	if err = store.SaveTree(ctx, run.ID, nodes); err != nil {
		t.Fatal(err)
	}
	dialog := []struct{ role, content string }{
		{"agent", "Расскажите, чем вы занимаетесь?"},
		{"user", "Я аналитик данных, строю отчёты"},
		{"agent", "Какие инструменты используете?"},
		{"user", quote},
	}
	for sequence, message := range dialog {
		if _, err = pool.Exec(ctx, `INSERT INTO run_chat_messages(id,run_id,role,content,sequence) VALUES($1,$2,$3,$4,$5)`,
			"msg-"+run.ID+"-"+strconv.Itoa(sequence+1), run.ID, message.role, message.content, sequence+1); err != nil {
			t.Fatal(err)
		}
	}

	recorder := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet,
		"/api/v1/runs/"+run.ID+"/evidence/"+evidenceID+"/context", "student", "",
		map[string]string{"id": run.ID, "evidence_id": evidenceID})
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	response := decodeMentorJSON[evidenceContextResponse](t, recorder)
	if response.Evidence.RefID != "answer-2" || response.Evidence.Quote != quote || response.Evidence.NodeTitle != "Аналитик данных" ||
		response.Evidence.SourceTitle != "Evidence context: интервью" {
		t.Fatalf("контекст подтверждения: %+v", response.Evidence)
	}
	if len(response.Messages) != len(dialog) {
		t.Fatalf("durable-сообщения: %+v", response.Messages)
	}
	for index, message := range response.Messages {
		if message.Sequence != index+1 || message.Role != dialog[index].role || message.Content != dialog[index].content {
			t.Fatalf("сообщение %d: %+v", index, message)
		}
	}
	if response.Match == nil || response.Match.Strategy != evidenceMatchStrategyAnswerIndex ||
		response.Match.Sequence != 4 || response.Match.MessageID != "msg-"+run.ID+"-4" || !response.Match.ContainsQuote {
		t.Fatalf("match: %+v", response.Match)
	}
	content := []rune(response.Messages[3].Content)
	if got := string(content[response.Match.QuoteStart:response.Match.QuoteEnd]); got != quote {
		t.Fatalf("подсветка цитаты = %q, ожидали %q", got, quote)
	}

	// Неизвестное подтверждение -> 404.
	unknown := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet,
		"/api/v1/runs/"+run.ID+"/evidence/"+mentorFeatureTestUUID(t, pool)+"/context", "student", "",
		map[string]string{"id": run.ID, "evidence_id": mentorFeatureTestUUID(t, pool)})
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("неизвестное подтверждение: ожидали 404, получили %d (%s)", unknown.Code, unknown.Body.String())
	}
	if malformed := doMentorHandler(t, server.handleEvidenceContext, http.MethodGet,
		"/api/v1/runs/run-1/evidence/ev-1/context", "student", "",
		map[string]string{"id": "run-1", "evidence_id": "ev-1"}); malformed.Code != http.StatusNotFound {
		t.Fatalf("невалидные id: ожидали 404, получили %d (%s)", malformed.Code, malformed.Body.String())
	}
}

func (s *evidenceContextStub) GetRun(id string) (*domain.Run, error) {
	return &domain.Run{ID: id}, nil
}
