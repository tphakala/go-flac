package frame

import (
	"bytes"
	"fmt"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/i32"
)

const (
	sideNarrowN     = 16
	sideNarrowOrder = 3
)

// sideNarrowRow is one frame bit depth of the side-channel predicate test.
type sideNarrowRow struct {
	bps   int
	left  int32
	right int32
	mid   int32
	coeff int32 // all sideNarrowOrder coefficients
	shift int
	side  int64 // constant side signal, left - right
}

// coeffs returns the row's coefficients.
func (r sideNarrowRow) coeffs() []int32 {
	c := make([]int32, sideNarrowOrder)
	for i := range c {
		c[i] = r.coeff
	}
	return c
}

// residual computes the LPC residual of the constant side signal with a plain
// int64 recurrence, so the fixture does not depend on the code under test.
func (r sideNarrowRow) residual() []int64 {
	var acc int64
	for _, c := range r.coeffs() {
		acc += int64(c) * r.side
	}
	res := make([]int64, sideNarrowN)
	for i := sideNarrowOrder; i < sideNarrowN; i++ {
		res[i] = r.side - acc>>uint(r.shift)
	}
	return res
}

// writeSide writes the side LPC subframe at the side channel's width, with the
// residuals in the Rice escape (raw 31-bit) form.
func (r sideNarrowRow) writeSide(bw *bitio.Writer) {
	sideBps := uint(r.bps + 1)
	bw.WriteBits(0, 1)                    // padding bit
	bw.WriteBits(32+sideNarrowOrder-1, 6) // LPC
	bw.WriteBits(0, 1)                    // no wasted bits
	for range sideNarrowOrder {
		bw.WriteSignedBits(r.side, sideBps)
	}
	bw.WriteBits(14, 4) // precision 15
	bw.WriteSignedBits(int64(r.shift), 5)
	for _, c := range r.coeffs() {
		bw.WriteSignedBits(int64(c), 15)
	}
	bw.WriteBits(0, 2)  // Rice, 4-bit parameters
	bw.WriteBits(0, 4)  // one partition
	bw.WriteBits(15, 4) // escape: raw residuals
	bw.WriteBits(31, 5) // raw width
	res := r.residual()
	for i := sideNarrowOrder; i < sideNarrowN; i++ {
		bw.WriteSignedBits(res[i], 31)
	}
}

// checkPreconditions asserts the row sits in the gap between the two predicates
// and that an int32 restore of the side subframe really wraps.
func (r sideNarrowRow) checkPreconditions(t *testing.T) {
	t.Helper()
	coeffs, shift := r.coeffs(), uint(r.shift)
	if !i32.LPCRestoreNarrowSafe(coeffs, uint(r.bps), shift) || i32.LPCRestoreNarrowSafe(coeffs, uint(r.bps+1), shift) {
		t.Fatalf("fixture must pass the narrow predicate at bps %d and fail it at bps+1", r.bps)
	}
	res := r.residual()
	got := make([]int32, sideNarrowN)
	for i := range got {
		got[i] = int32(r.side)
		if i >= sideNarrowOrder {
			got[i] = int32(res[i])
		}
	}
	i32.LPCRestoreNarrow(got, got, coeffs, shift)
	if int64(got[sideNarrowOrder]) == r.side {
		t.Fatalf("int32 restore did not wrap, fixture is outside the overflow regime")
	}
	sw := bitio.NewWriter()
	r.writeSide(sw)
	sw.AlignByte()
	for i, v := range decodeOneSubframe64(t, sw.Bytes(), sideNarrowN, r.bps+1) {
		if v != r.side {
			t.Fatalf("int64 reference sample %d = %d, want %d", i, v, r.side)
		}
	}
}

// buildFrame assembles a stereo frame for channel assignment 8, 9 or 10.
func (r sideNarrowRow) buildFrame(assignment, chCode int) []byte {
	bw := bitio.NewWriter()
	writeFrameHeader(bw, sideNarrowN, chCode, 44100, r.bps, 0)
	switch assignment {
	case 8:
		writeConstant(bw, r.left, 0, r.bps)
		r.writeSide(bw)
	case 9:
		r.writeSide(bw)
		writeConstant(bw, r.right, 0, r.bps)
	case 10:
		writeConstant(bw, r.mid, 0, r.bps)
		r.writeSide(bw)
	}
	finishFrame(bw)
	return bw.Bytes()
}

// TestDecodeStereo_SideLPCPredicateUsesSideWidth pins the extra bit the side
// channel carries on its way to the narrow-restore predicate. Each frame is
// left/side, right/side or mid/side, with a side LPC subframe whose coefficient
// sum passes i32.LPCRestoreNarrowSafe at the frame bit depth but fails it at
// bps+1. The side samples sit one above the minimum of the (bps+1)-bit range, so an
// int32 accumulator would wrap; only a predicate that sees bps+1 routes the
// subframe to the int64 restore and recovers the known left and right samples.
// Valid corpus data never wraps, so only crafted data like this separates the
// two routes.
func TestDecodeStereo_SideLPCPredicateUsesSideWidth(t *testing.T) {
	rows := []sideNarrowRow{
		{bps: 16, left: -32768, right: 32767, mid: -1, coeff: -14000, shift: 14, side: -65535},
		{bps: 24, left: -8388608, right: 8388607, mid: -1, coeff: -67, shift: 8, side: -16777215},
	}
	assignments := []struct {
		name   string
		code   int
		chCode int
	}{
		{"left/side", 8, chLeftSide},
		{"right/side", 9, chRightSide},
		{"mid/side", 10, chMidSide},
	}
	for _, as := range assignments {
		for _, row := range rows {
			t.Run(fmt.Sprintf("%s/%d-bit", as.name, row.bps), func(t *testing.T) {
				row.checkPreconditions(t)
				var fr Frame
				si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: row.bps}
				raw := row.buildFrame(as.code, as.chCode)
				if err := Decode(bitio.NewReader(bytes.NewReader(raw)), si, &fr); err != nil {
					t.Fatalf("Decode: %v", err)
				}
				for i := range sideNarrowN {
					if fr.Channels[0][i] != row.left || fr.Channels[1][i] != row.right {
						t.Fatalf("sample %d = (%d, %d), want (%d, %d)",
							i, fr.Channels[0][i], fr.Channels[1][i], row.left, row.right)
					}
				}
			})
		}
	}
}
