// Package pcm is the high-level, clean-room PCM streaming API for go-flac.
//
// It bridges raw interleaved little-endian PCM and the FLAC codec core,
// exposing an io.WriteCloser encoder and an io.Reader / io.WriterTo decoder.
//
// # Decoding
//
// NewDecoder returns a Decoder that reads interleaved little-endian PCM via
// io.Reader and io.WriterTo. SeekToSample provides sample-accurate random
// access when the source implements io.Seeker. A SEEKTABLE block in the stream
// accelerates seeks by narrowing the binary search; plain binary search is the
// fallback when no table is present.
//
// # Encoding
//
// NewEncoder accepts an io.Writer (or io.WriteSeeker) and a Config. Provide an
// io.WriteSeeker (for example an *os.File) so Close can write back the
// finalized STREAMINFO totals and MD5; a plain io.Writer still produces a valid
// stream, but those fields are left unknown.
//
// Opt-in SEEKTABLE emission: set Config.SeekTableInterval to a positive sample
// count to insert a SEEKTABLE. An interval equal to the sample rate produces
// roughly one seek point per second, for example:
//
//	enc, err := pcm.NewEncoder(f, pcm.Config{
//	    SampleRate:        44100,
//	    Channels:          2,
//	    BitDepth:          16,
//	    SeekTableInterval: 44100, // one seek point per second
//	})
//
// SEEKTABLE emission requires an io.WriteSeeker sink (Close patches the
// reserved block in place). Config.SeekTableMaxPoints caps the number of
// reserved points (default 4096 when unset and an interval is given).
//
// # Reuse and one-shot encoding
//
// Encoder.Reset rebinds an existing encoder to a new sink and Config, reusing its
// large internal buffers (the frame workspace and per-channel block buffers) when
// the channel count and LPC order are unchanged. A producer that encodes many
// independent streams can therefore pool encoders and skip the per-stream
// workspace allocation:
//
//	var pool = sync.Pool{New: func() any { e, _ := pcm.NewEncoder(io.Discard, cfg); return e }}
//
//	enc := pool.Get().(*pcm.Encoder)
//	defer pool.Put(enc)
//	if err := enc.Reset(f, cfg); err != nil { /* ... */ }
//	// write PCM, then Close.
//
// EncodeInterleaved is a one-shot helper for the common case of already holding a
// complete interleaved little-endian PCM buffer in memory. It runs the
// NewEncoder/Write/Close sequence (drawing from an internal encoder pool) and
// returns the first error, so callers do not reimplement the Close finalization:
//
//	err := pcm.EncodeInterleaved(f, cfg, pcmBytes)
package pcm
