package pcm

import (
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
