package programwatch

import (
	"strings"
	"testing"
)

func TestHTMLTextStripsMarkupAndDecodesEntities(t *testing.T) {
	page := `<html><head><title>ФГОС 09.03.01</title>
<style>.x { color: red }</style>
<script>var leaked = "УК-99";</script></head>
<body><!-- комментарий с УК-98 -->
<h1>Требования к результатам освоения</h1>
<p>УК-1. Способен&nbsp;осуществлять поиск &laquo;информации&raquo;.</p>
<noscript>УК-97</noscript>
<table><tr><td>ОПК-2</td><td>Способен разрабатывать ПО</td></tr></table>
<a href="/files/fgos.pdf">Скачать</a>
</body></html>`

	text, err := HTMLText(strings.NewReader(page))
	if err != nil {
		t.Fatalf("HTMLText: %v", err)
	}
	for _, gone := range []string{"<", "script", "style", "noscript", "color:red", "УК-99", "УК-98", "УК-97"} {
		if strings.Contains(text, gone) {
			t.Errorf("в тексте осталось %q:\n%s", gone, text)
		}
	}
	for _, want := range []string{
		"ФГОС 09.03.01",
		"Требования к результатам освоения",
		"УК-1. Способен осуществлять поиск «информации».",
		"ОПК-2",
		"Способен разрабатывать ПО",
		"Скачать",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в тексте нет %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\n\n") {
		t.Errorf("пустые строки должны схлопываться:\n%q", text)
	}
}

func TestHTMLTextEmpty(t *testing.T) {
	text, err := HTMLText(nil)
	if err != nil || text != "" {
		t.Fatalf("HTMLText(nil) = %q, %v", text, err)
	}
	text, err = HTMLText(strings.NewReader("  \n<p> </p>"))
	if err != nil || text != "" {
		t.Fatalf("HTMLText(пустой) = %q, %v", text, err)
	}
}

func TestLooksLikeFGOS(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "три кода",
			text: "УК-1. Способен... ОПК-2. Способен... ПК-3. Способен...",
			want: true,
		},
		{
			name: "коды с пробелами и тире",
			text: "УК - 1, ОПК–2, ПК—3",
			want: true,
		},
		{
			name: "компетенции и требования",
			text: "Требования к результатам освоения программы. Перечень компетенций.",
			want: true,
		},
		{
			name: "фраза разорвана переносом",
			text: "Требования к\nрезультатам освоения программы.\nКомпетенции выпускника.",
			want: true,
		},
		{
			name: "новость без стандарта",
			text: "Новости университета. Приёмная кампания 2026 года.",
			want: false,
		},
		{
			name: "два кода без требований",
			text: "УК-1 и УК-2. Общие положения программы.",
			want: false,
		},
		{
			name: "пусто",
			text: "   \n ",
			want: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := LooksLikeFGOS(testCase.text); got != testCase.want {
				t.Fatalf("LooksLikeFGOS(%q) = %v, ожидали %v", testCase.text, got, testCase.want)
			}
		})
	}
}

func TestPagesFromHTMLKeepsParagraphsWhole(t *testing.T) {
	paragraph := strings.Repeat("а", 800)
	paragraphs := make([]string, 0, 6)
	for index := 0; index < 6; index++ {
		paragraphs = append(paragraphs, paragraph)
	}
	pages := PagesFromHTML(strings.Join(paragraphs, "\n"))
	if len(pages) != 2 {
		t.Fatalf("ожидали 2 условные страницы, получили %d", len(pages))
	}
	for index, page := range pages {
		if page.Number != index+1 {
			t.Fatalf("номер страницы %d, ожидали %d", page.Number, index+1)
		}
		if strings.TrimSpace(page.Text) == "" {
			t.Fatalf("страница %d пустая", page.Number)
		}
		if got := strings.Count(page.Text, "\n"); got != 2 {
			t.Fatalf("страница %d должна содержать 3 целых абзаца, переводов строк: %d", page.Number, got)
		}
	}
}

func TestPagesFromHTMLSplitsOversizedParagraphByWords(t *testing.T) {
	words := make([]string, 0, 800)
	for index := 0; index < 800; index++ {
		words = append(words, "слово00000")
	}
	source := strings.Join(words, " ")
	pages := PagesFromHTML(source)
	if len(pages) < 2 {
		t.Fatalf("длинный абзац должен делиться на несколько страниц: %d", len(pages))
	}
	reconstructed := make([]string, 0, len(pages))
	for _, page := range pages {
		if size := len([]rune(page.Text)); size > htmlPageRunes {
			t.Fatalf("страница %d длиннее %d символов: %d", page.Number, htmlPageRunes, size)
		}
		reconstructed = append(reconstructed, page.Text)
	}
	if got := strings.Join(reconstructed, " "); got != source {
		t.Fatalf("текст потерялся при делении:\nполучено: %q\nожидали:  %q", got, source)
	}
}

func TestPagesFromHTMLSkipsEmptyText(t *testing.T) {
	if pages := PagesFromHTML("  \n\n \n"); len(pages) != 0 {
		t.Fatalf("пустой текст не должен давать страниц: %+v", pages)
	}
	pages := PagesFromHTML("первый\n\nвторой")
	if len(pages) != 1 || pages[0].Text != "первый\nвторой" {
		t.Fatalf("страницы: %+v", pages)
	}
}
