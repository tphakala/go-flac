//go:build arm

package i32

import "testing"

// The ceilings below are the measured literals for 32-bit ARM (see the tables at
// the constants). They are literals so that a retune has to change the test as
// well, which keeps TestLPCRestoreNarrow_EdgeOfPredicate from comparing the
// constant with itself.
const (
	wantMaxScalarRestoreOrder       = 2
	wantMaxScalarRestoreOrderNarrow = 32
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
