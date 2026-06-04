package rice

import (
	"math/bits"

	"github.com/tphakala/go-flac/internal/bitio"
)

const (
	maxParam4  = 14 // method 0 (4-bit param), 15 is escape4
	maxParam5  = 30 // method 1 (5-bit param), 31 is escape5
	maxRawBits = 31 // escape width field is 5 bits (0..31)
)

// zigzag maps a signed residual to the unsigned value the bitstream stores. It is
// the exact inverse of DecodeResidual's (u>>1)^-(u&1) de-interleave.
func zigzag(r int32) uint64 {
	return uint64(uint32((r << 1) ^ (r >> 31)))
}

// zigzag64 maps a signed int64 residual (up to ~34 bits at 32-bps) to the unsigned
// value the bitstream stores. Inverse of DecodeResidual's de-interleave.
func zigzag64(r int64) uint64 {
	return uint64((r << 1) ^ (r >> 63))
}

// PartPlan is the chosen coding for one partition. The fields stay unexported:
// the frame package holds []PartPlan opaquely (carrying a planned subframe's
// chosen partition coding from the planner to the writer) and passes it back to
// WritePlanned without reading the fields.
type PartPlan struct {
	escape  bool
	param   int // Rice parameter k (when !escape)
	rawBits int // raw sample width (when escape)
	payload int // bits for the partition body (excluding the parameter field itself)
}

// Scratch holds reusable Rice planning buffers owned by the caller (the encoder
// Workspace). Single-use within one planning call; safe to reuse across calls and
// across subframes. Buffers grow on demand to fit, then are reused.
type Scratch struct {
	sums  []uint64   // finest-order [partitions*ncols] flattened
	maxU  []uint64   // finest-order per-partition max zigzag
	cur   []PartPlan // working per-order plan buffer
	plans []PartPlan // chosen (best) partition plan, returned aliased
}

func (s *Scratch) ensure(parts, ncols int) {
	if cap(s.sums) < parts*ncols {
		s.sums = make([]uint64, parts*ncols)
	}
	s.sums = s.sums[:parts*ncols]
	if cap(s.maxU) < parts {
		s.maxU = make([]uint64, parts)
	}
	s.maxU = s.maxU[:parts]
	if cap(s.cur) < parts {
		s.cur = make([]PartPlan, parts)
	}
	s.cur = s.cur[:parts]
	if cap(s.plans) < parts {
		s.plans = make([]PartPlan, parts)
	}
}

// feasiblePmax returns the largest feasible partition order for a block of the
// given size and predictor order, capped at maxPartOrder. It reproduces the pmax
// loop from the legacy planResidual: an order is feasible only when blockSize is
// divisible by 2^po and the resulting partition length is at least predOrder and
// nonzero (the contiguous prefix property). It depends only on these three ints,
// not on the residual values.
func feasiblePmax(blockSize, predOrder, maxPartOrder int) int {
	pmax := 0
	for po := 1; po <= maxPartOrder; po++ {
		if blockSize%(1<<po) != 0 {
			break
		}
		partLen := blockSize >> po
		if partLen < predOrder || partLen == 0 {
			break
		}
		pmax = po
	}
	return pmax
}

// planPartition picks the cheapest coding for the zigzag values in zz.
func planPartition(zz []uint64) PartPlan {
	k, riceBitCount := bestParam(zz)

	// Escape alternative: store each residual raw at the max needed width. The width
	// field is 5 bits, so escape is only available when raw <= 31. If a residual needs
	// 32+ bits (possible at wide bit depth), escape cannot represent it and must be
	// skipped; the Rice parameter path or the subframe-level verbatim fallback covers it.
	var maxU uint64
	for _, u := range zz {
		if u > maxU {
			maxU = u
		}
	}
	if raw := bits.Len64(maxU); raw <= maxRawBits {
		escBits := 5 + raw*len(zz)
		if int64(escBits) < riceBitCount {
			return PartPlan{escape: true, rawBits: raw, payload: escBits}
		}
	}
	// Safe narrowing: riceBitCount <= ~131k for any real partition, inside int.
	return PartPlan{param: k, payload: int(riceBitCount)}
}

