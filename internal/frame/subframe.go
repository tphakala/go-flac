package frame

import (
	"fmt"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/lpc"
	"github.com/tphakala/go-flac/internal/rice"
)

// decodeSubframe decodes one subframe of len(dst) samples at the given effective
// bit depth into dst (int32 path). The int64 path is decodeSubframe64 below.
func decodeSubframe(br *bitio.Reader, dst []int32, bps int) error {
	return decodeSubframeT(br, dst, bps)
}

func decodeSubframe64(br *bitio.Reader, dst []int64, bps int) error {
	return decodeSubframeT(br, dst, bps)
}

func decodeSubframeT[T rice.Sample](br *bitio.Reader, dst []T, bps int) error {
	if pad, err := br.ReadBits(1); err != nil {
		return err
	} else if pad != 0 {
		return fmt.Errorf("subframe: nonzero padding bit: %w", flac.ErrUnsupported)
	}
	stype, err := br.ReadBits(6)
	if err != nil {
		return err
	}
	wasted, err := readWastedBits(br)
	if err != nil {
		return err
	}
	effBps := bps - wasted
	if effBps <= 0 {
		return fmt.Errorf("subframe: wasted bits %d leave no sample bits (bps %d): %w", wasted, bps, flac.ErrUnsupported)
	}

	switch {
	case stype == 0: // CONSTANT
		v, err := br.ReadSigned(uint(effBps))
		if err != nil {
			return err
		}
		for i := range dst {
			dst[i] = T(v)
		}
	case stype == 1: // VERBATIM
		for i := range dst {
			v, err := br.ReadSigned(uint(effBps))
			if err != nil {
				return err
			}
			dst[i] = T(v)
		}
	case stype >= 8 && stype <= 12: // FIXED, order = stype-8 (0..4)
		order := int(stype - 8)
		if err := decodeWarmup(br, dst, order, effBps); err != nil {
			return err
		}
		if err := rice.DecodeResidual(br, dst, len(dst), order); err != nil {
			return err
		}
		lpc.RestoreFixed(dst, order)
	case stype >= 32: // LPC, order = stype-31 (1..32)
		order := int(stype - 31)
		if err := decodeLPC(br, dst, order, effBps); err != nil {
			return err
		}
	default:
		return fmt.Errorf("subframe: reserved type %d: %w", stype, flac.ErrUnsupported)
	}

	if wasted > 0 {
		for i := range dst {
			dst[i] <<= uint(wasted)
		}
	}
	return nil
}

func readWastedBits(br *bitio.Reader) (int, error) {
	flag, err := br.ReadBits(1)
	if err != nil {
		return 0, err
	}
	if flag == 0 {
		return 0, nil
	}
	q, err := br.ReadUnary() // k-1 zeros then a 1
	if err != nil {
		return 0, err
	}
	// Wasted bits must be fewer than the sample bit depth (at most 32). A unary
	// run this long is malformed; reject it before int(q) can overflow or drive
	// a huge ReadSigned loop on a negative effective bit depth.
	if q >= 32 {
		return 0, fmt.Errorf("subframe: wasted bit count %d too large: %w", q+1, flac.ErrUnsupported)
	}
	return int(q) + 1, nil
}

func decodeWarmup[T rice.Sample](br *bitio.Reader, dst []T, order, bps int) error {
	if order > len(dst) {
		return fmt.Errorf("subframe: predictor order %d exceeds block size %d: %w", order, len(dst), flac.ErrUnsupported)
	}
	for i := range order {
		v, err := br.ReadSigned(uint(bps))
		if err != nil {
			return err
		}
		dst[i] = T(v)
	}
	return nil
}

func decodeLPC[T rice.Sample](br *bitio.Reader, dst []T, order, bps int) error {
	if err := decodeWarmup(br, dst, order, bps); err != nil {
		return err
	}
	precCode, err := br.ReadBits(4)
	if err != nil {
		return err
	}
	if precCode == 0x0F {
		return fmt.Errorf("subframe: invalid qlp precision 15: %w", flac.ErrUnsupported)
	}
	precision := int(precCode) + 1
	shiftSigned, err := br.ReadSigned(5)
	if err != nil {
		return err
	}
	if shiftSigned < 0 {
		return fmt.Errorf("subframe: negative qlp shift %d: %w", shiftSigned, flac.ErrUnsupported)
	}
	// LPC order is at most 32, so a fixed stack array avoids a per-subframe heap
	// allocation; RestoreLPC only reads coeffs, so the slice does not escape.
	var coeffsBuf [32]int32
	coeffs := coeffsBuf[:order]
	for i := range coeffs {
		c, err := br.ReadSigned(uint(precision))
		if err != nil {
			return err
		}
		coeffs[i] = int32(c)
	}
	if err := rice.DecodeResidual(br, dst, len(dst), order); err != nil {
		return err
	}
	lpc.RestoreLPC(dst, coeffs, int(shiftSigned), order)
	return nil
}
