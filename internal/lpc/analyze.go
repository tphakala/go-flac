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

// levinson runs the Levinson-Durbin recursion on the autocorrelation (which
// must have length >= maxOrder+1) and returns, for each order o in 1..maxOrder,
// the o predictor coefficients in lpcByOrder[o] and the prediction error in
// errByOrder[o] (errByOrder[0] = autoc[0]). The stored coefficients are sign-
// adjusted so that pred = sum_j lpcByOrder[o][j] * x[n-1-j] matches the decoder.
// maxComputed is the highest order actually produced; it is < maxOrder if the
// recursion hit a non-positive error and stopped early. lpcByOrder[o] is nil
// for orders beyond maxComputed.
func levinson(autoc []float64, maxOrder int) (lpcByOrder [][]float64, errByOrder []float64, maxComputed int) {
	lpcByOrder = make([][]float64, maxOrder+1)
	errByOrder = make([]float64, maxOrder+1)
	errByOrder[0] = autoc[0]

	lpc := make([]float64, maxOrder)
	err := autoc[0]
	for i := range maxOrder {
		if err <= 0 {
			return lpcByOrder, errByOrder, i
		}
		// Reflection coefficient.
		r := -autoc[i+1]
		for j := range i {
			r -= lpc[j] * autoc[i-j]
		}
		r /= err

		lpc[i] = r
		for j := range i / 2 {
			tmp := lpc[j]
			lpc[j] += r * lpc[i-1-j]
			lpc[i-1-j] += r * tmp
		}
		if i%2 == 1 {
			lpc[i/2] += lpc[i/2] * r
		}
		err *= 1 - r*r

		order := i + 1
		c := make([]float64, order)
		for j := range order {
			c[j] = -lpc[j] // predictor coefficients (decoder sign convention)
		}
		lpcByOrder[order] = c
		errByOrder[order] = err
	}
	return lpcByOrder, errByOrder, maxOrder
}

// quantizeCoefficients converts float predictor coefficients to int32 with a
// non-negative shift at the given precision (in bits), using error-feedback
// rounding. The shift is chosen so the largest-magnitude coefficient fills the
// precision range, then clamped to [0, maxQLPShift]; coefficients are clamped
// to [-2^(precision-1), 2^(precision-1)-1]. Returns ok=false when the
// coefficients carry no usable predictor (all zero, NaN, or Inf).
func quantizeCoefficients(lpc []float64, precision int) (qcoeff []int32, shift int, ok bool) {
	cmax := 0.0
	for _, c := range lpc {
		if math.IsNaN(c) || math.IsInf(c, 0) {
			return nil, 0, false
		}
		if a := math.Abs(c); a > cmax {
			cmax = a
		}
	}
	if cmax <= 0 {
		return nil, 0, false
	}

	// cmax = frac * 2^exp with frac in [0.5, 1). Scaling by 2^(precision-1-exp)
	// puts the largest coefficient at ~2^(precision-1).
	_, exp := math.Frexp(cmax)
	shift = precision - 1 - exp
	if shift > maxQLPShift {
		shift = maxQLPShift
	}
	if shift < 0 {
		// Coefficients too large for a non-negative shift; the decoder rejects
		// negative shift, so clamp to 0 and let the coeff clamp below handle it.
		shift = 0
	}

	qmax := int32(1)<<(precision-1) - 1
	qmin := -(int32(1) << (precision - 1))
	scale := math.Ldexp(1, shift) // 2^shift

	qcoeff = make([]int32, len(lpc))
	var errAcc float64
	for i, c := range lpc {
		errAcc += c * scale
		q := math.Round(errAcc)
		if q > float64(qmax) {
			q = float64(qmax)
		} else if q < float64(qmin) {
			q = float64(qmin)
		}
		errAcc -= q
		qcoeff[i] = int32(q)
	}
	return qcoeff, shift, true
}
