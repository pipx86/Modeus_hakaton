// Команда index-sources догоняет семантический индекс источников: строит
// фрагменты source_fragments и векторы fragment_embeddings для уже созданных
// источников. Нужна после применения migrations/optional/vector или при смене
// модели эмбеддингов. Модель чата не вызывается: только Ollama /api/embed.
//
// Примеры:
//
//	go run ./cmd/index-sources                 # все источники без векторов
//	go run ./cmd/index-sources -limit 10       # ограниченный пробный прогон
//	go run ./cmd/index-sources -all            # переиндексировать всё
//	go run ./cmd/index-sources -source <uuid>  # один источник
//	go run ./cmd/index-sources -dry-run        # только показать план
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"activity-descriptor/internal/search"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultSourceTimeout ограничивает индексацию одного источника: большой
// документ не должен занимать весь backfill. Переопределяется -timeout.
const defaultSourceTimeout = 5 * time.Minute

type options struct {
	databaseURL string
	ollamaURL   string
	embedModel  string
	sourceID    string
	limit       int
	all         bool
	dryRun      bool
	allowFailed bool
	timeout     time.Duration
}

// parseOptions разбирает флаги и подставляет параметры Ollama из окружения
// (OLLAMA_URL/OLLAMA_EMBED_MODEL с локальными умолчаниями search.ConfigFromEnv).
func parseOptions(args []string) (options, error) {
	env := search.ConfigFromEnv()
	opts := options{
		databaseURL: strings.TrimSpace(os.Getenv("DATABASE_URL")),
		ollamaURL:   env.OllamaURL,
		embedModel:  env.EmbedModel,
		timeout:     defaultSourceTimeout,
	}
	flags := flag.NewFlagSet("index-sources", flag.ContinueOnError)
	flags.StringVar(&opts.sourceID, "source", "", "индексировать один источник по UUID (вместо выборки)")
	flags.IntVar(&opts.limit, "limit", 0, "максимум источников за прогон, 0 — без ограничения")
	flags.BoolVar(&opts.all, "all", false, "переиндексировать все источники, а не только без векторов")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "только показать план, не вызывать модель")
	flags.BoolVar(&opts.allowFailed, "allow-failures", false, "вернуть код 0 даже при ошибках индексации")
	flags.DurationVar(&opts.timeout, "timeout", defaultSourceTimeout, "таймаут индексации одного источника")
	flags.StringVar(&opts.ollamaURL, "ollama-url", opts.ollamaURL, "адрес Ollama (по умолчанию OLLAMA_URL)")
	flags.StringVar(&opts.embedModel, "embed-model", opts.embedModel, "модель эмбеддингов (по умолчанию OLLAMA_EMBED_MODEL)")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() > 0 {
		return options{}, fmt.Errorf("неизвестные аргументы: %s", strings.Join(flags.Args(), " "))
	}
	if opts.limit < 0 {
		return options{}, errors.New("limit не может быть отрицательным")
	}
	if opts.timeout <= 0 {
		return options{}, errors.New("timeout должен быть положительным")
	}
	if strings.TrimSpace(opts.ollamaURL) == "" || strings.TrimSpace(opts.embedModel) == "" {
		return options{}, errors.New("нужны адрес Ollama и модель эмбеддингов")
	}
	if opts.sourceID != "" {
		var id pgtype.UUID
		if err := id.Scan(opts.sourceID); err != nil || !id.Valid {
			return options{}, fmt.Errorf("некорректный UUID источника: %q", opts.sourceID)
		}
	}
	if opts.databaseURL == "" {
		return options{}, errors.New("не задан DATABASE_URL")
	}
	return opts, nil
}

// backfillResult — итог прогона для отчёта и кода возврата.
type backfillResult struct {
	Indexed int
	Failed  int
}

