# go-flac

Native, pure-Go FLAC encoder and decoder. No CGO, no external binaries. SIMD
accelerated via [github.com/tphakala/simd](https://github.com/tphakala/simd)
behind a pure-Go fallback, with a simple high-level PCM streaming API.

## Status

Early groundwork. This repository currently contains the module skeleton and
tooling only: the public API surface compiles, but the codec is not yet
implemented. Constructors return `ErrNotImplemented`. Follow the milestones
below.

- M1 Groundwork (current): skeleton, tooling, CI.
- M2 Decoder: bitstream, metadata, frame decode, Rice + predictor restore,
  public `pcm.Decoder`.
- M3 Encoder: subframe analysis, decorrelation, Rice search, frame writer,
  public `pcm.Encoder`; bit-exact round-trip.
- M4 Streaming hardening: sample-accurate seek, resync, zero-copy drain.
- M5 SIMD integration.
- M6 completeness and v0.1.0.

## API (planned)

```go
import "github.com/tphakala/go-flac/pcm"

enc, err := pcm.NewEncoder(w, pcm.Config{SampleRate: 44100, BitDepth: 16, Channels: 2})
// enc implements io.WriteCloser; write interleaved little-endian PCM.

dec, err := pcm.NewDecoder(r)
// dec implements io.Reader and io.WriterTo; optional sample-accurate Seek.
```

## License

MIT. See [LICENSE](LICENSE) and [THIRD_PARTY.md](THIRD_PARTY.md). go-flac is an
independent reimplementation; no third-party source is copied.
