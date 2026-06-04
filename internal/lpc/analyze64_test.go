package lpc

import "testing"

// AnalyzeLPC must accept []int64 and produce a usable plan for wide input, and produce
// identical results for int32 vs int64 of the same values.
func TestAnalyzeLPCGenericParity(t *testing.T) {
	n := 256
	win := make([]float64, n)
	for i := range win {
		win[i] = 1.0 // rectangular window keeps the comparison exact
	}
	s32 := make([]int32, n)
	s64 := make([]int64, n)
	for i := range n {
		// uint32 arithmetic avoids overflowing a 32-bit int (the multiplier exceeds
		// 2^31), so the test compiles and runs on 32-bit targets too.
		v := int32((uint32(i)*2654435761)>>20) & 0x3FFFFF // ~22-bit, fits both
		s32[i] = v
		s64[i] = int64(v)
	}
	sc := NewScratch(n, 8)
	var qb32, qb64 [32]int32
	o32, sh32, qn32, ok32 := AnalyzeLPC(s32, win, 8, 15, 22, sc, qb32[:])
	o64, sh64, qn64, ok64 := AnalyzeLPC(s64, win, 8, 15, 22, sc, qb64[:])
	qc32 := qb32[:qn32]
	qc64 := qb64[:qn64]
	if ok32 != ok64 || o32 != o64 || sh32 != sh64 {
		t.Fatalf("parity: int32 (%v,%d,%d) vs int64 (%v,%d,%d)", ok32, o32, sh32, ok64, o64, sh64)
	}
	if len(qc32) != len(qc64) {
		t.Fatalf("coeff len %d vs %d", len(qc32), len(qc64))
	}
	for i := range qc32 {
		if qc32[i] != qc64[i] {
			t.Fatalf("coeff[%d] %d vs %d", i, qc32[i], qc64[i])
		}
	}
}
