package frame

import (
	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
	"github.com/tphakala/go-flac/internal/lpc"
)

// apodizationWindow returns the analysis window for one frame of length n, or
// nil when LPC is disabled. All subframes in a frame share the block length, so
// the window is computed once per frame.
func apodizationWindow(p Params, n int) []float64 {
	if p.MaxLPCOrder == 0 {
		return nil
	}
	// M3b ships a single window; Apodization selects it for forward-compat.
	return lpc.TukeyWindow(n, 0.5)
}

// EncodeFrame encodes one frame (one block per channel) into bw and returns the
// assembled frame bytes. bw is Reset at entry; the returned slice aliases bw's
// buffer and is valid until the next use of bw.
func EncodeFrame(bw *bitio.Writer, ws *Workspace, p Params, si flac.StreamInfo, ch [][]int32, frameNum uint64) []byte {
	bw.Reset()
	bs := len(ch[0])
	bps := si.BitDepth
	nch := len(ch)

	switch {
	case nch == 2 && p.Stereo != StereoIndependent && bps <= 24:
		encodeStereo(bw, ws, p, si.SampleRate, bps, bs, ch[0], ch[1], frameNum)
	case nch == 2 && p.Stereo != StereoIndependent && bps >= 25:
		encodeStereo64(bw, ws, p, si.SampleRate, bps, bs, ch[0], ch[1], frameNum)
	case bps >= 25:
		// Wide path (independent, mono, or multichannel): residuals can exceed int32,
		// so upcast each channel to int64 before planning and writing.
		window := ws.window(p, bs)
		writeFrameHeader(bw, bs, nch-1, si.SampleRate, bps, frameNum)
		buf := ws.ensureL64(bs) // reuse l64; never runs in same frame as encodeStereo64
		for c := range nch {
			for i := range bs {
				buf[i] = int64(ch[c][i])
			}
			plan := planSubframe64(ws, 0, buf, bps, p, window)
			writeSubframe64(bw, ws, buf, bps, &plan, p)
		}
		finishFrame(bw)
	default:
		window := ws.window(p, bs)
		writeFrameHeader(bw, bs, nch-1, si.SampleRate, bps, frameNum)
		for c := range nch {
			plan := planSubframe(ws, 0, ch[c], bps, p, window)
			writeSubframe(bw, ws, ch[c], bps, &plan, p)
		}
		finishFrame(bw)
	}
	return bw.Bytes()
}

// Channel assignment codes written in the frame header (inverse of the decoder's
// decodeStereoDecorrelated). Codes 0..7 are that many independent channels; 8..10
// are the stereo decorrelations.
const (
	chIndependent2 = 1  // two independent channels (left/right)
	chLeftSide     = 8  // left at bps, side (left-right) at bps+1
	chRightSide    = 9  // side at bps+1, right at bps
	chMidSide      = 10 // mid at bps, side at bps+1
)

