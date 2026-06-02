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

	// Choose chCode and the two subframes per the stereo mode.
	chCode := 1 // independent (channel assignment code 1 = 2 independent channels)
	best := indep
	if p.Stereo == StereoAdaptive {
		if ms < best {
			best, chCode = ms, 10
		}
	} else { // StereoFull
		if ls < best {
			best, chCode = ls, 8
		}
		if rs < best {
			best, chCode = rs, 9
		}
		if ms < best {
			best, chCode = ms, 10
		}
	}
	_ = best // used only for comparisons above; suppress unused-write lint

	writeFrameHeader(bw, bs, chCode, frameNum)
	switch chCode {
	case 8: // left/side: left at bps, side at bps+1
		writeSubframe(bw, l, bps, planL, p)
		writeSubframe(bw, side, bps+1, planS, p)
	case 9: // right/side: side at bps+1, right at bps
		writeSubframe(bw, side, bps+1, planS, p)
		writeSubframe(bw, r, bps, planR, p)
	case 10: // mid/side: mid at bps, side at bps+1
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
