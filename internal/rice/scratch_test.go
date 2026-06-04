package rice

import (
	"math/rand"
	"testing"
)

func TestPlanWithScratchNoAlloc(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	res := make([]int32, 4096)
	for i := range res {
		res[i] = int32(r.Intn(2001) - 1000)
	}
	var sc Scratch
	// Warm up (first call may allocate the scratch backing arrays).
	PlanResidualInt32(res, 4096, 2, 6, &sc)
	allocs := testing.AllocsPerRun(50, func() {
		PlanResidualInt32(res, 4096, 2, 6, &sc)
	})
	if allocs != 0 {
		t.Fatalf("PlanResidualInt32 allocates %.0f/op with a warm scratch", allocs)
	}
}
