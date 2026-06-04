package frame

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

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
	var ws Workspace
	o, bitsCost := chooseFixedOrder64(&ws, s, paramsLevel(t, 5))
	if o < 0 || o > 4 || bitsCost <= 0 {
		t.Fatalf("chooseFixedOrder64 = (%d,%d)", o, bitsCost)
	}
}

// A wide subframe written by writeSubframe64 must decode back exactly via the existing
// generic decodeSubframe64.
func TestWriteSubframe64RoundTrip(t *testing.T) {
	for _, bps := range []int{25, 28, 32, 33} { // 33 = side-channel width at 32 bps
		s := wideSamples(1024, bps, int64(bps))
		p := paramsLevel(t, 8)
		win := apodizationWindow(p, len(s))
		var ws Workspace
		plan := planSubframe64(&ws, s, bps, p, win)

		bw := bitio.NewWriter()
		bw.Reset()
		writeSubframe64(bw, &ws, s, bps, plan, p)
		bw.AlignByte()

		br := bitio.NewReader(bytesReaderFrame(bw.Bytes()))
		dst := make([]int64, len(s))
		if err := decodeSubframe64(br, dst, bps); err != nil {
			t.Fatalf("bps %d decode: %v", bps, err)
		}
		for i := range s {
			if dst[i] != s[i] {
				t.Fatalf("bps %d sample[%d] = %d, want %d", bps, i, dst[i], s[i])
			}
		}
	}
}

func bytesReaderFrame(b []byte) *bytes.Reader { return bytes.NewReader(b) }
