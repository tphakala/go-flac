package frame

import "github.com/tphakala/go-flac/internal/rice"

// candScratch holds one stereo candidate's carried scratch: the chosen residuals
// and the chosen Rice partition plan, computed once in the planner and read back
// by the writer so it never recomputes residuals or re-runs the Rice search. The
// four stereo candidates (L, R, M, S) each own a distinct candScratch so their
// carried data never aliases.
type candScratch struct {
	res   []int32         // carried int32 residuals
	res64 []int64         // carried wide-path residuals
	plans []rice.PartPlan // carried chosen partition plan (copied out of rice.Scratch)
}

// ensureRes grows c.res to hold at least n int32 residuals and returns the
// n-length prefix. It is defensive against a zero-value Workspace (used by unit
// tests that construct Workspace directly); NewWorkspace pre-sizes res to maxBlock
// so the steady-state encoder never reallocates here.
func (c *candScratch) ensureRes(n int) []int32 {
	if cap(c.res) < n {
		c.res = make([]int32, n)
	}
	return c.res[:n]
}

// ensureRes64 is the int64 analogue of ensureRes for the wide path.
func (c *candScratch) ensureRes64(n int) []int64 {
	if cap(c.res64) < n {
		c.res64 = make([]int64, n)
	}
	return c.res64[:n]
}

// Workspace holds the per-encoder scratch buffers reused across frames so the
// encode hot path allocates nothing in steady state. One Workspace per
// pcm.Encoder; not safe for concurrent use by multiple goroutines (each encoder
// owns its own). Defined in package frame (top of the encode DAG); rice and lpc
// stay leaf packages and receive caller-owned scratch slices instead.
type Workspace struct {
	// Stereo decorrelation block buffers (shared; int32 + int64 wide path).
	side, mid     []int32
	side64, mid64 []int64
	// Rice planning scratch reused across subframes (zigzag sums, plans).
	rice rice.Scratch
	// Per-candidate carried residuals + chosen plan. Slots 0..3 map to the four
	// stereo candidates (0=L, 1=R, 2=M, 3=S); the independent and wide-independent
	// paths use slot 0. Each slot owns distinct backing arrays so planning all four
	// candidates before writing two cannot corrupt the carried data via aliasing.
	cand [4]candScratch
}

// NewWorkspace allocates a Workspace sized for the given maximum block size,
// channel count, and maximum LPC order. maxOrder is reserved for later tasks.
func NewWorkspace(maxBlock, channels, maxOrder int) *Workspace {
	_ = channels
	_ = maxOrder
	ws := &Workspace{
		side:   make([]int32, maxBlock),
		mid:    make([]int32, maxBlock),
		side64: make([]int64, maxBlock),
		mid64:  make([]int64, maxBlock),
	}
	for i := range ws.cand {
		ws.cand[i] = candScratch{
			res:   make([]int32, maxBlock),
			res64: make([]int64, maxBlock),
			// 256 = FLAC max partitions (partition order 8); cap so append never reallocates.
			plans: make([]rice.PartPlan, 0, 256),
		}
	}
	return ws
}
