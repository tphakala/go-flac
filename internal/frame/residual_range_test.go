package frame

import (
	"bytes"
	"errors"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// writeRice30 writes a residual section with 5-bit Rice parameters and one
// partition at parameter 30, holding the given (quotient, remainder) symbols
// followed by enough zero symbols to fill n residual samples. Unlike the escape
// form of writeEscapeResidual it reaches folded values past 2^31.
func writeRice30(bw *bitio.Writer, n int, first [2]uint64) {
	bw.WriteBits(1, 2)  // Rice, 5-bit parameters
	bw.WriteBits(0, 4)  // one partition
	bw.WriteBits(30, 5) // parameter 30
	for i := range n {
		var q, r uint64
		if i == 0 {
			q, r = first[0], first[1]
		}
		bw.WriteUnary(q)
		bw.WriteBits(r, 30)
	}
}

// residualRangeCases returns CRC-valid frames whose first Rice residual folds to
// 2^32 or more but whose decoded samples are all in range once the residual is
// truncated to 32 bits or cancelled by the prediction. Without a bound on the
// residual itself, both decode to in-range samples and are accepted.
func residualRangeCases() []rangeCase {
	return []rangeCase{
		{
			// u = 8<<30 | 4 = 2^33+4: truncated to int32 it reads as 4, residual 2.
			name: "16-bit fixed residual past 32 bits truncates back in range", bits: 16, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				writeSubframeHeader(bw, 8, 0) // FIXED order 0
				writeRice30(bw, rangeBlock, [2]uint64{8, 4})
			},
		},
		{
			// u = 4<<30 = 2^32, residual +2^31, added to the prediction -2^31
			// (coefficient 16 times warm-up -2^27, shift 0): the sum is 0.
			name: "28-bit lpc residual 2^31 cancelled by its prediction", bits: 28, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				writeSubframeHeader(bw, 32, 0) // LPC order 1
				bw.WriteSignedBits(-(1 << 27), 28)
				bw.WriteBits(14, 4) // precision 15
				bw.WriteSignedBits(0, 5)
				bw.WriteSignedBits(16, 15)
				writeRice30(bw, rangeBlock-1, [2]uint64{4, 0})
			},
		},
	}
}

func TestDecodeRejectsOversizedRiceResidual(t *testing.T) {
	for _, c := range residualRangeCases() {
		t.Run(c.name, func(t *testing.T) {
			var fr Frame
			err := Decode(bitio.NewReader(bytes.NewReader(c.bytes())), c.streamInfo(), &fr)
			if !errors.Is(err, flac.ErrSampleOutOfRange) || errors.Is(err, flac.ErrCRCMismatch) {
				t.Errorf("Decode error = %v, want flac.ErrSampleOutOfRange", err)
			}
			// The same frame with a damaged CRC-16 must report the CRC, not the
			// range: the verdict is only given once the CRC-16 matches, which also
			// needs the residual read to its end.
			raw := c.bytes()
			raw[len(raw)-1] ^= 0xff
			err = Decode(bitio.NewReader(bytes.NewReader(raw)), c.streamInfo(), &fr)
			if !errors.Is(err, flac.ErrCRCMismatch) || errors.Is(err, flac.ErrSampleOutOfRange) {
				t.Errorf("damaged-CRC Decode error = %v, want only flac.ErrCRCMismatch", err)
			}
		})
	}
}
