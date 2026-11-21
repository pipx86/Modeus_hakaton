// program-watcher проверяет зарегистрированные URL образовательных программ и
// создаёт черновики импорта при изменении документа.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/fgos"
	"activity-descriptor/internal/llm/agents"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/ocr"
	"activity-descriptor/internal/programwatch"

	"github.com/jackc/pgx/v5/pgxpool"
)

const sourcesPerCycle = 5

func main() {
	if err := run(); err != nil {
		logging.L().Error("program_watcher_failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logging.Init()
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "PostgreSQL DSN (default: DATABASE_URL)")
	pollInterval := flag.Duration("interval", 15*time.Minute, "how often to look for due sources")
	lease := flag.Duration("lease", programwatch.DefaultLease, "lease duration for claimed sources (must exceed one check)")
	owner := flag.String("owner", programwatch.DefaultOwner(), "lock owner id (default: hostname-pid)")
	once := flag.Bool("once", false, "process one cycle, then exit")
	pdfLLM := flag.Bool("pdf-llm", false, "parse PDF/DOCX and HTML FGOS pages with the LLM FGOS adapter (requires LLM_* environment variables)")
	ocrEnabled := flag.Bool("ocr", false, "recognize scanned PDF/images with tesseract and poppler (implies -pdf-llm; tools are configured by OCR_* variables)")
	ocrLang := flag.String("ocr-lang", "", "tesseract languages for OCR (default: OCR_LANG or rus+eng)")
	ocrDPI := flag.Int("ocr-dpi", 0, "PDF render DPI for OCR (default: OCR_DPI or 200)")
	flag.Parse()

	if *dsn == "" {
		return errors.New("нужен -dsn или DATABASE_URL")
	}
	if *pollInterval <= 0 {
		return errors.New("-interval должен быть положительным")
	}
	if *lease <= 0 {
		return errors.New("-lease должен быть положительным")
	}
	if strings.TrimSpace(*owner) == "" {
		return errors.New("-owner не должен быть пустым")
	}
	if *ocrDPI < 0 {
		return errors.New("-ocr-dpi должен быть положительным")
	}
	ocrConfig := ocr.ConfigFromEnv()
	if lang := strings.TrimSpace(*ocrLang); lang != "" {
		ocrConfig.Lang = lang
	}
	if *ocrDPI > 0 {
		ocrConfig.DPI = *ocrDPI
	}
	if *ocrEnabled {
		ocrConfig.Enabled = true
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return errors.New("некорректная конфигурация PostgreSQL")
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return fmt.Errorf("PostgreSQL недоступен: %w", err)
	}

	repository := programwatch.NewRepository(pool)
	// PGStore реализует programwatch.DraftCreator и создаёт черновики тем же
	// SQL, что и POST /admin/programs/import.
	checker := programwatch.NewChecker(repository, api.NewPGStore(pool))
	checker.Logger = logging.L()
	// Owner и Lease участвуют в claim: так два воркера (и ручная проверка)
	// видят занятые источники и не проверяют их повторно.
	checker.Owner = *owner
	checker.Lease = *lease
	if *pdfLLM || ocrConfig.Enabled {
		checker.PDFExtractor = newFGOSExtractor(ocrConfig)
	}
	logging.L().Info("program_watcher_started",
		"owner", *owner, "lease", lease.String(), "interval", pollInterval.String(),
		"pdf_llm", checker.PDFExtractor != nil, "ocr", ocrConfig.Enabled)

	if *once {
		if _, err = checker.RunOnceAndLog(ctx, sourcesPerCycle); err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	}

	for {
		if _, cycleErr := checker.RunOnceAndLog(ctx, sourcesPerCycle); cycleErr != nil && ctx.Err() == nil {
			logging.L().Error("program_watcher_cycle_failed", "error", cycleErr)
		}
		timer := time.NewTimer(*pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			logging.L().Info("program_watcher_stopped")
			return nil
		case <-timer.C:
		}
	}
}

// newFGOSExtractor собирает LLM-адаптер ФГОС из тех же переменных окружения,
// что и cmd/server (LLM_API_KEY / LLM_BASE_URL / LLM_<AGENT>_*). Адаптер
// разбирает PDF/DOCX и HTML-текст ФГОС (FGOS_HTML_TEXT, по умолчанию включён).
// Если LLM не настроен, воркер продолжает работать без адаптера: PDF/DOCX и
// HTML-текст ФГОС остаются в статусе needs_parser, а не роняют цикл.
//
// При включённом OCR адаптер оборачивается ocr.FGOSExtractor: страницы без
// текстового слоя распознаются tesseract/poppler. Отсутствие инструментов не
// роняет воркер: документ получит статус error с инструкцией, цикл продолжится.
func newFGOSExtractor(ocrConfig ocr.Config) programwatch.PDFExtractor {
	cfg, err := agents.FromEnv()
	if err != nil {
		logging.L().Warn("fgos_llm_not_configured", "error", err)
		return nil
	}
	pool, err := agents.NewPool(cfg)
	if err != nil {
		logging.L().Warn("fgos_llm_pool_failed", "error", err)
		return nil
	}
	client, err := pool.Client(agents.Analyst)
	if err != nil {
		logging.L().Warn("fgos_llm_client_failed", "error", err)
		return nil
	}
	extractor := fgos.NewExtractor(client)
	// Воркер разбирает те же PDF/сканы, что и импорт, поэтому берёт тот же
	// бюджет времени. По умолчанию 15 минут: живой ФГОС на 10 страниц
	// занимает около 6 минут.
	extractor.Timeout = programImportBudget()
	if !ocrConfig.Enabled {
		logging.L().Info("fgos_llm_adapter_enabled",
			"model", cfg.Agents[agents.Analyst].Model,
			"timeout", extractor.Timeout.String(),
			"max_pages", extractor.MaxPages,
			"max_chars_per_chunk", extractor.MaxCharsPerChunk)
		return extractor
	}
	document := ocr.NewDocument(ocrConfig)
	if availableErr := document.Available(); availableErr != nil {
		// Не отключаем OCR: инструменты могут появиться позже, а документ
		// должен получить понятную ошибку с инструкцией.
		logging.L().Warn("fgos_ocr_unavailable", "error", availableErr)
	}
	logging.L().Info("fgos_ocr_enabled",
		"tesseract", ocrConfig.TesseractPath,
		"pdftoppm", ocrConfig.PDFToPPMPath,
		"lang", ocrConfig.Lang,
		"dpi", ocrConfig.DPI,
		"max_pages", ocrConfig.MaxPages,
		"timeout", ocrConfig.Timeout.String())
	return ocr.NewFGOSExtractor(extractor, document)
}

// programImportBudget читает PROGRAM_IMPORT_TIMEOUT_S (секунды или duration)
// с дефолтом 15 минут; тем же значением ограничен импорт через API.
func programImportBudget() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PROGRAM_IMPORT_TIMEOUT_S"))
	if raw == "" {
		return 15 * time.Minute
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
		return parsed
	}
	return 15 * time.Minute
}
