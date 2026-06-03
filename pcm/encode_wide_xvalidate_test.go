package pcm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// flacAtLeast14 returns the flac binary path if flac is installed and version >= 1.4
// (the first release with 25-32 bps support); otherwise it skips the test.
func flacAtLeast14(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("flac")
	if err != nil {
		t.Skip("flac binary not found; skipping wide cross-validation")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output() // e.g. "flac 1.5.0"
	if err != nil {
		t.Skipf("flac --version failed: %v", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) >= 2 {
		parts := strings.Split(fields[1], ".")
		if len(parts) >= 2 {
			major, _ := strconv.Atoi(parts[0])
			minor, _ := strconv.Atoi(parts[1])
			if major > 1 || (major == 1 && minor >= 4) {
				return bin
			}
		}
	}
	t.Skipf("flac %q < 1.4; skipping wide (25-32 bps) cross-validation", strings.TrimSpace(string(out)))
	return ""
}

// TestWideEncodeValidatedByFlac encodes PCM with our encoder at wide bit depths,
// then asserts the reference flac binary both verifies the stream (flac -t) and,
// for byte-aligned depths (8/16/24/32), decodes it back to the exact source PCM
// (flac -d). Non-byte-aligned depths (e.g. 28) use flac -t only: the flac CLI
// raw output mode only supports bps in {8,16,24,32}, so flac -d --force-raw-format
// rejects 28-bit with "bits per sample is 28, must be 8/16/24/32 for raw format
// output". The framing, CRC-8/16, and STREAMINFO MD5 are still verified by flac -t.
// Direction (a) of the wide-depth cross-validation.
func TestWideEncodeValidatedByFlac(t *testing.T) {
	flacBin := flacAtLeast14(t)
	cfgs := []Config{
		{SampleRate: 48000, BitDepth: 32, Channels: 2, CompressionLevel: 5},
		{SampleRate: 96000, BitDepth: 28, Channels: 2, CompressionLevel: 8},
		{SampleRate: 48000, BitDepth: 32, Channels: 1, CompressionLevel: 3},
	}
	for _, cfg := range cfgs {
		pcmBytes := genPCM(cfg, 4096+999)
		path := encodeToFile(t, cfg, pcmBytes)

		// flac -t validates sync, CRC-8/16, MD5, and decodability for all depths.
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		tcmd := exec.CommandContext(ctx, flacBin, "-t", "--silent", path)
		var terr bytes.Buffer
		tcmd.Stderr = &terr
		runErr := tcmd.Run()
		cancel()
		if runErr != nil {
			t.Fatalf("cfg %+v: flac -t failed: %v: %s", cfg, runErr, terr.String())
		}

		// flac -d raw output only works for byte-aligned depths; skip the byte
		// comparison for non-byte-aligned depths (e.g. 28-bit).
		if cfg.BitDepth%8 != 0 {
			continue
		}

		// flac -d to raw, little-endian signed, must equal the source PCM.
		ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Second)
		dcmd := exec.CommandContext(ctx2, flacBin, "-d", "--silent", "--force-raw-format",
			"--endian=little", "--sign=signed", "-c", path)
		var derr bytes.Buffer
		dcmd.Stderr = &derr
		ref, outErr := dcmd.Output()
		cancel2()
		if outErr != nil {
			t.Fatalf("cfg %+v: flac -d failed: %v: %s", cfg, outErr, derr.String())
		}
		if !bytes.Equal(ref, pcmBytes) {
			t.Fatalf("cfg %+v: flac decoded %d bytes != source %d", cfg, len(ref), len(pcmBytes))
		}
	}
}

// TestWideFlacDecodedByOurs encodes 32-bit PCM with the reference flac binary,
// then decodes the resulting .flac file with our decoder and asserts byte-exact
// output. Direction (b) of the wide-depth cross-validation.
//
// 32-bit only: the flac CLI raw reader requires bps in {8,16,24,32}, so a
// non-byte-aligned depth like 28 cannot be fed as raw input here. Direction (a)
// and the conformance corpus cover 28-bit.
func TestWideFlacDecodedByOurs(t *testing.T) {
	flacBin := flacAtLeast14(t)
	cfg := Config{SampleRate: 48000, BitDepth: 32, Channels: 2, CompressionLevel: 5}
	pcmBytes := genPCM(cfg, 4096+999)

	dir := t.TempDir()
	rawIn := filepath.Join(dir, "in.raw")
	if err := os.WriteFile(rawIn, pcmBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	flacFile := filepath.Join(dir, "ref.flac")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	cmd := exec.CommandContext(ctx, flacBin, "--silent", "--force-raw-format",
		"--endian=little", "--sign=signed", "--channels=2", "--bps=32",
		"--sample-rate=48000", "-o", flacFile, "-f", rawIn)
	var cerr bytes.Buffer
	cmd.Stderr = &cerr
	runErr := cmd.Run()
	cancel()
	if runErr != nil {
		t.Fatalf("flac encode failed: %v: %s", runErr, cerr.String())
	}
	data, err := os.ReadFile(flacFile)
	if err != nil {
		t.Fatal(err)
	}
	_, got := decodeAll(t, bytes.NewReader(data))
	if !bytes.Equal(got, pcmBytes) {
		t.Fatalf("our decode of flac 32-bit differs (%d vs %d bytes)", len(got), len(pcmBytes))
	}
}
