package pcm

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/internal/frame"
)

func TestDeinterleaveInverseOfPack(t *testing.T) {
	for _, bytesPS := range []int{1, 2, 3, 4} {
		const nch, bs = 2, 64
		// Use int64 so 1<<31 does not overflow when bytesPS==4.
		lim := int64(1) << (8*bytesPS - 1)
		ch := make([][]int32, nch)
		for c := range ch {
			ch[c] = make([]int32, bs)
			for i := range ch[c] {
				v := int32(int64(i*37+c*13)%(2*lim) - lim) // span the signed range
				ch[c][i] = v
			}
		}
		fr := &frame.Frame{BlockSize: bs, Channels: ch}
		packed := appendPacked(nil, fr, bytesPS)

		got := make([][]int32, nch)
		for c := range got {
			got[c] = make([]int32, bs)
		}
		deinterleaveSamples(got, packed, bs, nch, bytesPS)
		for c := range ch {
			for i := range ch[c] {
				if got[c][i] != ch[c][i] {
					t.Fatalf("bytesPS=%d ch=%d i=%d: got %d, want %d", bytesPS, c, i, got[c][i], ch[c][i])
				}
			}
		}
	}
}

// TestDeinterleave16MonoAllValues exhaustively checks the 16-bit mono fast path
// against every possible little-endian sample pattern, pinning sign extension
// for all 65536 values including 0x8000 (min), 0x7FFF (max), and 0xFFFF (-1).
func TestDeinterleave16MonoAllValues(t *testing.T) {
	const n = 1 << 16
	src := make([]byte, 2*n)
	for v := range n {
		src[2*v] = byte(v)
		src[2*v+1] = byte(v >> 8)
	}
	got := make([]int32, n)
	deinterleave16Mono(got, src, n)
	for v := range n {
		want := int32(int16(uint16(v))) //nolint:gosec // v < 1<<16 by loop bound
		if got[v] != want {
			t.Fatalf("v=%#04x: got %d, want %d", v, got[v], want)
		}
	}
}

// TestDeinterleave16MonoTails exercises the scalar tail of the unrolled mono
// loop across block sizes that are not multiples of the unroll factor.
func TestDeinterleave16MonoTails(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 63, 64, 65, 255, 256, 4095, 4096, 4097} {
		src := make([]byte, 2*n)
		for i := range src {
			src[i] = byte(i*131 + 7) // deterministic, spans negative samples
		}
		want := make([]int32, n)
		for i := range n {
			want[i] = int32(int16(uint16(src[2*i]) | uint16(src[2*i+1])<<8))
		}
		got := make([]int32, n)
		deinterleave16Mono(got, src, n)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("n=%d i=%d: got %d, want %d", n, i, got[i], want[i])
			}
		}
	}
}

// TestDeinterleave16StereoAllValues exhaustively checks the 16-bit stereo fast
// path. Each sample index carries a distinct left value and a scrambled right
// value so a channel swap or shared-load bug cannot pass.
func TestDeinterleave16StereoAllValues(t *testing.T) {
	const n = 1 << 16
	src := make([]byte, 4*n)
	scramble := func(i int) int { return (i * 0x9E37) & 0xFFFF }
	for i := range n {
		l, r := i, scramble(i)
		src[4*i] = byte(l)
		src[4*i+1] = byte(l >> 8)
		src[4*i+2] = byte(r)
		src[4*i+3] = byte(r >> 8)
	}
	left := make([]int32, n)
	right := make([]int32, n)
	deinterleave16Stereo(left, right, src, n)
	for i := range n {
		wl := int32(int16(uint16(i)))           //nolint:gosec // i < 1<<16
		wr := int32(int16(uint16(scramble(i)))) //nolint:gosec // masked to 16 bits
		if left[i] != wl || right[i] != wr {
			t.Fatalf("i=%#04x: got L=%d R=%d, want L=%d R=%d", i, left[i], right[i], wl, wr)
		}
	}
}

