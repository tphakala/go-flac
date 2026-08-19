package i32

import "testing"

// The decode path reconstructs samples in place: the subframe buffer holds the
// residual on input and the restored samples on output, so the wired kernels are
// called with dst and src being the same slice. These tests pin that exact
// aliasing (dst == src, identical slice) produces the same result as the
// two-buffer form for LPCRestore and Restore1..4. Partial overlap stays
// unsupported and never occurs at the call sites.

func TestLPCRestoreInPlace(t *testing.T) {
	for _, coeffs := range lpcCoeffSets() {
		for _, n := range lpcSizes {
			for _, shift := range lpcShifts {
				res := make([]int32, n)
				fillLPCSamples(res)

				want := make([]int32, n)
				LPCRestore(want, res, coeffs, shift)

				got := make([]int32, n)
				copy(got, res)
				LPCRestore(got, got, coeffs, shift) // dst aliases residual exactly

				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("order=%d n=%d shift=%d: in-place[%d]=%d, want %d",
							len(coeffs), n, shift, i, got[i], want[i])
					}
				}
			}
		}
	}
}

func TestRestoreInPlace(t *testing.T) {
	funcs := []struct {
		name string
		f    func(dst, src []int32)
	}{
		{"Restore1", Restore1},
		{"Restore2", Restore2},
		{"Restore3", Restore3},
		{"Restore4", Restore4},
	}
	for _, fn := range funcs {
		for _, n := range restoreSizes {
			src := make([]int32, n)
			fillRestoreSrc(src)

			want := make([]int32, n)
			fn.f(want, src)

			got := make([]int32, n)
			copy(got, src)
			fn.f(got, got) // dst aliases src exactly

			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%s n=%d: in-place[%d]=%d, want %d", fn.name, n, i, got[i], want[i])
				}
			}
		}
	}
}
