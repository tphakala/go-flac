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
