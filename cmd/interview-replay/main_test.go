package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func fastConfig() runConfig {
	return runConfig{pollInterval: 2 * time.Millisecond, requestTimeout: 2 * time.Second}
}

func TestParseOptionsDefaults(t *testing.T) {
	opts, err := parseOptions(nil, func(key string) string {
		if key == "MODEUS_TOKEN" {
			return " env-token "
		}
		return ""
	})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", opts.baseURL, defaultBaseURL)
	}
	if opts.profession != defaultProfession {
		t.Errorf("profession = %q, want %q", opts.profession, defaultProfession)
	}
	if opts.token != "env-token" {
		t.Errorf("token = %q, want token из MODEUS_TOKEN", opts.token)
	}
	if opts.answers != -1 {
		t.Errorf("answers = %d, want -1 (все ответы)", opts.answers)
	}
	if opts.timeout != defaultTimeout {
		t.Errorf("timeout = %s, want %s", opts.timeout, defaultTimeout)
	}
	if opts.dryRun || opts.jsonOutput {
		t.Errorf("dryRun/jsonOutput = %v/%v, want false", opts.dryRun, opts.jsonOutput)
	}
}

func TestParseOptionsOverrides(t *testing.T) {
	opts, err := parseOptions([]string{
		"-base-url", "http://example.test:9090/",
		"-profession", "  Инженер данных  ",
		"-token", "explicit-token",
		"-dry-run",
		"-answers", "3",
		"-timeout", "2m",
		"-json",
	}, func(string) string { return "env-token" })
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.baseURL != "http://example.test:9090" {
		t.Errorf("baseURL = %q, want без завершающего слэша", opts.baseURL)
	}
	if opts.profession != "Инженер данных" {
		t.Errorf("profession = %q, want обрезанную строку", opts.profession)
	}
	if opts.token != "explicit-token" {
		t.Errorf("token = %q, want явный флаг важнее env", opts.token)
	}
	if !opts.dryRun || !opts.jsonOutput {
		t.Errorf("dryRun/jsonOutput = %v/%v, want true", opts.dryRun, opts.jsonOutput)
	}
	if opts.answers != 3 || opts.timeout != 2*time.Minute {
		t.Errorf("answers/timeout = %d/%s", opts.answers, opts.timeout)
	}
}

func TestParseOptionsRejectsBadFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "неизвестный флаг", args: []string{"-unknown"}},
		{name: "лишний аргумент", args: []string{"positional"}},
		{name: "пустая профессия", args: []string{"-profession", "  "}},
		{name: "битый base-url", args: []string{"-base-url", "127.0.0.1:8080"}},
		{name: "отрицательные answers", args: []string{"-answers", "-2"}},
		{name: "нулевой timeout", args: []string{"-timeout", "0s"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := parseOptions(testCase.args, func(string) string { return "" }); err == nil {
				t.Fatalf("parseOptions(%v) вернул nil, хотели ошибку", testCase.args)
			}
		})
	}
}

func TestDryRunPrintsScenarioWithoutNetwork(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := runWithConfig([]string{"-base-url", server.URL, "-dry-run"}, &stdout, &stderr, fastConfig())
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, exitOK, stderr.String())
	}
	output := stdout.String()
	for _, expected := range []string{"Сценарий интервью: Аналитик данных", "[role]", "[function]", "[task]", "[action]", "Dry-run"} {
		if !strings.Contains(output, expected) {
			t.Errorf("dry-run не содержит %q:\n%s", expected, output)
		}
	}
	if requests != 0 {
		t.Errorf("dry-run сделал %d сетевых запросов, want 0", requests)
	}
}

func TestDryRunJSONIsMachineReadable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithConfig([]string{"-dry-run", "-json"}, &stdout, &stderr, fastConfig())
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, exitOK, stderr.String())
	}
	var parsed scenario
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("dry-run -json не разобрался: %v\n%s", err, stdout.String())
	}
	if err := parsed.validate(); err != nil {
		t.Fatalf("сценарий из dry-run невалиден: %v", err)
	}
}

func TestBuiltInScenarioIsValid(t *testing.T) {
	script, err := loadScenario()
	if err != nil {
		t.Fatalf("loadScenario: %v", err)
	}
	if err := script.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	answers := script.answers()
	if len(answers) < 5 {
		t.Fatalf("ответов %d, want минимум 5", len(answers))
	}
	replicas := len(script.Turns) * 2 // вопрос + ответ
	if replicas < 8 || replicas > 10 {
		t.Errorf("реплик %d, want 8–10", replicas)
	}
	levels := map[string]bool{}
	for index, turn := range script.Turns {
		levels[turn.Level] = true
		if strings.TrimSpace(turn.Question) == "" || strings.TrimSpace(turn.Answer) == "" {
			t.Errorf("вопрос/ответ %d пустые", index+1)
		}
	}
	for _, level := range []string{"role", "function", "task", "action"} {
		if !levels[level] {
			t.Errorf("нет уровня %q; дерево не покроет все четыре уровня", level)
		}
	}
}

