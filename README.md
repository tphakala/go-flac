# go-flac

[![CI](https://github.com/tphakala/go-flac/actions/workflows/ci.yml/badge.svg)](https://github.com/tphakala/go-flac/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tphakala/go-flac.svg)](https://pkg.go.dev/github.com/tphakala/go-flac)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Native Go FLAC encoder and decoder. No CGO and no external binaries, with a
simple high-level PCM streaming API. The encoder hot paths are SIMD-accelerated
(via [github.com/tphakala/simd](https://github.com/tphakala/simd)) with a pure-Go
fallback, so the library still builds and runs on every Go target; the SIMD
kernels are bit-identical to the scalar path, so encoded output is byte-for-byte
the same with or without SIMD. SIMD adds one direct module dependency,
github.com/tphakala/simd, plus its transitive golang.org/x/sys (CPU feature
detection).

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

`pcm.NewEncoder` encodes interleaved little-endian PCM to FLAC. It supports bit
depths 4-32, constant/verbatim/fixed and LPC predictors, full four-way stereo
decorrelation (independent, left-side, right-side, mid-side), and the 0-8
compression-level API. Levels 0-2 use fixed predictors; levels 3-8 add quantized
LPC with deeper residual-partition search.
Depths up to 24 bps run an int32 path; depths 25-32 bps run a dedicated int64
path (encoder and decoder), and the int32 output for <= 24 bps is byte-identical
to before the wide-depth work. The encoder is allocation-light: a per-encoder
reusable scratch buffer keeps steady-state per-block heap allocations near zero.

`SeekToSample` is sample-accurate and requires an io.Seeker; it returns
`ErrSeekUnsupported` when the source is not seekable and `ErrInvalidSeek` on a
negative sample index. Mid-stream resync from a non-fLaC start position remains
future work.

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
package main

import (
	"io"
	"log"
	"os"

	"github.com/tphakala/go-flac/pcm"
)

func main() {
	// pcmReader is any io.Reader of interleaved little-endian PCM matching the
	// Config below; here it is a raw PCM file.
	pcmReader, err := os.Open("input.pcm")
	if err != nil {
		log.Fatal(err)
	}
	defer pcmReader.Close()

	// out is an *os.File (an io.WriteSeeker) so Close can finalize the STREAMINFO
	// MD5, total-sample count, and frame sizes.
	out, err := os.Create("output.flac")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

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
}
```

Pass an `io.WriteSeeker` (for example an `*os.File`) as the destination so the
encoder can finalize the STREAMINFO MD5, total-sample count, and frame sizes when
`Close` is called. A non-seekable writer still produces a valid FLAC stream, but
those fields are left as "unknown".

API notes:

```go
import "github.com/tphakala/go-flac/pcm"

// Decoder: implemented.
dec, err := pcm.NewDecoder(r)
// dec implements io.Reader and io.WriterTo, yielding interleaved little-endian
// PCM. dec.Info() returns the stream's STREAMINFO properties.
// SeekToSample requires an io.Seeker: it returns ErrSeekUnsupported for
// non-seekable sources and ErrInvalidSeek on a negative index.

// Encoder: implemented.
enc, err := pcm.NewEncoder(w, pcm.Config{SampleRate: 44100, BitDepth: 16, Channels: 2})
// enc implements io.WriteCloser; write interleaved little-endian PCM and call
// Close to flush the final frame and finalize STREAMINFO. Pass an io.WriteSeeker
// so STREAMINFO totals and MD5 can be written back after encoding.
```

## Command-line example

`cmd/wav2flac` encodes a PCM WAV file to FLAC with the streaming encoder. It is a
runnable demo of the API and the go-flac side of the benchmark harness below:

```bash
go run ./cmd/wav2flac -level 5 input.wav output.flac
```

It accepts integer PCM WAV input (for example `pcm_s16le`, `s24`, `s32`);
IEEE-float WAV is rejected.

## Benchmarking

`scripts/bench-encoders.sh` compares go-flac against libFLAC, SoX, and ffmpeg on
the same input (encode, level 5, single-threaded), reporting wall time, CPU
seconds, peak RSS, throughput, and compression ratio. With no argument it
generates a deterministic 30-minute input so runs are reproducible across
machines:

```bash
scripts/bench-encoders.sh          # generated reproducible input
scripts/bench-encoders.sh my.wav   # your own WAV
```

It requires GNU `time`; `flac`, `sox`, and `ffmpeg` are each optional and skipped
if absent.

Results on the default input (deterministic 30-minute mono 48 kHz/16-bit mix,
level 5, single-threaded); throughput is input MB/s, ratio is encoded size over
input size:

| Encoder | amd64 (i7-1260P) | arm64 (Cortex-A76) | Compression ratio |
| ------- | ---------------: | -----------------: | :---------------: |
| go-flac |        147 MB/s  |          51 MB/s   |      0.8523       |
| libFLAC |        229 MB/s  |          76 MB/s   |      0.8523       |
| SoX     |        194 MB/s  |          n/a       |      0.8519       |
| ffmpeg  |        128 MB/s  |          48 MB/s   |      0.8519       |

go-flac matches libFLAC's compression ratio exactly and beats ffmpeg on both
throughput and ratio on each architecture; libFLAC's reference encoder remains
about 1.5x faster. Numbers vary with hardware and input, so re-run the script to
reproduce them on your own machine.

## License

MIT. See [LICENSE](LICENSE) and [THIRD_PARTY.md](THIRD_PARTY.md). go-flac is an
independent reimplementation; no third-party source is copied.
