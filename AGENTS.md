# AGENTS.md

Orientation for coding agents and automated reviewers working on this repository. README.md is the user-facing reference (usage examples, benchmarks, status); this file is the map for changing and reviewing the code.

## What this is

`github.com/tphakala/go-flac` (root package `flac`, streaming API in `pcm`) is a pure-Go FLAC encoder and decoder. It is the FLAC member of a family of audio libraries (go-wav, go-opus, go-aac, go-m4a) and deliberately presents the same API shape as its siblings. It aims to:

1. **Build anywhere Go builds.** `CGO_ENABLED=0`, no external binaries, no libFLAC. The encoder and decoder hot paths use SIMD (AVX2 on amd64, NEON on arm64) behind a pure-Go fallback, so every Go target still builds and runs.
2. **Be lossless and bit-exact.** Every encode reproduces the input bit for bit at 4 to 32 bits per sample, and the decoder matches the reference libFLAC byte for byte. SIMD and scalar paths produce identical output.
3. **Survive hostile input.** The decoder and metadata reader parse untrusted bytes. They must never panic, and allocations driven by a stream's own claims must stay bounded. Consumers are long-running services (BirdNET-Go is the main one, shipping on 32-bit ARM and 386 as well).
4. **Stay fast and allocation-light.** Per-frame decode is allocation-free; steady-state encode allocates close to nothing per block. Decoders and encoders are poolable through `Reset`.

Packages:

| Package | Role |
|---|---|
| `flac` (root) | Shared types only: `Version`, the sentinel errors, `StreamInfo`, `Tag` |
| `pcm` | The public streaming API: `Decoder`, `Encoder`, `FrameEncoder`, `FrameDecoder`, `Config`, one-shot `EncodeInterleaved` and `DecodeInterleaved[Limit]` |
| `internal/bitio` | MSB-first bit reader and writer in FLAC bit order |
| `internal/crc` | CRC-8 (frame header), CRC-16 (frame) |
| `internal/frame` | Frame and subframe coding (constant, verbatim, fixed, LPC), stereo decorrelation, frame-sync resync |
| `internal/i32` | int32 SIMD kernels (Rice cost, fixed predictors, LPC, decorrelation, interleave), vendored from `tphakala/simd` so the codec owns its hot path |
| `internal/lpc` | Linear prediction: windowing, autocorrelation, Levinson-Durbin, quantization |
| `internal/rice` | Partitioned Rice residual coding and the parameter search |
| `internal/meta` | Metadata blocks: STREAMINFO, VORBIS_COMMENT, SEEKTABLE, PICTURE, CUESHEET, PADDING, APPLICATION |
| `cmd/wav2flac` | Runnable demo and the go-flac side of the benchmark harness |
| `rules` | gocritic ruleguard matchers (build tag `ruleguard`, lint-only) |

Non-goals: float or IEEE-float input (the encoder takes integer PCM). Mid-stream resync from a non-`fLaC` start position is documented as future work.

### API stability

go-flac is past v1.0.0 and follows semantic versioning. Do not change or remove an exported symbol, an error sentinel, a documented default, or any emitted byte without a major version bump. Additive changes (a new option, a new `Config` field with a zero value that preserves behaviour) ship as a minor release. A new sentinel error goes in `flac.go` with a doc comment, and the README and package godoc are updated with any user-visible change.

## Design rules you must not break

A change that violates one of these is wrong even if tests pass.