func TestReplaySuccessReachesDone(t *testing.T) {
	script, err := loadScenario()
	if err != nil {
		t.Fatalf("loadScenario: %v", err)
	}
	backend := newFakeBackend(len(script.answers()), "done")
	server := httptest.NewServer(backend.handler())
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := runWithConfig([]string{
		"-base-url", server.URL,
		"-token", "test-token",
		"-timeout", "5s",
		"-json",
	}, &stdout, &stderr, fastConfig())
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, exitOK, stdout.String(), stderr.String())
	}

	var got report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("итог не JSON: %v\n%s", err, stdout.String())
	}
	if got.RunID != "run-1" || got.Status != "done" {
		t.Errorf("run_id/status = %q/%q, want run-1/done", got.RunID, got.Status)
	}
	if got.Nodes != 3 || got.Evidences != 4 {
		t.Errorf("nodes/evidences = %d/%d, want 3/4", got.Nodes, got.Evidences)
	}
	if got.Conflicts == nil || *got.Conflicts != 2 {
		t.Errorf("conflicts = %v, want 2", got.Conflicts)
	}
	if got.AnswersSent != len(script.answers()) {
		t.Errorf("answers_sent = %d, want %d", got.AnswersSent, len(script.answers()))
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.posted) != len(script.answers()) {
		t.Fatalf("backend получил %d ответов, want %d", len(backend.posted), len(script.answers()))
	}
	for index, answer := range script.answers() {
		if backend.posted[index] != answer {
			t.Errorf("ответ %d отправлен как %q, want %q", index+1, backend.posted[index], answer)
		}
	}
	if backend.authHeader != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", backend.authHeader)
	}
	if !backend.createdAsChat {
		t.Error("run создан не с type=chat")
	}
}

func TestReplayFailedPrintsRetryHint(t *testing.T) {
	script, err := loadScenario()
	if err != nil {
		t.Fatalf("loadScenario: %v", err)
	}
	backend := newFakeBackend(len(script.answers()), "failed")
	backend.treeBody = map[string]any{"nodes": []any{}}
	backend.conflictsStatus = http.StatusForbidden
	server := httptest.NewServer(backend.handler())
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := runWithConfig([]string{
		"-base-url", server.URL,
		"-token", "test-token",
		"-timeout", "5s",
	}, &stdout, &stderr, fastConfig())
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d (failed — не финальный провал сценария); stdout=%s", code, exitFailure, stdout.String())
	}
	output := stdout.String()
	for _, expected := range []string{
		"статус:      failed",
		"нажмите «Повторить анализ»",
		"POST /runs/run-1/retry",
		"ответы и история сохранены",
		"н/д (эндпоинт доступен только роли admin)",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("вывод не содержит %q:\n%s", expected, output)
		}
	}
	if !strings.Contains(stderr.String(), "ответ 5/5 отправлен") {
		t.Errorf("progress не показывает отправку ответов:\n%s", stderr.String())
	}
}

func TestReplayMissingTokenExitsThree(t *testing.T) {
	t.Setenv("MODEUS_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := runWithConfig([]string{"-base-url", "http://127.0.0.1:1"}, &stdout, &stderr, fastConfig())
	if code != exitNoToken {
		t.Fatalf("exit code = %d, want %d", code, exitNoToken)
	}
	if !strings.Contains(stderr.String(), "MODEUS_TOKEN") {
		t.Errorf("stderr не подсказывает про MODEUS_TOKEN:\n%s", stderr.String())
	}
}

func TestReplayNetworkErrorExitsTwo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()

	var stdout, stderr bytes.Buffer
	code := runWithConfig([]string{
		"-base-url", baseURL,
		"-token", "test-token",
		"-timeout", "2s",
	}, &stdout, &stderr, fastConfig())
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d; stdout=%s", code, exitFailure, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ошибка:") {
		t.Errorf("нет человекочитаемой ошибки:\n%s", stdout.String())
	}
}

func TestReplayAnswersLimitSendsOnlyRequested(t *testing.T) {
	backend := newFakeBackend(2, "done")
	server := httptest.NewServer(backend.handler())
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := runWithConfig([]string{
		"-base-url", server.URL,
		"-token", "test-token",
		"-timeout", "5s",
		"-answers", "2",
	}, &stdout, &stderr, fastConfig())
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, exitOK, stdout.String(), stderr.String())
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.posted) != 2 {
		t.Fatalf("отправлено %d ответов, want 2", len(backend.posted))
	}
}

