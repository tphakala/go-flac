package frame

import "github.com/tphakala/go-flac/internal/rice"

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
}

// NewWorkspace allocates a Workspace sized for the given maximum block size,
// channel count, and maximum LPC order. maxOrder is reserved for later tasks.
func NewWorkspace(maxBlock, channels, maxOrder int) *Workspace {
	_ = channels
	_ = maxOrder
	return &Workspace{
		side:   make([]int32, maxBlock),
		mid:    make([]int32, maxBlock),
		side64: make([]int64, maxBlock),
		mid64:  make([]int64, maxBlock),
	}
}
