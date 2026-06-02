package frame

import (
	"fmt"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
)

const syncCode = 0x3FFE // 14 bits

// readHeader reads and validates the frame header for standalone use (unit tests
// or any caller that does not also track the frame CRC-16). The header is byte
// aligned, so CRC-8 is computed over the consumed bytes via a tap.
func readHeader(br *bitio.Reader, si flac.StreamInfo, hdr *header) error {
	var c8 uint8
	br.SetTap(func(b byte) { c8 = crc.Update8(c8, b) })
	defer br.SetTap(nil)
	return readHeaderBody(br, si, hdr, &c8)
}

// readHeaderKeepingTap reads the header while also folding every consumed byte
// into the frame CRC-16 accumulator c16 (the frame CRC-16 covers the header too).
// The header's own CRC-8 is checked internally. The caller is responsible for
// clearing the tap when the frame is done.
func readHeaderKeepingTap(br *bitio.Reader, si flac.StreamInfo, hdr *header, c16 *uint16) error {
	var c8 uint8
	br.SetTap(func(b byte) {
		c8 = crc.Update8(c8, b)
		*c16 = crc.Update16(*c16, b)
	})
	return readHeaderBody(br, si, hdr, &c8)
}

// readHeaderBody reads the frame header fields and verifies the header CRC-8 using
// the running value pointed to by c8. The caller installs the tap that maintains
// c8 (and optionally the frame CRC-16) before calling.
func readHeaderBody(br *bitio.Reader, si flac.StreamInfo, hdr *header, c8 *uint8) error {
	sync, err := br.ReadBits(14)
	if err != nil {
		return err
	}
	if sync != syncCode {
		return fmt.Errorf("frame: bad sync %#x: %w", sync, flac.ErrUnsupported)
	}
	if r, err := br.ReadBits(1); err != nil {
		return err
	} else if r != 0 {
		return fmt.Errorf("frame: reserved sync bit set: %w", flac.ErrUnsupported)
	}
	bs, err := br.ReadBits(1)
	if err != nil {
		return err
	}
	hdr.variableBlockSize = bs == 1

	bsCode, err := br.ReadBits(4)
	if err != nil {
		return err
	}
	srCode, err := br.ReadBits(4)
	if err != nil {
		return err
	}
	chCode, err := br.ReadBits(4)
	if err != nil {
		return err
	}
	bpsCode, err := br.ReadBits(3)
	if err != nil {
		return err
	}
	if r, err := br.ReadBits(1); err != nil {
		return err
	} else if r != 0 {
		return fmt.Errorf("frame: reserved bit set: %w", flac.ErrUnsupported)
	}
	hdr.channelAssignment = int(chCode)
	if chCode >= 11 {
		return fmt.Errorf("frame: reserved channel assignment %d: %w", chCode, flac.ErrUnsupported)
	}

	hdr.number, err = readCodedNumber(br)
	if err != nil {
		return err
	}

	if err := decodeBlockSize(br, bsCode, hdr); err != nil {
		return err
	}
	if err := decodeSampleRate(br, srCode, si, hdr); err != nil {
		return err
	}
	if err := decodeBitsPerSample(bpsCode, si, hdr); err != nil {
		return err
	}

	computed := *c8
	stored, err := br.ReadBits(8)
	if err != nil {
		return err
	}
	if stored != uint64(computed) {
		return fmt.Errorf("frame: header CRC-8 %#x != %#x: %w", stored, computed, flac.ErrCRCMismatch)
	}
	return nil
}

func decodeBlockSize(br *bitio.Reader, code uint64, hdr *header) error {
	switch {
	case code == 0:
		return fmt.Errorf("frame: reserved blocksize code 0: %w", flac.ErrUnsupported)
	case code == 1:
		hdr.blockSize = 192
	case code >= 2 && code <= 5:
		hdr.blockSize = 576 << (code - 2)
	case code == 6:
		v, err := br.ReadBits(8)
		if err != nil {
			return err
		}
		hdr.blockSize = int(v) + 1
	case code == 7:
		v, err := br.ReadBits(16)
		if err != nil {
			return err
		}
		hdr.blockSize = int(v) + 1
	default: // 8..15
		hdr.blockSize = 256 << (code - 8)
	}
	return nil
}

var sampleRateTable = map[uint64]int{
	1: 88200, 2: 176400, 3: 192000, 4: 8000, 5: 16000, 6: 22050,
	7: 24000, 8: 32000, 9: 44100, 10: 48000, 11: 96000,
}

func decodeSampleRate(br *bitio.Reader, code uint64, si flac.StreamInfo, hdr *header) error {
	switch {
	case code == 0:
		hdr.sampleRate = si.SampleRate
	case code >= 1 && code <= 11:
		hdr.sampleRate = sampleRateTable[code]
	case code == 12:
		v, err := br.ReadBits(8)
		if err != nil {
			return err
		}
		hdr.sampleRate = int(v) * 1000
	case code == 13:
		v, err := br.ReadBits(16)
		if err != nil {
			return err
		}
		hdr.sampleRate = int(v)
	case code == 14:
		v, err := br.ReadBits(16)
		if err != nil {
			return err
		}
		hdr.sampleRate = int(v) * 10
	default: // 15
		return fmt.Errorf("frame: invalid sample rate code 15: %w", flac.ErrUnsupported)
	}
	return nil
}

func decodeBitsPerSample(code uint64, si flac.StreamInfo, hdr *header) error {
	switch code {
	case 0:
		hdr.bitsPerSample = si.BitDepth
	case 1:
		hdr.bitsPerSample = 8
	case 2:
		hdr.bitsPerSample = 12
	case 4:
		hdr.bitsPerSample = 16
	case 5:
		hdr.bitsPerSample = 20
	case 6:
		hdr.bitsPerSample = 24
	case 7:
		hdr.bitsPerSample = 32
	default: // 3 reserved
		return fmt.Errorf("frame: reserved bps code %d: %w", code, flac.ErrUnsupported)
	}
	return nil
}

// readCodedNumber decodes FLAC's extended UTF-8 coded number (up to 36 bits in
// up to 7 bytes). It is not standard UTF-8, so it is decoded by hand.
func readCodedNumber(br *bitio.Reader) (uint64, error) {
	b0, err := br.ReadBits(8)
	if err != nil {
		return 0, err
	}
	if b0 < 0x80 {
		return b0, nil
	}
	// Count leading ones in b0 to get the total byte length.
	var n int
	switch {
	case b0&0xE0 == 0xC0:
		n = 2
	case b0&0xF0 == 0xE0:
		n = 3
	case b0&0xF8 == 0xF0:
		n = 4
	case b0&0xFC == 0xF8:
		n = 5
	case b0&0xFE == 0xFC:
		n = 6
	case b0 == 0xFE:
		n = 7
	default:
		return 0, fmt.Errorf("frame: invalid coded number lead byte %#x: %w", b0, flac.ErrUnsupported)
	}
	val := b0 & uint64(0x7F>>uint(n))
	for range n - 1 {
		c, err := br.ReadBits(8)
		if err != nil {
			return 0, err
		}
		if c&0xC0 != 0x80 {
			return 0, fmt.Errorf("frame: invalid coded number continuation %#x: %w", c, flac.ErrUnsupported)
		}
		val = (val << 6) | (c & 0x3F)
	}
	return val, nil
}
