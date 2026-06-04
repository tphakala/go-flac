package frame

import (
	"github.com/tphakala/go-flac/internal/lpc"
	"github.com/tphakala/go-flac/internal/rice"
)

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
	// l64, r64 hold the int64 upcast of the L/R inputs for encodeStereo64. l64
	// is also reused as the per-channel scratch for the wide-independent path in
	// EncodeFrame; encodeStereo64 and that path never run within the same frame.
	l64, r64 []int64
	// Rice planning scratch reused across subframes (zigzag sums, plans).
	rice rice.Scratch
	// LPC analysis float64 scratch, shared across subframes. Single-use per
	// subframe: AnalyzeLPC fully overwrites every buffer before reading it, and
	// the planner extracts the quantized coefficients (by value) and residuals
	// before the next subframe runs, so sharing one Scratch is safe.
	lpc *lpc.Scratch
	// lpcCap is the max block length lpc was sized for, so lpcScratch can grow it
	// on demand (mirroring the cap checks in the other ensure* accessors).
	lpcCap int
	// costRes is the shared transient residual buffer for the cost-evaluation
	// passes in chooseFixedOrder/chooseLPCPlan. For a fixed winner the residual is
	// recomputed into the per-candidate carry buffer; for an LPC winner the residual
	// chooseLPCPlan left here is COPIED into the carry buffer (not re-run through the
	// FIR), so this buffer must stay intact between chooseLPCPlan returning and the
	// carry copy in planSubframe. Both consumers finish within one planSubframe call
	// before the next candidate reuses it, so sharing one buffer here is safe.
	costRes []int32
	// costRes64 is the int64 analogue of costRes for the wide (25-32 bps) path.
	// Used by chooseFixedOrder64 and chooseLPCPlan64 for cost-eval residuals; the LPC
	// winner's residual is copied from here into cand[idx].res64 rather than recomputed.
	costRes64 []int64
	// shifted32 and shifted64 are the wasted-bits-shifted sample buffers (int32 and
	// wide int64). Filled once per planSubframe/planSubframe64 call (when wasted > 0)
	// and consumed before the function returns, so sharing across the four stereo
	// candidates is safe. When wasted == 0 the shift is a no-op and the input slice
	// is used directly, so neither buffer is touched.
	shifted32 []int32
	shifted64 []int64
	// Apodization-window cache. Only two distinct block lengths ever occur in a
	// stream (the full block and the shorter final block), so two slots suffice.
	// The window is deterministic, so caching it is byte-identical to recomputing
	// it each frame.
	apodN   [2]int
	apodWin [2][]float64
	// Per-candidate carried residuals + chosen plan. Slots 0..3 map to the four
	// stereo candidates (0=L, 1=R, 2=M, 3=S); the independent and wide-independent
	// paths use slot 0. Each slot owns distinct backing arrays so planning all four
	// candidates before writing two cannot corrupt the carried data via aliasing.
	cand [4]candScratch
}

