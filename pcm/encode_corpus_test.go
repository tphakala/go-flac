package pcm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestReencodeSubsetCorpus decodes each subset file, re-encodes it with our
// encoder at level 5, decodes the result, and checks the PCM round-trips. Files
// whose bit depth is outside the M3 encoder scope (4..24) are skipped.
func TestReencodeSubsetCorpus(t *testing.T) {
	matches, _ := filepath.Glob(filepath.Join(corpusRoot, "subset", "*.flac"))
	if len(matches) == 0 {
		t.Skip("subset corpus unavailable (submodule not checked out?)")
	}
	for _, p := range matches {
		t.Run(filepath.Base(p), func(t *testing.T) {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			d, err := NewDecoder(bytes.NewReader(data))
			if err != nil {
				t.Skipf("decode setup: %v", err)
			}
			info := d.Info()
			if info.BitDepth < 4 || info.BitDepth > 24 {
				t.Skipf("bit depth %d outside M3 encoder scope", info.BitDepth)
			}
			var pcmBuf bytes.Buffer
			if _, err := d.WriteTo(&pcmBuf); err != nil {
				t.Skipf("decode: %v", err)
			}
			cfg := Config{
				SampleRate:       info.SampleRate,
				BitDepth:         info.BitDepth,
				Channels:         info.Channels,
				CompressionLevel: 5,
			}
			path := encodeToFile(t, cfg, pcmBuf.Bytes())
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cerr := f.Close(); cerr != nil {
					t.Errorf("close re-encoded file: %v", cerr)
				}
			}()
			_, got := decodeAll(t, f)
			if !bytes.Equal(got, pcmBuf.Bytes()) {
				t.Fatalf("%s: re-encode round trip mismatch (got %d, want %d)", p, len(got), pcmBuf.Len())
			}
		})
	}
}
