package pcm

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestCrossValidateLibFLAC decodes every subset corpus file with our decoder and
// with the reference libFLAC `flac -d` (raw little-endian signed output), and
// asserts the PCM matches byte-for-byte. It skips cleanly when the flac binary or
// the corpus is unavailable.
func TestCrossValidateLibFLAC(t *testing.T) {
	flacBin, err := exec.LookPath("flac")
	if err != nil {
		t.Skip("flac binary not found; skipping libFLAC cross-validation")
	}
	matches, _ := filepath.Glob(filepath.Join(corpusRoot, "subset", "*.flac"))
	if len(matches) == 0 {
		t.Skip("subset corpus unavailable")
	}
	for _, p := range matches {
		t.Run(filepath.Base(p), func(t *testing.T) {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			d, err := NewDecoder(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			// libFLAC's raw decode output supports only 8/16/24/32-bit
			// ("must be 8/16/24/32 for raw format output"). For other depths
			// (12, 20) the byte-for-byte compare is impossible; correctness for
			// those is covered by the STREAMINFO-MD5 conformance test instead.
			switch d.Info().BitDepth {
			case 8, 16, 24, 32:
			default:
				t.Skipf("libFLAC raw output does not support %d-bit; covered by STREAMINFO-MD5 conformance", d.Info().BitDepth)
			}
			var ours bytes.Buffer
			if _, err := io.Copy(&ours, d); err != nil {
				t.Fatalf("our decode: %v", err)
			}
			// flac -d to raw, little-endian signed, on stdout. Bound the
			// subprocess so a hung flac cannot stall the test, and surface its
			// stderr in the failure message.
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, flacBin, "-d", "--silent", "--force-raw-format",
				"--endian=little", "--sign=signed", "-c", p)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			ref, err := cmd.Output()
			if err != nil {
				t.Fatalf("flac -d: %v: %s", err, stderr.String())
			}
			if !bytes.Equal(ours.Bytes(), ref) {
				t.Fatalf("%s: PCM differs (ours %d bytes, flac %d bytes)", p, ours.Len(), len(ref))
			}
		})
	}
}
