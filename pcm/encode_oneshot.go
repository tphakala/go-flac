package pcm

import (
	"crypto/md5"
	"fmt"
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
// FLAC stream on w in a single call. Because it receives the whole buffer, it
// finalizes STREAMINFO total_samples and MD5 for any io.Writer, seekable or not:
// total_samples is derived from len(pcm) and MD5 is hashed up front, so an
// in-memory sink (bytes.Buffer, io.Pipe) gets the same duration and MD5 as a
// file-based encode with no io.WriteSeeker and no temp-file round trip. The
// min/max block and frame sizes still require a seekable sink (they are not
// knowable until the frames are encoded); on a non-seekable sink they keep their
// spec-legal "unknown" sentinels. It centralizes the NewEncoder/Write/Close
// finalization sequence so callers that already hold a full PCM buffer do not have
// to reimplement it (and cannot get the Close patch-back step subtly wrong).
//
// pcm must hold a whole number of inter-channel samples for cfg; a trailing
// partial sample yields an error before any sink write. Any Config.TotalSamples
// set by the caller is ignored: the buffer length is the authoritative sample
// count. The encoder is drawn from an internal pool and returned on completion, so
// repeated calls of a constant shape are allocation-light. EncodeInterleaved is
// safe for concurrent use.
func EncodeInterleaved(w io.Writer, cfg Config, pcm []byte) error {
	// Derive total_samples and the STREAMINFO MD5 from the whole buffer so a
	// non-seekable sink still gets a finalized header. Guard against an invalid
	// shape (bad Channels/BitDepth) so the division is safe; init below returns the
	// canonical config error when the shape is bad.
	var knownMD5 *[16]byte
	bytesPS := (cfg.BitDepth + 7) / 8
	if frameLen := bytesPS * cfg.Channels; frameLen > 0 {
		if len(pcm)%frameLen != 0 {
			return fmt.Errorf("go-flac/pcm: EncodeInterleaved: %d bytes is not a whole number of %d-byte samples", len(pcm), frameLen)
		}
		total := uint64(len(pcm) / frameLen)
		if total > maxTotalSamples {
			return fmt.Errorf("go-flac/pcm: EncodeInterleaved: %d samples exceeds the FLAC maximum of 2^36-1", total)
		}
		// The buffer is the authoritative source for both fields, so overwrite any
		// caller-set cfg.TotalSamples and hash the whole input once up front (the
		// encoder then skips its per-frame hashing). Doing both unconditionally, even
		// for an empty buffer, keeps a non-seekable encode byte-identical to the
		// seekable one, which patches the same total_samples and md5-of-the-input at
		// Close. cfg is a by-value copy, so this never touches the caller's struct.
		cfg.TotalSamples = total
		sum := md5.Sum(pcm)
		knownMD5 = &sum
	}

	enc := encoderPool.Get().(*Encoder)
	defer func() {
		// Drop the reference to the caller's sink so an idle pooled encoder does not
		// pin it (the reusable buffers are what we want to keep, not the writer), then
		// return the encoder to the pool. The next init rebinds w before any use.
		enc.w, enc.ws = nil, nil
		encoderPool.Put(enc)
	}()

	// Call init directly (rather than the public Reset) to thread the precomputed
	// MD5 through; op "EncodeInterleaved" also gives clearer error messages.
	if err := enc.init("EncodeInterleaved", w, cfg, knownMD5); err != nil {
		return err
	}
	if _, err := enc.Write(pcm); err != nil {
		return err
	}
	return enc.Close()
}
