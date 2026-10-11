package i32

import (
	"math"
	"testing"
)

// Benchmarks pair the dispatched (SIMD) path with the pure-Go baseline so the
// speedup is visible directly. SetBytes counts the three buffers touched
// (a + b read, dst written), 4 bytes per int32.

const benchN = 1000

func BenchmarkInterleave2_1000(b *testing.B) {
	a := make([]int32, benchN)
	c := make([]int32, benchN)
	dst := make([]int32, benchN*2)
	for i := range a {
		a[i] = int32(i)
		c[i] = int32(i + benchN)
	}
	b.SetBytes(benchN * 4 * 3)
	for b.Loop() {
		Interleave2(dst, a, c)
	}
}

func BenchmarkInterleave2Go_1000(b *testing.B) {
	a := make([]int32, benchN)
	c := make([]int32, benchN)
	dst := make([]int32, benchN*2)
	for i := range a {
		a[i] = int32(i)
		c[i] = int32(i + benchN)
	}
	b.SetBytes(benchN * 4 * 3)
	for b.Loop() {
		interleave2Go(dst, a, c)
	}
}

func BenchmarkDeinterleave2_1000(b *testing.B) {
	src := make([]int32, benchN*2)
	a := make([]int32, benchN)
	c := make([]int32, benchN)
	for i := range src {
		src[i] = int32(i)
	}
	b.SetBytes(benchN * 4 * 3)
	for b.Loop() {
		Deinterleave2(a, c, src)
	}
}

func BenchmarkDeinterleave2Go_1000(b *testing.B) {
	src := make([]int32, benchN*2)
	a := make([]int32, benchN)
	c := make([]int32, benchN)
	for i := range src {
		src[i] = int32(i)
	}
	b.SetBytes(benchN * 4 * 3)
	for b.Loop() {
		deinterleave2Go(a, c, src)
	}
}

func benchAB() (a, b, dst []int32) {
	a = make([]int32, benchN)
	b = make([]int32, benchN)
	dst = make([]int32, benchN)
	for i := range a {
		a[i] = int32(i)
		b[i] = int32(i - benchN/2)
	}
	return a, b, dst
}

func BenchmarkAdd_1000(b *testing.B) {
	x, y, dst := benchAB()
	b.SetBytes(benchN * 4 * 3)
	for b.Loop() {
		Add(dst, x, y)
	}
}

func BenchmarkAddGo_1000(b *testing.B) {
	x, y, dst := benchAB()
	b.SetBytes(benchN * 4 * 3)
	for b.Loop() {
		addGo(dst, x, y)
	}
}

func BenchmarkSub_1000(b *testing.B) {
	x, y, dst := benchAB()
	b.SetBytes(benchN * 4 * 3)
	for b.Loop() {
		Sub(dst, x, y)
	}
}

func BenchmarkSubGo_1000(b *testing.B) {
	x, y, dst := benchAB()
	b.SetBytes(benchN * 4 * 3)
	for b.Loop() {
		subGo(dst, x, y)
	}
}

func benchStereo() (left, right, p, q []int32) {
	left = make([]int32, benchN)
	right = make([]int32, benchN)
	p = make([]int32, benchN)
	q = make([]int32, benchN)
	for i := range left {
		left[i] = int32(i*3 - benchN)
		right[i] = int32(benchN - i)
	}
	return left, right, p, q
}

func BenchmarkMidSideEncode_1000(b *testing.B) {
	left, right, mid, side := benchStereo()
	b.SetBytes(benchN * 4 * 4)
	for b.Loop() {
		MidSideEncode(mid, side, left, right)
	}
}

func BenchmarkMidSideEncodeGo_1000(b *testing.B) {
	left, right, mid, side := benchStereo()
	b.SetBytes(benchN * 4 * 4)
	for b.Loop() {
		midSideEncodeGo(mid, side, left, right)
	}
}

func BenchmarkMidSideDecode_1000(b *testing.B) {
	mid, side, left, right := benchStereo()
	b.SetBytes(benchN * 4 * 4)
	for b.Loop() {
		MidSideDecode(left, right, mid, side)
	}
}

func BenchmarkMidSideDecodeGo_1000(b *testing.B) {
	mid, side, left, right := benchStereo()
	b.SetBytes(benchN * 4 * 4)
	for b.Loop() {
		midSideDecodeGo(left, right, mid, side)
	}
}