1. **No cgo, ever.** Everything builds with `CGO_ENABLED=0`. The only module dependencies are `github.com/tphakala/simd` (CPU feature detection, LPC autocorrelation, CRC-16) and its transitive `golang.org/x/sys`. `go-ruleguard/dsl` is lint-only, behind a build tag. Do not add a dependency to the shipped code.
2. **SIMD is an optimisation, never a behaviour.** Every kernel in `internal/i32` has a pure-Go fallback and the two are bit-identical. A new or changed kernel needs a parity test against the scalar path, and a fuzz target where the input space is large. Output must not depend on which CPU the code ran on.
3. **Encoded output is pinned.** Output for 4 to 24 bits per sample (the int32 path) is byte-identical release to release; the golden hashes in `pcm/golden_baseline_test.go` and `pcm/testdata/m5_golden.json` guard it. A change that moves emitted bytes needs a stated reason and a reviewed golden update, never a silent regeneration. Depths 25 to 32 run a separate int64 path in both encoder and decoder.
4. **Clean room.** The codec is original work (see `THIRD_PARTY.md`). mewkiz/flac and mycophonic/flac were algorithm references only; do not copy their code, and never import them from shipped code. The int32 kernels are the one vendored block, and they come from the author's own MIT-licensed `tphakala/simd`.
5. **Decoding untrusted bytes is total and bounded.** A malformed stream returns a wrapped sentinel error, never a panic or an index error. A count or length read from the stream is checked against what is actually present before it drives a `make`, a loop or an offset. The one-shot `DecodeInterleaved` stops at `DefaultMaxDecodedBytes` (1 GiB) and returns `ErrDecodeLimit`, because a constant subframe lets a tiny file decode to gigabytes; its pre-size is bounded against the declared length. A caller that wants unbounded or unknown length streams with `NewDecoder`.
6. **Typed errors with `%w`.** Map conditions to the sentinels in `flac.go` so callers can use `errors.Is`:
   - `ErrUnsupported`: reserved or illegal coded value, unsupported layout, or an invalid option value (for example a `WithConvertTo` depth other than 8, 16, 24 or 32).
   - `ErrCRCMismatch`: frame header CRC-8 or frame CRC-16 failed.
   - `ErrMD5Mismatch`: decoded audio does not match the STREAMINFO MD5.
   - `ErrMissingStreamInfo`: no STREAMINFO block, or not first.
   - `ErrTruncatedStream`: stream ended before the declared sample count, or a frame body was cut short (mid-frame truncation also wraps `io.ErrUnexpectedEOF`).
   - `ErrSeekUnsupported` (source is not an `io.Seeker`), `ErrInvalidSeek` (negative index), `ErrEncoderClosed` (write after `Close`).
   - `pcm.ErrDecodeLimit`: a resource limit, not a verdict on the stream. Keep it distinct from the corruption errors.
