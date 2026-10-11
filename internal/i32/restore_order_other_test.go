//go:build !amd64 && !arm64 && !arm

package i32

import "testing"

// The ceilings below are the unmeasured defaults for builds other than amd64,
// arm64 and arm. They are literals so that a retune has to change the test as
// well, which keeps TestLPCRestoreNarrow_EdgeOfPredicate from comparing the
// constant with itself.
const (
	wantMaxScalarRestoreOrder       = 7
	wantMaxScalarRestoreOrderNarrow = 7
)

func TestMaxScalarRestoreOrder_IsThePinnedCeiling(t *testing.T) {
	if maxScalarRestoreOrder != wantMaxScalarRestoreOrder {
		t.Fatalf("maxScalarRestoreOrder = %d, want %d", maxScalarRestoreOrder, wantMaxScalarRestoreOrder)
	}
}

func TestMaxScalarRestoreOrderNarrow_IsThePinnedCeiling(t *testing.T) {
	if maxScalarRestoreOrderNarrow != wantMaxScalarRestoreOrderNarrow {
		t.Fatalf("maxScalarRestoreOrderNarrow = %d, want %d", maxScalarRestoreOrderNarrow, wantMaxScalarRestoreOrderNarrow)
	}
}
