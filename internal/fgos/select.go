package fgos

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// DefaultSectionWindow — сколько соседних страниц добавлять к найденным по
	// умолчанию. Страница-продолжение таблицы компетенций часто не повторяет
	// заголовок раздела и маркеры, поэтому одна соседняя страница с каждой
	// стороны закрывает типовой разрыв.
	DefaultSectionWindow = 1
	// DefaultSectionMaxPages — жёсткий потолок отбора: больше 80 страниц
	// разбирать бессмысленно, это совпадает с DefaultMaxPages и защищает от
	// документа, где маркеры нашлись на каждой странице.
	DefaultSectionMaxPages = 80
	// sectionShareThreshold — доля страниц документа (в процентах), выше
	// которой отбор почти ничего не экономит. Результат всё равно остаётся
	// отобранным, но в предупреждения добавляется «отобрано N из M страниц»;
	// если не отброшено ни одной страницы, предупреждение не нужно.
	sectionShareThreshold = 60
)

// SectionNotFoundWarning — предупреждение, когда ни на одной странице нет
// признаков раздела компетенций: документ обрабатывается целиком, как раньше.
const SectionNotFoundWarning = "раздел компетенций не найден, обрабатывается весь документ"

// SectionSelectedWarning возвращает предупреждение о том, что отбор затронул
// больше 60% страниц: резать документ почти нечем, но отобранные страницы
// остаются отобранными.
func SectionSelectedWarning(selected, total int) string {
	return fmt.Sprintf("отобрано %d из %d страниц", selected, total)
}

// SectionProcessedWarning возвращает предупреждение черновика о том, что
// модель получила только раздел компетенций.
func SectionProcessedWarning(selected, total int) string {
	return fmt.Sprintf("обработано %d из %d страниц: только раздел компетенций", selected, total)
}

// SectionLimitWarning возвращает предупреждение о том, что отобранных страниц
// больше MaxPages, поэтому обработана только первая часть.
func SectionLimitWarning(selected, total, limit int) string {
	return fmt.Sprintf("отобрано %d из %d страниц, лимит %d: обработаны первые %d страниц",
		selected, total, limit, limit)
}

// sectionCodePattern находит код компетенции в строке таблицы: УК-1, ОПК-2,
// ПК-3, в том числе с пробелами вокруг дефиса. Текст перед проверкой
// приводится к нижнему регистру.
var sectionCodePattern = regexp.MustCompile(`(?:ук|опк|пк)\s*-\s*\d+`)

// SelectOptions управляет отбором страниц раздела компетенций.
type SelectOptions struct {
	// Window — сколько соседних страниц добавлять к каждой найденной. 0 —
	// только сами страницы с маркерами, отрицательное значение — значение по
	// умолчанию (DefaultSectionWindow).
	Window int
	// MaxPages — потолок результата. 0 — DefaultSectionMaxPages; значения
	// больше 80 ограничиваются 80. Отброшенные страницы в потолок не входят.
	MaxPages int
}

// SelectCompetencyPages оставляет страницы, относящиеся к разделу компетенций
// (УК/ОПК/ПК и KSA), и возвращает предупреждения о том, что отбор не сработал
// или затронул почти весь документ.
//
// Если признаков раздела нет ни на одной странице, возвращаются все страницы и
// предупреждение SectionNotFoundWarning: лучше отправить модели весь документ,
// чем потерять компетенции из нестандартно свёрстанного файла.
func SelectCompetencyPages(pages []Page) ([]Page, []string) {
	return SelectCompetencyPagesWithOptions(pages, SelectOptions{Window: DefaultSectionWindow})
}

// SelectCompetencyPagesWithOptions — SelectCompetencyPages с настройками окна и
// потолка страниц.
//
// Страницы сохраняют исходный порядок и содержимое. К найденным страницам
// добавляется окно соседних страниц: страница-продолжение таблицы может не
// содержать слов «компетенции» или «знать», но содержит строки с кодами, и без
// соседней страницы её цитаты потерялись бы.
func SelectCompetencyPagesWithOptions(pages []Page, options SelectOptions) ([]Page, []string) {
	if len(pages) == 0 {
		return []Page{}, []string{}
	}

	marked := make([]bool, len(pages))
	found := false
	for index, page := range pages {
		if isCompetencySectionPage(page.Text) {
			marked[index] = true
			found = true
		}
	}
	if !found {
		// Возвращаем копию: вызывающий код не должен зависеть от того,
		// переиспользует ли адаптер входной срез.
		return append([]Page(nil), pages...), []string{SectionNotFoundWarning}
	}

	window := options.Window
	if window < 0 {
		window = DefaultSectionWindow
	}
	if window > len(pages) {
		window = len(pages)
	}

	selectedFlags := make([]bool, len(pages))
	for index := range pages {
		if !marked[index] {
			continue
		}
		start := index - window
		if start < 0 {
			start = 0
		}
		end := index + window
		if end >= len(pages) {
			end = len(pages) - 1
		}
		for neighbor := start; neighbor <= end; neighbor++ {
			selectedFlags[neighbor] = true
		}
	}

	selected := make([]Page, 0, len(pages))
	for index, page := range pages {
		if selectedFlags[index] {
			selected = append(selected, page)
		}
	}

	warnings := []string{}
	if limit := sectionPageLimit(options.MaxPages); len(selected) > limit {
		warnings = append(warnings, SectionLimitWarning(len(selected), len(pages), limit))
		selected = selected[:limit]
	}
	if len(selected) < len(pages) && len(selected)*100 > len(pages)*sectionShareThreshold {
		warnings = append(warnings, SectionSelectedWarning(len(selected), len(pages)))
	}
	return selected, warnings
}

// isCompetencySectionPage сообщает, содержит ли страница признаки раздела
// компетенций и KSA. Проверка намеренно консервативная: ложное включение
// страницы стоит нескольких токенов, ложное исключение — потерянной
// компетенции.
func isCompetencySectionPage(text string) bool {
	normalized := normalizeSectionText(text)
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "компетенц") {
		return true
	}
	if strings.Contains(normalized, "требования к результатам освоения") {
		return true
	}
	// «профессиональн» само по себе не признак: оно встречается в общих
	// положениях и описании профессиональной деятельности. Требуемое сочетание
	// «профессиональные компетенции» уже покрыто проверкой «компетенц» выше.
	// Знать/уметь/владеть — маркеры KSA. Они же встречаются в таблицах
	// результатов освоения, поэтому страница с ними относится к разделу.
	if strings.Contains(normalized, "знать") ||
		strings.Contains(normalized, "уметь") ||
		strings.Contains(normalized, "владеть") {
		return true
	}
	// Строки таблицы с кодами: страница-продолжение без заголовка раздела.
	if sectionCodePattern.MatchString(normalized) {
		return true
	}
	return false
}

// normalizeSectionText приводит текст страницы к нижнему регистру и схлопывает
// пробелы: в PDF заголовок «Требования к результатам освоения» может быть
// разорван переносом строки.
func normalizeSectionText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// sectionPageLimit нормализует MaxPages: 0 — значение по умолчанию, больше 80
// ограничивается 80, отрицательное — тоже значение по умолчанию.
func sectionPageLimit(value int) int {
	if value <= 0 || value > DefaultSectionMaxPages {
		return DefaultSectionMaxPages
	}
	return value
}