// NewWorkspace allocates a Workspace sized for the given maximum block size and
// maximum LPC order. channels is accepted for call-site clarity and forward
// compatibility; the buffers are sized for the worst case (full stereo
// decorrelation needs four candidate slots) regardless of channel count. maxOrder
// sizes the LPC analysis scratch; AnalyzeLPC clamps the effective order to the
// block size and 32, so a Workspace built with the encoder's configured maxOrder
// serves every frame.
func NewWorkspace(maxBlock, channels, maxOrder int) *Workspace {
	ws := &Workspace{
		side:      make([]int32, maxBlock),
		mid:       make([]int32, maxBlock),
		side64:    make([]int64, maxBlock),
		mid64:     make([]int64, maxBlock),
		l64:       make([]int64, maxBlock),
		r64:       make([]int64, maxBlock),
		lpc:       lpc.NewScratch(maxBlock, maxOrder),
		lpcCap:    maxBlock,
		costRes:   make([]int32, maxBlock),
		costRes64: make([]int64, maxBlock),
		shifted32: make([]int32, maxBlock),
		shifted64: make([]int64, maxBlock),
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

// window returns the apodization window of length n, building and caching it the
// first time each distinct n is seen. Only the full block and the shorter final
// block ever occur in a stream, so two slots suffice. The window is deterministic,
// so caching is byte-identical to recomputing it each frame. Returns nil when LPC
// is disabled (apodizationWindow returns nil); callers already guard window != nil.
func (ws *Workspace) window(p Params, n int) []float64 {
	for i := range 2 {
		if ws.apodN[i] == n && ws.apodWin[i] != nil {
			return ws.apodWin[i]
		}
	}
	w := apodizationWindow(p, n)
	if w == nil {
		return nil
	}
	slot := 0
	if ws.apodWin[0] != nil && ws.apodN[0] != n {
		slot = 1
	}
	ws.apodN[slot] = n
	ws.apodWin[slot] = w
	return w
}

// ensureCostRes grows ws.costRes to hold at least n int32 cost-eval residuals and
// returns the n-length prefix. Like candScratch.ensureRes, it is defensive against
// a zero-value Workspace built directly in unit tests; NewWorkspace pre-sizes
// costRes to maxBlock so the steady-state encoder never reallocates here.
func (ws *Workspace) ensureCostRes(n int) []int32 {
	if cap(ws.costRes) < n {
		ws.costRes = make([]int32, n)
	}
	return ws.costRes[:n]
}

// ensureCostRes64 grows ws.costRes64 to hold at least n int64 cost-eval residuals
// and returns the n-length prefix. Defensive against a zero-value Workspace;
// NewWorkspace pre-sizes costRes64 so the steady-state encoder never allocates.
func (ws *Workspace) ensureCostRes64(n int) []int64 {
	if cap(ws.costRes64) < n {
		ws.costRes64 = make([]int64, n)
	}
	return ws.costRes64[:n]
}

// ensureShifted32 grows ws.shifted32 to hold at least n int32 shifted samples
// and returns the n-length prefix. Defensive against a zero-value Workspace;
// NewWorkspace pre-sizes shifted32 so the steady-state encoder never allocates.
func (ws *Workspace) ensureShifted32(n int) []int32 {
	if cap(ws.shifted32) < n {
		ws.shifted32 = make([]int32, n)
	}
	return ws.shifted32[:n]
}

// ensureShifted64 grows ws.shifted64 to hold at least n int64 shifted samples
// and returns the n-length prefix. Defensive against a zero-value Workspace;
// NewWorkspace pre-sizes shifted64 so the steady-state encoder never allocates.
func (ws *Workspace) ensureShifted64(n int) []int64 {
	if cap(ws.shifted64) < n {
		ws.shifted64 = make([]int64, n)
	}
	return ws.shifted64[:n]
}

// ensureSide / ensureMid / ensureSide64 / ensureMid64 grow the stereo
// decorrelation buffers to hold at least n samples and return the n-length
// prefix. Like the other ensure* accessors they are defensive against a
// zero-value Workspace; NewWorkspace pre-sizes all four to maxBlock, so the
// steady-state encoder never reallocates here.
func (ws *Workspace) ensureSide(n int) []int32 {
	if cap(ws.side) < n {
		ws.side = make([]int32, n)
	}
	return ws.side[:n]
}

func (ws *Workspace) ensureMid(n int) []int32 {
	if cap(ws.mid) < n {
		ws.mid = make([]int32, n)
	}
	return ws.mid[:n]
}

func (ws *Workspace) ensureSide64(n int) []int64 {
	if cap(ws.side64) < n {
		ws.side64 = make([]int64, n)
	}
	return ws.side64[:n]
}

func (ws *Workspace) ensureMid64(n int) []int64 {
	if cap(ws.mid64) < n {
		ws.mid64 = make([]int64, n)
	}
	return ws.mid64[:n]
}

// ensureL64 grows ws.l64 to hold at least n int64 samples and returns the
// n-length prefix. Defensive against a zero-value Workspace; NewWorkspace
// pre-sizes l64 so the steady-state encoder never allocates.
func (ws *Workspace) ensureL64(n int) []int64 {
	if cap(ws.l64) < n {
		ws.l64 = make([]int64, n)
	}
	return ws.l64[:n]
}

// ensureR64 grows ws.r64 to hold at least n int64 samples and returns the
// n-length prefix. Defensive against a zero-value Workspace; NewWorkspace
// pre-sizes r64 so the steady-state encoder never allocates.
func (ws *Workspace) ensureR64(n int) []int64 {
	if cap(ws.r64) < n {
		ws.r64 = make([]int64, n)
	}
	return ws.r64[:n]
}

// lpcScratch returns the workspace LPC analysis scratch, lazily allocating (or
// growing) it for a zero-value Workspace (unit tests construct Workspace directly).
// NewWorkspace always pre-builds it to the configured maxBlock, so the steady-state
// encoder never allocates here. The grow-on-demand check mirrors the other ensure*
// accessors so a zero-value Workspace reused across increasing block sizes cannot
// later panic on an undersized buffer. The lazy scratch uses the largest order
// AnalyzeLPC ever needs (32) so it serves any order.
func (ws *Workspace) lpcScratch(maxBlock int) *lpc.Scratch {
	if ws.lpc == nil || ws.lpcCap < maxBlock {
		ws.lpc = lpc.NewScratch(maxBlock, 32)
		ws.lpcCap = maxBlock
	}
	return ws.lpc
}
