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
