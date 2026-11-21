// Command interview-replay прогоняет встроенный сценарий интервью через живой
// backend: создаёт chat-прогон, дожидается вопроса интервьюера, отправляет
// ответы «Аналитика данных» и печатает итог анализа (статус, узлы,
// обоснования, конфликты).
//
// Сценарий встроен в бинарь (scenario.json через go:embed): он не зависит от
// модели и сети, поэтому один и тот же диалог можно повторить после падения
// провайдера. -dry-run печатает сценарий без единого запроса.
//
// Exit codes: 0 — прогон завершён (done или degraded), 2 — прогон упал,
// ошибка сети/API или неверные флаги, 3 — нет токена (для не-dry-run).
// Падение анализа не является окончательным провалом сценария: CLI печатает
// подсказку про «Повторить анализ» и POST /runs/{id}/retry.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	exitOK      = 0
	exitFailure = 2
	exitNoToken = 3

	defaultBaseURL    = "http://127.0.0.1:8080"
	defaultProfession = "Аналитик данных"
	defaultTimeout    = 10 * time.Minute
)

// options — разобранные флаги команды.
type options struct {
	baseURL    string
	profession string
	token      string
	dryRun     bool
	answers    int
	timeout    time.Duration
	jsonOutput bool
}

// runConfig — настройки, которые тесты подменяют, чтобы не ждать реальные
// интервалы поллинга. В обычном запуске используются значения по умолчанию.
type runConfig struct {
	pollInterval   time.Duration
	requestTimeout time.Duration
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithConfig(args, stdout, stderr, runConfig{
		pollInterval:   time.Second,
		requestTimeout: 30 * time.Second,
	})
}

