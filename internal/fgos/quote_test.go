package fgos

import (
	"testing"
)

func TestNormalizeForQuote(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"мягкий дефис", "инфор\u00adмация", "информация"},
		{"перенос по дефису", "информа-\nция", "информация"},
		{"перенос CRLF", "информа-\r\nция", "информация"},
		{"неразрывный дефис", "информа\u2011ция", "информа-ция"},
		{"пробелы и табы", "  Методы\u00a0\tсистемного \n анализа ", "методы системного анализа"},
		{"ё и регистр", "ЁЖИК И ЁЛКА", "ежик и елка"},
		{"пустая строка", " \u00a0\n\t ", ""},
		{"обычный текст", "Способен осуществлять поиск", "способен осуществлять поиск"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := NormalizeForQuote(testCase.input); got != testCase.want {
				t.Fatalf("NormalizeForQuote(%q) = %q, ожидали %q", testCase.input, got, testCase.want)
			}
		})
	}
}

// quoteTestPages — документ с переносом внутри слова, двойными пробелами и
// пропуском номера страницы: проверяются и нормализация, и порядок среза.
func quoteTestPages() []Page {
	return []Page{
		{Number: 1, Text: "УК-1. Способен осуществлять поиск и критический анализ информации."},
		{Number: 2, Text: "информа-\nция и методы  анализа"},
		{Number: 4, Text: "ОПК-2. Способен разрабатывать программное обеспечение."},
		{Number: 5, Text: "методы  анализа данных"},
	}
}

func TestQuoteFound(t *testing.T) {
	const neighborQuote = "информация и методы анализа"
	pages := quoteTestPages()

	cases := []struct {
		name   string
		pages  []Page
		page   int
		quote  string
		window int
		want   bool
	}{
		{"та же страница", pages, 2, neighborQuote, 1, true},
		{"соседняя вперёд", pages, 1, neighborQuote, 1, true},
		{"окно выключено", pages, 1, neighborQuote, 0, false},
		{"соседняя назад", pages, 4, "информация и методы анализа", 1, true},
		{"порядок среза важнее номеров", pages, 2, "Способен разрабатывать программное обеспечение", 1, true},
		{"вне окна", pages, 1, "Способен разрабатывать программное обеспечение", 1, false},
		{"страницы нет", pages, 9, neighborQuote, 1, false},
		{"пустая цитата", pages, 1, "   ", 1, false},
		{"короткая цитата без нормализации", pages, 5, "методы анализа", 1, false},
		{"короткая цитата точно", pages, 5, "методы  анализа", 1, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := QuoteFound(testCase.pages, testCase.page, testCase.quote, testCase.window)
			if got != testCase.want {
				t.Fatalf("QuoteFound(page=%d, window=%d) = %v, ожидали %v",
					testCase.page, testCase.window, got, testCase.want)
			}
		})
	}
}

// TestQuoteFoundNegativeWindowUsesEnv — отрицательное окно читает
// FGOS_CITATION_WINDOW: 0 запрещает соседние страницы.
func TestQuoteFoundNegativeWindowUsesEnv(t *testing.T) {
	pages := quoteTestPages()
	quote := "информация и методы анализа"

	t.Setenv("FGOS_CITATION_WINDOW", "0")
	if QuoteFound(pages, 1, quote, -1) {
		t.Fatal("FGOS_CITATION_WINDOW=0 должен запрещать соседние страницы")
	}
	t.Setenv("FGOS_CITATION_WINDOW", "1")
	if !QuoteFound(pages, 1, quote, -1) {
		t.Fatal("FGOS_CITATION_WINDOW=1 должен разрешать соседнюю страницу")
	}
}

// TestCitationWindowFromEnv — значение по умолчанию, 0, явное окно,
// некорректные значения и чтение env литералом Extractor.
func TestCitationWindowFromEnv(t *testing.T) {
	cases := []struct {
		value string
		want  int
	}{
		{"", DefaultCitationWindow},
		{"0", 0},
		{"2", 2},
		{"-1", DefaultCitationWindow},
		{"abc", DefaultCitationWindow},
	}
	for _, testCase := range cases {
		t.Run("value="+testCase.value, func(t *testing.T) {
			t.Setenv("FGOS_CITATION_WINDOW", testCase.value)
			extractor := NewExtractor(nil)
			if extractor.CitationWindow != testCase.want {
				t.Fatalf("CitationWindow = %d, ожидали %d", extractor.CitationWindow, testCase.want)
			}
			if got := extractor.citationWindow(); got != testCase.want {
				t.Fatalf("citationWindow() = %d, ожидали %d", got, testCase.want)
			}
		})
	}

	t.Setenv("FGOS_CITATION_WINDOW", "3")
	literal := &Extractor{}
	if got := literal.citationWindow(); got != 3 {
		t.Fatalf("литерал должен читать env: citationWindow() = %d, ожидали 3", got)
	}
}
