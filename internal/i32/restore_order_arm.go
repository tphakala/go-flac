//go:build arm

package i32

// maxScalarRestoreOrder is the largest order LPCRestore routes to the unrolled
// pure-Go scalar kernels (lpc_small.go); higher orders run lpcRestoreGo. There is
// no SIMD path on 32-bit ARM. Tuned from BenchmarkLPCRestoreOrder on a Raspberry
// Pi 4 (Cortex-A72 running AArch32 code, GOARM=7; min of 10 runs, ns per 1000
// samples, scalar kernel / lpcRestoreGo):
//
//	order  1:  8078 / 22038    order  4: 21631 / 19913    order  7: 34958 / 31523
//	order  2: 10071 / 12274    order  5: 23099 / 23889    order  8: 39901 / 35433
//	order  3: 16974 / 16154    order  6: 30005 / 27900
//
// The kernel wins at orders 1 and 2 and is 5-12% slower than the generic loop from
// order 3 on, except at order 5 where it is 3% faster. The ceiling is the highest
// order below the first order where the kernel loses by more than 2%. The value
// applies to every GOARM and every 32-bit ARM core; only a Cortex-A72 was measured.
const maxScalarRestoreOrder = 2

// maxScalarRestoreOrderNarrow is the largest order LPCRestoreNarrow routes to int32
// code: the unrolled kernels through maxScalarKernelOrderNarrow and the generic
// loop lpcRestoreGoNarrow above them. The int32 sum is far cheaper than int64 on
// 32-bit registers. Tuned from BenchmarkLPCRestoreNarrowOrder on the same Raspberry
// Pi 4 (min of 10 runs, ns per 1000 samples, int64 LPCRestore / int32 code):
//
//	order  1:  8083 /  2821    order  9: 38175 / 10017    order 17: 66814 / 26395
//	order  2: 10078 /  3933    order 10: 43612 / 16085    order 18: 70355 / 27468
//	order  3: 17003 /  3945    order 11: 46622 / 16080    order 19: 73330 / 28533
//	order  4: 21642 /  3948    order 12: 49754 / 14404    order 20: 78398 / 29602
//	order  5: 23106 /  5058    order 13: 52868 / 22091    order 24: 91986 / 34386
//	order  6: 30423 /  5613    order 14: 56485 / 23174    order 28: 104086 / 39406
//	order  7: 34974 /  6720    order 15: 61484 / 24252    order 32: 118224 / 44389
//	order  8: 35155 /  7919    order 16: 63692 / 25323
//
// Orders 1-12 are the unrolled kernels (2.7-5.5x faster), orders 13-32 the generic
// loop (2.4x faster, 58-63% lower time, p=0.000 at every measured order for 1000
// and 4096 samples). The generic loop with four accumulators was slower than the
// single-accumulator one at every order and is not kept.
const maxScalarRestoreOrderNarrow = 32
