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
	kind          int // 0 constant, 1 verbatim, 2 fixed, 3 LPC
	order         int // fixed predictor order (kind 2) or LPC order (kind 3)
	wasted        int
	bits          int       // estimated total subframe bits, for decorrelation selection
	qcoeff        [32]int32 // LPC quantized coefficients (kind 3); the first `order` entries are valid
	shift         int       // LPC quantization shift (kind 3)
	cand          int       // workspace slot 0..3 holding this subframe's carried residuals+plan
	riceParamBits int       // param-field width (4 or 5) of the carried plan
}

// planSubframe chooses the cheapest subframe encoding for s at the given bps and
// stores the winning predictor's residuals and chosen Rice partition plan into
// workspace slot idx, so writeSubframe emits the carried data without recomputing
// residuals or re-running the Rice search. idx is the candidate slot (0..3 for the
// stereo candidates; the independent path uses 0).
//
//nolint:dupl // intentional: typed parallel of planSubframe64
func planSubframe(ws *Workspace, idx int, s []int32, bps int, p Params, window []float64) subframePlan {
	if allEqual(s) {
		return subframePlan{kind: 0, bits: 1 + 6 + 1 + bps, cand: idx}
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

	fOrder, fResBits := chooseFixedOrder(ws, shifted, p)
	fixedBits := hdrBits + fOrder*eff + fResBits
	verbatimBits := hdrBits + len(s)*eff

	// Start from verbatim; fixed and LPC must strictly beat the running best so
	// that verbatim wins ties against fixed (preserving the prior behavior).
	best := subframePlan{kind: 1, wasted: wasted, bits: verbatimBits, cand: idx}
	if fixedBits < best.bits {
		best = subframePlan{kind: 2, order: fOrder, wasted: wasted, bits: fixedBits, cand: idx}
	}

	if p.MaxLPCOrder > 0 && window != nil {
		if lOrder, qc, shift, lResBits, ok := chooseLPCPlan(ws, shifted, eff, p, window); ok {
			lpcBits := hdrBits + lOrder*eff + 4 + 5 + lOrder*p.LPCPrecision + lResBits
			if lpcBits < best.bits {
				best = subframePlan{kind: 3, order: lOrder, wasted: wasted, bits: lpcBits, qcoeff: qc, shift: shift, cand: idx}
			}
		}
	}

	// Carry the winner's residuals and chosen Rice plan into slot idx so the writer
	// emits them without recomputing. Constant/verbatim subframes carry no residual.
	// This recomputes exactly what the writer used to compute (same shifted, order,
	// predictor), so the emitted bits are byte-identical.
	switch best.kind {
	case 2: // fixed
		c := &ws.cand[idx]
		res := c.ensureRes(len(s) - best.order)
		lpc.ComputeFixedResiduals(res, shifted, best.order)
		_, plans, paramBits, _ := rice.PlanResidualInt32(res, len(s), best.order, p.MaxPartitionOrder, &ws.rice)
		c.plans = append(c.plans[:0], plans...)
		best.riceParamBits = paramBits
	case 3: // LPC
		c := &ws.cand[idx]
		res := c.ensureRes(len(s) - best.order)
		lpc.ComputeLPCResiduals(res, shifted, best.qcoeff[:best.order], best.shift, best.order)
		_, plans, paramBits, _ := rice.PlanResidualInt32(res, len(s), best.order, p.MaxPartitionOrder, &ws.rice)
		c.plans = append(c.plans[:0], plans...)
		best.riceParamBits = paramBits
	}
	return best
}

// writeSubframe writes s according to plan. plan is taken by pointer to avoid
// copying the heavy subframePlan struct on this per-subframe path.
func writeSubframe(bw *bitio.Writer, ws *Workspace, s []int32, bps int, plan *subframePlan, p Params) {
	switch plan.kind {
	case 0:
		writeConstant(bw, s[0], 0, bps)
	case 1:
		writeVerbatim(bw, s, plan.wasted, bps)
	case 3:
		writeLPC(bw, ws, s, bps, plan, p.LPCPrecision)
	default:
		writeFixed(bw, ws, s, bps, plan)
	}
}

// writeLPC writes a SUBFRAME_LPC: header (type code 31+order), warmup samples,
// 4-bit precision-1, 5-bit shift, the quantized coefficients, then the Rice
// residual. precision is the coefficient bit width (p.LPCPrecision). The residuals
// and chosen Rice plan were computed by planSubframe into ws.cand[plan.cand]; this
// emits them via rice.WritePlanned without recomputing or re-searching.
func writeLPC(bw *bitio.Writer, ws *Workspace, s []int32, bps int, plan *subframePlan, precision int) {
	order := plan.order
	writeSubframeHeader(bw, 31+order, plan.wasted)
	eff := uint(bps - plan.wasted)
	for i := range order {
		bw.WriteSignedBits(int64(s[i]>>uint(plan.wasted)), eff)
	}
	bw.WriteBits(uint64(precision-1), 4)
	bw.WriteSignedBits(int64(plan.shift), 5)
	for i := range order {
		bw.WriteSignedBits(int64(plan.qcoeff[i]), uint(precision))
	}
	c := &ws.cand[plan.cand]
	rice.WritePlanned(bw, c.res[:len(s)-order], order, len(s), c.plans, plan.riceParamBits)
}

// writeFixed writes a fixed-predictor subframe of the given order. The residuals
// and chosen Rice plan were computed by planSubframe into ws.cand[plan.cand]; this
// emits them via rice.WritePlanned without recomputing or re-searching.
func writeFixed(bw *bitio.Writer, ws *Workspace, s []int32, bps int, plan *subframePlan) {
	order := plan.order
	writeSubframeHeader(bw, 8+order, plan.wasted)
	eff := uint(bps - plan.wasted)
	for i := range order {
		bw.WriteSignedBits(int64(s[i]>>uint(plan.wasted)), eff)
	}
	c := &ws.cand[plan.cand]
	rice.WritePlanned(bw, c.res[:len(s)-order], order, len(s), c.plans, plan.riceParamBits)
}

// chooseFixedOrder returns the fixed predictor order to use for shifted together
// with the Rice cost (in bits) of that order's residuals, so the caller does not
// have to recompute the residuals to learn their cost.
//
//nolint:dupl // intentional: typed parallel of chooseFixedOrder64
func chooseFixedOrder(ws *Workspace, shifted []int32, p Params) (order, resBits int) {
	if p.ExhaustiveFixed {
		bestOrder, bestBits := 0, int(^uint(0)>>1)
		maxOrder := min(4, len(shifted)-1)
		res := ws.ensureCostRes(len(shifted)) // reused across orders
		for o := 0; o <= maxOrder; o++ {
			r := res[:len(shifted)-o]
			lpc.ComputeFixedResiduals(r, shifted, o)
			b := rice.CostResidual(r, len(shifted), o, p.MaxPartitionOrder, &ws.rice)
			if b < bestBits {
				bestBits, bestOrder = b, o
			}
		}
		return bestOrder, bestBits
	}
	order = lpc.BestFixedOrder(shifted, 4)
	res := ws.ensureCostRes(len(shifted) - order)
	lpc.ComputeFixedResiduals(res, shifted, order)
	return order, rice.CostResidual(res, len(shifted), order, p.MaxPartitionOrder, &ws.rice)
}

// chooseLPCPlan runs LPC analysis on the wasted-bits-shifted samples and, if
// applicable, returns the chosen order, its quantized coefficients and shift,
// and the exact Rice residual cost (the residual cost only; the warmup, coeff,
// precision and shift field bits are added by the caller). ok is false when LPC
// is not applicable for this subframe.
func chooseLPCPlan(ws *Workspace, shifted []int32, eff int, p Params, window []float64) (order int, qcoeff [32]int32, shift, resBits int, ok bool) {
	var qc [32]int32
	o, sh, _, aok := lpc.AnalyzeLPC(shifted, window, p.MaxLPCOrder, p.LPCPrecision, eff, ws.lpcScratch(len(shifted)), qc[:])
	if !aok {
		return 0, qc, 0, 0, false
	}
	res := ws.ensureCostRes(len(shifted) - o)
	lpc.ComputeLPCResiduals(res, shifted, qc[:o], sh, o)
	return o, qc, sh, rice.CostResidual(res, len(shifted), o, p.MaxPartitionOrder, &ws.rice), true
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
// wasted bits. Returns s directly when wasted is zero. Used by tests; the encoder
// hot path uses planSubframe64's inline workspace-backed shift instead.
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
//
//nolint:dupl // intentional: typed parallel of chooseFixedOrder
func chooseFixedOrder64(ws *Workspace, shifted []int64, p Params) (order, resBits int) {
	if p.ExhaustiveFixed {
		bestOrder, bestBits := 0, int(^uint(0)>>1)
		maxOrder := min(4, len(shifted)-1)
		res := ws.ensureCostRes64(len(shifted)) // reused across orders
		for o := 0; o <= maxOrder; o++ {
			r := res[:len(shifted)-o]
			lpc.ComputeFixedResiduals64(r, shifted, o)
			b := rice.CostResidual64(r, len(shifted), o, p.MaxPartitionOrder, &ws.rice)
			if b < bestBits {
				bestBits, bestOrder = b, o
			}
		}
		return bestOrder, bestBits
	}
	order = lpc.BestFixedOrder64(shifted, 4)
	res := ws.ensureCostRes64(len(shifted) - order)
	lpc.ComputeFixedResiduals64(res, shifted, order)
	return order, rice.CostResidual64(res, len(shifted), order, p.MaxPartitionOrder, &ws.rice)
}

// chooseLPCPlan64 runs LPC analysis on the wasted-bits-shifted int64 samples and,
// if applicable, returns the chosen order, its quantized coefficients and shift,
// and the exact Rice residual cost. Mirrors chooseLPCPlan for wide bit-depth support.
func chooseLPCPlan64(ws *Workspace, shifted []int64, eff int, p Params, window []float64) (order int, qcoeff [32]int32, shift, resBits int, ok bool) {
	var qc [32]int32
	o, sh, _, aok := lpc.AnalyzeLPC(shifted, window, p.MaxLPCOrder, p.LPCPrecision, eff, ws.lpcScratch(len(shifted)), qc[:])
	if !aok {
		return 0, qc, 0, 0, false
	}
	res := ws.ensureCostRes64(len(shifted) - o)
	lpc.ComputeLPCResiduals64(res, shifted, qc[:o], sh, o)
	return o, qc, sh, rice.CostResidual64(res, len(shifted), o, p.MaxPartitionOrder, &ws.rice), true
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
// emitting the carried residuals+plan from ws.cand[plan.cand] via WritePlanned64.
func writeLPC64(bw *bitio.Writer, ws *Workspace, s []int64, bps int, plan *subframePlan, precision int) {
	order := plan.order
	writeSubframeHeader(bw, 31+order, plan.wasted)
	eff := uint(bps - plan.wasted)
	for i := range order {
		bw.WriteSignedBits(s[i]>>uint(plan.wasted), eff)
	}
	bw.WriteBits(uint64(precision-1), 4)
	bw.WriteSignedBits(int64(plan.shift), 5)
	for i := range order {
		bw.WriteSignedBits(int64(plan.qcoeff[i]), uint(precision))
	}
	c := &ws.cand[plan.cand]
	rice.WritePlanned64(bw, c.res64[:len(s)-order], order, len(s), c.plans, plan.riceParamBits)
}

// writeFixed64 writes a fixed-predictor subframe for int64 samples.
// Mirrors writeFixed exactly, emitting the carried residuals+plan via WritePlanned64.
func writeFixed64(bw *bitio.Writer, ws *Workspace, s []int64, bps int, plan *subframePlan) {
	order := plan.order
	writeSubframeHeader(bw, 8+order, plan.wasted)
	eff := uint(bps - plan.wasted)
	for i := range order {
		bw.WriteSignedBits(s[i]>>uint(plan.wasted), eff)
	}
	c := &ws.cand[plan.cand]
	rice.WritePlanned64(bw, c.res64[:len(s)-order], order, len(s), c.plans, plan.riceParamBits)
}

// planSubframe64 chooses the cheapest subframe encoding for int64 samples at the
// given bps and stores the winning predictor's residuals and chosen Rice plan into
// workspace slot idx. Mirrors planSubframe exactly: same accounting, same
// tie-breaking rules.
//
//nolint:dupl // intentional: typed parallel of planSubframe
func planSubframe64(ws *Workspace, idx int, s []int64, bps int, p Params, window []float64) subframePlan {
	if allEqual64(s) {
		return subframePlan{kind: 0, bits: 1 + 6 + 1 + bps, cand: idx}
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
	var shifted []int64
	if wasted == 0 {
		shifted = s
	} else {
		shifted = ws.ensureShifted64(len(s))
		for i, v := range s {
			shifted[i] = v >> uint(wasted)
		}
	}

	fOrder, fResBits := chooseFixedOrder64(ws, shifted, p)
	fixedBits := hdrBits + fOrder*eff + fResBits
	verbatimBits := hdrBits + len(s)*eff

	best := subframePlan{kind: 1, wasted: wasted, bits: verbatimBits, cand: idx}
	if fixedBits < best.bits {
		best = subframePlan{kind: 2, order: fOrder, wasted: wasted, bits: fixedBits, cand: idx}
	}

	if p.MaxLPCOrder > 0 && window != nil {
		if lOrder, qc, shift, lResBits, ok := chooseLPCPlan64(ws, shifted, eff, p, window); ok {
			lpcBits := hdrBits + lOrder*eff + 4 + 5 + lOrder*p.LPCPrecision + lResBits
			if lpcBits < best.bits {
				best = subframePlan{kind: 3, order: lOrder, wasted: wasted, bits: lpcBits, qcoeff: qc, shift: shift, cand: idx}
			}
		}
	}

	// Carry the winner's residuals and chosen Rice plan into slot idx (wide path).
	switch best.kind {
	case 2: // fixed
		c := &ws.cand[idx]
		res := c.ensureRes64(len(s) - best.order)
		lpc.ComputeFixedResiduals64(res, shifted, best.order)
		_, plans, paramBits, _ := rice.PlanResidualInt64(res, len(s), best.order, p.MaxPartitionOrder, &ws.rice)
		c.plans = append(c.plans[:0], plans...)
		best.riceParamBits = paramBits
	case 3: // LPC
		c := &ws.cand[idx]
		res := c.ensureRes64(len(s) - best.order)
		lpc.ComputeLPCResiduals64(res, shifted, best.qcoeff[:best.order], best.shift, best.order)
		_, plans, paramBits, _ := rice.PlanResidualInt64(res, len(s), best.order, p.MaxPartitionOrder, &ws.rice)
		c.plans = append(c.plans[:0], plans...)
		best.riceParamBits = paramBits
	}
	return best
}

// writeSubframe64 writes s according to plan for int64 samples. Mirrors writeSubframe.
func writeSubframe64(bw *bitio.Writer, ws *Workspace, s []int64, bps int, plan *subframePlan, p Params) {
	switch plan.kind {
	case 0:
		writeConstant64(bw, s[0], 0, bps)
	case 1:
		writeVerbatim64(bw, s, plan.wasted, bps)
	case 3:
		writeLPC64(bw, ws, s, bps, plan, p.LPCPrecision)
	default:
		writeFixed64(bw, ws, s, bps, plan)
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
