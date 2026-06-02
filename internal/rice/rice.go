package rice

import (
	"fmt"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// Sample is the integer width used for decoded residuals/samples.
type Sample interface{ ~int32 | ~int64 }

const (
	method4bit = 0
	method5bit = 1
	escape4    = 0x0F
	escape5    = 0x1F
)

// DecodeResidual decodes the partitioned Rice residual that follows the warmup
// samples of a subframe, filling dst[predOrder:blockSize]. dst[:predOrder] holds
// warmup samples already written by the caller.
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
			u := (q << k) | r
			// zigzag decode
			dst[idx] = T(int64(u>>1) ^ -int64(u&1))
			idx++
		}
	}
	return nil
}
