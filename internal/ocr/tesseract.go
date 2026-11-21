package ocr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Engine распознаёт текст на изображении. Lang — языки tesseract в формате
// rus+eng; пустая строка означает язык по умолчанию.
type Engine interface {
	Recognize(ctx context.Context, imagePath, lang string) (string, error)
}

// Tesseract — Engine поверх внешнего tesseract. Поля можно задавать напрямую;
// нулевые значения заменяются на Default*.
type Tesseract struct {
	// Path — путь или имя бинаря (по умолчанию tesseract, env OCR_TESSERACT).
	Path string
	// Lang — языки по умолчанию (env OCR_LANG, по умолчанию rus+eng).
	Lang string
	// Timeout — таймаут одной страницы (env OCR_PAGE_TIMEOUT_S, по умолчанию 60с).
	Timeout time.Duration
}

// Available проверяет, что tesseract находится в PATH или по заданному пути.
func (t *Tesseract) Available() error {
	binary := t.binaryPath()
	if _, err := exec.LookPath(binary); err != nil {
		return toolNotFoundError(binary, EnvTesseract, tesseractInstallHint, tesseractPathExample)
	}
	return nil
}

// Recognize запускает `tesseract <image> stdout -l <lang>` и возвращает
// распознанный текст. Пустой вывод — ошибка ErrNoText, отсутствие бинаря —
// ErrToolNotFound с инструкцией по установке, превышение таймаута — ErrTimeout.
func (t *Tesseract) Recognize(ctx context.Context, imagePath, lang string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(imagePath) == "" {
		return "", errors.New("tesseract: не указан путь к изображению")
	}
	if err := t.Available(); err != nil {
		return "", err
	}
	language := strings.TrimSpace(lang)
	if language == "" {
		language = t.lang()
	}
	timeout := t.timeout()
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stderr bytes.Buffer
	command := exec.CommandContext(commandCtx, t.binaryPath(), imagePath, "stdout", "-l", language)
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return "", fmt.Errorf("%w: распознавание прервано общим таймаутом: %w", ErrTimeout, ctxErr)
			}
			return "", fmt.Errorf("tesseract: распознавание прервано: %w", ctxErr)
		}
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%w: tesseract не ответил за %s", ErrTimeout, timeout)
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", toolNotFoundError(t.binaryPath(), EnvTesseract, tesseractInstallHint, tesseractPathExample)
		}
		details := truncateRunes(stderr.String(), 400)
		if details == "" {
			details = truncateRunes(err.Error(), 400)
		}
		if strings.Contains(strings.ToLower(details), "failed loading language") {
			details += fmt.Sprintf("; установите языковые данные %q в tessdata (https://github.com/tesseract-ocr/tessdata)", language)
		}
		return "", fmt.Errorf("tesseract: не удалось распознать %s: %s", filepath.Base(imagePath), details)
	}
	text := cleanText(string(output))
	if text == "" {
		return "", fmt.Errorf("%w: tesseract вернул пустой текст для %s", ErrNoText, filepath.Base(imagePath))
	}
	return text, nil
}

func (t *Tesseract) binaryPath() string {
	if t != nil && strings.TrimSpace(t.Path) != "" {
		return strings.TrimSpace(t.Path)
	}
	return DefaultTesseract
}

func (t *Tesseract) lang() string {
	if t != nil && strings.TrimSpace(t.Lang) != "" {
		return strings.TrimSpace(t.Lang)
	}
	return DefaultLang
}

func (t *Tesseract) timeout() time.Duration {
	if t != nil && t.Timeout > 0 {
		return t.Timeout
	}
	return DefaultPageTimeout
}

var _ Engine = (*Tesseract)(nil)