// parseOptions разбирает флаги и подставляет значения по умолчанию. Токен
// берётся из -token, иначе из MODEUS_TOKEN; пустой токен допустим только в
// dry-run (проверка выполняется в runWithConfig).
func parseOptions(args []string, getenv func(string) string) (options, error) {
	flags := flag.NewFlagSet("interview-replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var parsed options
	flags.StringVar(&parsed.baseURL, "base-url", defaultBaseURL, "адрес backend, например http://127.0.0.1:8080")
	flags.StringVar(&parsed.profession, "profession", defaultProfession, "профессия для интервью")
	flags.StringVar(&parsed.token, "token", "", "Bearer-токен (иначе берётся из MODEUS_TOKEN)")
	flags.BoolVar(&parsed.dryRun, "dry-run", false, "напечатать сценарий и выйти без сети")
	flags.IntVar(&parsed.answers, "answers", -1, "сколько ответов отправить (по умолчанию все)")
	flags.DurationVar(&parsed.timeout, "timeout", defaultTimeout, "ожидание финального статуса, например 10m")
	flags.BoolVar(&parsed.jsonOutput, "json", false, "машинный JSON вместо текста")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() > 0 {
		return options{}, fmt.Errorf("неожиданные аргументы: %s", strings.Join(flags.Args(), " "))
	}

	parsed.baseURL = strings.TrimRight(strings.TrimSpace(parsed.baseURL), "/")
	if parsed.baseURL == "" {
		return options{}, errors.New("base-url не задан")
	}
	endpoint, err := url.Parse(parsed.baseURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return options{}, fmt.Errorf("base-url должен быть http(s)-адресом, получено %q", parsed.baseURL)
	}
	parsed.profession = strings.TrimSpace(parsed.profession)
	if parsed.profession == "" {
		return options{}, errors.New("profession не может быть пустой")
	}
	if parsed.answers < -1 {
		return options{}, errors.New("answers должен быть -1 (все), 0 или положительным числом")
	}
	if parsed.timeout <= 0 {
		return options{}, errors.New("timeout должен быть положительным")
	}

	parsed.token = strings.TrimSpace(parsed.token)
	if parsed.token == "" {
		parsed.token = strings.TrimSpace(getenv("MODEUS_TOKEN"))
	}
	return parsed, nil
}

// runWithConfig — тестируемая точка входа: тесты передают короткий pollInterval
// и таймаут HTTP-запроса, не меняя поведение команды в проде.
func runWithConfig(args []string, stdout, stderr io.Writer, config runConfig) int {
	opts, err := parseOptions(args, os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintf(stderr, "interview-replay: %v\n", err)
		fmt.Fprintln(stderr, "usage: interview-replay [-base-url URL] [-profession NAME] [-token TOKEN] [-dry-run] [-answers N] [-timeout 10m] [-json]")
		return exitFailure
	}
	if config.pollInterval <= 0 {
		config.pollInterval = time.Second
	}
	if config.requestTimeout <= 0 {
		config.requestTimeout = 30 * time.Second
	}

	script, err := loadScenario()
	if err != nil {
		fmt.Fprintf(stderr, "interview-replay: %v\n", err)
		return exitFailure
	}
	if opts.dryRun {
		printDryRun(stdout, script, opts.jsonOutput)
		return exitOK
	}
	if opts.token == "" {
		fmt.Fprintln(stderr, "interview-replay: нужен токен: передайте -token или задайте MODEUS_TOKEN")
		fmt.Fprintln(stderr, "локальный JWT для стенда выдаёт: go run ./cmd/local-token -role admin")
		return exitNoToken
	}

	answers := script.answers()
	if opts.answers >= 0 {
		if opts.answers > len(answers) {
			fmt.Fprintf(stderr, "interview-replay: -answers %d больше, чем ответов в сценарии (%d)\n", opts.answers, len(answers))
			return exitFailure
		}
		answers = answers[:opts.answers]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	started := time.Now()
	client := newAPIClient(opts.baseURL, opts.token, config.requestTimeout)
	result, replayErr := replay(ctx, client, opts, answers, config.pollInterval, stderr)
	result.DurationMS = time.Since(started).Milliseconds()

	if replayErr == nil {
		collectCounts(ctx, client, &result)
	} else {
		result.Error = describeError(replayErr, opts.timeout)
	}
	if result.Status == "failed" && result.RunID != "" {
		result.RetryHint = retryHint(result.RunID)
	}

	printReport(stdout, result, opts.jsonOutput)

	if replayErr != nil {
		return exitFailure
	}
	switch result.Status {
	case "done", "degraded":
		return exitOK
	case "failed":
		return exitFailure
	default:
		return exitFailure
	}
}

// report — итог прогона. Поля с omitempty не печатаются в JSON, если пусты;
// conflicts остаётся null, когда эндпоинт недоступен (нет роли admin).
type report struct {
	RunID       string   `json:"run_id,omitempty"`
	Status      string   `json:"status,omitempty"`
	Profession  string   `json:"profession,omitempty"`
	Nodes       int      `json:"nodes"`
	Evidences   int      `json:"evidences"`
	Conflicts   *int     `json:"conflicts"`
	AnswersSent int      `json:"answers_sent"`
	DurationMS  int64    `json:"duration_ms"`
	RetryHint   string   `json:"retry_hint,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// replay создаёт прогон, отправляет ответы по одному и ждёт финальный статус.
// Возвращает частичный report даже при ошибке: run_id и число отправленных
// ответов нужны, чтобы человек мог продолжить проверку вручную.
func replay(ctx context.Context, client *apiClient, opts options, answers []string, poll time.Duration, progress io.Writer) (report, error) {
	result := report{Profession: opts.profession}

	created, err := client.createRun(ctx, opts.profession)
	if err != nil {
		return result, fmt.Errorf("создать исследование: %w", err)
	}
	result.RunID = created.ID
	result.Status = created.Status
	fmt.Fprintf(progress, "interview-replay: создан run %s (status=%s)\n", created.ID, created.Status)

	for index, answer := range answers {
		current, hasQuestion, err := waitForQuestion(ctx, client, created.ID, poll)
		if err != nil {
			return result, err
		}
		if !hasQuestion {
			// Прогон завершился, пока мы ждали вопрос: это штатный failed/done,
			// а не сетевая ошибка. Дальше его разберёт основной код.
			result.Status = current.Status
			return result, nil
		}

		if err := postAnswerWithRetry(ctx, client, created.ID, answer, poll, progress); err != nil {
			if errors.Is(err, errSessionFinished) {
				final, statusErr := client.getRun(ctx, created.ID)
				if statusErr != nil {
					return result, fmt.Errorf("прочитать статус завершённого интервью: %w", statusErr)
				}
				result.Status = final.Status
				return result, nil
			}
			return result, fmt.Errorf("отправить ответ %d: %w", index+1, err)
		}
		result.AnswersSent = index + 1
		fmt.Fprintf(progress, "interview-replay: ответ %d/%d отправлен\n", index+1, len(answers))
	}

	// Сценарий может закончиться раньше плана интервьюера: если модель задала
	// ещё вопрос, отправляем закрывающий ответ, чтобы прогон дошёл до финализации,
	// а не завис в ожидании ответа.
	const closingAnswer = "Больше добавить нечего: основные функции, задачи и действия я описал выше."
	for extra := 0; extra < 3; extra++ {
		current, hasQuestion, err := waitForQuestion(ctx, client, created.ID, poll)
		if err != nil {
			return result, err
		}
		if !hasQuestion {
			result.Status = current.Status
			return result, nil
		}
		if err := postAnswerWithRetry(ctx, client, created.ID, closingAnswer, poll, progress); err != nil {
			if errors.Is(err, errSessionFinished) {
				final, statusErr := client.getRun(ctx, created.ID)
				if statusErr != nil {
					return result, fmt.Errorf("прочитать статус завершённого интервью: %w", statusErr)
				}
				result.Status = final.Status
				return result, nil
			}
			return result, fmt.Errorf("отправить закрывающий ответ %d: %w", extra+1, err)
		}
		result.AnswersSent++
		fmt.Fprintln(progress, "interview-replay: отправлен закрывающий ответ")
	}

	final, err := waitForTerminal(ctx, client, created.ID, poll)
	if err != nil {
		return result, err
	}
	result.Status = final.Status
	return result, nil
}

// errSessionFinished сигнализирует, что backend уже закрыл интервью и ответ
// принять нельзя; вызывающий перечитывает статус прогона.
var errSessionFinished = errors.New("интервью уже завершено")

// postAnswerWithRetry повторяет отправку при CHAT_CONFLICT: backend сообщает,
// что интервьюер ещё готовит следующий вопрос, и просит повторить позже.
func postAnswerWithRetry(ctx context.Context, client *apiClient, runID, answer string, poll time.Duration, progress io.Writer) error {
	for {
		err := client.postAnswer(ctx, runID, answer)
		if err == nil {
			return nil
		}
		var apiErr *apiError
		if !errors.As(err, &apiErr) {
			return err
		}
		switch apiErr.Code {
		case "CHAT_CONFLICT":
			fmt.Fprintln(progress, "interview-replay: интервьюер готовит вопрос, повторяем отправку")
			if sleepErr := sleep(ctx, poll); sleepErr != nil {
				return sleepErr
			}
		case "SESSION_FINISHED":
			return errSessionFinished
		default:
			return err
		}
	}
}

// waitForQuestion ждёт, пока последнее сообщение станет вопросом агента.
// Параллельно проверяется статус прогона: если провайдер упал, интервью
// завершится failed, и ждать вопрос бессмысленно.
func waitForQuestion(ctx context.Context, client *apiClient, runID string, poll time.Duration) (runInfo, bool, error) {
	for {
		history, err := client.messages(ctx, runID)
		if err != nil {
			// Сессия может появиться на мгновение позже создания run; 404 не
			// фатален, пока прогон активен. Остальные ошибки (401, 503) — фатальны.
			var apiErr *apiError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
				return runInfo{}, false, fmt.Errorf("прочитать историю интервью: %w", err)
			}
		} else if len(history) > 0 {
			last := history[len(history)-1]
			if last.Role == "agent" && strings.TrimSpace(last.Content) != "" {
				return runInfo{}, true, nil
			}
		}

		current, statusErr := client.getRun(ctx, runID)
		if statusErr != nil {
			return runInfo{}, false, fmt.Errorf("прочитать статус исследования: %w", statusErr)
		}
		if isTerminal(current.Status) {
			return current, false, nil
		}
		if sleepErr := sleep(ctx, poll); sleepErr != nil {
			return runInfo{}, false, fmt.Errorf("ожидание вопроса интервьюера: %w", sleepErr)
		}
	}
}

// waitForTerminal поллит GET /runs/{id}, пока не появится done, degraded или
// failed. Дольше -timeout ожидание не продолжается.
func waitForTerminal(ctx context.Context, client *apiClient, runID string, poll time.Duration) (runInfo, error) {
	for {
		current, err := client.getRun(ctx, runID)
		if err != nil {
			return runInfo{}, fmt.Errorf("прочитать статус исследования: %w", err)
		}
		if isTerminal(current.Status) {
			return current, nil
		}
		if sleepErr := sleep(ctx, poll); sleepErr != nil {
			return runInfo{}, fmt.Errorf("ожидание завершения анализа: %w", sleepErr)
		}
	}
}

func isTerminal(status string) bool {
	switch status {
	case "done", "degraded", "failed":
		return true
	default:
		return false
	}
}

// sleep ждёт interval или завершения контекста, возвращая его ошибку.
func sleep(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// collectCounts дочитывает дерево и конфликты. Это вспомогательные данные:
// их недоступность не отменяет успешный анализ, поэтому ошибки попадают в
// warnings, а не в exit code.
func collectCounts(ctx context.Context, client *apiClient, result *report) {
	nodes, err := client.tree(ctx, result.RunID)
	if err != nil {
		result.Warnings = append(result.Warnings, "не удалось прочитать дерево: "+err.Error())
	} else {
		result.Nodes = len(nodes)
		for _, node := range nodes {
			result.Evidences += len(node.Evidence)
		}
	}

	conflicts, err := client.conflicts(ctx, result.RunID)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 403 || apiErr.StatusCode == 501) {
			result.Warnings = append(result.Warnings, "конфликты недоступны: нужен токен роли admin")
		} else {
			result.Warnings = append(result.Warnings, "не удалось прочитать конфликты: "+err.Error())
		}
		return
	}
	result.Conflicts = &conflicts
}

// describeError переводит технические ошибки в понятный текст, сохраняя
// исходную причину для диагностики.
func describeError(err error, timeout time.Duration) string {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return "токен не принят API (HTTP 401): " + apiErr.Message
		case 403:
			return "недостаточно прав (HTTP 403): " + apiErr.Message
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("не дождались финального статуса за %s: проверьте GET /runs/{id} или повторите позже", timeout)
	}
	if errors.Is(err, context.Canceled) {
		return "прогон прерван"
	}
	return err.Error()
}

// retryHint — единая формулировка для текста и JSON. Именно она объясняет,
// что failed не тупик: интервью сохранено и анализ можно перезапустить.
func retryHint(runID string) string {
	return fmt.Sprintf("нажмите «Повторить анализ» или выполните POST /runs/%s/retry", runID)
}

// printDryRun печатает встроенный сценарий, не создавая HTTP-клиент.
func printDryRun(w io.Writer, script scenario, asJSON bool) {
	if asJSON {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(script)
		return
	}
	fmt.Fprintf(w, "Сценарий интервью: %s\n", script.Profession)
	fmt.Fprintf(w, "Реплик: %d (вопросов: %d, ответов: %d)\n", len(script.Turns)*2, len(script.Turns), len(script.Turns))
	fmt.Fprintln(w, "Уровни: role → function → task → action; ожидаемый материал: 8–12 узлов.")
	for index, turn := range script.Turns {
		fmt.Fprintf(w, "\n[%s] Вопрос %d: %s\n", turn.Level, index+1, turn.Question)
		fmt.Fprintf(w, "Ответ %d: %s\n", index+1, turn.Answer)
	}
	fmt.Fprintln(w, "\nDry-run: сеть не используется. Для прогона уберите -dry-run и задайте -token или MODEUS_TOKEN.")
}

// printReport печатает итог либо человеку, либо в JSON для автоматизации.
func printReport(w io.Writer, result report, asJSON bool) {
	if asJSON {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
		return
	}

	if result.RunID != "" {
		fmt.Fprintf(w, "run_id:      %s\n", result.RunID)
	}
	if result.Status != "" {
		fmt.Fprintf(w, "статус:      %s\n", result.Status)
	}
	if result.RunID != "" {
		fmt.Fprintf(w, "узлов:       %d\n", result.Nodes)
		fmt.Fprintf(w, "обоснований: %d\n", result.Evidences)
		if result.Conflicts == nil {
			fmt.Fprintln(w, "конфликтов:  н/д (эндпоинт доступен только роли admin)")
		} else {
			fmt.Fprintf(w, "конфликтов:  %d\n", *result.Conflicts)
		}
		fmt.Fprintf(w, "ответов:     %d\n", result.AnswersSent)
	}
	if result.DurationMS > 0 {
		fmt.Fprintf(w, "длительность: %s\n", (time.Duration(result.DurationMS) * time.Millisecond).Round(time.Millisecond))
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(w, "внимание:    %s\n", warning)
	}
	if result.Status == "degraded" {
		fmt.Fprintln(w, "Результат неполный (degraded): дерево построено с ограничениями.")
	}
	if result.Status == "failed" {
		fmt.Fprintln(w, "Интервью не завершилось: анализ упал, но ответы и история сохранены.")
		fmt.Fprintf(w, "Подсказка: %s.\n", result.RetryHint)
	}
	if result.Error != "" {
		fmt.Fprintf(w, "ошибка:      %s\n", result.Error)
	}
}
