# Small committed corpus

Tiny real FLAC files for fast, submodule-independent unit tests. Each file is a
genuine FLAC stream with a STREAMINFO MD5 signature, so `TestSmallCorpusMD5`
(in `pcm/conformance_test.go`) decodes it and checks the decoded-audio MD5 against
the stored signature.

| File | Rate | Channels | Bit depth | Content |
| --- | --- | --- | --- | --- |
| `sine_16_stereo.flac` | 44100 Hz | 2 | 16 | 0.1 s 440 Hz sine |
| `sine_8_mono.flac` | 8000 Hz | 1 | 8 | 0.1 s 220 Hz sine |

## Regenerating

Reference-encoded with libFLAC (`flac --best`) from PCM WAVs produced by ffmpeg:

```bash
ffmpeg -f lavfi -i "sine=frequency=440:duration=0.1:sample_rate=44100" \
  -ac 2 -sample_fmt s16 -y /tmp/s16.wav
flac --best -f -o testdata/small/sine_16_stereo.flac /tmp/s16.wav

ffmpeg -f lavfi -i "sine=frequency=220:duration=0.1:sample_rate=8000" \
  -ac 1 -c:a pcm_u8 -y /tmp/u8.wav
flac --best -f -o testdata/small/sine_8_mono.flac /tmp/u8.wav
```

The broad feature coverage (every blocksize, bit depth, channel layout, predictor,
and metadata block) lives in the IETF `flac-test-files` submodule, exercised by the
other conformance tests; this small set just keeps a fast path that needs no
submodule checkout.
