package frame

import (
	"math/bits"

	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/lpc"
	"github.com/tphakala/go-flac/internal/rice"
)

// wastedBits returns the number of low-order zero bits common to every sample.
// Returns 0 when all samples are zero (that case is encoded as a constant subframe).
func wastedBits(s []int32) int {
	var orAll int32
	for _, v := range s {
		orAll |= v
	}
	if orAll == 0 {
		return 0
	}
	return bits.TrailingZeros32(uint32(orAll))
}

// writeSubframeHeader writes the 1-bit zero pad, 6-bit type code, and the
// wasted-bits field. When wasted == 0 the flag bit is 0. When wasted > 0 the
// flag bit is 1 followed by a unary value of (wasted-1) zeros then a 1.
func writeSubframeHeader(bw *bitio.Writer, typeCode, wasted int) {
	bw.WriteBits(0, 1)
	bw.WriteBits(uint64(typeCode), 6)
	if wasted == 0 {
		bw.WriteBits(0, 1)
	} else {
		bw.WriteBits(1, 1)
		bw.WriteUnary(uint64(wasted - 1))
	}
}

// writeConstant writes a FLAC constant subframe. The decoder fills every sample
// slot with the single stored value then shifts left by wasted bits, so the
// encoder stores value>>wasted at bps-wasted bits.
func writeConstant(bw *bitio.Writer, value int32, wasted, bps int) {
	writeSubframeHeader(bw, 0, wasted)
	bw.WriteSignedBits(int64(value>>uint(wasted)), uint(bps-wasted))
}

// writeVerbatim writes a FLAC verbatim subframe. Each sample is stored
// right-shifted by wasted bits at bps-wasted bits. The decoder reads each
// stored sample and then shifts left by wasted to restore the original value.
func writeVerbatim(bw *bitio.Writer, s []int32, wasted, bps int) {
	writeSubframeHeader(bw, 1, wasted)
	eff := uint(bps - wasted)
	for _, v := range s {
		bw.WriteSignedBits(int64(v>>uint(wasted)), eff)
	}
}

// subframePlan records the chosen encoding of one subframe signal.
type subframePlan struct {
	kind   int // 0 constant, 1 verbatim, 2 fixed, 3 LPC
	order  int // fixed predictor order (kind 2) or LPC order (kind 3)
	wasted int
	bits   int     // estimated total subframe bits, for decorrelation selection
	qcoeff []int32 // LPC quantized coefficients (kind 3)
	shift  int     // LPC quantization shift (kind 3)
}

// planSubframe chooses the cheapest subframe encoding for s at the given bps.
//
//nolint:dupl // intentional: typed parallel of planSubframe64
func planSubframe(s []int32, bps int, p Params, window []float64) subframePlan {
	if allEqual(s) {
		return subframePlan{kind: 0, bits: 1 + 6 + 1 + bps}
	}
	wasted := wastedBits(s)
	if wasted >= bps {
		// Defend against out-of-range input (samples wider than bps, possible only
		// when the caller supplies PCM that does not fit the declared bit depth):
		// keep at least one value bit so bps-wasted stays positive and the shift
		// widths below cannot underflow. In-range input always has wasted < bps.
		wasted = bps - 1
	}
	eff := bps - wasted
	hdrBits := 1 + 6 + 1 // pad + type + wasted flag
	if wasted > 0 {
		hdrBits += wasted // unary (wasted-1 zeros + 1)
	}
	shifted := shiftRight(s, wasted)

	fOrder, fResBits := chooseFixedOrder(shifted, p)
	fixedBits := hdrBits + fOrder*eff + fResBits
	verbatimBits := hdrBits + len(s)*eff

	// Start from verbatim; fixed and LPC must strictly beat the running best so
	// that verbatim wins ties against fixed (preserving the prior behavior).
	best := subframePlan{kind: 1, wasted: wasted, bits: verbatimBits}
	if fixedBits < best.bits {
		best = subframePlan{kind: 2, order: fOrder, wasted: wasted, bits: fixedBits}
	}

	if p.MaxLPCOrder > 0 && window != nil {
		if lOrder, qc, shift, lResBits, ok := chooseLPCPlan(shifted, eff, p, window); ok {
			lpcBits := hdrBits + lOrder*eff + 4 + 5 + lOrder*p.LPCPrecision + lResBits
			if lpcBits < best.bits {
				best = subframePlan{kind: 3, order: lOrder, wasted: wasted, bits: lpcBits, qcoeff: qc, shift: shift}
			}
		}
	}
	return best
}