func benchSrcDst() (src, dst []int32) { return benchSrcDstN(benchN) }

func benchSrcDstN(n int) (src, dst []int32) {
	src = make([]int32, n)
	dst = make([]int32, n)
	for i := range src {
		src[i] = int32(i * i)
	}
	return src, dst
}

func BenchmarkDiff1_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		Diff1(dst, src)
	}
}

func BenchmarkDiff1Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		diff1Go(dst, src)
	}
}

func BenchmarkDiff2_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		Diff2(dst, src)
	}
}

func BenchmarkDiff2Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		diff2Go(dst, src)
	}
}

func BenchmarkDiff3_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		Diff3(dst, src)
	}
}

func BenchmarkDiff3Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		diff3Go(dst, src)
	}
}

func BenchmarkDiff4_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		Diff4(dst, src)
	}
}

func BenchmarkDiff4Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		diff4Go(dst, src)
	}
}

// Restore benchmarks pair the SIMD cumulative-sum decomposition (RestoreK)
// against restoreKGo, the pure-Go direct decode recurrence a scalar FLAC decoder
// would use, so the speedup of the prefix-sum kernel (and the K-pass cost at
// higher orders) is visible honestly.

func BenchmarkRestore1_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		Restore1(dst, src)
	}
}

func BenchmarkRestore1Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		restore1Go(dst, src)
	}
}

func BenchmarkRestore2_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		Restore2(dst, src)
	}
}

func BenchmarkRestore2Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		restore2Go(dst, src)
	}
}

func BenchmarkRestore3_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		Restore3(dst, src)
	}
}

func BenchmarkRestore3Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		restore3Go(dst, src)
	}
}

func BenchmarkRestore4_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		Restore4(dst, src)
	}
}

func BenchmarkRestore4Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		restore4Go(dst, src)
	}
}

// LPC benchmarks pair the dispatched path against the pure-Go reference at two
// representative FLAC predictor orders. LPCResidualEncode is a parallel FIR;
// LPCRestore is the serial decode recurrence (vectorized only across taps, and
// only above the per-architecture scalar ceiling maxScalarRestoreOrder), benched
// honestly so the difference is visible.

func benchLPCCoeffs(order int) []int32 {
	c := make([]int32, order)
	for j := range c {
		v := int32(16000 >> (j / 4))
		if j%2 == 1 {
			v = -v
		}
		c[j] = v
	}
	return c
}

func BenchmarkLPCResidualEncode8_1000(b *testing.B) {
	src, dst := benchSrcDst()
	coeffs := benchLPCCoeffs(8)
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		LPCResidualEncode(dst, src, coeffs, 12)
	}
}

func BenchmarkLPCResidualEncode8Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	coeffs := benchLPCCoeffs(8)
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		lpcResidualEncodeGo(dst, src, coeffs, 12)
	}
}

func BenchmarkLPCResidualEncode32_1000(b *testing.B) {
	src, dst := benchSrcDst()
	coeffs := benchLPCCoeffs(32)
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		LPCResidualEncode(dst, src, coeffs, 12)
	}
}

func BenchmarkLPCResidualEncode32Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	coeffs := benchLPCCoeffs(32)
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		lpcResidualEncodeGo(dst, src, coeffs, 12)
	}
}

func BenchmarkLPCRestore8_1000(b *testing.B) {
	src, dst := benchSrcDst()
	coeffs := benchLPCCoeffs(8)
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		LPCRestore(dst, src, coeffs, 12)
	}
}

func BenchmarkLPCRestore8Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	coeffs := benchLPCCoeffs(8)
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		lpcRestoreGo(dst, src, coeffs, 12)
	}
}

func BenchmarkLPCRestore32_1000(b *testing.B) {
	src, dst := benchSrcDst()
	coeffs := benchLPCCoeffs(32)
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		LPCRestore(dst, src, coeffs, 12)
	}
}

func BenchmarkLPCRestore32Go_1000(b *testing.B) {
	src, dst := benchSrcDst()
	coeffs := benchLPCCoeffs(32)
	b.SetBytes(benchN * 4 * 2)
	for b.Loop() {
		lpcRestoreGo(dst, src, coeffs, 12)
	}
}

