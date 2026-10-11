package pcm

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// benchCorpusFile returns the bytes of the named subset file, or the first
// subset file when name is absent, skipping the benchmark when the corpus is
// not checked out.
func benchCorpusFile(b *testing.B, name string) []byte {
	b.Helper()
	matches, _ := filepath.Glob(filepath.Join(corpusRoot, "subset", "*.flac"))
	if len(matches) == 0 {
		b.Skip("subset corpus unavailable")
	}
	path := matches[0]
	for _, m := range matches {
		if filepath.Base(m) == name {
			path = m
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	return data
}

func BenchmarkDecodeSubset(b *testing.B) {
	// Use a mid-size representative file if present, else the first.
	data := benchCorpusFile(b, "01 - blocksize 4096.flac")
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
	benchDecodeReuse(b, "01 - blocksize 4096.flac")
}

// BenchmarkDecodeReuse24 is BenchmarkDecodeReuse on a 24-bit file.
func BenchmarkDecodeReuse24(b *testing.B) {
	benchDecodeReuse(b, "28 - high resolution audio, default settings.flac")
}

func benchDecodeReuse(b *testing.B, name string) {
	b.Helper()
	data := benchCorpusFile(b, name)
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
