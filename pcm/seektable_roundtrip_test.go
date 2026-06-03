package pcm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/meta"
)

// encodeRampSeektable is encodeRamp with a SEEKTABLE enabled.
func encodeRampSeektable(t *testing.T, channels, bps, samples, interval int) []byte {
	t.Helper()
	var sb seekBuffer
	enc, err := NewEncoder(&sb, Config{
		SampleRate: 44100, Channels: channels, BitDepth: bps, CompressionLevel: 2,
		SeekTableInterval: interval, SeekTableMaxPoints: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	bytesPS := (bps + 7) / 8
	pcm := make([]byte, samples*channels*bytesPS)
	idx := 0
	for s := range samples {
		for c := range channels {
			v := uint32(int32(s + c))
			for b := range bytesPS {
				pcm[idx] = byte(v >> (uint(b) * 8))
				idx++
			}
		}
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return sb.Bytes()
}

// rawRamp builds interleaved little-endian PCM where inter-channel sample s, channel c
// holds value s+c (matching encodeRamp), for feeding the reference flac binary.
func rawRamp(channels, bps, samples int) []byte {
	bytesPS := (bps + 7) / 8
	out := make([]byte, 0, samples*channels*bytesPS)
	for s := range samples {
		for c := range channels {
			v := uint32(int32(s + c))
			for b := range bytesPS {
				out = append(out, byte(v>>(uint(b)*8)))
			}
		}
	}
	return out
}

func TestSeekParityWithAndWithoutTable(t *testing.T) {
	const ch, bps, n = 2, 16, 6*4096 + 11
	withTable := encodeRampSeektable(t, ch, bps, n, 4096)
	noTable, total := encodeRamp(t, ch, bps, n)
	targets := []int64{0, 100, 4096, 4097, 12345, int64(total) - 1}
	for _, tg := range targets {
		dt, err := NewDecoder(bytes.NewReader(withTable))
		if err != nil {
			t.Fatal(err)
		}
		dn, err := NewDecoder(bytes.NewReader(noTable))
		if err != nil {
			t.Fatal(err)
		}
		gt, err := dt.SeekToSample(tg)
		if err != nil {
			t.Fatalf("with-table seek %d: %v", tg, err)
		}
		gn, err := dn.SeekToSample(tg)
		if err != nil {
			t.Fatalf("no-table seek %d: %v", tg, err)
		}
		if gt != gn || gt != tg {
			t.Fatalf("target %d: with-table=%d no-table=%d", tg, gt, gn)
		}
		if readChan0(t, dt, ch, bps) != rampValue(tg, bps) || readChan0(t, dn, ch, bps) != rampValue(tg, bps) {
			t.Fatalf("target %d: landed sample mismatch", tg)
		}
	}
}

// TestLibFLACSeekTableInterop checks both directions against the reference flac binary:
// (a) a seek-table file we emit passes flac -t; (b) a SEEKTABLE that libFLAC writes is
// parsed by our decoder and drives a sample-accurate SeekToSample. Skips if flac is
// not on PATH.
func TestLibFLACSeekTableInterop(t *testing.T) {
	flacBin, err := exec.LookPath("flac")
	if err != nil {
		t.Skip("flac binary not found; skipping SEEKTABLE interop")
	}
	dir := t.TempDir()

	// (a) our emitted seek-table file passes flac -t.
	data := encodeRampSeektable(t, 2, 16, 5*4096, 4096)
	ourPath := filepath.Join(dir, "ours.flac")
	if err := os.WriteFile(ourPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	tcmd := exec.CommandContext(ctx, flacBin, "-t", "--silent", ourPath)
	var terr bytes.Buffer
	tcmd.Stderr = &terr
	runErr := tcmd.Run()
	cancel()
	if runErr != nil {
		t.Fatalf("flac -t on our seek-table file failed: %v: %s", runErr, terr.String())
	}

	// (b) libFLAC-produced SEEKTABLE: encode a position-encoded raw ramp with flac -S,
	// then drive our SeekToSample over it. Sample indices stay below 32768 so int16
	// does not wrap.
	const ch, bps, n = 2, 16, 7 * 4096
	rawIn := filepath.Join(dir, "in.raw")
	if err := os.WriteFile(rawIn, rawRamp(ch, bps, n), 0o600); err != nil {
		t.Fatal(err)
	}
	libPath := filepath.Join(dir, "lib.flac")
	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
	ecmd := exec.CommandContext(ctx2, flacBin, "--silent", "--force-raw-format",
		"--endian=little", "--sign=signed", "--channels=2", "--bps=16",
		"--sample-rate=44100", "-S", "100x", "-o", libPath, "-f", rawIn)
	var eerr bytes.Buffer
	ecmd.Stderr = &eerr
	encErr := ecmd.Run()
	cancel2()
	if encErr != nil {
		t.Fatalf("flac encode with seek table failed: %v: %s", encErr, eerr.String())
	}
	libData, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	sm, err := meta.ReadMetadata(bitio.NewReader(bytes.NewReader(libData)))
	if err != nil {
		t.Fatalf("parsing libFLAC metadata: %v", err)
	}
	if len(sm.SeekPoints) == 0 {
		t.Fatal("libFLAC file carried no parseable seek points")
	}
	dec, err := NewDecoder(bytes.NewReader(libData))
	if err != nil {
		t.Fatal(err)
	}
	got, err := dec.SeekToSample(9000)
	if err != nil || got != 9000 {
		t.Fatalf("libFLAC-table seek: got %d err %v", got, err)
	}
	if v := readChan0(t, dec, ch, bps); v != rampValue(9000, bps) {
		t.Fatalf("libFLAC-table landed sample = %d, want %d", v, rampValue(9000, bps))
	}
}
