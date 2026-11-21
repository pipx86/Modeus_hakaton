package fgos

import (
	"os"
	"testing"
)

// TestMain отключает poppler: синтетические PDF в тестах рассчитаны на
// встроенный извлекатель, а pdftotext на машине разработчика даёт другой
// результат и ломает ожидания.
func TestMain(m *testing.M) {
	_ = os.Setenv("FGOS_PDFTOTEXT", "off")
	os.Exit(m.Run())
}
