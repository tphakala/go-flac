package meta

import (
	"encoding/binary"
	"fmt"

	flac "github.com/tphakala/go-flac"
)

// TypeVorbisComment is the VORBIS_COMMENT metadata block type. It carries the
// stream's textual tags (a vendor string plus zero or more "NAME=value" fields).
const TypeVorbisComment = 4

// MaxMetadataBodyLen is the largest body a metadata block can carry: the block
// header's length field is 24 bits. A VORBIS_COMMENT body larger than this cannot
// be framed and must be rejected before it corrupts the header length.
const MaxMetadataBodyLen = 1<<24 - 1

// EncodeVorbisComment builds a FLAC VORBIS_COMMENT block body from a vendor string
// and a list of "NAME=value" comment strings. The layout follows the Vorbis comment
// spec as FLAC embeds it: a 32-bit little-endian vendor length, the vendor bytes, a
// 32-bit little-endian comment count, then each comment as a 32-bit little-endian
// length followed by its UTF-8 bytes. Unlike Ogg Vorbis, FLAC omits the trailing
// framing bit. These length fields are the sole little-endian quantities in FLAC's
// otherwise big-endian metadata.
func EncodeVorbisComment(vendor string, comments []string) []byte {
	size := 4 + len(vendor) + 4
	for _, c := range comments {
		size += 4 + len(c)
	}
	out := make([]byte, size)
	off := 0
	binary.LittleEndian.PutUint32(out[off:], uint32(len(vendor)))
	off += 4
	off += copy(out[off:], vendor)
	binary.LittleEndian.PutUint32(out[off:], uint32(len(comments)))
	off += 4
	for _, c := range comments {
		binary.LittleEndian.PutUint32(out[off:], uint32(len(c)))
		off += 4
		off += copy(out[off:], c)
	}
	return out
}

// parseVorbisComment parses a FLAC VORBIS_COMMENT block body into its vendor string
// and "NAME=value" comment strings. Every length field is validated against the
// bytes remaining in the body, so a truncated or malformed block is rejected with
// flac.ErrUnsupported rather than driving an out-of-range slice or an oversized
// allocation. Trailing bytes after the last comment are ignored (some encoders pad).
func parseVorbisComment(body []byte) (vendor string, comments []string, err error) {
	r := body
	takeU32 := func() (uint32, bool) {
		if len(r) < 4 {
			return 0, false
		}
		v := binary.LittleEndian.Uint32(r)
		r = r[4:]
		return v, true
	}
	take := func(n uint32) ([]byte, bool) {
		if uint64(n) > uint64(len(r)) {
			return nil, false
		}
		b := r[:n]
		r = r[n:]
		return b, true
	}

	vlen, ok := takeU32()
	if !ok {
		return "", nil, fmt.Errorf("meta: VORBIS_COMMENT truncated vendor length: %w", flac.ErrUnsupported)
	}
	vb, ok := take(vlen)
	if !ok {
		return "", nil, fmt.Errorf("meta: VORBIS_COMMENT truncated vendor string: %w", flac.ErrUnsupported)
	}
	vendor = string(vb)

	count, ok := takeU32()
	if !ok {
		return "", nil, fmt.Errorf("meta: VORBIS_COMMENT truncated comment count: %w", flac.ErrUnsupported)
	}
	// Each comment needs at least its own 4-byte length field, so a count larger than
	// len(r)/4 cannot be satisfied by the remaining body. Bounding the count here caps
	// the make below against a crafted block declaring billions of comments.
	if uint64(count) > uint64(len(r)/4) {
		return "", nil, fmt.Errorf("meta: VORBIS_COMMENT comment count %d exceeds body length: %w", count, flac.ErrUnsupported)
	}
	// Pre-size to a small cap rather than the untrusted count. The guard above bounds
	// count to len(r)/4, but that still lets a crafted block near the 24-bit block-size
	// limit reserve millions of empty string headers (a ~4x blow-up over the on-disk
	// bytes); append grows the slice from here as real comments are read.
	comments = make([]string, 0, min(count, 1024))
	for i := range count {
		clen, ok := takeU32()
		if !ok {
			return "", nil, fmt.Errorf("meta: VORBIS_COMMENT truncated comment length: %w", flac.ErrUnsupported)
		}
		cb, ok := take(clen)
		if !ok {
			return "", nil, fmt.Errorf("meta: VORBIS_COMMENT truncated comment %d: %w", i, flac.ErrUnsupported)
		}
		comments = append(comments, string(cb))
	}
	return vendor, comments, nil
}
