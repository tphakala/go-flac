package pcm

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestEncodeCrossValidateLibFLAC encodes PCM with our encoder, then asserts the
// reference flac binary both verifies the stream (flac -t) and decodes it back to
// the exact source PCM (flac -d). Skips when flac is unavailable.
func TestEncodeCrossValidateLibFLAC(t *testing.T) {
	flacBin, err := exec.LookPath("flac")
	if err != nil {
		t.Skip("flac binary not found; skipping encode cross-validation")
	}
	cfgs := []Config{
		{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 2},
		{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 0},
		{SampleRate: 48000, BitDepth: 24, Channels: 2, CompressionLevel: 8},
		{SampleRate: 8000, BitDepth: 8, Channels: 1, CompressionLevel: 5},
	}
	for _, cfg := range cfgs {
		pcmBytes := genPCM(cfg, 4096+999) // a full frame plus a short final frame
		path := encodeToFile(t, cfg, pcmBytes)

		// flac -t validates sync, CRC-8/16, MD5, and decodability.
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		tcmd := exec.CommandContext(ctx, flacBin, "-t", "--silent", path)
		var terr bytes.Buffer
		tcmd.Stderr = &terr
		runErr := tcmd.Run()
		cancel()
		if runErr != nil {
			t.Fatalf("cfg %+v: flac -t failed: %v: %s", cfg, runErr, terr.String())
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