// BenchmarkLPCRestoreOrder measures the decode recurrence per predictor order.
// "dispatch" is the public path, "generic" the lpcRestoreGo reference, and
// "dispatch_4096" the public path at the usual FLAC block size. "scalar" times
// the specialized kernel directly (orders up to maxScalarKernelOrder), whatever
// the routing, so the scalar and SIMD sides of the cutover stay comparable.
func BenchmarkLPCRestoreOrder(b *testing.B) {
	src, dst := benchSrcDst()
	src4096, dst4096 := benchSrcDstN(4096)
	for _, order := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} {
		coeffs := benchLPCCoeffs(order)
		name := "o" + itoa(order)
		b.Run(name+"/dispatch", func(b *testing.B) {
			b.SetBytes(benchN * 4 * 2)
			for b.Loop() {
				LPCRestore(dst, src, coeffs, 12)
			}
		})
		b.Run(name+"/generic", func(b *testing.B) {
			b.SetBytes(benchN * 4 * 2)
			for b.Loop() {
				lpcRestoreGo(dst, src, coeffs, 12)
			}
		})
		if order <= maxScalarKernelOrder {
			b.Run(name+"/scalar", func(b *testing.B) {
				b.SetBytes(benchN * 4 * 2)
				for b.Loop() {
					lpcRestoreScalar(dst, src, coeffs, 12)
				}
			})
		}
		b.Run(name+"/dispatch_4096", func(b *testing.B) {
			b.SetBytes(4096 * 4 * 2)
			for b.Loop() {
				LPCRestore(dst4096, src4096, coeffs, 12)
			}
		})
	}
}

// benchLPCCoeffsNarrow is benchLPCCoeffs scaled so the absolute coefficient sum
// stays below 2^16 up to order 32, which satisfies LPCRestoreNarrowSafe for 16-bit
// samples at shift 12. benchLPCCoeffs itself fails the predicate from order 5.
func benchLPCCoeffsNarrow(order int) []int32 {
	c := make([]int32, order)
	for j := range c {
		v := int32(4000 >> (j / 4))
		if j%2 == 1 {
			v = -v
		}
		c[j] = v
	}
	return c
}

// benchNarrowResidual returns the residual of a 16-bit synthetic signal of n
// samples under benchLPCCoeffsNarrow(order) at shift 12, so the restore runs on
// predicate-satisfying, in-range data. It fails the benchmark if the predicate
// does not hold.
func benchNarrowResidual(b *testing.B, order, n int) (res, coeffs []int32) {
	b.Helper()
	coeffs = benchLPCCoeffsNarrow(order)
	if !LPCRestoreNarrowSafe(coeffs, 16, 12) {
		b.Fatalf("order %d: benchmark coefficients fail the narrow predicate", order)
	}
	samples := make([]int32, n)
	x := uint32(0x2545F491)
	for i := range samples {
		x = x*1664525 + 1013904223
		v := 9000*math.Sin(float64(i)*0.031) + 5000*math.Sin(float64(i)*0.173+1)
		samples[i] = int32(v) + int32(x>>28) - 8
	}
	res = make([]int32, n)
	lpcResidualEncodeGo(res, samples, coeffs, 12)
	return res, coeffs
}

// benchNarrowOrders are the orders the narrow routing benchmarks cover.
var benchNarrowOrders = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 16, 20, 24, 32}

