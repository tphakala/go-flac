# go-flac

[![CI](https://github.com/tphakala/go-flac/actions/workflows/ci.yml/badge.svg)](https://github.com/tphakala/go-flac/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tphakala/go-flac.svg)](https://pkg.go.dev/github.com/tphakala/go-flac)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Native, pure-Go FLAC encoder and decoder. No CGO, no external binaries. SIMD
accelerated via [github.com/tphakala/simd](https://github.com/tphakala/simd)
behind a pure-Go fallback, with a simple high-level PCM streaming API.

## Install

```bash
go get github.com/tphakala/go-flac
```

## Status

Both decoder and encoder are implemented. `pcm.NewDecoder` reads real FLAC
streams and exposes the audio as interleaved little-endian PCM through `io.Reader`
and `io.WriterTo`. It is validated bit-exactly against the IETF FLAC test corpus
(every `subset` file's decoded-audio MD5 matches its STREAMINFO signature),
byte-for-byte against the reference libFLAC `flac -d`, and fuzzed for
panic-freedom.

`pcm.NewEncoder` (M3) encodes interleaved little-endian PCM to FLAC. It supports
bit depths 4-24, constant/verbatim/fixed predictors, full four-way stereo
decorrelation (independent, left-side, right-side, mid-side), and the 0-8
compression-level API. LPC predictors and the additional compression that
levels 3-8 promise are deferred to M3b; those levels currently compress about
like level 2 and will improve automatically when LPC lands.

- M1 Groundwork: skeleton, tooling, CI. (done)
- M2 Decoder: bitstream, metadata, frame decode, Rice + predictor restore,
  inter-channel decorrelation, public `pcm.Decoder`, MD5 + corpus validation. (done)
- M3 Encoder: constant/verbatim/fixed predictors, four-way stereo decorrelation,
  0-8 compression-level API, bit-exact round-trip, libFLAC cross-validation. (done)
- M3b LPC: linear-predictive coding, bringing levels 3-8 to their full potential.
- M4 Streaming hardening: sample-accurate seek, resync, zero-copy drain.
- M5 SIMD integration.
- M6 completeness and v0.1.0.

Forward-only decoding lands in M2; `SeekToSample` returns `ErrSeekUnsupported`
for non-seekable sources, and mid-stream resync (streams that do not start at the
`fLaC` marker) is deferred to M4.

## Usage

Decode a FLAC file to raw interleaved little-endian PCM:

```go
package main

import (
	"io"
	"log"
	"os"

	"github.com/tphakala/go-flac/pcm"
)

func main() {
	f, err := os.Open("input.flac")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	dec, err := pcm.NewDecoder(f)
	if err != nil {
		log.Fatal(err)
	}

	info := dec.Info()
	log.Printf("%d Hz, %d channel(s), %d-bit", info.SampleRate, info.Channels, info.BitDepth)

	// dec is an io.Reader and io.WriterTo of interleaved little-endian PCM.
	out, err := os.Create("output.pcm")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, dec); err != nil {
		log.Fatal(err)
	}
}
```

Encode interleaved little-endian PCM to a FLAC file:

```go
enc, err := pcm.NewEncoder(out, pcm.Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5})
if err != nil {
	log.Fatal(err)
}
if _, err := io.Copy(enc, pcmReader); err != nil {
	log.Fatal(err)
}
if err := enc.Close(); err != nil {
	log.Fatal(err)
}
```

`out` should be an `io.WriteSeeker` (for example an `*os.File`) so the encoder
can finalize the STREAMINFO MD5, total-sample count, and frame sizes when
`Close` is called. A non-seekable writer still produces a valid FLAC stream, but
those fields are left as "unknown".

API notes:

```go
import "github.com/tphakala/go-flac/pcm"

// Decoder: implemented.
dec, err := pcm.NewDecoder(r)
// dec implements io.Reader and io.WriterTo, yielding interleaved little-endian
// PCM. dec.Info() returns the stream's STREAMINFO properties. Until M4,
// SeekToSample returns ErrSeekUnsupported for non-seekable sources and
// ErrNotImplemented otherwise.

// Encoder: implemented (M3).
enc, err := pcm.NewEncoder(w, pcm.Config{SampleRate: 44100, BitDepth: 16, Channels: 2})
// enc implements io.WriteCloser; write interleaved little-endian PCM and call
// Close to flush the final frame and finalize STREAMINFO. Pass an io.WriteSeeker
// so STREAMINFO totals and MD5 can be written back after encoding.
```

## License

MIT. See [LICENSE](LICENSE) and [THIRD_PARTY.md](THIRD_PARTY.md). go-flac is an
independent reimplementation; no third-party source is copied.