// writeSubframe writes s according to plan.
func writeSubframe(bw *bitio.Writer, s []int32, bps int, plan subframePlan, p Params) {
	switch plan.kind {
	case 0:
		writeConstant(bw, s[0], 0, bps)
	case 1:
		writeVerbatim(bw, s, plan.wasted, bps)
	case 3:
		writeLPC(bw, s, plan.order, plan.qcoeff, plan.shift, plan.wasted, bps, p.LPCPrecision, p.MaxPartitionOrder)
	default:
		writeFixed(bw, s, plan.order, plan.wasted, bps, p.MaxPartitionOrder)
	}
}

// writeLPC writes a SUBFRAME_LPC: header (type code 31+order), warmup samples,
// 4-bit precision-1, 5-bit shift, the quantized coefficients, then the Rice
// residual. precision is the coefficient bit width (p.LPCPrecision). It mirrors
// writeFixed and recomputes residuals with ComputeLPCResiduals (the integer
// inverse of RestoreLPC), so the decoder reconstructs the samples exactly.
func writeLPC(bw *bitio.Writer, s []int32, order int, qcoeff []int32, shift, wasted, bps, precision, maxPartOrder int) {
	writeSubframeHeader(bw, 31+order, wasted)
	eff := uint(bps - wasted)
	shifted := shiftRight(s, wasted)
	for i := range order {
		bw.WriteSignedBits(int64(shifted[i]), eff)
	}
	bw.WriteBits(uint64(precision-1), 4)
	bw.WriteSignedBits(int64(shift), 5)
	for i := range order {
		bw.WriteSignedBits(int64(qcoeff[i]), uint(precision))
	}
	res := make([]int32, len(shifted)-order)
	lpc.ComputeLPCResiduals(res, shifted, qcoeff, shift, order)
	rice.EncodeResidual(bw, res, len(shifted), order, maxPartOrder)
}

// writeFixed writes a fixed-predictor subframe of the given order.
func writeFixed(bw *bitio.Writer, s []int32, order, wasted, bps, maxPartOrder int) {
	writeSubframeHeader(bw, 8+order, wasted)
	eff := uint(bps - wasted)
	shifted := shiftRight(s, wasted)
	for i := range order {
		bw.WriteSignedBits(int64(shifted[i]), eff)
	}
	res := make([]int32, len(shifted)-order)
	lpc.ComputeFixedResiduals(res, shifted, order)
	rice.EncodeResidual(bw, res, len(shifted), order, maxPartOrder)
}

// chooseFixedOrder returns the fixed predictor order to use for shifted together
// with the Rice cost (in bits) of that order's residuals, so the caller does not
// have to recompute the residuals to learn their cost.
func chooseFixedOrder(shifted []int32, p Params) (order, resBits int) {
	if p.ExhaustiveFixed {
		bestOrder, bestBits := 0, int(^uint(0)>>1)
		maxOrder := min(4, len(shifted)-1)
		res := make([]int32, len(shifted)) // reused across orders
		for o := 0; o <= maxOrder; o++ {
			r := res[:len(shifted)-o]
			lpc.ComputeFixedResiduals(r, shifted, o)
			b := rice.CostResidual(r, len(shifted), o, p.MaxPartitionOrder)
			if b < bestBits {
				bestBits, bestOrder = b, o
			}
		}
		return bestOrder, bestBits
	}
	order = lpc.BestFixedOrder(shifted, 4)
	res := make([]int32, len(shifted)-order)
	lpc.ComputeFixedResiduals(res, shifted, order)
	return order, rice.CostResidual(res, len(shifted), order, p.MaxPartitionOrder)
}

