# Third-party provenance

go-flac is an independent, clean-room reimplementation of the FLAC codec. The
codec logic here is original work licensed under the MIT License (see LICENSE).
The one block of code with external provenance is the int32 SIMD kernel package
under internal/i32, vendored from the author's own MIT-licensed
github.com/tphakala/simd project; see the SIMD section below.

The following projects were used only as algorithm references or design
inspiration, as noted. They are credited here in gratitude; none of their code
is present in this repository.

## Algorithm references (public domain)

- mewkiz/flac (Unlicense): the canonical pure-Go FLAC decoder lineage. Studied
  for bitstream structure, metadata parsing, and frame/subframe decoding.
- mycophonic/flac (Unlicense): a performance fork of mewkiz/flac that adds an
  encoder. Studied for encoder structure (fixed-predictor analysis, Rice
  parameter search, decorrelation). Also used as a test-only differential
  oracle during development; never imported by shipped code.

These projects are public domain (Unlicense), so using them as references
imposes no obligation. go-flac is nonetheless an independent reimplementation,
not a copy.

## Design inspiration only (third-party, Apache-2.0 - source not consulted)

- mycophonic/saprobe-flac, mycophonic/saprobe-alac (Apache-2.0): third-party
  PCM-streaming codec wrappers that motivated the high-level streaming API. To
  keep this MIT codebase free of any Apache obligation, their source was NOT
  read or consulted for structure. The PCM layer here is designed from the
  FLAC specification and the public-domain references only.

## Test corpus

- ietf-wg-cellar/flac-test-files: the IETF FLAC conformance corpus, included as
  a git submodule under testdata/flac-test-files for testing only.

## SIMD

- github.com/tphakala/simd (MIT, same author): the int32 SIMD kernels that back
  the encoder's hot paths (Rice partition cost search, fixed-predictor residuals,
  quantized-LPC residual evaluation, stereo decorrelation) were developed in this
  sibling project and are vendored verbatim into internal/i32, so the codec owns
  its integer hot path end-to-end. They dispatch to AVX2 (amd64) / NEON (arm64)
  kernels at runtime with a pure-Go fallback on other CPUs and short inputs; every
  kernel is bit-identical to the scalar path, so the encoded stream is byte-for-byte
  the same with or without SIMD (the rice/lpc parity tests assert this on both the
  SIMD and pure-Go paths).
- github.com/tphakala/simd also remains a normal module dependency for the parts
  that did not move in-tree: simd/cpu (CPU feature detection, used by the vendored
  kernels' dispatch), simd/f64 (LPC autocorrelation), and simd/crc (FLAC CRC-16).
  Its only transitive dependency is golang.org/x/sys.
