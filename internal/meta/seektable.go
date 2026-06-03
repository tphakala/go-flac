package meta

import flac "github.com/tphakala/go-flac"

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
