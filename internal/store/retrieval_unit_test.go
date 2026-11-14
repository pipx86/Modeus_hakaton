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

func TestВекторОтклоняетБесконечностьИНечисло(t *testing.T) {
	for _, value := range []float32{float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.NaN())} {
		vector := make([]float32, 768)
		vector[4] = value
		if _, ok := formatVector(vector); ok {
			t.Fatalf("недопустимое число принято: %v", value)
		}
	}
}