// chooseLPCPlan runs LPC analysis on the wasted-bits-shifted samples and, if
// applicable, returns the chosen order, its quantized coefficients and shift,
// and the exact Rice residual cost (the residual cost only; the warmup, coeff,
// precision and shift field bits are added by the caller). ok is false when LPC
// is not applicable for this subframe.
func chooseLPCPlan(shifted []int32, eff int, p Params, window []float64) (order int, qcoeff []int32, shift, resBits int, ok bool) {
	o, sh, qc, aok := lpc.AnalyzeLPC(shifted, window, p.MaxLPCOrder, p.LPCPrecision, eff)
	if !aok {
		return 0, nil, 0, 0, false
	}
	res := make([]int32, len(shifted)-o)
	lpc.ComputeLPCResiduals(res, shifted, qc, sh, o)
	return o, qc, sh, rice.CostResidual(res, len(shifted), o, p.MaxPartitionOrder), true
}

// wastedBits64 returns the number of low-order zero bits common to every sample.
// Returns 0 when all samples are zero (that case is encoded as a constant subframe).
func wastedBits64(s []int64) int {
	var orAll int64
	for _, v := range s {
		orAll |= v
	}
	if orAll == 0 {
		return 0
	}
	return bits.TrailingZeros64(uint64(orAll))
}

// allEqual64 reports whether every element of s is the same value.
func allEqual64(s []int64) bool {
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}

// shiftRight64 returns a new slice with every element of s shifted right by
// wasted bits. Returns s directly when wasted is zero.
func shiftRight64(s []int64, wasted int) []int64 {
	if wasted == 0 {
		return s
	}
	out := make([]int64, len(s))
	for i, v := range s {
		out[i] = v >> uint(wasted)
	}
	return out
}

// chooseFixedOrder64 returns the fixed predictor order to use for shifted together
// with the Rice cost (in bits) of that order's residuals. Mirrors chooseFixedOrder
// but operates on int64 samples for wide bit-depth support.
func chooseFixedOrder64(shifted []int64, p Params) (order, resBits int) {
	if p.ExhaustiveFixed {
		bestOrder, bestBits := 0, int(^uint(0)>>1)
		maxOrder := min(4, len(shifted)-1)
		res := make([]int64, len(shifted))
		for o := 0; o <= maxOrder; o++ {
			r := res[:len(shifted)-o]
			lpc.ComputeFixedResiduals64(r, shifted, o)
			b := rice.CostResidual64(r, len(shifted), o, p.MaxPartitionOrder)
			if b < bestBits {
				bestBits, bestOrder = b, o
			}
		}
		return bestOrder, bestBits
	}
	order = lpc.BestFixedOrder64(shifted, 4)
	res := make([]int64, len(shifted)-order)
	lpc.ComputeFixedResiduals64(res, shifted, order)
	return order, rice.CostResidual64(res, len(shifted), order, p.MaxPartitionOrder)
}

// chooseLPCPlan64 runs LPC analysis on the wasted-bits-shifted int64 samples and,
// if applicable, returns the chosen order, its quantized coefficients and shift,
// and the exact Rice residual cost. Mirrors chooseLPCPlan for wide bit-depth support.
func chooseLPCPlan64(shifted []int64, eff int, p Params, window []float64) (order int, qcoeff []int32, shift, resBits int, ok bool) {
	o, sh, qc, aok := lpc.AnalyzeLPC(shifted, window, p.MaxLPCOrder, p.LPCPrecision, eff)
	if !aok {
		return 0, nil, 0, 0, false
	}
	res := make([]int64, len(shifted)-o)
	lpc.ComputeLPCResiduals64(res, shifted, qc, sh, o)
	return o, qc, sh, rice.CostResidual64(res, len(shifted), o, p.MaxPartitionOrder), true
}

// writeConstant64 writes a FLAC constant subframe for int64 samples.
func writeConstant64(bw *bitio.Writer, value int64, wasted, bps int) {
	writeSubframeHeader(bw, 0, wasted)
	bw.WriteSignedBits(value>>uint(wasted), uint(bps-wasted))
}

// writeVerbatim64 writes a FLAC verbatim subframe for int64 samples.
func writeVerbatim64(bw *bitio.Writer, s []int64, wasted, bps int) {
	writeSubframeHeader(bw, 1, wasted)
	eff := uint(bps - wasted)
	for _, v := range s {
		bw.WriteSignedBits(v>>uint(wasted), eff)
	}
}

