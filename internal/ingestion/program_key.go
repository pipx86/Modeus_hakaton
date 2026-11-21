package ingestion

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ProgramKeyFormatExample - канонический пример, который API показывает в 400,
// чтобы администратор видел ожидаемый формат без чтения документации.
const ProgramKeyFormatExample = "mirea/09.03.01/2024/full-time"

const programKeyFormatHint = "org/code/year/form, например " + ProgramKeyFormatExample

var (
	programKeyOrgPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}$`)
	programKeyCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.]{1,20}$`)
	programKeyYearPattern = regexp.MustCompile(`^[0-9]{4}$`)
	programKeyForms       = map[string]bool{
		"full-time": true,
		"part-time": true,
		"evening":   true,
		"distance":  true,
		"mixed":     true,
		"other":     true,
	}
)

// NormalizeProgramKey проверяет строгий канонический ключ
// {org}/{code}/{year}/{form} и возвращает его без пробелов по краям. Регистр не
// понижается автоматически: ключ - часть внешнего контракта, и молчаливая
// замена «МИРЭА» на «мирэа» спрятала бы ошибку в источнике.
func NormalizeProgramKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("ключ программы пуст; ожидается %s", programKeyFormatHint)
	}
	parts := strings.Split(key, "/")
	if len(parts) != 4 {
		return "", fmt.Errorf("ключ %q должен иметь формат %s (получено частей: %d)", key, programKeyFormatHint, len(parts))
	}
	org, code, year, form := parts[0], parts[1], parts[2], parts[3]
	if !programKeyOrgPattern.MatchString(org) {
		return "", fmt.Errorf("org %q должен содержать 2-31 символа [a-z0-9-], начинаться с буквы или цифры и быть в нижнем регистре", org)
	}
	if !programKeyCodePattern.MatchString(code) {
		return "", fmt.Errorf("code %q должен содержать 2-21 символ [a-z0-9.], начинаться с буквы или цифры и быть в нижнем регистре", code)
	}
	if !programKeyYearPattern.MatchString(year) {
		return "", fmt.Errorf("year %q должен состоять из 4 цифр", year)
	}
	yearNumber, err := strconv.Atoi(year)
	if err != nil || yearNumber < 2000 || yearNumber > 2100 {
		return "", fmt.Errorf("year %q должен быть в диапазоне 2000-2100", year)
	}
	if !programKeyForms[form] {
		return "", fmt.Errorf("form %q должна быть одной из: full-time, part-time, evening, distance, mixed, other", form)
	}
	return key, nil
}

// IsCanonicalProgramKey сообщает, проходит ли ключ строгую проверку.
func IsCanonicalProgramKey(raw string) bool {
	_, err := NormalizeProgramKey(raw)
	return err == nil
}

// ProgramKeyWarning объясняет, почему ключ неканонический. Для канонического
// ключа возвращает false. Пустой ключ здесь не предупреждение, а ошибка
// валидации: его отсекают вызывающие.
func ProgramKeyWarning(raw string) (string, bool) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", false
	}
	if _, err := NormalizeProgramKey(key); err == nil {
		return "", false
	}
	if !strings.Contains(key, "/") {
		return fmt.Sprintf("ключ программы %q сохранён в старом формате без «/»; для новых программ используйте %s", key, ProgramKeyFormatExample), true
	}
	return fmt.Sprintf("ключ программы %q не соответствует каноническому формату %s и сохранён как есть для совместимости", key, ProgramKeyFormatExample), true
}

// NormalizeProgramKeyLenient - путь импорта: канонический ключ возвращается как
// есть, старый/неканонический принимается с предупреждением. ok=false только
// для пустого ключа.
func NormalizeProgramKeyLenient(raw string) (key, warning string, ok bool) {
	key = strings.TrimSpace(raw)
	if key == "" {
		return "", "", false
	}
	if canonical, err := NormalizeProgramKey(key); err == nil {
		return canonical, "", true
	}
	warning, _ = ProgramKeyWarning(key)
	return key, warning, true
}

// NextProgramVersion - чистое правило версионирования публикаций: новая
// программа получает 1, существующая - следующую версию. Вынесено отдельно,
// чтобы правило тестировалось без PostgreSQL.
func NextProgramVersion(current int, exists bool) int {
	if !exists || current < 1 {
		return 1
	}
	return current + 1
}
