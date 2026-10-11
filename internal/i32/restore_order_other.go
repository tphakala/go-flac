//go:build !amd64 && !arm64 && !arm

package i32

// maxScalarRestoreOrder is the largest order LPCRestore routes to the pure-Go
// scalar kernels (lpc_small.go). There is no SIMD path on these builds; the
// value is deliberately conservative (the original ceiling) because the larger
// kernels are not measured here. NOT MEASURED on these builds.
const maxScalarRestoreOrder = 7

// maxScalarRestoreOrderNarrow is the largest order LPCRestoreNarrow routes to the
// int32 scalar kernels (lpc_small_narrow.go). It matches maxScalarRestoreOrder;
// NOT MEASURED on these builds.
const maxScalarRestoreOrderNarrow = 7
