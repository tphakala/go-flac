package frame

import "testing"

// paramsLevel returns frame.Params for a compression level, mirroring the encoder's
// paramsForLevel table (pcm/encoder.go). Used by the wide-depth subframe/frame tests.
func paramsLevel(t *testing.T, level int) Params {
	t.Helper()
	if level < 0 {
		level = 0
	}
	if level > 8 {
		level = 8
	}
	table := [9]Params{
		0: {Stereo: StereoIndependent, MaxPartitionOrder: 3},
		1: {Stereo: StereoAdaptive, MaxPartitionOrder: 3},
		2: {Stereo: StereoFull, MaxPartitionOrder: 3},
		3: {Stereo: StereoFull, MaxPartitionOrder: 4, MaxLPCOrder: 6, LPCPrecision: 15},
		4: {Stereo: StereoFull, MaxPartitionOrder: 4, ExhaustiveFixed: true, MaxLPCOrder: 8, LPCPrecision: 15},
		5: {Stereo: StereoFull, MaxPartitionOrder: 5, ExhaustiveFixed: true, MaxLPCOrder: 8, LPCPrecision: 15},
		6: {Stereo: StereoFull, MaxPartitionOrder: 6, ExhaustiveFixed: true, MaxLPCOrder: 8, LPCPrecision: 15},
		7: {Stereo: StereoFull, MaxPartitionOrder: 6, ExhaustiveFixed: true, MaxLPCOrder: 12, LPCPrecision: 15},
		8: {Stereo: StereoFull, MaxPartitionOrder: 6, ExhaustiveFixed: true, MaxLPCOrder: 12, LPCPrecision: 15},
	}
	return table[level]
}

func TestWastedBits64(t *testing.T) {
	if g := wastedBits64([]int64{0, 0}); g != 0 {
		t.Fatalf("all-zero wastedBits64 = %d, want 0", g)
	}
	if g := wastedBits64([]int64{8, 16, 24}); g != 3 {
		t.Fatalf("wastedBits64 = %d, want 3", g)
	}
	if g := wastedBits64([]int64{1 << 33}); g != 33 {
		t.Fatalf("wastedBits64(1<<33) = %d, want 33", g)
	}
}

func TestAllEqual64AndShiftRight64(t *testing.T) {
	if !allEqual64([]int64{5, 5, 5}) || allEqual64([]int64{5, 6}) {
		t.Fatal("allEqual64 wrong")
	}
	got := shiftRight64([]int64{8, -8, 1 << 33}, 3)
	want := []int64{1, -1, 1 << 30}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("shiftRight64[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestChooseFixedOrder64Runs(t *testing.T) {
	s := wideSamples(512, 30, 1)
	o, bitsCost := chooseFixedOrder64(s, paramsLevel(t, 5))
	if o < 0 || o > 4 || bitsCost <= 0 {
		t.Fatalf("chooseFixedOrder64 = (%d,%d)", o, bitsCost)
	}
}
