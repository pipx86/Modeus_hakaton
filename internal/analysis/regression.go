package analysis

import "math"

// RegressionCoefficient is one explainable OLS coefficient.
type RegressionCoefficient struct {
	Name     string  `json:"name"`
	Estimate float64 `json:"estimate"`
	StdError float64 `json:"std_error"`
	TStat    float64 `json:"t_stat"`
}

// RegressionResult is a compact, explainable linear model.
type RegressionResult struct {
	Samples      int                     `json:"samples"`
	R2           float64                 `json:"r2"`
	Intercept    float64                 `json:"intercept"`
	Coefficients []RegressionCoefficient `json:"coefficients"`
}

// OLS fits y = intercept + sum(beta_i * x_i) with a small ridge term so a
// near-singular design matrix does not crash the endpoint.
func OLS(y []float64, x [][]float64, names []string) RegressionResult {
	if len(y) == 0 || len(x) != len(y) || len(names) == 0 {
		return RegressionResult{}
	}
	cols := len(names)
	rows := len(y)
	// Design matrix with intercept.
	design := make([][]float64, rows)
	for i := range design {
		if len(x[i]) != cols {
			return RegressionResult{}
		}
		design[i] = make([]float64, cols+1)
		design[i][0] = 1
		copy(design[i][1:], x[i])
	}
	// Normal equations: (X'X + lambda I) beta = X'y.
	size := cols + 1
	xtx := make([][]float64, size)
	for i := range xtx {
		xtx[i] = make([]float64, size)
	}
	xty := make([]float64, size)
	for i := 0; i < rows; i++ {
		for a := 0; a < size; a++ {
			xty[a] += design[i][a] * y[i]
			for b := 0; b < size; b++ {
				xtx[a][b] += design[i][a] * design[i][b]
			}
		}
	}
	for i := 1; i < size; i++ {
		xtx[i][i] += 1e-6
	}
	beta, ok := solveLinear(xtx, xty)
	if !ok {
		return RegressionResult{}
	}
	mean := 0.0
	for _, value := range y {
		mean += value
	}
	mean /= float64(rows)
	ssTotal, ssResidual := 0.0, 0.0
	for i := 0; i < rows; i++ {
		predicted := beta[0]
		for j := 1; j < size; j++ {
			predicted += beta[j] * design[i][j]
		}
		residual := y[i] - predicted
		ssResidual += residual * residual
		ssTotal += (y[i] - mean) * (y[i] - mean)
	}
	r2 := 0.0
	if ssTotal > 0 {
		r2 = 1 - ssResidual/ssTotal
	}
	// Approximate standard errors from residual variance and diagonal of
	// inverse X'X; with tiny ridge this is a stable descriptive estimate.
	variance := 0.0
	if rows > size {
		variance = ssResidual / float64(rows-size)
	}
	coefficients := make([]RegressionCoefficient, 0, cols)
	for j := 0; j < cols; j++ {
		index := j + 1
		se := 0.0
		if diagonal, ok := solveLinear(xtx, unitVector(size, index)); ok {
			se = math.Sqrt(math.Max(0, variance*diagonal[index]))
		}
		t := 0.0
		if se > 0 {
			t = beta[index] / se
		}
		coefficients = append(coefficients, RegressionCoefficient{
			Name: names[j], Estimate: beta[index], StdError: se, TStat: t,
		})
	}
	return RegressionResult{Samples: rows, R2: r2, Intercept: beta[0], Coefficients: coefficients}
}

func unitVector(size, index int) []float64 {
	out := make([]float64, size)
	if index >= 0 && index < size {
		out[index] = 1
	}
	return out
}

func solveLinear(a [][]float64, b []float64) ([]float64, bool) {
	n := len(a)
	matrix := make([][]float64, n)
	for i := range a {
		matrix[i] = append([]float64(nil), a[i]...)
		matrix[i] = append(matrix[i], b[i])
	}
	for col := 0; col < n; col++ {
		pivot := col
		for row := col + 1; row < n; row++ {
			if math.Abs(matrix[row][col]) > math.Abs(matrix[pivot][col]) {
				pivot = row
			}
		}
		if math.Abs(matrix[pivot][col]) < 1e-12 {
			return nil, false
		}
		matrix[col], matrix[pivot] = matrix[pivot], matrix[col]
		divisor := matrix[col][col]
		for j := col; j <= n; j++ {
			matrix[col][j] /= divisor
		}
		for row := 0; row < n; row++ {
			if row == col {
				continue
			}
			factor := matrix[row][col]
			for j := col; j <= n; j++ {
				matrix[row][j] -= factor * matrix[col][j]
			}
		}
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = matrix[i][n]
	}
	return out, true
}
