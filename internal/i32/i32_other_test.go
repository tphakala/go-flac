//go:build !amd64 && !arm64

package i32

import "testing"

// TestFirstSIMDRestoreOrder_NoneWithoutSIMD pins that builds without a SIMD
// restore kernel report no SIMD order, so reach guards skip instead of
// expecting a kernel that never runs.
func TestFirstSIMDRestoreOrder_NoneWithoutSIMD(t *testing.T) {
	if got := FirstSIMDRestoreOrder(); got != 0 {
		t.Fatalf("FirstSIMDRestoreOrder() = %d, want 0 on a build without a SIMD restore kernel", got)
	}
}

// wantMaxScalarRestoreOrderNarrow is the narrow scalar ceiling these builds use.
// It is not measured here (see maxScalarRestoreOrderNarrow). It is a literal so
// that a retune has to change the test as well, which keeps
// TestLPCRestoreNarrow_EdgeOfPredicate from comparing the constant with itself.
const wantMaxScalarRestoreOrderNarrow = 7

func TestMaxScalarRestoreOrderNarrow_IsThePinnedCeiling(t *testing.T) {
	if maxScalarRestoreOrderNarrow != wantMaxScalarRestoreOrderNarrow {
		t.Fatalf("maxScalarRestoreOrderNarrow = %d, want %d", maxScalarRestoreOrderNarrow, wantMaxScalarRestoreOrderNarrow)
	}
}