// writeLPC64 writes a SUBFRAME_LPC for int64 samples. Mirrors writeLPC exactly,
// with the int32(...) casts removed and the *64 residual callees used.
func writeLPC64(bw *bitio.Writer, s []int64, order int, qcoeff []int32, shift, wasted, bps, precision, maxPartOrder int) {
	writeSubframeHeader(bw, 31+order, wasted)
	eff := uint(bps - wasted)
	shifted := shiftRight64(s, wasted)
	for i := range order {
		bw.WriteSignedBits(shifted[i], eff)
	}
	bw.WriteBits(uint64(precision-1), 4)
	bw.WriteSignedBits(int64(shift), 5)
	for i := range order {
		bw.WriteSignedBits(int64(qcoeff[i]), uint(precision))
	}
	res := make([]int64, len(shifted)-order)
	lpc.ComputeLPCResiduals64(res, shifted, qcoeff, shift, order)
	rice.EncodeResidual64(bw, res, len(shifted), order, maxPartOrder)
}

// writeFixed64 writes a fixed-predictor subframe for int64 samples.
// Mirrors writeFixed exactly, with the int32(...) casts removed and the *64 callees used.
func writeFixed64(bw *bitio.Writer, s []int64, order, wasted, bps, maxPartOrder int) {
	writeSubframeHeader(bw, 8+order, wasted)
	eff := uint(bps - wasted)
	shifted := shiftRight64(s, wasted)
	for i := range order {
		bw.WriteSignedBits(shifted[i], eff)
	}
	res := make([]int64, len(shifted)-order)
	lpc.ComputeFixedResiduals64(res, shifted, order)
	rice.EncodeResidual64(bw, res, len(shifted), order, maxPartOrder)
}

// planSubframe64 chooses the cheapest subframe encoding for int64 samples at the
// given bps. Mirrors planSubframe exactly: same accounting, same tie-breaking rules.
//
//nolint:dupl // intentional: typed parallel of planSubframe
func planSubframe64(s []int64, bps int, p Params, window []float64) subframePlan {
	if allEqual64(s) {
		return subframePlan{kind: 0, bits: 1 + 6 + 1 + bps}
	}
	wasted := wastedBits64(s)
	if wasted >= bps {
		wasted = bps - 1
	}
	eff := bps - wasted
	hdrBits := 1 + 6 + 1
	if wasted > 0 {
		hdrBits += wasted
	}
	shifted := shiftRight64(s, wasted)

	fOrder, fResBits := chooseFixedOrder64(shifted, p)
	fixedBits := hdrBits + fOrder*eff + fResBits
	verbatimBits := hdrBits + len(s)*eff

	best := subframePlan{kind: 1, wasted: wasted, bits: verbatimBits}
	if fixedBits < best.bits {
		best = subframePlan{kind: 2, order: fOrder, wasted: wasted, bits: fixedBits}
	}

	if p.MaxLPCOrder > 0 && window != nil {
		if lOrder, qc, shift, lResBits, ok := chooseLPCPlan64(shifted, eff, p, window); ok {
			lpcBits := hdrBits + lOrder*eff + 4 + 5 + lOrder*p.LPCPrecision + lResBits
			if lpcBits < best.bits {
				best = subframePlan{kind: 3, order: lOrder, wasted: wasted, bits: lpcBits, qcoeff: qc, shift: shift}
			}
		}
	}
	return best
}

// writeSubframe64 writes s according to plan for int64 samples. Mirrors writeSubframe.
func writeSubframe64(bw *bitio.Writer, s []int64, bps int, plan subframePlan, p Params) {
	switch plan.kind {
	case 0:
		writeConstant64(bw, s[0], 0, bps)
	case 1:
		writeVerbatim64(bw, s, plan.wasted, bps)
	case 3:
		writeLPC64(bw, s, plan.order, plan.qcoeff, plan.shift, plan.wasted, bps, p.LPCPrecision, p.MaxPartitionOrder)
	default:
		writeFixed64(bw, s, plan.order, plan.wasted, bps, p.MaxPartitionOrder)
	}
}

// allEqual reports whether every element of s is the same value.
func allEqual(s []int32) bool {
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}

// shiftRight returns a new slice with every element of s shifted right by
// wasted bits. Returns s directly when wasted is zero.
func shiftRight(s []int32, wasted int) []int32 {
	if wasted == 0 {
		return s
	}
	out := make([]int32, len(s))
	for i, v := range s {
		out[i] = v >> uint(wasted)
	}
	return out
}
