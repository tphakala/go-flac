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

// BenchmarkDecodeReuse decodes the same file as BenchmarkDecodeSubset but rebinds
// one decoder with Reset each iteration instead of constructing a fresh one, the
// pooled/streaming reuse case. It isolates the win from issue #24: the per-decoder
// setup buffers are allocated once (in the warm-up decode below) and reused, so
// allocs/op should be a small fraction of BenchmarkDecodeSubset's.
func BenchmarkDecodeReuse(b *testing.B) {
	matches, _ := filepath.Glob(filepath.Join(corpusRoot, "subset", "*.flac"))
	if len(matches) == 0 {
		b.Skip("subset corpus unavailable")
	}
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
	// Build and fully use the decoder once so every reusable buffer exists at its
	// high-water size before measurement.
	d, err := NewDecoder(bytes.NewReader(data))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, d); err != nil {
		b.Fatal(err)
	}
	r := bytes.NewReader(data)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		r.Reset(data)
		if err := d.Reset(r); err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, d); err != nil {
			b.Fatal(err)
		}
	}
}
