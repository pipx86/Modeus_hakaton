package ocr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain обслуживает Tesseract-тесты без внешнего бинаря: тестовый бинарь
// запускается как «tesseract» и по OCR_TEST_HELPER печатает заданный ответ.
func TestMain(m *testing.M) {
	// Синтетические PDF проверяют встроенный извлекатель и OCR, а не pdftotext.
	_ = os.Setenv("FGOS_PDFTOTEXT", "off")
	if mode := os.Getenv("OCR_TEST_HELPER"); mode != "" {
		runOCRTestHelper(mode)
	}
	os.Exit(m.Run())
}

func runOCRTestHelper(mode string) {
	switch mode {
	case "text":
		fmt.Fprint(os.Stdout, "распознанный текст страницы")
		os.Exit(0)
	case "args":
		fmt.Fprint(os.Stdout, strings.Join(os.Args, "\n"))
		os.Exit(0)
	case "empty":
		os.Exit(0)
	case "slow":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "tesseract failed: bad image")
		os.Exit(2)
	default:
		os.Exit(0)
	}
}

func helperTesseract(t *testing.T, mode string) *Tesseract {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("OCR_TEST_HELPER", mode)
	return &Tesseract{Path: executable, Timeout: 2 * time.Second}
}

func TestTesseractRecognizeReturnsText(t *testing.T) {
	engine := helperTesseract(t, "text")
	text, err := engine.Recognize(context.Background(), "page.png", "rus+eng")
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	if text != "распознанный текст страницы" {
		t.Fatalf("текст: %q", text)
	}
}

func TestTesseractRecognizeInvokesExpectedCommand(t *testing.T) {
	engine := helperTesseract(t, "args")
	text, err := engine.Recognize(context.Background(), filepath.Join("tmp", "page.png"), "eng")
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	args := strings.Split(text, "\n")
	joined := strings.Join(args, " ")
	for _, want := range []string{"page.png", "stdout", "-l", "eng"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("в аргументах tesseract нет %q: %v", want, args)
		}
	}
}

func TestTesseractEmptyOutputIsNoText(t *testing.T) {
	engine := helperTesseract(t, "empty")
	_, err := engine.Recognize(context.Background(), "page.png", "")
	if !errors.Is(err, ErrNoText) {
		t.Fatalf("ожидали ErrNoText, получили %v", err)
	}
	if !strings.Contains(err.Error(), "не распознал текст") {
		t.Fatalf("понятный текст ошибки: %v", err)
	}
}

func TestTesseractTimeout(t *testing.T) {
	engine := helperTesseract(t, "slow")
	engine.Timeout = 50 * time.Millisecond
	started := time.Now()
	_, err := engine.Recognize(context.Background(), "page.png", "eng")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("ожидали ErrTimeout, получили %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("таймаут не сработал: %s", elapsed)
	}
}

func TestTesseractCommandFailureIncludesStderr(t *testing.T) {
	engine := helperTesseract(t, "fail")
	_, err := engine.Recognize(context.Background(), "page.png", "eng")
	if err == nil || !strings.Contains(err.Error(), "tesseract failed") {
		t.Fatalf("ожидали stderr в ошибке, получили %v", err)
	}
}

func TestTesseractBinaryNotFound(t *testing.T) {
	engine := &Tesseract{Path: "tesseract-does-not-exist-for-tests"}
	_, err := engine.Recognize(context.Background(), "page.png", "eng")
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("ожидали ErrToolNotFound, получили %v", err)
	}
	for _, want := range []string{"UB-Mannheim.TesseractOCR", EnvTesseract, DocsPath} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в ошибке нет %q: %v", want, err)
		}
	}
}

func TestTesseractDefaultsFromFields(t *testing.T) {
	engine := &Tesseract{}
	if engine.binaryPath() != DefaultTesseract {
		t.Fatalf("бинарь по умолчанию: %q", engine.binaryPath())
	}
	if engine.lang() != DefaultLang {
		t.Fatalf("язык по умолчанию: %q", engine.lang())
	}
	if engine.timeout() != DefaultPageTimeout {
		t.Fatalf("таймаут по умолчанию: %s", engine.timeout())
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvLang, "rus")
	t.Setenv(EnvTesseract, `C:\Tools\tesseract.exe`)
	t.Setenv(EnvPDFToPPM, `C:\Tools\pdftoppm.exe`)
	t.Setenv(EnvDPI, "300")
	t.Setenv(EnvPageTimeout, "15")
	t.Setenv(EnvMaxPages, "7")
	t.Setenv(EnvTotalTimeout, "2m")

	cfg := ConfigFromEnv()
	if !cfg.Enabled || cfg.Lang != "rus" || cfg.TesseractPath != `C:\Tools\tesseract.exe` ||
		cfg.PDFToPPMPath != `C:\Tools\pdftoppm.exe` || cfg.DPI != 300 ||
		cfg.PageTimeout != 15*time.Second || cfg.MaxPages != 7 || cfg.Timeout != 2*time.Minute {
		t.Fatalf("конфигурация из env: %+v", cfg)
	}
}

func TestConfigFromEnvDefaultsAndInvalidValues(t *testing.T) {
	for _, name := range []string{EnvEnabled, EnvLang, EnvTesseract, EnvPDFToPPM, EnvDPI, EnvPageTimeout, EnvMaxPages, EnvTotalTimeout} {
		t.Setenv(name, "")
	}
	cfg := ConfigFromEnv()
	if cfg.Enabled || cfg.Lang != DefaultLang || cfg.TesseractPath != DefaultTesseract ||
		cfg.PDFToPPMPath != DefaultPDFToPPM || cfg.DPI != DefaultDPI ||
		cfg.PageTimeout != DefaultPageTimeout || cfg.MaxPages != DefaultMaxPages || cfg.Timeout != DefaultTimeout {
		t.Fatalf("значения по умолчанию: %+v", cfg)
	}

	t.Setenv(EnvDPI, "not-a-number")
	t.Setenv(EnvPageTimeout, "-5")
	t.Setenv(EnvMaxPages, "0")
	t.Setenv(EnvTotalTimeout, "wrong")
	cfg = ConfigFromEnv()
	if cfg.DPI != DefaultDPI || cfg.PageTimeout != DefaultPageTimeout ||
		cfg.MaxPages != DefaultMaxPages || cfg.Timeout != DefaultTimeout {
		t.Fatalf("некорректные значения должны заменяться: %+v", cfg)
	}
}

func TestConfigWithDefaults(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.Lang != DefaultLang || cfg.TesseractPath != DefaultTesseract || cfg.PDFToPPMPath != DefaultPDFToPPM ||
		cfg.DPI != DefaultDPI || cfg.PageTimeout != DefaultPageTimeout || cfg.MaxPages != DefaultMaxPages ||
		cfg.Timeout != DefaultTimeout {
		t.Fatalf("нормализация: %+v", cfg)
	}
}

func TestCleanTextNormalizesTesseractOutput(t *testing.T) {
	got := cleanText("  первая строка\r\n\r\n\r\nвторая\f  \n\n")
	if got != "первая строка\n\nвторая" {
		t.Fatalf("cleanText: %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("abcdef", 3); got != "…def" {
		t.Fatalf("truncateRunes: %q", got)
	}
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Fatalf("truncateRunes без обрезки: %q", got)
	}
}
