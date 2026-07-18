package pcm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// goldenCase returns the encoded FLAC bytes for one deterministic <= 24 bps config.
func goldenCase(t *testing.T, sampleRate, channels, bitDepth, level int) []byte {
	t.Helper()
	const frames = 4096
	bytesPS := (bitDepth + 7) / 8
	pcm := make([]byte, frames*channels*bytesPS)
	for i := range pcm {
		// uint32 arithmetic keeps the generator deterministic across word sizes, so
		// the golden hashes hold on 32-bit (GOARCH 386/arm) as well as 64-bit. The
		// extracted byte is unchanged on 64-bit (it reads bits below 2^32).
		pcm[i] = byte((uint32(i)*1103515245 + 12345) >> 7)
	}
	var out bytes.Buffer
	enc, err := NewEncoder(&out, Config{SampleRate: sampleRate, Channels: channels, BitDepth: bitDepth, CompressionLevel: level})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out.Bytes()
}

func TestInt32OutputUnchanged(t *testing.T) {
	// goldenCase encodes to a non-seekable bytes.Buffer without declaring
	// TotalSamples, so STREAMINFO min/max block size is finalized up front. These
	// hashes were regenerated when that field changed from the illegal 0 "unknown"
	// sentinel to encoderBlockSize (4096); the only bytes that differ from the prior
	// baseline are STREAMINFO offsets 8..11 (the min/max block-size fields), verified
	// by zeroing them and reproducing the old hashes. The encoded audio is unchanged.
	want := map[string]string{
		"44100/1/16/5": "c346650b1e0ecb9dd1b9609a9529255763baca56687feace241a7c1dc2b97213",
		"44100/2/16/5": "1a066116c9163906b5c43c5f6add17f3fef71b4d07dc5ad48321760abe790dd3",
		"48000/2/24/8": "aa1f9fdcaa157a25365174fbf50207bcf966c4be899445ee602265c9313e44c6",
		"44100/2/16/0": "acb17a7669eda09f234123032b25e71c66365f529dcef82b89adcad910a550ac",
		"96000/2/24/3": "7080617c7512f76962b93eba1323069d63f1a5b31e2df8f6eb153e8f4be207cf",
	}
	cases := []struct{ sr, ch, bd, lvl int }{
		{44100, 1, 16, 5}, {44100, 2, 16, 5}, {48000, 2, 24, 8},
		{44100, 2, 16, 0}, {96000, 2, 24, 3},
	}
	for _, c := range cases {
		key := fmt.Sprintf("%d/%d/%d/%d", c.sr, c.ch, c.bd, c.lvl)
		got := sha256.Sum256(goldenCase(t, c.sr, c.ch, c.bd, c.lvl))
		if hex.EncodeToString(got[:]) != want[key] {
			t.Fatalf("int32 output for %s changed: got %s", key, hex.EncodeToString(got[:]))
		}
	}
}