// encodeStereo selects a channel assignment by estimated bits and writes it.
//
//nolint:dupl // intentional: typed parallel of encodeStereo64
func encodeStereo(bw *bitio.Writer, ws *Workspace, p Params, sampleRate, bps, bs int, l, r []int32, frameNum uint64) {
	side := ws.ensureSide(bs)
	mid := ws.ensureMid(bs)
	for i := range l {
		side[i] = l[i] - r[i]
		mid[i] = (l[i] + r[i]) >> 1
	}
	window := ws.window(p, bs)
	planL := planSubframe(ws, 0, l, bps, p, window)
	planR := planSubframe(ws, 1, r, bps, p, window)
	planM := planSubframe(ws, 2, mid, bps, p, window)
	planS := planSubframe(ws, 3, side, bps+1, p, window)

	// Candidate costs.
	indep := planL.bits + planR.bits
	ls := planL.bits + planS.bits
	rs := planS.bits + planR.bits
	ms := planM.bits + planS.bits

	// Choose chCode by minimum estimated cost. minCost tracks the running best;
	// each branch updates it only when a later comparison still reads it, so there
	// is no dead final write.
	chCode := chIndependent2
	minCost := indep
	if p.Stereo == StereoAdaptive {
		if ms < minCost {
			chCode = chMidSide
		}
	} else { // StereoFull
		if ls < minCost {
			minCost, chCode = ls, chLeftSide
		}
		if rs < minCost {
			minCost, chCode = rs, chRightSide
		}
		if ms < minCost {
			chCode = chMidSide
		}
	}

	writeFrameHeader(bw, bs, chCode, sampleRate, bps, frameNum)
	switch chCode {
	case chLeftSide:
		writeSubframe(bw, ws, l, bps, &planL, p)
		writeSubframe(bw, ws, side, bps+1, &planS, p)
	case chRightSide:
		writeSubframe(bw, ws, side, bps+1, &planS, p)
		writeSubframe(bw, ws, r, bps, &planR, p)
	case chMidSide:
		writeSubframe(bw, ws, mid, bps, &planM, p)
		writeSubframe(bw, ws, side, bps+1, &planS, p)
	default: // independent
		writeSubframe(bw, ws, l, bps, &planL, p)
		writeSubframe(bw, ws, r, bps, &planR, p)
	}
	finishFrame(bw)
}

// finishFrame zero-pads to a byte boundary and appends the frame CRC-16.
func finishFrame(bw *bitio.Writer) {
	bw.AlignByte()
	bw.WriteBits(uint64(crc.Checksum16(bw.Bytes())), 16)
}

// encodeStereo64 is the int64 analogue of encodeStereo for 25-32 bps. The l/r inputs
// arrive as int32 and are upcast to int64 before wide-domain decorrelation.
//
//nolint:dupl // intentional: typed parallel of encodeStereo
func encodeStereo64(bw *bitio.Writer, ws *Workspace, p Params, sampleRate, bps, bs int, l32, r32 []int32, frameNum uint64) {
	l := ws.ensureL64(bs)
	r := ws.ensureR64(bs)
	side := ws.ensureSide64(bs)
	mid := ws.ensureMid64(bs)
	// Upcast and decorrelate in a single pass over the block.
	for i := range bs {
		li, ri := int64(l32[i]), int64(r32[i])
		l[i], r[i] = li, ri
		side[i] = li - ri
		mid[i] = (li + ri) >> 1
	}
	window := ws.window(p, bs)
	planL := planSubframe64(ws, 0, l, bps, p, window)
	planR := planSubframe64(ws, 1, r, bps, p, window)
	planM := planSubframe64(ws, 2, mid, bps, p, window)
	planS := planSubframe64(ws, 3, side, bps+1, p, window)

	indep := planL.bits + planR.bits
	ls := planL.bits + planS.bits
	rs := planS.bits + planR.bits
	ms := planM.bits + planS.bits

	chCode := chIndependent2
	minCost := indep
	if p.Stereo == StereoAdaptive {
		if ms < minCost {
			chCode = chMidSide
		}
	} else { // StereoFull
		if ls < minCost {
			minCost, chCode = ls, chLeftSide
		}
		if rs < minCost {
			minCost, chCode = rs, chRightSide
		}
		if ms < minCost {
			chCode = chMidSide
		}
	}

	writeFrameHeader(bw, bs, chCode, sampleRate, bps, frameNum)
	switch chCode {
	case chLeftSide:
		writeSubframe64(bw, ws, l, bps, &planL, p)
		writeSubframe64(bw, ws, side, bps+1, &planS, p)
	case chRightSide:
		writeSubframe64(bw, ws, side, bps+1, &planS, p)
		writeSubframe64(bw, ws, r, bps, &planR, p)
	case chMidSide:
		writeSubframe64(bw, ws, mid, bps, &planM, p)
		writeSubframe64(bw, ws, side, bps+1, &planS, p)
	default: // independent
		writeSubframe64(bw, ws, l, bps, &planL, p)
		writeSubframe64(bw, ws, r, bps, &planR, p)
	}
	finishFrame(bw)
}
