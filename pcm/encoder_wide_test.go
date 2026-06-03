package pcm

import (
	"bytes"
	"testing"
)

func TestNewEncoderAcceptsWideDepth(t *testing.T) {
	for _, bd := range []int{25, 28, 32} {
		var buf bytes.Buffer
		if _, err := NewEncoder(&buf, Config{SampleRate: 48000, Channels: 2, BitDepth: bd}); err != nil {
			t.Fatalf("BitDepth %d rejected: %v", bd, err)
		}
	}
	var buf bytes.Buffer
	if _, err := NewEncoder(&buf, Config{SampleRate: 48000, Channels: 2, BitDepth: 33}); err == nil {
		t.Fatal("BitDepth 33 should be rejected")
	}
}
