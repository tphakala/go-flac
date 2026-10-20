package rice

import (
	"fmt"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// Sample is the integer width used for decoded residuals/samples.
type Sample interface{ ~int32 | ~int64 }

// ErrResidualRange is returned by DecodeResidual when a Rice-coded residual
// leaves the signed 32-bit range. It is returned only after the whole residual has
// been read, so the caller can finish its frame and report the violation once the
// frame CRC-16 has matched. It wraps flac.ErrSampleOutOfRange so it classifies
// correctly if it ever escapes. It is a package-level value so returning it
// allocates nothing.
var ErrResidualRange = fmt.Errorf("rice: residual outside the signed 32-bit range: %w", flac.ErrSampleOutOfRange)

const (
	method4bit = 0
	method5bit = 1
	escape4    = 0x0F
	escape5    = 0x1F
)

// highBits is nonzero exactly when the folded value (q<<k)|r is 2^32 or more, for
// any r below 2^k. It tests the quotient before the shift, so a q large enough to
// wrap q<<k in uint64 is still seen. k must be at most 30 (the largest 5-bit
// parameter below the escape code), so the shift count is at least 2.
func highBits(q uint64, k uint) uint64 { return q >> (32 - k) }

// DecodeResidual decodes the partitioned Rice residual that follows the warmup
// samples of a subframe, filling dst[predOrder:blockSize]. dst[:predOrder] holds
// warmup samples already written by the caller.
//
// A Rice-coded symbol whose zigzag-folded value is 2^32 or more decodes to a
// residual outside the signed 32-bit range; the whole residual is still read and
// ErrResidualRange is returned at the end. The bound is one value looser than
// RFC 9639 section 9.2.7.3, which also excludes -2^31: it accepts the folded value
// 2^32-1 (residual -2^31), the limit libFLAC 1.5.0 applies in its signed Rice
// block reader (src/libFLAC/deduplication/bitreader_read_rice_signed_block.c at
// tag 1.5.0, line 17 and lines 69 to 70). Past it the residual does not fit
// int32, and narrowing it on the int32 path would wrap without notice. That
// libFLAC function does not check the Rice parameter 0 path (lines 27 to 28) or its
// tail path from line 96; this decoder checks every Rice symbol. Escape
// partitions carry at most 31 raw bits and are always in range.
func DecodeResidual[T Sample](br *bitio.Reader, dst []T, blockSize, predOrder int) error {
	method, err := br.ReadBits(2)
	if err != nil {
		return err
	}
	var paramBits uint
	var escape uint64
	switch method {
	case method4bit:
		paramBits, escape = 4, escape4
	case method5bit:
		paramBits, escape = 5, escape5
	default:
		return fmt.Errorf("rice: reserved coding method %d: %w", method, flac.ErrUnsupported)
	}

	po, err := br.ReadBits(4)
	if err != nil {
		return err
	}
	partitions := 1 << po
	if blockSize%partitions != 0 {
		return fmt.Errorf("rice: blocksize %d not divisible by %d partitions: %w", blockSize, partitions, flac.ErrUnsupported)
	}
	partLen := blockSize / partitions
	idx := predOrder
	var over uint64
	for p := range partitions {
		n := partLen
		if p == 0 {
			n -= predOrder
		}
		if n < 0 {
			return fmt.Errorf("rice: partition %d has negative length: %w", p, flac.ErrUnsupported)
		}
		param, err := br.ReadBits(paramBits)
		if err != nil {
			return err
		}
		if param == escape {
			raw, err := br.ReadBits(5)
			if err != nil {
				return err
			}
			for range n {
				v, err := br.ReadSigned(uint(raw))
				if err != nil {
					return err
				}
				dst[idx] = T(v)
				idx++
			}
			continue
		}
		k := uint(param)
		for range n {
			q, err := br.ReadUnary()
			if err != nil {
				return err
			}
			r, err := br.ReadBits(k)
			if err != nil {
				return err
			}
			over |= highBits(q, k)
			u := (q << k) | r
			// zigzag decode
			dst[idx] = T(int64(u>>1) ^ -int64(u&1))
			idx++
		}
	}
	if over != 0 {
		return ErrResidualRange
	}
	return nil
}
