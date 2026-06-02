package frame

import (
	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
)

// EncodeFrame encodes one frame (one block per channel) into bw and returns the
// assembled frame bytes. bw is Reset at entry; the returned slice aliases bw's
// buffer and is valid until the next use of bw.
func EncodeFrame(bw *bitio.Writer, p Params, si flac.StreamInfo, ch [][]int32, frameNum uint64) []byte {
	bw.Reset()
	bs := len(ch[0])
	bps := si.BitDepth
	nch := len(ch)

	if nch == 2 && p.Stereo != StereoIndependent && bps <= 24 {
		encodeStereo(bw, p, bps, bs, ch[0], ch[1], frameNum)
	} else {
		writeFrameHeader(bw, bs, nch-1, frameNum)
		for c := range nch {
			plan := planSubframe(ch[c], bps, p)
			writeSubframe(bw, ch[c], bps, plan, p)
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
func encodeStereo(bw *bitio.Writer, p Params, bps, bs int, l, r []int32, frameNum uint64) {
	side := make([]int32, bs)
	mid := make([]int32, bs)
	for i := range l {
		side[i] = l[i] - r[i]
		mid[i] = (l[i] + r[i]) >> 1
	}
	planL := planSubframe(l, bps, p)
	planR := planSubframe(r, bps, p)
	planM := planSubframe(mid, bps, p)
	planS := planSubframe(side, bps+1, p)

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

	writeFrameHeader(bw, bs, chCode, frameNum)
	switch chCode {
	case chLeftSide:
		writeSubframe(bw, l, bps, planL, p)
		writeSubframe(bw, side, bps+1, planS, p)
	case chRightSide:
		writeSubframe(bw, side, bps+1, planS, p)
		writeSubframe(bw, r, bps, planR, p)
	case chMidSide:
		writeSubframe(bw, mid, bps, planM, p)
		writeSubframe(bw, side, bps+1, planS, p)
	default: // independent
		writeSubframe(bw, l, bps, planL, p)
		writeSubframe(bw, r, bps, planR, p)
	}
	finishFrame(bw)
}

// finishFrame zero-pads to a byte boundary and appends the frame CRC-16.
func finishFrame(bw *bitio.Writer) {
	bw.AlignByte()
	bw.WriteBits(uint64(crc.Checksum16(bw.Bytes())), 16)
}
