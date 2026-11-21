package programwatch

import (
	"html"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"activity-descriptor/internal/fgos"
)

const (
	// HTMLPageNumbersWarning добавляется к предупреждениям черновика при разборе
	// HTML: у HTML-страницы нет постраничного разбиения, номера страниц —
	// условные порции текста.
	HTMLPageNumbersWarning = "номера страниц из HTML условные"
	// htmlPageRunes — целевой размер условной страницы. Реальные страницы ФГОС
	// содержат 2–4 тыс. символов; 3000 сохраняет цитаты в пределах одного-двух
	// условных листов и не раздувает число чанков.
	htmlPageRunes = 3000
)

var (
	// htmlCommentPattern — комментарии: в них иногда лежит закомментированная
	// разметка, которая не должна попасть в текст.
	htmlCommentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)
	// htmlHiddenBlockPattern вырезает script/style/noscript/template вместе с
	// содержимым. Скрипты и стили содержат код, а не текст стандарта, и без
	// вырезания попадали бы в цитаты.
	htmlHiddenBlockPattern = regexp.MustCompile(`(?is)<(?:script|style|noscript|template)\b[^>]*>.*?</(?:script|style|noscript|template)\s*>`)
	// htmlBlockBoundaryPattern превращает границы блоков в перевод строки, чтобы
	// соседние ячейки таблицы и абзацы не склеивались в одно слово.
	htmlBlockBoundaryPattern = regexp.MustCompile(`(?i)</?(?:p|div|br|li|tr|td|th|h[1-6]|section|article|table|thead|tbody|tfoot|ul|ol|dl|dt|dd|blockquote|pre|header|footer|main|nav|aside|figure|figcaption|form|hr)\b[^>]*>`)
	// htmlTagPattern убирает остальные (строчные) теги.
	htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)
	// fgosCodePattern находит коды компетенций в HTML-тексте: УК-1, ОПК-2,
	// ПК-3, в том числе с пробелами вокруг дефиса и разными тире.
	fgosCodePattern = regexp.MustCompile(`(?i)(?:ук|опк|пк)\s*[-–—]\s*\d+`)
)

// HTMLText превращает HTML-страницу в обычный текст: вырезает script, style,
// noscript и template вместе с содержимым, убирает комментарии и теги,
// декодирует HTML-сущности (html.UnescapeString), схлопывает пробелы и пустые
// строки. Абзацы разделяются одним переводом строки — по ним
// PagesFromHTML строит условные страницы.
func HTMLText(r io.Reader) (string, error) {
	if r == nil {
		return "", nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return htmlTextFromString(string(data)), nil
}

// htmlTextFromString — детерминированная часть HTMLText без чтения.
func htmlTextFromString(source string) string {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	text := htmlCommentPattern.ReplaceAllString(source, "\n")
	text = htmlHiddenBlockPattern.ReplaceAllString(text, "\n")
	text = htmlBlockBoundaryPattern.ReplaceAllString(text, "\n")
	text = htmlTagPattern.ReplaceAllString(text, "")
	text = html.UnescapeString(text)

	lines := make([]string, 0, 128)
	for _, line := range strings.Split(text, "\n") {
		// Fields понимает U+00A0 из &nbsp; и схлопывает повторные пробелы.
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// LooksLikeFGOS сообщает, похож ли текст на полный текст ФГОС: на странице есть
// не меньше трёх кодов вида УК-1/ОПК-2/ПК-3 либо одновременно слова
// «компетенц» и «требования к результатам». Проверка нужна, чтобы не разбирать
// как стандарт обычную новость или страницу со ссылкой на PDF.
func LooksLikeFGOS(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	if len(fgosCodePattern.FindAllStringIndex(text, 3)) >= 3 {
		return true
	}
	// Фраза в HTML может быть разорвана переносом строки, поэтому сигнальные
	// слова ищем в тексте с уже схлопнутыми пробелами.
	signal := strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.Contains(signal, "компетенц") && strings.Contains(signal, "требования к результатам")
}

// PagesFromHTML режет HTML-текст на условные страницы по ~3000 символов, не
// разрывая абзац. Номера страниц идут с 1; точных страниц у HTML нет, поэтому
// вызывающий код добавляет предупреждение HTMLPageNumbersWarning.
//
// Абзац длиннее страницы (например, весь стандарт одной строкой без разметки)
// делится по словам: иначе страница получилась бы неограниченной длины.
func PagesFromHTML(text string) []fgos.Page {
	paragraphs := strings.Split(text, "\n")
	pages := make([]fgos.Page, 0, len(paragraphs)/2+1)
	current := strings.Builder{}
	currentRunes := 0
	flush := func() {
		body := strings.TrimSpace(current.String())
		current.Reset()
		currentRunes = 0
		if body == "" {
			return
		}
		pages = append(pages, fgos.Page{Number: len(pages) + 1, Text: body})
	}

	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		paragraphRunes := len([]rune(paragraph))
		if paragraphRunes > htmlPageRunes {
			flush()
			for _, part := range splitHTMLParagraph(paragraph, htmlPageRunes) {
				current.WriteString(part)
				flush()
			}
			continue
		}
		if currentRunes > 0 && currentRunes+1+paragraphRunes > htmlPageRunes {
			flush()
		}
		if currentRunes > 0 {
			current.WriteString("\n")
			currentRunes++
		}
		current.WriteString(paragraph)
		currentRunes += paragraphRunes
	}
	flush()
	return pages
}

// splitHTMLParagraph делит слишком длинный абзац по словам на части не больше
// limit символов. Слово длиннее limit остаётся целым: резать слова нельзя, иначе
// цитата станет непоисковой.
func splitHTMLParagraph(paragraph string, limit int) []string {
	if limit <= 0 {
		limit = htmlPageRunes
	}
	words := strings.Fields(paragraph)
	parts := make([]string, 0, len(words)/64+1)
	current := strings.Builder{}
	currentRunes := 0
	for _, word := range words {
		wordRunes := len([]rune(word))
		if currentRunes > 0 && currentRunes+1+wordRunes > limit {
			parts = append(parts, current.String())
			current.Reset()
			currentRunes = 0
		}
		if currentRunes > 0 {
			current.WriteString(" ")
			currentRunes++
		}
		current.WriteString(word)
		currentRunes += wordRunes
	}
	if currentRunes > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// htmlTextEnabled читает FGOS_HTML_TEXT. Пустое или некорректное значение
// означает true: HTML-текст стандарта — основной источник, если он есть.
// false возвращает прежнее поведение — искать на странице ссылку на документ.
func htmlTextEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("FGOS_HTML_TEXT"))
	if raw == "" {
		return true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return true
	}
	return value
}