// bestParam returns the Rice parameter k minimizing payload bits for zz, and that
// bit count (unary quotients + remainders, not including the parameter field).
func bestParam(zz []uint64) (k int, bitCount int64) {
	var sum uint64
	for _, u := range zz {
		sum += u
	}
	n := len(zz)
	est := 0
	if n > 0 {
		mean := sum / uint64(n)
		est = bits.Len64(mean) // approximately floor(log2(mean))+1
	}
	// Search a window around the estimate to find the true minimum.
	lo, hi := est-1, est+2
	if lo < 0 {
		lo = 0
	}
	if hi > maxParam5 {
		hi = maxParam5
	}
	// When est > maxParam5+1, lo was set to est-1 > hi. Clamp lo down so the
	// loop always evaluates at least k=maxParam5 (the best feasible parameter).
	if lo > hi {
		lo = hi
	}
	best, bestBits := 0, int64(^uint64(0)>>1)
	for kk := lo; kk <= hi; kk++ {
		b := riceBits(zz, kk)
		if b < bestBits {
			bestBits, best = b, kk
		}
	}
	return best, bestBits
}

// riceBits counts the payload bits (unary + remainder) for zz with parameter k.
// The accumulator is int64 so a pathological partition (large residuals coded at a
// small k) cannot overflow on a 32-bit build, where a wrapped count would make
// bestParam choose a non-optimal parameter.
func riceBits(zz []uint64, k int) int64 {
	// k is a Rice parameter in [0, maxParam5]; the & 63 mask is a no-op on its
	// value but proves the shift count is < 64 to the compiler, which then drops
	// the oversized-shift guard (CMP/SBB/AND) from this per-residual hot loop.
	shift := uint(k) & 63
	// Each residual costs (u>>k) quotient bits + 1 stop bit + k remainder bits. The
	// constant per-residual term (1 + k) is summed once at the end rather than every
	// iteration, leaving just a shift-and-add in the hot loop.
	var quotients int64
	for _, u := range zz {
		quotients += int64(u >> shift)
	}
	return quotients + int64(len(zz))*(1+int64(k))
}

// EncodeResidual writes the partitioned Rice residual for res (the blockSize-
// predOrder residuals that follow the warmup samples) and returns the number of
// bits written (excluding any byte-alignment padding). It plans the partition
// then emits it via WritePlanned. The caller supplies sc; the chosen plan aliases
// sc.plans, so no other rice call may run between the PlanResidualInt32 below and
// the write loop.
func EncodeResidual(bw *bitio.Writer, res []int32, blockSize, predOrder, maxPartOrder int, sc *Scratch) int {
	_, plans, paramBits, _ := PlanResidualInt32(res, blockSize, predOrder, maxPartOrder, sc)
	return WritePlanned(bw, res, predOrder, blockSize, plans, paramBits)
}

// WritePlanned emits the partitioned-Rice body for res using a precomputed plan
// (from PlanResidualInt32), computing zigzag(res[i]) on the fly. It writes the
// 2-bit method, the 4-bit partition order, the per-partition parameter/escape
// fields, and the residual payloads, returning the bits written. It performs no
// search; the partition order is recovered from len(plans).
func WritePlanned(bw *bitio.Writer, res []int32, predOrder, blockSize int, plans []PartPlan, paramBits int) int {
	method := method4bit
	escapeCode := uint64(escape4)
	if paramBits == 5 {
		method = method5bit
		escapeCode = uint64(escape5)
	}
	po := bits.Len(uint(len(plans))) - 1 // partitions = 1<<po
	written := 2 + 4
	bw.WriteBits(uint64(method), 2)
	bw.WriteBits(uint64(po), 4)
	partitions := 1 << po
	partLen := blockSize / partitions
	idx := 0
	for p := range partitions {
		n := partLen
		if p == 0 {
			n -= predOrder
			if n < 0 {
				n = 0
			}
		}
		pl := plans[p]
		written += paramBits + pl.payload
		if pl.escape {
			bw.WriteBits(escapeCode, uint(paramBits))
			bw.WriteBits(uint64(pl.rawBits), 5)
			for i := range n {
				bw.WriteSignedBits(int64(res[idx+i]), uint(pl.rawBits))
			}
		} else {
			k := uint(pl.param)
			bw.WriteBits(uint64(pl.param), uint(paramBits))
			for i := range n {
				u := zigzag(res[idx+i])
				bw.WriteUnary(u >> k)
				bw.WriteBits(u&((uint64(1)<<k)-1), k)
			}
		}
		idx += n
	}
	return written
}