// BenchmarkLPCRestoreNarrowOrder compares the int64 restore (wide) with the
// int32-accumulating one on identical predicate-satisfying 16-bit data, per
// order: "wide" is LPCRestore, "narrow" the public LPCRestoreNarrow (whatever it
// routes), "narrow_scalar" the specialized kernel directly (orders up to
// maxScalarKernelOrderNarrow), and n4096 the usual FLAC block size.
func BenchmarkLPCRestoreNarrowOrder(b *testing.B) {
	for _, order := range benchNarrowOrders {
		res, coeffs := benchNarrowResidual(b, order, benchN)
		dst := make([]int32, benchN)
		res4096, _ := benchNarrowResidual(b, order, 4096)
		dst4096 := make([]int32, 4096)
		name := "o" + itoa(order)
		b.Run(name+"/wide", func(b *testing.B) {
			b.SetBytes(benchN * 4 * 2)
			for b.Loop() {
				LPCRestore(dst, res, coeffs, 12)
			}
		})
		b.Run(name+"/narrow", func(b *testing.B) {
			b.SetBytes(benchN * 4 * 2)
			for b.Loop() {
				LPCRestoreNarrow(dst, res, coeffs, 12)
			}
		})
		if order <= maxScalarKernelOrderNarrow {
			b.Run(name+"/narrow_scalar", func(b *testing.B) {
				b.SetBytes(benchN * 4 * 2)
				for b.Loop() {
					lpcRestoreScalarNarrow(dst, res, coeffs, 12)
				}
			})
		}
		b.Run(name+"/n4096/wide", func(b *testing.B) {
			b.SetBytes(4096 * 4 * 2)
			for b.Loop() {
				LPCRestore(dst4096, res4096, coeffs, 12)
			}
		})
		b.Run(name+"/n4096/narrow", func(b *testing.B) {
			b.SetBytes(4096 * 4 * 2)
			for b.Loop() {
				LPCRestoreNarrow(dst4096, res4096, coeffs, 12)
			}
		})
	}
}

// benchRiceRes makes a residual stream with realistic small-magnitude values so
// the cost-minimizing Rice parameter is interior to the 0..14 scan.
func benchRiceRes() []int32 {
	res := make([]int32, benchN)
	x := uint32(0x12345)
	for i := range res {
		x = x*1664525 + 1013904223 // LCG
		res[i] = int32(x%4096) - 2048
	}
	return res
}

func BenchmarkRiceSums_1000(b *testing.B) {
	res := benchRiceRes()
	sums := make([]uint64, riceParamCount)
	b.SetBytes(benchN * 4)
	for b.Loop() {
		RiceSums(sums, res)
	}
}

func BenchmarkRiceSumsGo_1000(b *testing.B) {
	res := benchRiceRes()
	sums := make([]uint64, riceParamCount)
	b.SetBytes(benchN * 4)
	for b.Loop() {
		riceSumsGo(sums, res)
	}
}

// Wide variants exercise the FLAC 5-bit range (31 columns, k=0..30): the SIMD
// path now vectorizes all of it, where it previously fell back to scalar.
func BenchmarkRiceSumsWide_1000(b *testing.B) {
	res := benchRiceRes()
	sums := make([]uint64, riceMaxParam5+1)
	b.SetBytes(benchN * 4)
	for b.Loop() {
		RiceSums(sums, res)
	}
}

func BenchmarkRiceSumsWideGo_1000(b *testing.B) {
	res := benchRiceRes()
	sums := make([]uint64, riceMaxParam5+1)
	b.SetBytes(benchN * 4)
	for b.Loop() {
		riceSumsGo(sums, res)
	}
}

func BenchmarkRiceBestParam_1000(b *testing.B) {
	res := benchRiceRes()
	b.SetBytes(benchN * 4)
	for b.Loop() {
		RiceBestParam(res, riceMaxParam)
	}
}

func BenchmarkZigzagSum_1000(b *testing.B) {
	res := benchRiceRes()
	b.SetBytes(benchN * 4)
	for b.Loop() {
		ZigzagSum(res)
	}
}

func BenchmarkZigzagSumGo_1000(b *testing.B) {
	res := benchRiceRes()
	b.SetBytes(benchN * 4)
	for b.Loop() {
		zigzagSumGo(res)
	}
}

func BenchmarkFixedAbsSums_1000(b *testing.B) {
	src, _ := benchSrcDst()
	var sums [5]uint64
	b.SetBytes(benchN * 4)
	for b.Loop() {
		FixedAbsSums(src, &sums)
	}
}

func BenchmarkFixedAbsSumsGo_1000(b *testing.B) {
	src, _ := benchSrcDst()
	var sums [5]uint64
	b.SetBytes(benchN * 4)
	for b.Loop() {
		fixedAbsSumsGo(src, &sums)
	}
}

func BenchmarkMinMax_1000(b *testing.B) {
	res := benchRiceRes()
	b.SetBytes(benchN * 4)
	for b.Loop() {
		MinMax(res)
	}
}

func BenchmarkMinMaxGo_1000(b *testing.B) {
	res := benchRiceRes()
	b.SetBytes(benchN * 4)
	for b.Loop() {
		minMaxGo(res)
	}
}
