package ingestion

import (
	"strings"
	"testing"
)

func TestИзвлечениеИзТекстаПрограммы(t *testing.T) {
	text := "Образовательная программа\nПланируемые результаты\nУК-1. Способен анализировать данные\nЗнать:\n- методы анализа\nУметь: проверять данные\nВладеть: инструментами обработки\nОПК-2\nСпособен документировать результаты\nЗнания: правила оформления"
	p, err := ExtractEducationText(text, "Учебная программа")
	if err != nil || len(p.Competencies) != 2 || p.Competencies[0].Code != "УК-1" || len(p.Competencies[0].KSA) != 3 {
		t.Fatalf("%+v %v", p, err)
	}
	if !strings.Contains(p.Competencies[0].KSA[0].Title, "методы анализа") {
		t.Fatal("исходный текст не сохранён")
	}
	for _, bad := range []string{"произвольный текст", "ПК-1. Анализировать данные"} {
		if _, err := ExtractEducationText(bad, "Программа"); err == nil {
			t.Fatal("неполный документ принят")
		}
	}
}