// fakeBackend воспроизводит контракт chat-интервью: приветствие появляется не
// сразу (CLI обязан поллить), после каждого ответа агент «думает» один poll и
// только затем задаёт следующий вопрос. Финальный статус — done или failed.
type fakeBackend struct {
	mu sync.Mutex

	totalAnswers    int
	terminal        string
	treeBody        any
	conflictsBody   any
	conflictsStatus int

	messagePolls      int
	greetingPolls     int
	greetingSent      bool
	questionPolls     int
	questionPollLimit int
	messages          []chatMessage
	posted            []string
	authHeader        string
	createdAsChat     bool
}

func newFakeBackend(totalAnswers int, terminal string) *fakeBackend {
	return &fakeBackend{
		totalAnswers:      totalAnswers,
		terminal:          terminal,
		greetingPolls:     1,
		questionPollLimit: 2,
		treeBody: map[string]any{
			"nodes": []any{
				map[string]any{"id": "n-role", "type": "role", "evidence": []any{map[string]any{"id": "e1"}}},
				map[string]any{"id": "n-func", "type": "function", "evidence": []any{map[string]any{"id": "e2"}, map[string]any{"id": "e3"}}},
				map[string]any{"id": "n-task", "type": "task", "evidence": []any{map[string]any{"id": "e4"}}},
			},
		},
		conflictsBody: []any{map[string]any{"id": "c1"}, map[string]any{"id": "c2"}},
	}
}

func (b *fakeBackend) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/runs", b.handleCreateRun)
	mux.HandleFunc("GET /api/v1/runs/run-1/messages", b.handleMessages)
	mux.HandleFunc("POST /api/v1/runs/run-1/messages", b.handlePostMessage)
	mux.HandleFunc("GET /api/v1/runs/run-1/tree", func(w http.ResponseWriter, _ *http.Request) {
		b.mu.Lock()
		body := b.treeBody
		b.mu.Unlock()
		writeJSON(w, http.StatusOK, body)
	})
	mux.HandleFunc("GET /api/v1/runs/run-1/conflicts", func(w http.ResponseWriter, _ *http.Request) {
		b.mu.Lock()
		status, body := b.conflictsStatus, b.conflictsBody
		b.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		writeJSON(w, status, body)
	})
	mux.HandleFunc("GET /api/v1/runs/run-1", b.handleGetRun)
	return mux
}

func (b *fakeBackend) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profession string `json:"profession"`
		Type       string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "BAD_REQUEST", "message": err.Error()})
		return
	}
	b.mu.Lock()
	b.authHeader = r.Header.Get("Authorization")
	b.createdAsChat = body.Type == "chat" && strings.TrimSpace(body.Profession) != ""
	b.mu.Unlock()
	writeJSON(w, http.StatusCreated, runInfo{ID: "run-1", Status: "running", Profession: body.Profession})
}

func (b *fakeBackend) handleGetRun(w http.ResponseWriter, _ *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	status := "running"
	if len(b.posted) >= b.totalAnswers {
		status = b.terminal
	}
	writeJSON(w, http.StatusOK, runInfo{ID: "run-1", Status: status, Profession: defaultProfession})
}

func (b *fakeBackend) handleMessages(w http.ResponseWriter, _ *http.Request) {
	b.mu.Lock()
	b.messagePolls++
	if !b.greetingSent && b.messagePolls > b.greetingPolls {
		b.messages = append(b.messages, chatMessage{ID: "m-greeting", Role: "agent", Content: "Здравствуйте! Начнём интервью."})
		b.greetingSent = true
	}
	if b.greetingSent && len(b.messages) > 0 {
		last := b.messages[len(b.messages)-1]
		if last.Role == "user" && len(b.posted) < b.totalAnswers {
			b.questionPolls++
			if b.questionPolls >= b.questionPollLimit {
				b.questionPolls = 0
				b.messages = append(b.messages, chatMessage{
					ID:      fmt.Sprintf("m-question-%d", len(b.posted)),
					Role:    "agent",
					Content: fmt.Sprintf("Вопрос интервьюера %d", len(b.posted)+1),
				})
			}
		}
	}
	history := append([]chatMessage(nil), b.messages...)
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, history)
}

func (b *fakeBackend) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "BAD_REQUEST", "message": err.Error()})
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "BAD_REQUEST", "message": "content is required"})
		return
	}
	b.mu.Lock()
	b.posted = append(b.posted, body.Content)
	b.messages = append(b.messages, chatMessage{
		ID:      fmt.Sprintf("m-answer-%d", len(b.posted)),
		Role:    "user",
		Content: body.Content,
	})
	b.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func TestDescribeError(t *testing.T) {
	if got := describeError(&apiError{StatusCode: 401, Message: "bad token"}, time.Second); !strings.Contains(got, "HTTP 401") {
		t.Errorf("describeError(401) = %q", got)
	}
	timeoutErr := fmt.Errorf("ожидание: %w", context.DeadlineExceeded)
	if got := describeError(timeoutErr, time.Minute); !strings.Contains(got, "не дождались") {
		t.Errorf("describeError(timeout) = %q", got)
	}
}
