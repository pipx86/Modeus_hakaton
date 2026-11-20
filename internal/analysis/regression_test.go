package analysis

import "testing"

func TestOLSRecoversPlane(t *testing.T) {
	y := []float64{3, 5, 7, 9, 11}
	x := [][]float64{{0}, {1}, {2}, {3}, {4}}
	result := OLS(y, x, []string{"x"})
	if len(result.Coefficients) != 1 || result.Samples != 5 || result.R2 < 0.99 {
		t.Fatalf("result = %+v", result)
	}
	if result.Coefficients[0].Estimate < 1.99 || result.Coefficients[0].Estimate > 2.01 {
		t.Fatalf("slope = %+v", result.Coefficients[0])
	}
}