// backfill индексирует источники по списку; list вызывается один раз, index —
// по одному источнику с собственным таймаутом. Ошибки отдельных источников не
// прерывают прогон, отмена контекста — прерывает.
func backfill(ctx context.Context, ids []pgtype.UUID, timeout time.Duration, delay bool, index func(context.Context, pgtype.UUID) error) backfillResult {
	var result backfillResult
	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "index-sources: прервано на %d/%d: %v\n", i, len(ids), err)
			return result
		}
		if delay {
			// Прогресс в stdout, чтобы длинный прогон было видно в терминале.
			fmt.Printf("[%d/%d] index source=%s\n", i+1, len(ids), id.String())
		}
		started := time.Now()
		itemCtx, cancel := context.WithTimeout(ctx, timeout)
		err := index(itemCtx, id)
		cancel()
		elapsed := time.Since(started)
		if err != nil {
			result.Failed++
			fmt.Fprintf(os.Stderr, "index-sources: FAIL source=%s elapsed=%s error=%v\n", id.String(), elapsed.Round(time.Millisecond), err)
			continue
		}
		result.Indexed++
		fmt.Printf("[%d/%d] ok source=%s elapsed=%s\n", i+1, len(ids), id.String(), elapsed.Round(time.Millisecond))
	}
	return result
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "index-sources:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(connectCtx, opts.databaseURL)
	if err != nil {
		return errors.New("неверный DATABASE_URL")
	}
	defer pool.Close()
	if err := pool.Ping(connectCtx); err != nil {
		return fmt.Errorf("нет подключения к PostgreSQL: %w", err)
	}
	if err := ensureVectorSchema(connectCtx, pool); err != nil {
		return err
	}

	cfg := search.Config{OllamaURL: strings.TrimRight(strings.TrimSpace(opts.ollamaURL), "/"), EmbedModel: strings.TrimSpace(opts.embedModel)}
	service := cfg.Service(pool)
	fmt.Printf("index-sources: model=%s url=%s\n", cfg.EmbedModel, cfg.OllamaURL)
	if !opts.dryRun {
		// Ранняя проверка Ollama: иначе каждый источник падал бы отдельной
		// ошибкой соединения, а причина была бы не видна в итоговой сводке.
		if _, err := cfg.Client().Embed(connectCtx, "проверка модели эмбеддингов"); err != nil {
			return fmt.Errorf("Ollama недоступна (%s, модель %s): %w", cfg.OllamaURL, cfg.EmbedModel, err)
		}
	}

	ids, err := selectSources(ctx, pool, opts)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Println("index-sources: нечего индексировать: подходящих источников нет")
		return nil
	}
	fmt.Printf("index-sources: к индексации=%d\n", len(ids))
	if opts.dryRun {
		for i, id := range ids {
			fmt.Printf("[%d/%d] would index source=%s\n", i+1, len(ids), id.String())
		}
		return nil
	}

	result := backfill(ctx, ids, opts.timeout, true, func(itemCtx context.Context, id pgtype.UUID) error {
		return service.IndexSource(itemCtx, id)
	})
	fmt.Printf("index-sources: indexed=%d failed=%d total=%d\n", result.Indexed, result.Failed, len(ids))
	if result.Failed > 0 && !opts.allowFailed {
		return fmt.Errorf("не удалось проиндексировать %d источник(ов); повторите прогон", result.Failed)
	}
	return nil
}

// ensureVectorSchema проверяет, что применены необязательные миграции; без них
// индексация невозможна, и сообщение должно прямо говорить, что делать.
func ensureVectorSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var ready bool
	err := pool.QueryRow(ctx, `SELECT to_regclass('public.source_fragments') IS NOT NULL AND to_regclass('public.fragment_embeddings') IS NOT NULL`).Scan(&ready)
	if err != nil {
		return fmt.Errorf("проверить схему поиска: %w", err)
	}
	if !ready {
		return errors.New("нет таблиц source_fragments/fragment_embeddings: примените migrations/optional/vector (см. migrations/optional/vector/VECTOR_STORAGE_GUIDE.md)")
	}
	return nil
}

// selectSources возвращает источники для индексации: один по -source либо
// выборку источников без векторов текущей модели (или всех при -all).
func selectSources(ctx context.Context, pool *pgxpool.Pool, opts options) ([]pgtype.UUID, error) {
	if opts.sourceID != "" {
		var id pgtype.UUID
		if err := id.Scan(opts.sourceID); err != nil || !id.Valid {
			return nil, fmt.Errorf("некорректный UUID источника: %q", opts.sourceID)
		}
		return []pgtype.UUID{id}, nil
	}
	const base = `SELECT s.id FROM sources s WHERE btrim(COALESCE(s.content,'')) <> ''`
	const missing = ` AND NOT EXISTS (
		SELECT 1 FROM fragment_embeddings e
		JOIN source_fragments f ON f.id = e.fragment_id
		WHERE f.source_id = s.id AND e.model = $1)`
	query := base
	args := []any{}
	if !opts.all {
		query += missing
		args = append(args, opts.embedModel)
	}
	query += ` ORDER BY s.created_at, s.id`
	if opts.limit > 0 {
		args = append(args, opts.limit)
		query += fmt.Sprintf(` LIMIT $%d`, len(args))
	}
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("выбрать источники для индексации: %w", err)
	}
	defer rows.Close()
	ids := make([]pgtype.UUID, 0)
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("прочитать список источников: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("прочитать список источников: %w", err)
	}
	return ids, nil
}
