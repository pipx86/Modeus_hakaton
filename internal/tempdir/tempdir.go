// Package tempdir создаёт временные каталоги с запасными путями.
//
// На части установок Windows процессу запрещено писать в %TEMP% (например,
// из-за политик безопасности или антивируса). Тогда MkdirTemp("", ...) падает
// с Access denied, хотя каталог проекта доступен. Пакет пробует несколько
// мест по порядку и сообщает обо всех попытках, чтобы администратор видел
// причину и мог задать OCR_TMP_DIR.
package tempdir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvOverride — переменная окружения с явным каталогом для временных файлов.
const EnvOverride = "OCR_TMP_DIR"

// Create создаёт временный каталог по шаблону (например, "fgos-ocr-*").
// Порядок: OCR_TMP_DIR, системный Temp, кэш пользователя, рабочий каталог.
func Create(pattern string) (string, error) {
	candidates := make([]string, 0, 4)
	if configured := strings.TrimSpace(os.Getenv(EnvOverride)); configured != "" {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates, os.TempDir())
	if cache, err := os.UserCacheDir(); err == nil {
		candidates = append(candidates, filepath.Join(cache, "modeus", "tmp"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, ".tmp"))
	}

	problems := make([]string, 0, len(candidates))
	for _, base := range candidates {
		if err := os.MkdirAll(base, 0o700); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", base, err))
			continue
		}
		directory, err := os.MkdirTemp(base, pattern)
		if err == nil {
			return directory, nil
		}
		problems = append(problems, fmt.Sprintf("%s: %v", base, err))
	}
	return "", fmt.Errorf("не удалось создать временный каталог (%s); задайте %s", strings.Join(problems, "; "), EnvOverride)
}
