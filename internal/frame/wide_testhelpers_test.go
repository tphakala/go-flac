package frame

import "math/rand"

// wideSamples builds n deterministic test samples that exercise the full signed
// range of an effBps-bit value (so residuals can exceed int32 for effBps >= 25).
// seed makes channels differ. Values stay within [-2^(effBps-1), 2^(effBps-1)-1].
func wideSamples(n, effBps int, seed int64) []int64 {
	r := rand.New(rand.NewSource(seed))
	half := int64(1) << (effBps - 1)
	out := make([]int64, n)
	// A low-order ramp plus noise: predictable enough for predictors to engage,
	// wide enough to overflow int32 in the residual.
	for i := range out {
		v := int64(i)*(half/int64(n+1)) + r.Int63n(half) - half/2
		if v >= half {
			v = half - 1
		}
		if v < -half {
			v = -half
		}
		out[i] = v
	}
	return out
}

// asInt32 narrows an int64 sample slice to int32 (lossless when each value fits).
func asInt32(s []int64) []int32 {
	out := make([]int32, len(s))
	for i, v := range s {
		out[i] = int32(v)
	}
	return out
}
