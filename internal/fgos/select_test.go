package fgos

import (
	"fmt"
	"reflect"
	"testing"
)

// neutralPage — страница с общими положениями ФГОС: ни одного признака
// раздела компетенций.
func neutralPage(number int) Page {
	return Page{
		Number: number,
		Text: fmt.Sprintf(
			"Общие положения стандарта, страница %d. Настоящий документ устанавливает правила обучения.",
			number),
	}
}

// competencyPage — страница с заголовком раздела и кодом компетенции.
func competencyPage(number int, code string) Page {
	return Page{
		Number: number,
		Text: fmt.Sprintf(
			"Требования к результатам освоения. %s. Способен выполнять задачу %d.", code, number),
	}
}

// continuationPage — страница-продолжение таблицы: заголовка раздела нет,
// есть только строки с кодами компетенций.
func continuationPage(number int, code string) Page {
	return Page{
		Number: number,
		Text:   fmt.Sprintf("%s. Способен применять методы %d.", code, number),
	}
}

func selectedNumbers(pages []Page) []int {
	numbers := make([]int, 0, len(pages))
	for _, page := range pages {
		numbers = append(numbers, page.Number)
	}
	return numbers
}

// TestSelectCompetencyPagesKeepsContinuation — страница с компетенциями и
// соседняя страница-продолжение таблицы попадают в отбор вместе с окном ±1.
func TestSelectCompetencyPagesKeepsContinuation(t *testing.T) {
	pages := []Page{
		neutralPage(1),
		neutralPage(2),
		competencyPage(3, "УК-1"),
		continuationPage(4, "ОПК-2"),
		neutralPage(5),
		neutralPage(6),
		neutralPage(7),
		neutralPage(8),
	}
	selected, warnings := SelectCompetencyPages(pages)
	want := []int{2, 3, 4, 5}
	if got := selectedNumbers(selected); !reflect.DeepEqual(got, want) {
		t.Fatalf("отобраны страницы %v, ожидали %v", got, want)
	}
	if len(warnings) != 0 {
		t.Fatalf("4 из 8 страниц не требуют предупреждений: %v", warnings)
	}
}

// TestSelectCompetencyPagesTwoOfTen — раздел компетенций занимает 2 страницы
// из 10: с окном 1 модель получает 4 страницы, а не весь документ.
func TestSelectCompetencyPagesTwoOfTen(t *testing.T) {
	pages := make([]Page, 0, 10)
	for number := 1; number <= 10; number++ {
		pages = append(pages, neutralPage(number))
	}
	pages[3] = competencyPage(4, "УК-1")
	pages[4] = continuationPage(5, "ПК-2")

	selected, warnings := SelectCompetencyPages(pages)
	want := []int{3, 4, 5, 6}
	if got := selectedNumbers(selected); !reflect.DeepEqual(got, want) {
		t.Fatalf("отобраны страницы %v, ожидали %v", got, want)
	}
	if len(warnings) != 0 {
		t.Fatalf("4 из 10 страниц — обычный отбор, предупреждений быть не должно: %v", warnings)
	}
}

// TestSelectCompetencyPagesWithoutMarkersReturnsAll — нестандартная вёрстка не
// должна терять документ: без маркеров возвращаются все страницы и
// предупреждение.
func TestSelectCompetencyPagesWithoutMarkersReturnsAll(t *testing.T) {
	pages := make([]Page, 0, 6)
	for number := 1; number <= 6; number++ {
		pages = append(pages, neutralPage(number))
	}
	selected, warnings := SelectCompetencyPages(pages)
	if !reflect.DeepEqual(selected, pages) {
		t.Fatalf("без маркеров должны вернуться все страницы: %+v", selectedNumbers(selected))
	}
	if len(warnings) != 1 || warnings[0] != SectionNotFoundWarning {
		t.Fatalf("ожидали предупреждение %q, получили %v", SectionNotFoundWarning, warnings)
	}
}

