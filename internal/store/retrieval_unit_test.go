package store

import (
	"math"
	"testing"
)

func TestПроверкаРазмерностиВектора(t *testing.T) {
	vector := make([]float32, 768)
	if _, ok := formatVector(vector); ok {
		t.Fatal("нулевой вектор нельзя сравнить косинусом")
	}
	vector[0] = float32(math.NaN())
	if _, ok := formatVector(vector); ok {
		t.Fatal("NaN недопустим")
	}
	vector[0] = 1
	if _, ok := formatVector(vector); !ok {
		t.Fatal("корректный вектор отклонён")
	}
}
