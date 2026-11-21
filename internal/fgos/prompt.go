package fgos

import (
	"fmt"
	"strings"
	"sync"

	promptassets "activity-descriptor/prompts"
)

var (
	systemPromptOnce sync.Once
	systemPromptText string
	systemPromptErr  error
)

// SystemPrompt возвращает встроенный системный промпт адаптера
// prompts/fgos/<PromptVersion>/system.md. Промпт вкомпилирован в бинарь, чтобы
// разбор не зависел от рабочего каталога процесса.
func SystemPrompt() (string, error) {
	systemPromptOnce.Do(func() {
		path := "fgos/" + PromptVersion + "/system.md"
		data, readErr := promptassets.FS.ReadFile(path)
		if readErr != nil {
			systemPromptErr = fmt.Errorf("fgos: не удалось прочитать промпт %s: %w", path, readErr)
			return
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			systemPromptErr = fmt.Errorf("fgos: промпт %s пуст", path)
			return
		}
		systemPromptText = text
	})
	if systemPromptErr != nil {
		return "", systemPromptErr
	}
	return systemPromptText, nil
}
