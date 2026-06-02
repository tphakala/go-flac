# go-flac

Native, pure-Go FLAC encoder and decoder. No CGO, no external binaries. SIMD
accelerated via [github.com/tphakala/simd](https://github.com/tphakala/simd)
behind a pure-Go fallback, with a simple high-level PCM streaming API.

## Status

The decoder is implemented. `pcm.NewDecoder` reads real FLAC streams and exposes
the audio as interleaved little-endian PCM through `io.Reader` and `io.WriterTo`.
It is validated bit-exactly against the IETF FLAC test corpus (every `subset`
file's decoded-audio MD5 matches its STREAMINFO signature), byte-for-byte against
the reference libFLAC `flac -d`, and fuzzed for panic-freedom. The encoder is
still a skeleton: `pcm.NewEncoder` validates its input and returns
`ErrNotImplemented` until M3.

- M1 Groundwork: skeleton, tooling, CI. (done)
- M2 Decoder: bitstream, metadata, frame decode, Rice + predictor restore,
  inter-channel decorrelation, public `pcm.Decoder`, MD5 + corpus validation. (done)
- M3 Encoder (next): subframe analysis, decorrelation, Rice search, frame writer,
  public `pcm.Encoder`; bit-exact round-trip.
- M4 Streaming hardening: sample-accurate seek, resync, zero-copy drain.
- M5 SIMD integration.
- M6 completeness and v0.1.0.

Forward-only decoding lands in M2; `SeekToSample` returns `ErrSeekUnsupported`
for non-seekable sources, and mid-stream resync (streams that do not start at the
`fLaC` marker) is deferred to M4.

## API

```go
import "github.com/tphakala/go-flac/pcm"

// Decoder: implemented now.
dec, err := pcm.NewDecoder(r)
// dec implements io.Reader and io.WriterTo, yielding interleaved little-endian
// PCM. dec.Info() returns the stream's STREAMINFO properties. SeekToSample
// returns ErrSeekUnsupported until M4.

// Encoder: planned for M3.
enc, err := pcm.NewEncoder(w, pcm.Config{SampleRate: 44100, BitDepth: 16, Channels: 2})
// enc will implement io.WriteCloser; write interleaved little-endian PCM.
```

## License

MIT. See [LICENSE](LICENSE) and [THIRD_PARTY.md](THIRD_PARTY.md). go-flac is an
independent reimplementation; no third-party source is copied.
