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

// planResidual searches partition orders 0..maxPartOrder, returns the best order,
// per-partition plans, parameter field width (4 or 5), and total bits
// (2 method + 4 order + sum of paramBits + payload per partition).
func planResidual(zz []uint64, blockSize, predOrder, maxPartOrder int) (bestPO int, bestPlans []partPlan, paramBits, totalBits int) {
	totalBits = int(^uint(0) >> 1)
	for po := 0; po <= maxPartOrder; po++ {
		partitions := 1 << po
		if blockSize%partitions != 0 {
			continue
		}
		partLen := blockSize / partitions
		if partLen-predOrder < 0 {
			continue
		}
		if partLen == 0 {
			continue
		}
		plans := make([]partPlan, partitions)
		maxK := 0
		sumPayload := 0
		idx := 0
		ok := true
		for p := range partitions {
			n := partLen
			if p == 0 {
				n -= predOrder
			}
			if n < 0 || idx+n > len(zz) {
				ok = false
				break
			}
			pl := planPartition(zz[idx : idx+n])
			plans[p] = pl
			if !pl.escape && pl.param > maxK {
				maxK = pl.param
			}
			sumPayload += pl.payload
			idx += n
		}
		if !ok {
			continue
		}
		pb := 4
		if maxK > maxParam4 {
			pb = 5
		}
		total := 2 + 4 + partitions*pb + sumPayload
		if total < totalBits {
			totalBits, bestPO, bestPlans, paramBits = total, po, plans, pb
		}
	}
	if bestPlans == nil {
		// Fallback: order 0, single partition.
		var pl partPlan
		if len(zz) > 0 {
			pl = planPartition(zz)
		}
		bestPlans = []partPlan{pl}
		paramBits = 4
		if !pl.escape && pl.param > maxParam4 {
			paramBits = 5
		}
		bestPO = 0
		totalBits = 2 + 4 + paramBits + pl.payload
	}
	return bestPO, bestPlans, paramBits, totalBits
}
