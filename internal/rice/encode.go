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

// partPlan is the chosen coding for one partition.
type partPlan struct {
	escape  bool
	param   int // Rice parameter k (when !escape)
	rawBits int // raw sample width (when escape)
	payload int // bits for the partition body (excluding the parameter field itself)
}

// planPartition picks the cheapest coding for the zigzag values in zz.
func planPartition(zz []uint64) partPlan {
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
			return partPlan{escape: true, rawBits: raw, payload: escBits}
		}
	}
	// Safe narrowing: riceBitCount <= ~131k for any real partition, inside int.
	return partPlan{param: k, payload: int(riceBitCount)}
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
// bits written (excluding any byte-alignment padding).
func EncodeResidual(bw *bitio.Writer, res []int32, blockSize, predOrder, maxPartOrder int) int {
	zz := make([]uint64, len(res))
	for i, r := range res {
		zz[i] = zigzag(r)
	}

	po, plans, paramBits, _ := planResidual(zz, blockSize, predOrder, maxPartOrder)

	method := method4bit
	escapeCode := uint64(escape4)
	if paramBits == 5 {
		method = method5bit
		escapeCode = uint64(escape5)
	}

	written := 2 + 4 // method (2 bits) + partition order (4 bits)
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
				// planResidual never selects an order where the first partition is
				// shorter than predOrder; this guards a future predictor (LPC) whose
				// order could exceed a tiny partition length.
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
				u := zz[idx+i]
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
func CostResidual(res []int32, blockSize, predOrder, maxPartOrder int) int {
	zz := make([]uint64, len(res))
	for i, r := range res {
		zz[i] = zigzag(r)
	}
	_, _, _, total := planResidual(zz, blockSize, predOrder, maxPartOrder)
	return total
}

// EncodeResidual64 is the int64-residual analogue of EncodeResidual, for wide
// (25-32 bps) subframes. Planning operates on the shared zz []uint64 path.
func EncodeResidual64(bw *bitio.Writer, res []int64, blockSize, predOrder, maxPartOrder int) int {
	zz := make([]uint64, len(res))
	for i, r := range res {
		zz[i] = zigzag64(r)
	}

	po, plans, paramBits, _ := planResidual(zz, blockSize, predOrder, maxPartOrder)

	method := method4bit
	escapeCode := uint64(escape4)
	if paramBits == 5 {
		method = method5bit
		escapeCode = uint64(escape5)
	}

	written := 2 + 4 // method (2 bits) + partition order (4 bits)
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
				// planResidual never selects an order where the first partition is
				// shorter than predOrder; this guards a future predictor (LPC) whose
				// order could exceed a tiny partition length.
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
				u := zz[idx+i]
				bw.WriteUnary(u >> k)
				bw.WriteBits(u&((uint64(1)<<k)-1), k)
			}
		}
		idx += n
	}
	return written
}

// CostResidual64 returns the bits EncodeResidual64 would write for res.
func CostResidual64(res []int64, blockSize, predOrder, maxPartOrder int) int {
	zz := make([]uint64, len(res))
	for i, r := range res {
		zz[i] = zigzag64(r)
	}
	_, _, _, total := planResidual(zz, blockSize, predOrder, maxPartOrder)
	return total
}

