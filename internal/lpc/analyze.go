// Package lpc, analysis side: windowing, autocorrelation, Levinson-Durbin,
// coefficient quantization, and order estimation for the encoder. All of this
// is float64; the integer encode/decode contract lives in lpc.go and encode.go.
package lpc

import "math"

// maxQLPShift is the largest quantization shift the FLAC bitstream allows in
// the 5-bit non-negative shift field (the decoder rejects negative shift).
const maxQLPShift = 15

// TukeyWindow returns a length-n Tukey window with taper fraction alpha.
// For alpha == 0.5 (the M3b default) the middle 50% is flat (weight 1) and
// each 25% end is a cosine taper. Weights are in [0, 1]. Special cases:
// n <= 1 returns all ones; alpha <= 0 returns a rectangle; alpha >= 1 returns
// a Hann window.
func TukeyWindow(n int, alpha float64) []float64 {
	w := make([]float64, n)
	if n <= 1 {
		for i := range w {
			w[i] = 1
		}
		return w
	}
	if alpha <= 0 {
		for i := range w {
			w[i] = 1
		}
		return w
	}
	nm1 := float64(n - 1)
	if alpha >= 1 {
		for i := range w {
			w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/nm1))
		}
		return w
	}
	edge := alpha * nm1 / 2
	upper := nm1 * (1 - alpha/2)
	for i := range w {
		x := float64(i)
		switch {
		case x < edge:
			w[i] = 0.5 * (1 + math.Cos(math.Pi*(2*x/(alpha*nm1)-1)))
		case x <= upper:
			w[i] = 1
		default:
			w[i] = 0.5 * (1 + math.Cos(math.Pi*(2*x/(alpha*nm1)-2/alpha+1)))
		}
	}
	return w
}

// autocorrelate returns the autocorrelation of x for lags 0..maxLag inclusive,
// so the result has length maxLag+1. autoc[lag] = sum_{i=lag}^{N-1} x[i]*x[i-lag].
func autocorrelate(x []float64, maxLag int) []float64 {
	autoc := make([]float64, maxLag+1)
	n := len(x)
	for lag := 0; lag <= maxLag; lag++ {
		var sum float64
		for i := lag; i < n; i++ {
			sum += x[i] * x[i-lag]
		}
		autoc[lag] = sum
	}
	return autoc
}
