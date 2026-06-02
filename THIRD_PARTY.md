# Third-party provenance

go-flac is an independent, clean-room reimplementation. No third-party source
code is copied into this repository; everything here is original work licensed
under the MIT License (see LICENSE).

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

- github.com/tphakala/simd: this project's own SIMD library, consumed as a
  normal Go module dependency in a later milestone.
