package pcm

import (
	"io"
	"sync"
)

// encoderPool recycles Encoders for EncodeInterleaved so back-to-back one-shot
// encodes of the same shape reuse the large internal buffers (the frame workspace
// and per-channel block buffers) instead of re-allocating them. Every Get is
// paired with a Reset before any encoding, so a recycled encoder never carries
// state from a prior call.
var encoderPool = sync.Pool{New: func() any { return new(Encoder) }}

// EncodeInterleaved encodes a complete interleaved little-endian PCM buffer to a
// FLAC stream on w in a single call. Passing an io.WriteSeeker (for example an
// *os.File) lets it finalize the STREAMINFO totals and MD5; a plain io.Writer
// still produces a valid stream with those fields left at their spec-legal unknown
// sentinels. It centralizes the NewEncoder/Write/Close finalization sequence so
// callers that already hold a full PCM buffer in memory do not have to reimplement
// it (and cannot get the Close patch-back step subtly wrong).
//
// pcm must hold a whole number of inter-channel samples for cfg; a trailing
// partial sample yields an error from the final flush. The encoder is drawn from
// an internal pool and returned on completion, so repeated calls of a constant
// shape are allocation-light. EncodeInterleaved is safe for concurrent use.
func EncodeInterleaved(w io.Writer, cfg Config, pcm []byte) error {
	enc := encoderPool.Get().(*Encoder)
	defer func() {
		// Drop the reference to the caller's sink so an idle pooled encoder does not
		// pin it (the reusable buffers are what we want to keep, not the writer), then
		// return the encoder to the pool. The next Get/Reset rebinds w before any use.
		enc.w, enc.ws = nil, nil
		encoderPool.Put(enc)
	}()

	if err := enc.Reset(w, cfg); err != nil {
		return err
	}
	if _, err := enc.Write(pcm); err != nil {
		return err
	}
	return enc.Close()
}