// TestDeinterleave16StereoTails exercises the scalar tail of the unrolled stereo
// loop across block sizes that are not multiples of the unroll factor.
func TestDeinterleave16StereoTails(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 63, 64, 65, 255, 256, 4095, 4096, 4097} {
		src := make([]byte, 4*n)
		for i := range src {
			src[i] = byte(i*197 + 11) // deterministic, spans negative samples
		}
		wantL := make([]int32, n)
		wantR := make([]int32, n)
		for i := range n {
			wantL[i] = int32(int16(uint16(src[4*i]) | uint16(src[4*i+1])<<8))
			wantR[i] = int32(int16(uint16(src[4*i+2]) | uint16(src[4*i+3])<<8))
		}
		gotL := make([]int32, n)
		gotR := make([]int32, n)
		deinterleave16Stereo(gotL, gotR, src, n)
		for i := range n {
			if gotL[i] != wantL[i] || gotR[i] != wantR[i] {
				t.Fatalf("n=%d i=%d: got L=%d R=%d, want L=%d R=%d", n, i, gotL[i], gotR[i], wantL[i], wantR[i])
			}
		}
	}
}

// scalarPack16 is the byte-exact reference the widened 16-bit stores must match:
// each int32 sample is truncated to its low 16 bits and emitted little-endian, the
// same bytes appendPacked's generic scalar store produces. Interleaves nch channels
// per inter-channel sample.
func scalarPack16(ch [][]int32, n int) []byte {
	nch := len(ch)
	out := make([]byte, 2*n*nch)
	idx := 0
	for i := range n {
		for c := range nch {
			v := uint16(ch[c][i])
			out[idx] = byte(v)
			out[idx+1] = byte(v >> 8)
			idx += 2
		}
	}
	return out
}

// TestPackLE16MonoMatchesScalar pins the widened mono store byte-for-byte against
// the scalar reference across tail-exercising block sizes, with samples that span
// the signed range. Deleting the widened 64-bit store body (leaving only the
// scalar tail) keeps this green; deleting the uint16() truncation cast reddens it
// on the negative-sample cases below.
func TestPackLE16MonoMatchesScalar(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 63, 64, 65, 255, 256, 4095, 4096, 4097} {
		src := make([]int32, n)
		for i := range n {
			src[i] = int32(int16(uint16(i*131 + 7))) // spans negatives via int16 wrap
		}
		want := scalarPack16([][]int32{src}, n)
		got := make([]byte, 2*n)
		packLE16Mono(got, src, n)
		if !bytes.Equal(got, want) {
			t.Fatalf("n=%d: widened mono store bytes differ from scalar reference", n)
		}
	}
}

// TestPackLE16StereoMatchesScalar pins the widened stereo store against the scalar
// reference. Distinct scrambled left/right values per index catch a channel swap
// or a shared-lane bug; the sizes exercise the 32-bit odd-frame tail.
func TestPackLE16StereoMatchesScalar(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 63, 64, 65, 255, 256, 4095, 4096, 4097} {
		left := make([]int32, n)
		right := make([]int32, n)
		for i := range n {
			left[i] = int32(int16(uint16(i*29 + 3)))
			right[i] = int32(int16(uint16(i*0x9E37 + 101))) // scrambled, distinct from left
		}
		want := scalarPack16([][]int32{left, right}, n)
		got := make([]byte, 4*n)
		packLE16Stereo(got, left, right, n)
		if !bytes.Equal(got, want) {
			t.Fatalf("n=%d: widened stereo store bytes differ from scalar reference", n)
		}
	}
}

// TestPackLE16NegativeTruncation is the explicit sign-bleed guard: negative int32
// samples must truncate to their low 16 bits, not sign-extend across neighbors.
// Removing the uint16() cast in packLE16Mono/packLE16Stereo turns this red because
// the sign extension of one lane bleeds into the widened word's other lanes.
func TestPackLE16NegativeTruncation(t *testing.T) {
	mono := []int32{-1, -2, -3, -4, -5}
	wantMono := scalarPack16([][]int32{mono}, len(mono))
	gotMono := make([]byte, 2*len(mono))
	packLE16Mono(gotMono, mono, len(mono))
	if !bytes.Equal(gotMono, wantMono) {
		t.Fatalf("mono negatives: got % x, want % x", gotMono, wantMono)
	}

	left := []int32{-1, -100, -32768, 32767, -3}
	right := []int32{-2, 12345, -1, -32768, 4}
	wantStereo := scalarPack16([][]int32{left, right}, len(left))
	gotStereo := make([]byte, 4*len(left))
	packLE16Stereo(gotStereo, left, right, len(left))
	if !bytes.Equal(gotStereo, wantStereo) {
		t.Fatalf("stereo negatives: got % x, want % x", gotStereo, wantStereo)
	}
}