// TestSelectCompetencyPagesWindow — window=0 отключает соседние страницы,
// window=2 добавляет по две с каждой стороны; ровно 60% не считается
// «отобрано почти всё».
func TestSelectCompetencyPagesWindow(t *testing.T) {
	pages := make([]Page, 0, 10)
	for number := 1; number <= 10; number++ {
		pages = append(pages, neutralPage(number))
	}
	pages[2] = competencyPage(3, "УК-1")
	pages[6] = competencyPage(7, "ПК-2")

	zero, zeroWarnings := SelectCompetencyPagesWithOptions(pages, SelectOptions{Window: 0})
	if want := []int{3, 7}; !reflect.DeepEqual(selectedNumbers(zero), want) {
		t.Fatalf("window=0: страницы %v, ожидали %v", selectedNumbers(zero), want)
	}
	if len(zeroWarnings) != 0 {
		t.Fatalf("window=0: предупреждения %v", zeroWarnings)
	}

	one, oneWarnings := SelectCompetencyPagesWithOptions(pages, SelectOptions{Window: 1})
	if want := []int{2, 3, 4, 6, 7, 8}; !reflect.DeepEqual(selectedNumbers(one), want) {
		t.Fatalf("window=1: страницы %v, ожидали %v", selectedNumbers(one), want)
	}
	if len(oneWarnings) != 0 {
		t.Fatalf("ровно 60%% страниц не требует предупреждения: %v", oneWarnings)
	}

	two, twoWarnings := SelectCompetencyPagesWithOptions(pages, SelectOptions{Window: 2})
	if want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}; !reflect.DeepEqual(selectedNumbers(two), want) {
		t.Fatalf("window=2: страницы %v, ожидали %v", selectedNumbers(two), want)
	}
	if !hasWarning(twoWarnings, SectionSelectedWarning(9, 10)) {
		t.Fatalf("window=2: нет предупреждения об отборе 9 из 10: %v", twoWarnings)
	}
}

// TestSelectCompetencyPagesMaxPages — потолок отбора срабатывает только на
// отобранных страницах и никогда не превышает 80.
func TestSelectCompetencyPagesMaxPages(t *testing.T) {
	six := make([]Page, 0, 6)
	for number := 1; number <= 6; number++ {
		six = append(six, competencyPage(number, "УК-1"))
	}
	selected, warnings := SelectCompetencyPagesWithOptions(six, SelectOptions{Window: 0, MaxPages: 3})
	if len(selected) != 3 {
		t.Fatalf("MaxPages=3: отобрано %d страниц", len(selected))
	}
	if !hasWarning(warnings, SectionLimitWarning(6, 6, 3)) {
		t.Fatalf("нет предупреждения о потолке: %v", warnings)
	}

	many := make([]Page, 0, 100)
	for number := 1; number <= 100; number++ {
		many = append(many, competencyPage(number, "УК-1"))
	}
	selected, warnings = SelectCompetencyPagesWithOptions(many, SelectOptions{Window: 0, MaxPages: 500})
	if len(selected) != DefaultSectionMaxPages {
		t.Fatalf("MaxPages больше 80 должен ограничиваться 80: отобрано %d", len(selected))
	}
	if !hasWarning(warnings, SectionLimitWarning(100, 100, DefaultSectionMaxPages)) {
		t.Fatalf("нет предупреждения о лимите 80: %v", warnings)
	}
	if !hasWarning(warnings, SectionSelectedWarning(DefaultSectionMaxPages, 100)) {
		t.Fatalf("нет предупреждения об отборе 80 из 100: %v", warnings)
	}
}

// TestSelectCompetencyPagesProfessionalOnlyIsNotMarker — «профессиональн»
// учитывается только вместе с «компетенц», иначе общие положения о
// профессиональной деятельности ошибочно попадали бы в отбор.
func TestSelectCompetencyPagesProfessionalOnlyIsNotMarker(t *testing.T) {
	if isCompetencySectionPage("Профессиональная деятельность и общие положения.") {
		t.Fatal("«профессиональная деятельность» без «компетенц» не признак раздела")
	}
	if !isCompetencySectionPage("Профессиональные компетенции выпускника.") {
		t.Fatal("«профессиональные компетенции» должны быть признаком раздела")
	}
}

// TestSelectCompetencyPagesEmpty — пустой документ не даёт предупреждений.
func TestSelectCompetencyPagesEmpty(t *testing.T) {
	selected, warnings := SelectCompetencyPages(nil)
	if len(selected) != 0 || len(warnings) != 0 {
		t.Fatalf("пустой документ: pages=%v warnings=%v", selected, warnings)
	}
}
