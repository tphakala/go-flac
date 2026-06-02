package pcm

import (
	"bytes"
	"crypto/md5"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const corpusRoot = "../testdata/flac-test-files"

func decodeAndCheckMD5(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDecoder(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: NewDecoder: %v", path, err)
	}
	h := md5.New()
	if _, err := io.Copy(h, d); err != nil {
		t.Fatalf("%s: decode: %v", path, err)
	}
	var zero [16]byte
	want := d.Info().MD5
	if want == zero {
		return // no MD5 stored, nothing to compare
	}
	var got [16]byte
	copy(got[:], h.Sum(nil))
	if got != want {
		t.Fatalf("%s: decoded MD5 %x != STREAMINFO %x", path, got, want)
	}
}

func TestConformanceSubset(t *testing.T) {
	runCorpusDir(t, filepath.Join(corpusRoot, "subset"), decodeAndCheckMD5)
}

// m4ResyncDeferred lists uncommon corpus files that M2 cannot decode by design:
// they do not begin with the "fLaC" marker (file 10 starts directly at a frame
// header, file 11 starts with unparsable leading data), so decoding them needs
// mid-stream frame resync, which the M2 design defers to M4 (see the design's
// non-goals and the "leading non-fLaC bytes" note). In M2 the decoder must reject
// them with a clean error and no panic; this test pins that boundary.
var m4ResyncDeferred = map[string]bool{
	"10 - file starting at frame header.flac":      true,
	"11 - file starting with unparsable data.flac": true,
}

func TestConformanceUncommon(t *testing.T) {
	runCorpusDir(t, filepath.Join(corpusRoot, "uncommon"), func(t *testing.T, path string) {
		t.Helper()
		if m4ResyncDeferred[filepath.Base(path)] {
			t.Logf("M2-deferred (M4 resync): %s must error without panic, not decode", filepath.Base(path))
			assertErrorsNoPanic(t, path)
			return
		}
		decodeAndCheckMD5(t, path)
	})
}

// assertErrorsNoPanic verifies a stream the M2 decoder is not expected to handle
// fails with an error (at metadata parse or during decode) and never panics.
func assertErrorsNoPanic(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDecoder(bytes.NewReader(data))
	if err != nil {
		return // rejected at metadata parse, as expected
	}
	if _, err := io.Copy(io.Discard, d); err == nil {
		t.Fatalf("%s: expected a decode error in M2 (M4 resync deferred), got success", path)
	}
}

func TestConformanceFaultyNoPanic(t *testing.T) {
	runCorpusDir(t, filepath.Join(corpusRoot, "faulty"), func(t *testing.T, path string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// Must not panic; an error is the expected outcome.
		d, err := NewDecoder(bytes.NewReader(data))
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, d)
	})
}

func TestSmallCorpusMD5(t *testing.T) {
	matches, _ := filepath.Glob("../testdata/small/*.flac")
	if len(matches) == 0 {
		t.Skip("no small corpus committed")
	}
	for _, p := range matches {
		t.Run(filepath.Base(p), func(t *testing.T) { decodeAndCheckMD5(t, p) })
	}
}

func runCorpusDir(t *testing.T, dir string, fn func(*testing.T, string)) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("corpus dir %s unavailable (submodule not checked out?): %v", dir, err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".flac" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		t.Run(e.Name(), func(t *testing.T) { fn(t, p) })
	}
}