// planResidual chooses the partition order and per-partition coding that minimize
// the Rice payload for the zigzag residuals zz, using the libFLAC merge-upward
// search: per-partition sums are computed once at the finest feasible order and
// merged upward, instead of rescanning zz once per partition order. It is a
// decision-for-decision reformulation of the older per-order rescan search,
// which the pcm byte-identical golden test guards against any drift.
func planResidual(zz []uint64, blockSize, predOrder, maxPartOrder int) (bestPO int, bestPlans []partPlan, paramBits, totalBits int) {
	// pmax: largest feasible order (contiguous prefix property, see Task 1 notes).
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
	// Guard: po==0 itself must be feasible; otherwise fall back like the reference.
	if blockSize-predOrder < 0 || blockSize == 0 {
		return riceFallbackPlan(zz)
	}

	// Column count: only k in [est-1, est+2] is ever read, and est <= bits.Len64(maxU)
	// over the whole block, so ncols = min(maxParam5, globalRaw+2)+1 covers every
	// query while keeping the finest pass cheap for quiet signals.
	var globalMaxU uint64
	for _, u := range zz {
		if u > globalMaxU {
			globalMaxU = u
		}
	}
	kHi := bits.Len64(globalMaxU) + 2
	if kHi > maxParam5 {
		kHi = maxParam5
	}
	ncols := kHi + 1

	// Finest-order tables: one row of ncols sums plus one maxU per partition.
	sums, maxU := buildFinestTables(zz, blockSize, predOrder, pmax, kHi, ncols)

	// Evaluate each order pmax..0, merging upward as we descend.
	totalBits = int(^uint(0) >> 1)
	for po := pmax; po >= 0; po-- {
		parts := 1 << po
		pl := make([]partPlan, parts)
		maxK := 0
		sumPayload := 0
		for p := range parts {
			n := blockSize >> po
			if p == 0 {
				n -= predOrder
			}
			pl[p] = choosePartition(sums[p*ncols:p*ncols+ncols], maxU[p], n, kHi)
			if !pl[p].escape && pl[p].param > maxK {
				maxK = pl[p].param
			}
			sumPayload += pl[p].payload
		}
		pb := 4
		if maxK > maxParam4 {
			pb = 5
		}
		total := 2 + 4 + parts*pb + sumPayload
		if total <= totalBits { // <= so the smallest order wins on ties (we descend)
			totalBits, bestPO, bestPlans, paramBits = total, po, pl, pb
		}
		// Merge adjacent partition pairs into the next coarser table (in place).
		if po > 0 {
			mergeUpward(sums, maxU, parts, ncols)
		}
	}
	return bestPO, bestPlans, paramBits, totalBits
}

// riceFallbackPlan reproduces planResidualReference's order-0 single-partition
// fallback for blocks where no partition order (not even order 0) is feasible.
func riceFallbackPlan(zz []uint64) (bestPO int, bestPlans []partPlan, paramBits, totalBits int) {
	var pl partPlan
	if len(zz) > 0 {
		pl = planPartition(zz)
	}
	paramBits = 4
	if !pl.escape && pl.param > maxParam4 {
		paramBits = 5
	}
	return 0, []partPlan{pl}, paramBits, 2 + 4 + paramBits + pl.payload
}

// buildFinestTables computes, for the finest feasible partition order pmax, the
// per-partition sums table (sums[p*ncols+k] = sum of u>>k over the partition, for
// k in 0..kHi) and the per-partition maxU. These tables are merged upward by
// mergeUpward as planResidual descends through coarser orders.
func buildFinestTables(zz []uint64, blockSize, predOrder, pmax, kHi, ncols int) (sums, maxU []uint64) {
	P := 1 << pmax
	sums = make([]uint64, P*ncols) // sums[p*ncols + k]
	maxU = make([]uint64, P)
	partLen := blockSize >> pmax
	idx := 0
	for p := range P {
		n := partLen
		if p == 0 {
			n -= predOrder
		}
		base := p * ncols
		var mu uint64
		for j := range n {
			u := zz[idx+j]
			if u > mu {
				mu = u
			}
			// sums[k] += u>>k for k in 0..kHi
			for k := 0; k <= kHi; k++ {
				sums[base+k] += u >> uint(k)
			}
		}
		maxU[p] = mu
		idx += n
	}
	return sums, maxU
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
func choosePartition(sums []uint64, maxU uint64, n, kHi int) partPlan {
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
			return partPlan{escape: true, rawBits: raw, payload: escBits}
		}
	}
	return partPlan{param: best, payload: int(bestBits)}
}
