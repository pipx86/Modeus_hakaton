package ocr

import (
	"activity-descriptor/internal/tempdir"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Renderer рендерит страницы PDF в PNG. Возвращаемые пути соответствуют
// страницам firstPage..lastPage по порядку; cleanup удаляет временные файлы и
// может быть nil. Document вызывает cleanup сразу после распознавания
// диапазона, поэтому рендерер не оставляет мусор в TEMP.
type Renderer interface {
	Render(ctx context.Context, pdfPath string, firstPage, lastPage int) (paths []string, cleanup func(), err error)
}

// PDFRenderer — Renderer поверх poppler pdftoppm. Поля можно задавать напрямую;
// нулевые значения заменяются на Default*.
type PDFRenderer struct {
	// Path — путь или имя бинаря (по умолчанию pdftoppm, env OCR_PDFTOPPM).
	Path string
	// DPI — разрешение рендеринга (env OCR_DPI, по умолчанию 200).
	DPI int
}

// Available проверяет, что pdftoppm находится в PATH или по заданному пути.
func (r *PDFRenderer) Available() error {
	binary := r.binaryPath()
	if _, err := exec.LookPath(binary); err != nil {
		return toolNotFoundError(binary, EnvPDFToPPM, popplerInstallHint, popplerPathExample)
	}
	return nil
}

// Render запускает
//
//	pdftoppm -r <dpi> -png -f <first> -l <last> <pdf> <prefix>
//
// и возвращает пути к PNG в порядке страниц. При ошибке временный каталог
// удаляется сразу; при успехе удаление выполняет cleanup.
func (r *PDFRenderer) Render(ctx context.Context, pdfPath string, firstPage, lastPage int) ([]string, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if firstPage < 1 || lastPage < firstPage {
		return nil, nil, fmt.Errorf("pdftoppm: неверный диапазон страниц %d–%d", firstPage, lastPage)
	}
	if strings.TrimSpace(pdfPath) == "" {
		return nil, nil, errors.New("pdftoppm: не указан путь к PDF")
	}
	if err := r.Available(); err != nil {
		return nil, nil, err
	}
	directory, err := tempdir.Create("fgos-ocr-*")
	if err != nil {
		return nil, nil, fmt.Errorf("OCR: %w", err)
	}
	cleanup := sync.OnceFunc(func() { _ = os.RemoveAll(directory) })

	prefix := filepath.Join(directory, "page")
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, r.binaryPath(),
		"-r", strconv.Itoa(r.dpi()),
		"-png",
		"-f", strconv.Itoa(firstPage),
		"-l", strconv.Itoa(lastPage),
		pdfPath, prefix)
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		cleanup()
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return nil, nil, fmt.Errorf("%w: рендеринг PDF не уложился в отведённое время: %w", ErrTimeout, ctxErr)
			}
			return nil, nil, fmt.Errorf("pdftoppm: рендеринг прерван: %w", ctxErr)
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return nil, nil, toolNotFoundError(r.binaryPath(), EnvPDFToPPM, popplerInstallHint, popplerPathExample)
		}
		details := truncateRunes(stderr.String(), 400)
		if details == "" {
			details = truncateRunes(err.Error(), 400)
		}
		return nil, nil, fmt.Errorf("pdftoppm: не удалось отрендерить PDF: %s", details)
	}
	paths, err := collectRenderedPages(directory, firstPage, lastPage)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return paths, cleanup, nil
}

// collectRenderedPages проверяет, что pdftoppm создал по PNG на каждую страницу
// диапазона, и возвращает пути в порядке firstPage..lastPage. Имена вида
// page-05.png зависят от ширины номера, поэтому номер парсится, а не угадывается.
func collectRenderedPages(directory string, firstPage, lastPage int) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("OCR: не удалось прочитать временный каталог: %w", err)
	}
	byNumber := make(map[int]string, lastPage-firstPage+1)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "page-") || !strings.HasSuffix(strings.ToLower(name), ".png") {
			continue
		}
		numberText := strings.TrimSuffix(strings.TrimPrefix(name, "page-"), filepath.Ext(name))
		number, parseErr := strconv.Atoi(numberText)
		if parseErr != nil {
			continue
		}
		byNumber[number] = filepath.Join(directory, name)
	}
	paths := make([]string, 0, lastPage-firstPage+1)
	missing := make([]int, 0)
	for number := firstPage; number <= lastPage; number++ {
		path, ok := byNumber[number]
		if !ok {
			missing = append(missing, number)
			continue
		}
		paths = append(paths, path)
	}
	if len(missing) > 0 {
		sort.Ints(missing)
		return nil, fmt.Errorf("pdftoppm не создал PNG для страниц: %v", missing)
	}
	return paths, nil
}

func (r *PDFRenderer) binaryPath() string {
	if r != nil && strings.TrimSpace(r.Path) != "" {
		return strings.TrimSpace(r.Path)
	}
	return DefaultPDFToPPM
}

func (r *PDFRenderer) dpi() int {
	if r != nil && r.DPI > 0 {
		return r.DPI
	}
	return DefaultDPI
}

var _ Renderer = (*PDFRenderer)(nil)

// createWorkDir создаёт временный каталог для страниц PDF. На некоторых
// установках Windows процессу запрещено писать в %TEMP%, поэтому пробуем
// несколько мест: OCR_TMP_DIR, системный Temp, кэш пользователя и каталог
// проекта. Ошибка содержит все попытки, чтобы администратор видел причину.
func createWorkDir() (string, error) {
	candidates := make([]string, 0, 4)
	if configured := strings.TrimSpace(os.Getenv("OCR_TMP_DIR")); configured != "" {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates, os.TempDir())
	if cache, cacheErr := os.UserCacheDir(); cacheErr == nil {
		candidates = append(candidates, filepath.Join(cache, "modeus", "ocr"))
	}
	if wd, wdErr := os.Getwd(); wdErr == nil {
		candidates = append(candidates, filepath.Join(wd, ".ocr-tmp"))
	}
	problems := make([]string, 0, len(candidates))
	for _, base := range candidates {
		if mkErr := os.MkdirAll(base, 0o700); mkErr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", base, mkErr))
			continue
		}
		directory, dirErr := os.MkdirTemp(base, "fgos-ocr-*")
		if dirErr == nil {
			return directory, nil
		}
		problems = append(problems, fmt.Sprintf("%s: %v", base, dirErr))
	}
	return "", fmt.Errorf("не удалось создать временный каталог (%s); задайте OCR_TMP_DIR", strings.Join(problems, "; "))
}
