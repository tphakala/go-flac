package frame

import (
	"bytes"
	"errors"
	"io"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// FindResult reports the outcome of FindNextFrame.
type FindResult int

const (
	// FrameFound: a CRC-16-valid frame begins at the returned start offset.
	FrameFound FindResult = iota
	// FrameNotFound: no valid frame begins anywhere in the buffer.
	FrameNotFound
	// FrameTruncated: a sync candidate decoded cleanly until the buffer ran out; the
	// caller should supply more bytes from the same base and retry.
	FrameTruncated
)

// FindNextFrame scans data byte-aligned for the first offset where the strict Decode
// succeeds, decoding that frame into dst. A FLAC frame header is byte aligned and
// begins with 0xFF followed by a byte matching b&0xFE == 0xF8 (14-bit sync + reserved
// 0 bit). Each candidate is fully decoded so CRC-16 rejects a false sync inside
// residual data. It returns the start offset within data, the byte length of the
// decoded frame, and a result code.
func FindNextFrame(data []byte, si flac.StreamInfo, dst *Frame) (start, consumed int, res FindResult) {
	for i := 0; i+1 < len(data); i++ {
		if data[i] != 0xFF || data[i+1]&0xFE != 0xF8 {
			continue
		}
		r := bitio.NewReader(bytes.NewReader(data[i:]))
		err := Decode(r, si, dst)
		if err == nil {
			return i, int(r.BytesRead()), FrameFound
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// The candidate is the earliest unresolved one (all earlier candidates
			// failed with CRC/format errors and were skipped). It may be a real frame
			// needing more data.
			return i, 0, FrameTruncated
		}
		// CRC mismatch / bad format: a false positive. Keep scanning.
	}
	return 0, 0, FrameNotFound
}