7. **Correct on 32-bit.** `int` is 32 bits on 386 and arm. Sizes, sample counts and Rice cost accumulators use explicit 64-bit types where they can exceed 2^31. CI runs the Rice package as a 386 binary and the golden generator uses `uint32` arithmetic so hashes hold on every word size. 32-bit ARM has its own LPC restore order cutover (`restore_order_arm.go`); keep it consistent with the routing boundary the tests derive.
8. **MD5 semantics.** The encoder hashes the input and writes the digest into STREAMINFO on `Close` when the sink is an `io.WriteSeeker`. A non-seekable sink, or `Config.SkipMD5`, leaves the all-zero "unknown" MD5, which is spec-legal. The decoder verifies MD5 unless `SkipMD5Verification` is passed, and verifies on the native samples even under `WithConvertTo`. A seek disables the MD5 and truncation checks. These options apply to one `NewDecoder` or `Reset` call and are not remembered across `Reset`.
9. **Native output width, explicit conversion.** The decoder emits samples right-aligned at the stream's own depth in `ceil(bits/8)` bytes. Only `WithConvertTo` changes that, by shifting (exact when widening, arithmetic-shift truncation when narrowing, no rounding or dither). `Info()` always describes the stream; `OutputBitDepth()` describes what `Read` yields.
10. **Interop is the oracle.** The decoder must pass the IETF FLAC test corpus (every `subset` file's decoded-audio MD5 matches its STREAMINFO signature) and match libFLAC `flac -d` byte for byte; encoder output must decode with libFLAC. The encoder ratio matches libFLAC's at the same level.
11. **Pooling must stay safe.** `Decoder.Reset` and `Encoder.Reset` fully recover from a failed call through another `Reset`, reuse buffers at their high-water size, and re-derive behaviour from the arguments each call. A zero-value `Decoder` is a valid `Reset` target.

## Layout

```
flac.go               Version, sentinel errors, StreamInfo, Tag
doc.go                Package godoc (root package is shared types only)

pcm/pcm.go            Config (the encoder's single configuration struct)
pcm/encoder.go        Encoder (io.WriteCloser), NewEncoder, Reset, MaxSampleRate, compression levels 0-8
pcm/encode_oneshot.go EncodeInterleaved over a pooled encoder
pcm/frameio.go        FrameEncoder and FrameDecoder: frame-at-a-time encode and decode for callers that mux
                      FLAC frames into another container (MP4 fLaC, Matroska)
pcm/decoder.go        Decoder (io.Reader, io.WriterTo), options, Reset, SeekToSample, SEEKTABLE-narrowed seek
pcm/decode_oneshot.go DecodeInterleaved[Limit], DefaultMaxDecodedBytes, ErrDecodeLimit
pcm/convert.go        WithConvertTo sample shifting
pcm/pack.go           Interleave and pack helpers (16-bit mono and stereo fast paths, generic fallback)

internal/frame/       decode and encode of frames and subframes, workspace (reusable scratch), resync
internal/i32/         i32_amd64.go/.s and i32_arm64.go/.s (SIMD), i32_go.go and i32_other.go (pure Go),
                      lpc*.go, restore*.go, rice.go, fixed.go, decorrelate.go, minmax.go
internal/lpc/         analyze (encode side), restore (decode side), simd.go glue to tphakala/simd
internal/rice/        encode, estimate (cost search), simd.go
internal/meta/        metadata block parse and encode, seektable.go, vorbiscomment.go
internal/bitio/       reader.go (64-bit bulk refill, no per-byte interface dispatch), writer.go
internal/crc/         CRC-8, CRC-16
cmd/wav2flac/         WAV to FLAC demo
scripts/              bench-encoders.sh (go-flac vs libFLAC, SoX, ffmpeg)
testdata/             small/ (committed fixtures), flac-test-files/ (IETF corpus, git submodule)
```

Metadata, frame and subframe parsing lives in `internal`; seek policy lives in `pcm` (`internal/meta` only parses the SEEKTABLE).

## Testing approach

Tests run without audio hardware. The corpus and libFLAC checks skip when their prerequisites are absent, so a plain `go test ./...` works in a fresh clone.

- **Regression tests.** New behaviour gets a test. A bug fix gets a regression test that fails before the fix. A test must be able to fail: break the line it pins and confirm it fails, and avoid fixtures where every path gives the same result.
- **IETF corpus and libFLAC.** `pcm/validate_test.go` (`TestCrossValidateLibFLAC`, `task validate`) decodes every `subset` file from the `testdata/flac-test-files` submodule and compares with `flac -d`. It needs the `flac` binary and `git submodule update --init`; it skips without either, CI installs both.
- **Golden output.** `pcm/golden_baseline_test.go` and `golden_m5_test.go` pin SHA-256 hashes of deterministic encodes across rates, channel counts, depths and levels. Regenerate only for an intended output change and explain the diff.
- **Fuzzing.** `FuzzDecode` (`internal/frame`), `FuzzReadMetadata` (`internal/meta`), `FuzzReadBits` and `FuzzReaderOracle` (`internal/bitio`), `FuzzSeek`, `FuzzEncodeRoundTrip` and `FuzzDecodeConvert` (`pcm`), and the `FuzzI32*` kernels against their scalar references. A `-fuzz` pattern that matches nothing passes without running, so run a new target in the package that defines it. CI runs a short smoke of the primary targets; a crasher a run finds is committed under `testdata/fuzz` as a regression seed. New parsing paths get a fuzz target or a seed.
- **Scalar-fallback parity.** CI reruns `internal/i32`, `internal/lpc`, `internal/frame` and `pcm` with `GODEBUG=cpu.avx2=off`, because GitHub amd64 runners have AVX2 and would otherwise never exercise the pure-Go path.
- **Allocation tests.** Per-frame decode and the encoder workspace are guarded by allocation tests (`decode_alloc_test.go`, `workspace_alloc_test.go`); a change on those paths keeps them green.
- **Architectures.** `go vet` runs for amd64, arm64, arm (GOARM=7) and 386; `internal/rice` runs as a 386 binary. Real arm64 hardware is the reference for NEON and Cortex-A76 numbers; do not use QEMU user-mode for arm64 benchmarks or SIMD validation.
- **Benchmarks.** `task bench`, and `scripts/bench-encoders.sh` for the cross-encoder comparison. Timings are machine-dependent; compare before and after on the same host.

## Commands

Go version is read from `go.mod` (currently 1.27). The project tracks the latest stable Go, so no shims or build tags for older releases. golangci-lint is pinned to v2.14.0 in `GOLANGCI_LINT_VERSION` in `.github/workflows/ci.yml`; bump it by hand when a release ships (Dependabot does not see that string) and fix any new findings in the same change.

```
task build           # go build ./...
task test            # go test -v ./...
task test:race       # go test -race ./...
task vet             # go vet for amd64, arm64, arm (GOARM=7) and 386
task lint            # golangci-lint run ./...
task validate        # libFLAC cross-validation (needs the flac binary and the submodule)
task bench           # all benchmarks with allocation counts
task tidy            # go mod tidy and verify
task hooks:install   # point git at .githooks
task check           # build, vet, lint, race tests
```

Run `task check` before pushing. The race-instrumented `pcm` suite (corpus re-encode plus libFLAC cross-validation, LPC at levels 3 to 8) is slow; CI runs it with `-timeout 20m`, and a local run may need the same. The `.githooks` pre-commit runs gofmt and `golangci-lint --fix`; pre-push runs build and vet only and leaves the race suite to CI and `task check`. CI additionally runs tests on Linux, macOS and Windows, the scalar-fallback parity run, the 386 Rice run, build for linux, darwin and windows on amd64 and arm64, the fuzz smoke job, govulncheck and OpenSSF Scorecard.

## Conventions

- **Lint.** Config is `.golangci.yaml` (gocritic with ruleguard from `rules/*.go`, revive, errorlint, exhaustive with `default` counting as exhaustive, gocognit 50, `modernize` on, `unused` off). `pcm.Config` is passed by value on purpose and `hugeParam` is raised to 128 bytes for it. Use `errors.New` for constant messages.
- **No `//nolint` without a linter name and a reason.** If a linter is wrong often enough to silence globally, change `.golangci.yaml` with a comment saying why.
- **Errors.** Wrap with `%w`. Error strings carry the `go-flac:` prefix (`go-flac/pcm:` for errors defined in `pcm`).
- **Comments.** Explain why: the FLAC format rule where it is not obvious, the failure being avoided, the measurement behind a threshold or cutover. Match the existing density; the rationale on `DefaultMaxDecodedBytes` and `Config` fields is the model for documenting a limit or an option.
- **Modern Go.** Use current language and standard library features where they make code simpler (`min`/`max` builtins, range-over-int, `errors.AsType`). The ruleguard matchers carry these patterns.
- **Version.** `flac.Version` in `flac.go` is bumped in its own `chore: bump version to X.Y.Z` commit. A `v*.*.*` tag triggers the release workflow, which creates a draft GitHub release with generated notes.
- **Commits** use an imperative subject with a type prefix and optional scope: `feat(pcm): ...`, `fix(pcm): ...`, `perf(decode): ...`, `refactor: ...`, `test(frame): ...`, `docs: ...`, `ci: ...`, `chore: ...`.
- **Text style.** No em or en dashes anywhere (code, comments, docs, commit messages); use commas, colons, parentheses or a plain hyphen.
- **Dependencies.** Dependabot keeps Go modules and GitHub Actions current; merge its PRs once CI passes.

## Review guidance

For CodeRabbit, Copilot and human reviewers. Rate findings on what a caller of the library would see.

Flag these, they are real defects here:

- Any break of the design rules above: cgo, a new shipped dependency, a SIMD kernel that differs from its scalar twin, a change to emitted bytes without a reviewed golden update.
- A panic, index error, or unbounded allocation reachable from stream bytes, in the decoder, `internal/meta`, `internal/frame` or `internal/bitio`.
- A count or length from the stream trusted before it is checked against the bytes present, or an overflow on 32-bit (`int` arithmetic on a sample count, byte offset or cost accumulator).
- A corrupt stream reported as `ErrDecodeLimit` or `ErrUnsupported`, or a limit reported as corruption; a returned error that does not wrap a sentinel where one applies.
- An MD5 or truncation check that runs after a seek, or is skipped when it should run.
- An allocation on the per-frame decode path or the steady-state encode path, or a `Reset` that leaves state from the previous stream behind.
- A new kernel or parsing path with no parity test or fuzz target.
- A test that cannot fail on the behaviour it names, or a doc sentence that is false for some bit depth or architecture.

Intentional, do not flag:

- `DefaultMaxDecodedBytes` (1 GiB) and the `presizeHint` bounding: a measured trade documented where it is defined.
- `pcm.Config` passed by value, and the raised `hugeParam` threshold.
- The all-zero MD5 written for a non-seekable sink or under `SkipMD5`.
- The decoder returning `Tag.Value` bytes verbatim, which may not be valid UTF-8 for a stream another tool wrote; the encoder rejects non-UTF-8 values.
- Vendored kernels in `internal/i32` that duplicate code in `tphakala/simd`; the duplication is deliberate so the codec owns its hot path.
- Different LPC restore cutover orders per architecture; they are measured per-architecture.
- Compression ratio matching libFLAC but throughput trailing it; the README states this.
- Large `_test.go` files and many small test helpers across the `pcm` package.

## Things that do not belong in the repo

- Planning artifacts: design docs, specs and plans live in the private tracker and under the gitignored `/docs/`. Never commit them.
- Local tool state and caches (`cache/`, `.serena/`, `.claude/`, `.codegraph/`) and build outputs (`*.test`, `*.out`, coverage files, `/bin/`).
- Large or unscrubbed audio. Committed media is limited to small fixtures under `testdata/`; the IETF corpus comes in through the submodule.
