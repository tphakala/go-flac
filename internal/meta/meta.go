package meta

import (
	"errors"
	"fmt"
	"io"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// Metadata block types (FLAC spec).
const (
	typeStreamInfo = 0
	// 1 PADDING, 2 APPLICATION, 3 SEEKTABLE, 4 VORBIS_COMMENT, 5 CUESHEET,
	// 6 PICTURE are recognized and skipped by length; 127 is invalid.
	typeInvalid = 127
)

// ReadMetadata reads the stream marker (skipping a leading ID3v2 tag if present)
// and all metadata blocks, returning the STREAMINFO-derived properties. It leaves
// the reader positioned at the first audio frame.
func ReadMetadata(br *bitio.Reader) (StreamMeta, error) {
	sm, err := readMetadata(br)
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF // metadata precedes audio; any EOF here is truncation
	}
	return sm, err
}

func readMetadata(br *bitio.Reader) (StreamMeta, error) {
	if err := skipID3v2AndMagic(br); err != nil {
		return StreamMeta{}, err
	}
	var sm StreamMeta
	haveStreamInfo := false
	first := true
	for {
		last, btype, length, err := readBlockHeader(br)
		if err != nil {
			return StreamMeta{}, err
		}
		if btype == typeInvalid {
			return StreamMeta{}, fmt.Errorf("meta: invalid block type 127: %w", flac.ErrUnsupported)
		}
		if first && btype != typeStreamInfo {
			return StreamMeta{}, flac.ErrMissingStreamInfo
		}
		switch btype {
		case typeStreamInfo:
			if !first {
				return StreamMeta{}, flac.ErrMissingStreamInfo
			}
			if length != 34 {
				return StreamMeta{}, fmt.Errorf("meta: STREAMINFO length %d != 34: %w", length, flac.ErrUnsupported)
			}
			si, mnb, mxb, mxf, err := readStreamInfo(br)
			if err != nil {
				return StreamMeta{}, err
			}
			sm.Info, sm.MinBlock, sm.MaxBlock, sm.MaxFrame = si, mnb, mxb, mxf
			haveStreamInfo = true
		default:
			if err := skipBytes(br, int(length)); err != nil {
				return StreamMeta{}, err
			}
		}
		first = false
		if last {
			break
		}
	}
	if !haveStreamInfo {
		return StreamMeta{}, flac.ErrMissingStreamInfo
	}
	return sm, nil
}

// readBlockHeader reads the 4-byte metadata block header.
func readBlockHeader(br *bitio.Reader) (last bool, btype, length uint64, err error) {
	lb, err := br.ReadBits(1)
	if err != nil {
		return false, 0, 0, err
	}
	btype, err = br.ReadBits(7)
	if err != nil {
		return false, 0, 0, err
	}
	length, err = br.ReadBits(24)
	if err != nil {
		return false, 0, 0, err
	}
	return lb == 1, btype, length, nil
}

func skipBytes(br *bitio.Reader, n int) error {
	for range n {
		if _, err := br.ReadBits(8); err != nil {
			return err
		}
	}
	return nil
}

// skipID3v2AndMagic consumes an optional leading ID3v2 tag, then requires "fLaC".
func skipID3v2AndMagic(br *bitio.Reader) error {
	first4, err := readBytes(br, 4)
	if err != nil {
		return err
	}
	if string(first4[:3]) == "ID3" {
		// Already read 4 bytes ("ID3" + version major). Read version minor + flags +
		// 4 syncsafe size bytes, then skip the body.
		rest, err := readBytes(br, 6)
		if err != nil {
			return err
		}
		size := int(rest[2]&0x7F)<<21 | int(rest[3]&0x7F)<<14 | int(rest[4]&0x7F)<<7 | int(rest[5]&0x7F)
		// The flags byte (rest[1]) bit 4 marks a 10-byte footer not counted in size.
		if rest[1]&0x10 != 0 {
			size += 10
		}
		if err := skipBytes(br, size); err != nil {
			return err
		}
		first4, err = readBytes(br, 4)
		if err != nil {
			return err
		}
	}
	if string(first4) != "fLaC" {
		return fmt.Errorf("meta: bad stream marker %q: %w", first4, flac.ErrUnsupported)
	}
	return nil
}

func readBytes(br *bitio.Reader, n int) ([]byte, error) {
	out := make([]byte, n)
	for i := range out {
		b, err := br.ReadBits(8)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		out[i] = byte(b)
	}
	return out, nil
}
