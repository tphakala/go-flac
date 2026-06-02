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

// WriteStreamHeader writes the "fLaC" marker followed by a STREAMINFO metadata
// block (last-block flag set) carrying body.
func WriteStreamHeader(w io.Writer, body []byte) error {
	hdr := make([]byte, 0, StreamInfoBodyOffset+len(body))
	// "fLaC" marker, then the 4-byte metadata block header: last-block flag (1) |
	// 7-bit block type, then a 24-bit big-endian body length.
	hdr = append(hdr, 'f', 'L', 'a', 'C',
		0x80|byte(typeStreamInfo),
		byte(len(body)>>16),
		byte(len(body)>>8),
		byte(len(body)),
	)
	hdr = append(hdr, body...)
	_, err := w.Write(hdr)
	return err
}
