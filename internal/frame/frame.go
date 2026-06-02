package frame

import (
	"fmt"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
)

// Frame holds one decoded FLAC frame. Channels is reused across Decode calls.
type Frame struct {
	BlockSize     int
	SampleRate    int
	BitsPerSample int
	Channels      [][]int32 // len == number of channels; each len == BlockSize
	Number        uint64    // sample number (variable blocksize) or frame number (fixed)
}

// header holds the parsed frame header.
type header struct {
	variableBlockSize bool
	blockSize         int
	sampleRate        int
	channelAssignment int
	bitsPerSample     int
	number            uint64
}

// channels returns the channel count implied by the channel assignment.
func (h *header) channels() int {
	switch h.channelAssignment {
	case 8, 9, 10: // left/side, right/side, mid/side
		return 2
	default:
		return h.channelAssignment + 1
	}
}

// Decode decodes exactly one frame from br into dst. dst.Channels is grown/reused
// to hold the frame's channels at its block size.
func Decode(br *bitio.Reader, si flac.StreamInfo, dst *Frame) error {
	var c16 uint16
	br.SetTap(func(b byte) { c16 = crc.Update16(c16, b) })
	defer br.SetTap(nil)

	var hdr header
	if err := readHeaderKeepingTap(br, si, &hdr, &c16); err != nil {
		return err
	}

	nch := hdr.channels()
	ensureChannels(dst, nch, hdr.blockSize)

	if hdr.channelAssignment <= 7 {
		for ch := range nch {
			if err := decodeSubframe(br, dst.Channels[ch][:hdr.blockSize], hdr.bitsPerSample); err != nil {
				return err
			}
		}
	} else if err := decodeStereoDecorrelated(br, &hdr, dst); err != nil {
		return err
	}

	if err := br.SkipToByteBoundary(); err != nil {
		return err
	}
	computed := c16
	stored, err := br.ReadBits(16)
	if err != nil {
		return err
	}
	if stored != uint64(computed) {
		return fmt.Errorf("frame: CRC-16 %#x != %#x: %w", stored, computed, flac.ErrCRCMismatch)
	}

	dst.BlockSize = hdr.blockSize
	dst.SampleRate = hdr.sampleRate
	dst.BitsPerSample = hdr.bitsPerSample
	dst.Number = hdr.number
	return nil
}

func ensureChannels(dst *Frame, nch, blockSize int) {
	if cap(dst.Channels) < nch {
		dst.Channels = make([][]int32, nch)
	}
	dst.Channels = dst.Channels[:nch]
	for ch := range dst.Channels {
		if cap(dst.Channels[ch]) < blockSize {
			dst.Channels[ch] = make([]int32, blockSize)
		}
		dst.Channels[ch] = dst.Channels[ch][:blockSize]
	}
}

func decodeStereoDecorrelated(br *bitio.Reader, hdr *header, dst *Frame) error {
	bs := hdr.blockSize
	bps := hdr.bitsPerSample
	out0, out1 := dst.Channels[0][:bs], dst.Channels[1][:bs]

	if bps == 32 {
		a := make([]int64, bs)
		b := make([]int64, bs)
		switch hdr.channelAssignment {
		case 8: // left/side
			if err := decodeSubframe64(br, a, bps); err != nil {
				return err
			}
			if err := decodeSubframe64(br, b, bps+1); err != nil {
				return err
			}
			decorrelateLeftSide64(a, b, out0, out1)
		case 9: // right/side
			if err := decodeSubframe64(br, a, bps+1); err != nil {
				return err
			}
			if err := decodeSubframe64(br, b, bps); err != nil {
				return err
			}
			decorrelateRightSide64(a, b, out0, out1)
		case 10: // mid/side
			if err := decodeSubframe64(br, a, bps); err != nil {
				return err
			}
			if err := decodeSubframe64(br, b, bps+1); err != nil {
				return err
			}
			decorrelateMidSide64(a, b, out0, out1)
		}
		return nil
	}

	a := make([]int32, bs)
	b := make([]int32, bs)
	switch hdr.channelAssignment {
	case 8:
		if err := decodeSubframe(br, a, bps); err != nil {
			return err
		}
		if err := decodeSubframe(br, b, bps+1); err != nil {
			return err
		}
		decorrelateLeftSide(a, b, out0, out1)
	case 9:
		if err := decodeSubframe(br, a, bps+1); err != nil {
			return err
		}
		if err := decodeSubframe(br, b, bps); err != nil {
			return err
		}
		decorrelateRightSide(a, b, out0, out1)
	case 10:
		if err := decodeSubframe(br, a, bps); err != nil {
			return err
		}
		if err := decodeSubframe(br, b, bps+1); err != nil {
			return err
		}
		decorrelateMidSide(a, b, out0, out1)
	}
	return nil
}
