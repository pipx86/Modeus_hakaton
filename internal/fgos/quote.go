package fgos

import (
	"os"
	"strconv"
	"strings"
)

const (
	// DefaultCitationWindow — сколько соседних страниц проверяется вместе с
	// указанной, когда цитата не нашлась на ней самой. Модель часто берёт
	// страницу, на которой начинается пункт, хотя сама формулировка уехала на
	// следующую страницу. Одна страница с каждой стороны закрывает типовой
	// разрыв таблицы или абзаца.
	DefaultCitationWindow = 1
	// minFuzzyQuoteRunes — порог, ниже которого цитата проверяется точным
	// вхождением без нормализации. Короткая строка после схлопывания пробелов
	// совпадает в любом документе, поэтому мягкое сравнение для неё опасно.
	minFuzzyQuoteRunes = 20
)

// NormalizeForQuote приводит текст к виду, пригодному для сравнения цитаты.
// Нормализация нужна только для сравнения: в черновик и в ответы API текст
// попадает в исходном виде.
//
// Что делает функция (аналог NFKC в объёме, который нужен цитатам, без внешних
// зависимостей):
//
//   - удаляет мягкий дефис U+00AD — он невидим в документе и не воспроизводится
//     моделью;
//   - неразрывный дефис U+2011 заменяет обычным;
//   - склеивает перенос слова на границе строки: «информа-\nция» становится
//     «информация»;
//   - все виды пробелов (пробелы, табы, переводы строк, U+00A0 и другие
//     Unicode-пробелы) схлопывает в один пробел и обрезает края;
//   - приводит текст к нижнему регистру;
//   - заменяет «ё» на «е»: в PDF и HTML эти буквы взаимозаменяемы, и из-за
//     одной буквы цитата не должна отбрасываться.
func NormalizeForQuote(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\u00ad", "")
	value = strings.ReplaceAll(value, "\u2011", "-")
	value = strings.ReplaceAll(value, "-\r\n", "")
	value = strings.ReplaceAll(value, "-\n", "")
	value = strings.ReplaceAll(value, "-\r", "")
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "ё", "е")
	// strings.Fields понимает все Unicode-пробелы (включая U+00A0, табы и
	// переводы строк) и схлопывает их в один пробел.
	return strings.Join(strings.Fields(value), " ")
}

// QuoteFound сообщает, встречается ли цитата на странице page или в пределах
// ±window соседних страниц документа. Страницы берутся в порядке среза, а не
// по арифметике номеров: после OCR и отбрасывания пустых страниц нумерация
// может идти с пропусками.
//
// Сравнение мягкое: NormalizeForQuote снимает переносы, мягкие дефисы, ё/е и
// лишние пробелы. Цитата короче 20 символов проверяется точным вхождением в
// исходный текст страницы: после нормализации такие строки совпадают слишком
// легко.
//
// window == 0 означает «только указанная страница», window < 0 — значение из
// FGOS_CITATION_WINDOW (по умолчанию DefaultCitationWindow).
func QuoteFound(pages []Page, page int, quote string, window int) bool {
	normalizedQuote := NormalizeForQuote(quote)
	if normalizedQuote == "" {
		return false
	}
	if window < 0 {
		window = citationWindowFromEnv()
	}
	candidates := citationCandidates(pages, page, window)
	if len(candidates) == 0 {
		return false
	}

	if len([]rune(normalizedQuote)) < minFuzzyQuoteRunes {
		exact := strings.TrimSpace(quote)
		if exact == "" {
			return false
		}
		for _, candidate := range candidates {
			if strings.Contains(candidate.Text, exact) {
				return true
			}
		}
		return false
	}

	for _, candidate := range candidates {
		if strings.Contains(NormalizeForQuote(candidate.Text), normalizedQuote) {
			return true
		}
	}
	return false
}

// citationCandidates возвращает страницы для проверки цитаты: сначала
// указанную, затем соседние в пределах окна. Если указанной страницы нет в
// документе, кандидатов нет: номер страницы в цитате невалиден.
func citationCandidates(pages []Page, page int, window int) []Page {
	index := -1
	for position, item := range pages {
		if item.Number == page {
			index = position
			break
		}
	}
	if index < 0 {
		return nil
	}
	if window < 0 {
		window = 0
	}
	start := index - window
	if start < 0 {
		start = 0
	}
	end := index + window
	if end >= len(pages) {
		end = len(pages) - 1
	}
	candidates := make([]Page, 0, end-start+1)
	candidates = append(candidates, pages[index])
	for offset := 1; offset <= window; offset++ {
		if index+offset <= end {
			candidates = append(candidates, pages[index+offset])
		}
		if index-offset >= start {
			candidates = append(candidates, pages[index-offset])
		}
	}
	return candidates
}

// citationWindowFromEnv читает FGOS_CITATION_WINDOW. 0 отключает соседние
// страницы, отрицательное или некорректное значение даёт
// DefaultCitationWindow.
func citationWindowFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("FGOS_CITATION_WINDOW"))
	if raw == "" {
		return DefaultCitationWindow
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return DefaultCitationWindow
	}
	return value
}
