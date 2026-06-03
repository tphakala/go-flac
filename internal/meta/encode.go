package meta

import (
	"io"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

const (
	// StreamInfoBodyLen is the fixed STREAMINFO body size in bytes.
	StreamInfoBodyLen = 34
	// StreamInfoBodyOffset is the byte offset of the STREAMINFO body from the start
	// of the stream: 4-byte "fLaC" marker + 4-byte metadata block header.
	StreamInfoBodyOffset = 4 + 4
)

// EncodeStreamInfo builds the 34-byte STREAMINFO body. minFrame/maxFrame == 0 and
// totalSamples (from si) == 0 and an all-zero si.MD5 are the spec-legal "unknown"
// sentinels. Layout mirrors readStreamInfo.
func EncodeStreamInfo(si flac.StreamInfo, minBlock, maxBlock, minFrame, maxFrame int) []byte {
	bw := bitio.NewWriter()
	bw.WriteBits(uint64(minBlock), 16)
	bw.WriteBits(uint64(maxBlock), 16)
	bw.WriteBits(uint64(minFrame), 24)
	bw.WriteBits(uint64(maxFrame), 24)
	bw.WriteBits(uint64(si.SampleRate), 20)
	bw.WriteBits(uint64(si.Channels-1), 3)
	bw.WriteBits(uint64(si.BitDepth-1), 5)
	bw.WriteBits(si.TotalSamples, 36)
	for _, b := range si.MD5 {
		bw.WriteBits(uint64(b), 8)
	}
	return bw.Bytes()
}

// WriteStreamHeaderEx writes the "fLaC" marker and the STREAMINFO block header (with
// the given last-block flag) followed by body. WriteStreamHeader is the last=true case.
func WriteStreamHeaderEx(w io.Writer, body []byte, last bool) error {
	out := make([]byte, 0, 4+4+len(body))
	out = append(out, 'f', 'L', 'a', 'C')
	out = append(out, EncodeBlockHeader(last, typeStreamInfo, len(body))...)
	out = append(out, body...)
	_, err := w.Write(out)
	return err
}

// WriteStreamHeader writes the "fLaC" marker followed by a STREAMINFO metadata
// block (last-block flag set) carrying body.
func WriteStreamHeader(w io.Writer, body []byte) error {
	return WriteStreamHeaderEx(w, body, true)
}

// SeekTablePlaceholder returns a SEEKTABLE body of n placeholder points (all
// PlaceholderSampleNumber), 18*n bytes.
func SeekTablePlaceholder(n int) []byte {
	pts := make([]SeekPoint, n)
	for i := range pts {
		pts[i] = SeekPoint{SampleNumber: PlaceholderSampleNumber}
	}
	return EncodeSeekPoints(pts)
}