// CostResidual returns the number of bits EncodeResidual would write for res,
// without actually writing to any buffer. It runs the identical planning path.
func CostResidual(res []int32, blockSize, predOrder, maxPartOrder int, sc *Scratch) int {
	_, _, _, total := PlanResidualInt32(res, blockSize, predOrder, maxPartOrder, sc)
	return total
}

// EncodeResidual64 is the int64-residual analogue of EncodeResidual, for wide
// (25-32 bps) subframes. It plans the partition then emits it via WritePlanned64.
// The chosen plan aliases sc.plans, so no other rice call may run between the
// PlanResidualInt64 below and the write loop.
func EncodeResidual64(bw *bitio.Writer, res []int64, blockSize, predOrder, maxPartOrder int, sc *Scratch) int {
	_, plans, paramBits, _ := PlanResidualInt64(res, blockSize, predOrder, maxPartOrder, sc)
	return WritePlanned64(bw, res, predOrder, blockSize, plans, paramBits)
}

// WritePlanned64 is the int64-residual analogue of WritePlanned, for wide
// (25-32 bps) subframes. It computes zigzag64(res[i]) on the fly and performs no
// search; the partition order is recovered from len(plans).
func WritePlanned64(bw *bitio.Writer, res []int64, predOrder, blockSize int, plans []PartPlan, paramBits int) int {
	method := method4bit
	escapeCode := uint64(escape4)
	if paramBits == 5 {
		method = method5bit
		escapeCode = uint64(escape5)
	}
	po := bits.Len(uint(len(plans))) - 1 // partitions = 1<<po
	written := 2 + 4
	bw.WriteBits(uint64(method), 2)
	bw.WriteBits(uint64(po), 4)
	partitions := 1 << po
	partLen := blockSize / partitions
	idx := 0
	for p := range partitions {
		n := partLen
		if p == 0 {
			n -= predOrder
			if n < 0 {
				n = 0
			}
		}
		pl := plans[p]
		written += paramBits + pl.payload
		if pl.escape {
			bw.WriteBits(escapeCode, uint(paramBits))
			bw.WriteBits(uint64(pl.rawBits), 5)
			for i := range n {
				bw.WriteSignedBits(res[idx+i], uint(pl.rawBits))
			}
		} else {
			k := uint(pl.param)
			bw.WriteBits(uint64(pl.param), uint(paramBits))
			for i := range n {
				u := zigzag64(res[idx+i])
				bw.WriteUnary(u >> k)
				bw.WriteBits(u&((uint64(1)<<k)-1), k)
			}
		}
		idx += n
	}
	return written
}

// CostResidual64 returns the bits EncodeResidual64 would write for res.
func CostResidual64(res []int64, blockSize, predOrder, maxPartOrder int, sc *Scratch) int {
	_, _, _, total := PlanResidualInt64(res, blockSize, predOrder, maxPartOrder, sc)
	return total
}

// PlanResidualInt32 zigzags res internally and runs the libFLAC merge-upward
// search, writing the chosen plan into sc.plans. It returns the partition order,
// the plan (aliased into sc.plans, valid until the next call on the same Scratch),
// the param-field width, and the total payload bits. Per-partition sums are
// computed once at the finest feasible order and merged upward, instead of
// rescanning the residuals once per partition order; this is a decision-for-
// decision reformulation of the older per-order rescan search, which the pcm
// byte-identical golden test guards against any drift.
//
//nolint:dupl // intentional: typed parallel of PlanResidualInt64
func PlanResidualInt32(res []int32, blockSize, predOrder, maxPartOrder int, sc *Scratch) (bestPO int, plans []PartPlan, paramBits, totalBits int) {
	pmax := feasiblePmax(blockSize, predOrder, maxPartOrder)
	// Guard: po==0 itself must be feasible; otherwise fall back like the reference.
	if blockSize-predOrder < 0 || blockSize == 0 {
		zz := make([]uint64, len(res))
		for i, r := range res {
			zz[i] = zigzag(r)
		}
		return riceFallbackPlan(zz)
	}

	// Column count: only k in [est-1, est+2] is ever read, and est <= bits.Len64(maxU)
	// over the whole block, so ncols = min(maxParam5, globalRaw+2)+1 covers every
	// query while keeping the finest pass cheap for quiet signals.
	var globalMaxU uint64
	for _, r := range res {
		u := zigzag(r)
		if u > globalMaxU {
			globalMaxU = u
		}
	}
	kHi := bits.Len64(globalMaxU) + 2
	if kHi > maxParam5 {
		kHi = maxParam5
	}
	ncols := kHi + 1

	P := 1 << pmax
	sc.ensure(P, ncols)
	clear(sc.sums[:P*ncols]) // sums is accumulated with += and reused, so zero it first.
	buildFinestTablesInt32(sc, res, blockSize, predOrder, pmax, ncols)

	return planFromTables(sc, blockSize, predOrder, pmax, kHi, ncols)
}

