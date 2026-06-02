package pcm

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkDecodeSubset(b *testing.B) {
	matches, _ := filepath.Glob(filepath.Join(corpusRoot, "subset", "*.flac"))
	if len(matches) == 0 {
		b.Skip("subset corpus unavailable")
	}
	// Use a mid-size representative file if present, else the first.
	path := matches[0]
	for _, m := range matches {
		if filepath.Base(m) == "01 - blocksize 4096.flac" {
			path = m
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		d, err := NewDecoder(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, d); err != nil {
			b.Fatal(err)
		}
	}
}
