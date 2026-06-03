package meta

import (
	"encoding/binary"

	flac "github.com/tphakala/go-flac"
)

// PlaceholderSampleNumber marks an unused SEEKTABLE point (FLAC spec).
const PlaceholderSampleNumber uint64 = 0xFFFFFFFFFFFFFFFF

// SeekPoint is one parsed SEEKTABLE entry. ByteOffset is relative to the first
// audio frame. Placeholder points are dropped during parsing, so callers never see
// PlaceholderSampleNumber here.
type SeekPoint struct {
	SampleNumber uint64
	ByteOffset   uint64
	FrameSamples uint16
}

// StreamMeta is everything ReadMetadata recovers from the metadata section.
type StreamMeta struct {
	Info       flac.StreamInfo
	MinBlock   int // STREAMINFO min block size
	MaxBlock   int // STREAMINFO max block size; nominal block size when MinBlock == MaxBlock
	MaxFrame   int // STREAMINFO max frame size in bytes (0 if unknown), for seek-probe sizing
	SeekPoints []SeekPoint
}

// SeekPointBytes is the on-disk size of one SEEKTABLE point.
const SeekPointBytes = 18

// EncodeBlockHeader returns the 4-byte metadata block header: 1-bit last-block flag,
// 7-bit block type, 24-bit big-endian body length.
func EncodeBlockHeader(last bool, btype, length int) []byte {
	b0 := byte(btype & 0x7F)
	if last {
		b0 |= 0x80
	}
	return []byte{b0, byte(length >> 16), byte(length >> 8), byte(length)}
}

// EncodeSeekPoints serializes points to a SEEKTABLE body (18 bytes each, big-endian).
func EncodeSeekPoints(points []SeekPoint) []byte {
	out := make([]byte, len(points)*SeekPointBytes)
	for i, p := range points {
		off := i * SeekPointBytes
		binary.BigEndian.PutUint64(out[off:off+8], p.SampleNumber)
		binary.BigEndian.PutUint64(out[off+8:off+16], p.ByteOffset)
		binary.BigEndian.PutUint16(out[off+16:off+18], p.FrameSamples)
	}
	return out
}

// parseSeekTable parses a SEEKTABLE body (a multiple of 18 bytes), dropping placeholder
// points. A length not divisible by 18 is malformed.
func parseSeekTable(body []byte) ([]SeekPoint, error) {
	if len(body)%SeekPointBytes != 0 {
		return nil, flac.ErrUnsupported
	}
	out := make([]SeekPoint, 0, len(body)/SeekPointBytes)
	for i := 0; i < len(body); i += SeekPointBytes {
		sn := binary.BigEndian.Uint64(body[i : i+8])
		if sn == PlaceholderSampleNumber {
			continue
		}
		out = append(out, SeekPoint{
			SampleNumber: sn,
			ByteOffset:   binary.BigEndian.Uint64(body[i+8 : i+16]),
			FrameSamples: binary.BigEndian.Uint16(body[i+16 : i+18]),
		})
	}
	return out, nil
}