// PlanResidualInt64 is the wide-path analogue of PlanResidualInt32 (zigzag64).
//
//nolint:dupl // intentional: typed parallel of PlanResidualInt32
func PlanResidualInt64(res []int64, blockSize, predOrder, maxPartOrder int, sc *Scratch) (bestPO int, plans []PartPlan, paramBits, totalBits int) {
	pmax := feasiblePmax(blockSize, predOrder, maxPartOrder)
	if blockSize-predOrder < 0 || blockSize == 0 {
		zz := make([]uint64, len(res))
		for i, r := range res {
			zz[i] = zigzag64(r)
		}
		return riceFallbackPlan(zz)
	}

	var globalMaxU uint64
	for _, r := range res {
		u := zigzag64(r)
		if u > globalMaxU {
			globalMaxU = u
		}
	}
	kHi := bits.Len64(globalMaxU) + 2
	if kHi > maxParam5 {
		kHi = maxParam5
	}
	ncols := kHi + 1

	P := 1 << pmax
	sc.ensure(P, ncols)
	clear(sc.sums[:P*ncols]) // sums is accumulated with += and reused, so zero it first.
	buildFinestTablesInt64(sc, res, blockSize, predOrder, pmax, ncols)

	return planFromTables(sc, blockSize, predOrder, pmax, kHi, ncols)
}

// planFromTables runs the order-selection over the finest-order tables already
// built into sc.sums/sc.maxU. It is type-independent: the only type-specific work
// (zigzag of the residuals) happened in buildFinestTables*. It descends pmax..0,
// merging upward between orders, and copies the winning order's plans into
// sc.plans via a separate working buffer (sc.cur) so a later, smaller order
// cannot overwrite the chosen plans before they are returned.
func planFromTables(sc *Scratch, blockSize, predOrder, pmax, kHi, ncols int) (bestPO int, plans []PartPlan, paramBits, totalBits int) {
	totalBits = int(^uint(0) >> 1)
	for po := pmax; po >= 0; po-- {
		parts := 1 << po
		cur := sc.cur[:parts]
		maxK := 0
		sumPayload := 0
		for p := range parts {
			n := blockSize >> po
			if p == 0 {
				n -= predOrder
			}
			cur[p] = choosePartition(sc.sums[p*ncols:p*ncols+ncols], sc.maxU[p], n, kHi)
			if !cur[p].escape && cur[p].param > maxK {
				maxK = cur[p].param
			}
			sumPayload += cur[p].payload
		}
		pb := 4
		if maxK > maxParam4 {
			pb = 5
		}
		total := 2 + 4 + parts*pb + sumPayload
		if total <= totalBits { // <= so the smallest order wins on ties (we descend)
			totalBits, bestPO, paramBits = total, po, pb
			sc.plans = append(sc.plans[:0], cur...)
		}
		// Merge adjacent partition pairs into the next coarser table (in place).
		if po > 0 {
			mergeUpward(sc.sums, sc.maxU, parts, ncols)
		}
	}
	return bestPO, sc.plans, paramBits, totalBits
}

// riceFallbackPlan reproduces planResidualReference's order-0 single-partition
// fallback for blocks where no partition order (not even order 0) is feasible.
func riceFallbackPlan(zz []uint64) (bestPO int, bestPlans []PartPlan, paramBits, totalBits int) {
	var pl PartPlan
	if len(zz) > 0 {
		pl = planPartition(zz)
	}
	paramBits = 4
	if !pl.escape && pl.param > maxParam4 {
		paramBits = 5
	}
	return 0, []PartPlan{pl}, paramBits, 2 + 4 + paramBits + pl.payload
}

