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
package pcm
