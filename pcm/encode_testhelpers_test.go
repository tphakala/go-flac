package pcm

import (
	"errors"
	"io"
)

// seekBuffer is an in-memory io.WriteSeeker, used so the encoder exercises its
// STREAMINFO seek-back patch path without touching the filesystem.
type seekBuffer struct {
	data []byte
	pos  int64
}

var _ io.WriteSeeker = (*seekBuffer)(nil)

func (s *seekBuffer) Write(p []byte) (int, error) {
	end := s.pos + int64(len(p))
	if end > int64(len(s.data)) {
		s.data = append(s.data, make([]byte, end-int64(len(s.data)))...)
	}
	copy(s.data[s.pos:end], p)
	s.pos = end
	return len(p), nil
}

func (s *seekBuffer) Seek(off int64, whence int) (int64, error) {
	var n int64
	switch whence {
	case io.SeekStart:
		n = off
	case io.SeekCurrent:
		n = s.pos + off
	case io.SeekEnd:
		n = int64(len(s.data)) + off
	default:
		return 0, errors.New("seekBuffer: bad whence")
	}
	if n < 0 {
		return 0, errors.New("seekBuffer: negative position")
	}
	s.pos = n
	return n, nil
}

func (s *seekBuffer) Bytes() []byte { return s.data }