// buildFinestTablesInt32 computes, for the finest feasible partition order pmax,
// the per-partition sums table (sc.sums[p*ncols+k] = sum of u>>k over the
// partition, for k in 0..kHi) and the per-partition max zigzag (sc.maxU), reading
// residuals from res and zigzagging on the fly. sc.sums must be zeroed by the
// caller (it accumulates with +=); sc.maxU entries are fully assigned per
// partition and need no pre-zeroing. These tables are merged upward by mergeUpward
// as planFromTables descends through coarser orders.
func buildFinestTablesInt32(sc *Scratch, res []int32, blockSize, predOrder, pmax, ncols int) {
	P := 1 << pmax
	partLen := blockSize >> pmax
	idx := 0
	for p := range P {
		n := partLen
		if p == 0 {
			n -= predOrder
		}
		// row aliases this partition's sums columns (ncols == kHi+1); hoisting the
		// slice out of the per-residual loop and ranging over it drops the per-element
		// bounds check on row[k], and the & 63 mask drops the oversized-shift guard
		// (k is always <= kHi <= maxParam5 = 30). Byte-identical to sums[base+k] += u>>k.
		row := sc.sums[p*ncols : p*ncols+ncols]
		var mu uint64
		for j := range n {
			u := zigzag(res[idx+j])
			if u > mu {
				mu = u
			}
			for k := range row {
				row[k] += u >> (uint(k) & 63)
			}
		}
		sc.maxU[p] = mu
		idx += n
	}
}

// buildFinestTablesInt64 is the wide-path analogue of buildFinestTablesInt32,
// reading int64 residuals and zigzagging with zigzag64.
func buildFinestTablesInt64(sc *Scratch, res []int64, blockSize, predOrder, pmax, ncols int) {
	P := 1 << pmax
	partLen := blockSize >> pmax
	idx := 0
	for p := range P {
		n := partLen
		if p == 0 {
			n -= predOrder
		}
		// See buildFinestTablesInt32: row slice + & 63 mask drop the bounds check and
		// oversized-shift guard from this hot loop. Byte-identical.
		row := sc.sums[p*ncols : p*ncols+ncols]
		var mu uint64
		for j := range n {
			u := zigzag64(res[idx+j])
			if u > mu {
				mu = u
			}
			for k := range row {
				row[k] += u >> (uint(k) & 63)
			}
		}
		sc.maxU[p] = mu
		idx += n
	}
}

// mergeUpward folds the current order's parts partitions into parts/2 coarser
// partitions in place: adjacent sums columns add, and the coarser maxU is the max
// of the two finer maxU. parts is the partition count of the order just evaluated.
func mergeUpward(sums, maxU []uint64, parts, ncols int) {
	half := parts / 2
	for p := range half {
		dst := p * ncols
		a := (2 * p) * ncols
		b := (2*p + 1) * ncols
		for k := range ncols {
			sums[dst+k] = sums[a+k] + sums[b+k]
		}
		if maxU[2*p+1] > maxU[2*p] {
			maxU[p] = maxU[2*p+1]
		} else {
			maxU[p] = maxU[2*p]
		}
	}
}

// choosePartition reproduces planPartition+bestParam for one partition given its
// precomputed sums[k] table (k in 0..kHi), its maxU, and its residual count n.
func choosePartition(sums []uint64, maxU uint64, n, kHi int) PartPlan {
	// bestParam windowed search, byte-identical to bestParam(zz).
	est := 0
	if n > 0 {
		mean := sums[0] / uint64(n)
		est = bits.Len64(mean)
	}
	lo, hi := est-1, est+2
	if lo < 0 {
		lo = 0
	}
	if hi > maxParam5 {
		hi = maxParam5
	}
	if lo > hi {
		lo = hi
	}
	if hi > kHi {
		hi = kHi // sums beyond kHi are unused; est+2 cannot exceed kHi by construction
	}
	best, bestBits := 0, int64(^uint64(0)>>1)
	for k := lo; k <= hi; k++ {
		b := int64(sums[k]) + int64(n)*(1+int64(k))
		if b < bestBits {
			bestBits, best = b, k
		}
	}
	// Escape alternative, byte-identical to planPartition.
	if raw := bits.Len64(maxU); raw <= maxRawBits {
		escBits := 5 + raw*n
		if int64(escBits) < bestBits {
			return PartPlan{escape: true, rawBits: raw, payload: escBits}
		}
	}
	return PartPlan{param: best, payload: int(bestBits)}
}
