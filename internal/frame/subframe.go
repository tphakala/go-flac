package frame

import (
	"errors"
	"fmt"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/i32"
	"github.com/tphakala/go-flac/internal/lpc"
	"github.com/tphakala/go-flac/internal/rice"
)

// errSubframeOutOfRange is the deferral signal for a subframe, or a stereo
// channel computed from subframes, that restored to a sample outside its bit
// depth, or whose Rice residual left the signed 32-bit range. The subframe is
// still read to its end, so Decode can finish the frame and report this only once
// the frame CRC-16 has matched (a corrupted frame must keep reporting
// ErrCRCMismatch). It wraps the public sentinel so it classifies correctly if it
// ever escapes. It is a package-level value so deferring it allocates nothing.
var errSubframeOutOfRange = fmt.Errorf("frame: subframe sample outside its bit depth: %w", flac.ErrSampleOutOfRange)

// decodeSubframe decodes one subframe of len(dst) samples at the given effective
// bit depth into dst (int32 path). The int64 path is decodeSubframe64 below.
func decodeSubframe(br *bitio.Reader, dst []int32, bps int) error {
	return decodeSubframeT(br, dst, bps)
}

func decodeSubframe64(br *bitio.Reader, dst []int64, bps int) error {
	return decodeSubframeT(br, dst, bps)
}

// decodeSubframeT decodes one subframe into dst. A FIXED or LPC subframe that
// restores to a sample outside its effective bit depth, or whose Rice residual is
// outside the signed 32-bit range (rice.DecodeResidual), returns
// errSubframeOutOfRange, after the whole subframe has been read.
//
// The sample check runs on the restored samples before the wasted-bits shift, so
// the shift cannot wrap an out-of-range value back into range. The residual bound
// is applied by rice.DecodeResidual before the restore, so a residual too large
// for int32 is never restored into samples that could look in range. The sample
// check covers every FIXED order (order 0 residuals are bounded by the 32-bit
// limit but can still leave a narrower effective width) and LPC; CONSTANT,
// VERBATIM and warm-up samples are read with ReadSigned at the effective width
// and fit by construction. Checking each subframe, not only the final output,
// also makes the verdict independent of the LPC restore route: the
// int32-accumulating and int64 kernels agree up to and including the first sample
// outside the width (the prediction history is in range until then,
// FuzzI32LPCNarrow pins it), and any
// stored sample outside the width is rejected, so the later, route-dependent
// values are never returned. On the int32 path (bps 24 or less) the check sees
// samples as stored: when LPCRestoreNarrowSafe fails, i32.LPCRestore stores
// residual + int32(sum >> shift) (internal/i32/lpc.go:127-128), so a shifted sum
// that leaves int32 wraps and can land back in range and pass. Whether every
// kernel wraps it identically is NOT MEASURED. The 25 to 32 bit path restores
// through lpc.RestoreLPC[int64] (internal/lpc/lpc.go:39), which stores the exact
// int64 value, so the check sees it unwrapped.
//
// This is stricter than libFLAC 1.5.0, which compares only the decoded output of
// each channel with the frame bit depth (stream_decoder.c, FLAC 1.5.0 tag, lines
// 2455 to 2470) and lets a crafted frame whose wasted-bits shift or 32-bit
// arithmetic wraps back into range through. No conforming encoder writes such a
// frame.
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
		if err := decodeResidual(br, dst, order); err != nil {
			return err
		}
		restoreFixed(dst, order)
		if !inRange(dst[order:], effBps) {
			return errSubframeOutOfRange
		}
	case stype >= 32: // LPC, order = stype-31 (1..32)
		order := int(stype - 31)
		if err := decodeLPC(br, dst, order, effBps); err != nil {
			return err
		}
		if !inRange(dst[order:], effBps) {
			return errSubframeOutOfRange
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

// decodeResidual reads the Rice residual of a FIXED or LPC subframe and maps a
// residual outside the signed 32-bit range to errSubframeOutOfRange, so both
// subframe kinds defer the verdict the same way. Any other error is returned
// unchanged.
func decodeResidual[T rice.Sample](br *bitio.Reader, dst []T, order int) error {
	err := rice.DecodeResidual(br, dst, len(dst), order)
	if err != nil && errors.Is(err, rice.ErrResidualRange) {
		return errSubframeOutOfRange
	}
	return err
}

// inRange reports whether every sample of s fits a signed bits-wide value. The
// int32 path scans with the SIMD i32.MinMax reduction; the type assertion never
// escapes, so it does not allocate. The int64 path serves bps >= 25, where a
// narrowing to int32 could hide a violation, so it must see the int64 values.
func inRange[T rice.Sample](s []T, bits int) bool {
	if d, ok := any(s).([]int32); ok {
		if bits >= 32 {
			return true
		}
		lo, hi := i32.MinMax(d)
		lim := int32(1) << uint(bits-1)
		return lo >= -lim && hi < lim
	}
	lim := int64(1) << uint(bits-1)
	for _, v := range s {
		if x := int64(v); x < -lim || x >= lim {
			return false
		}
	}
	return true
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
	if err := decodeResidual(br, dst, order); err != nil {
		return err
	}
	restoreLPC(dst, coeffs, int(shiftSigned), order, bps)
	return nil
}

// restoreFixed reconstructs the subframe in place from its fixed-predictor
// residual. The int32 path (the common 16/24-bps case) dispatches to the SIMD
// i32.Restore kernels via lpc.RestoreFixed32; the int64 wide path (bps >= 25)
// keeps the scalar lpc.RestoreFixed. Order 0 leaves the residual as the samples,
// a no-op on both paths. The type assertion never escapes, so it does not
// allocate.
func restoreFixed[T rice.Sample](dst []T, order int) {
	if order == 0 {
		return
	}
	if d, ok := any(dst).([]int32); ok {
		lpc.RestoreFixed32(d, order)
		return
	}
	lpc.RestoreFixed(dst, order)
}

// restoreLPC reconstructs the subframe in place from its quantized-LPC residual.
// The int32 path dispatches via lpc.RestoreLPC32 to i32.LPCRestoreNarrow
// (int32 accumulation) when the subframe's coefficients and effective sample
// width bps make that exact, and to the int64 i32.LPCRestore otherwise
// (specialized scalar kernels for small orders, the SIMD kernel above a
// per-architecture cutover where one exists); the int64 wide path keeps the
// scalar lpc.RestoreLPC and ignores bps. The type assertion never escapes, so it
// does not allocate.
func restoreLPC[T rice.Sample](dst []T, coeffs []int32, shift, order, bps int) {
	if d, ok := any(dst).([]int32); ok {
		lpc.RestoreLPC32(d, coeffs, shift, bps)
		return
	}
	lpc.RestoreLPC(dst, coeffs, shift, order)
}
