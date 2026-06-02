package pcm_test

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/pcm"
)

const (
	testRate     = 44100
	testBits     = 16
	testChannels = 2
)

func validConfig() pcm.Config {
	return pcm.Config{SampleRate: testRate, BitDepth: testBits, Channels: testChannels}
}

func TestNewEncoderRejectsNilWriter(t *testing.T) {
	if _, err := pcm.NewEncoder(nil, validConfig()); err == nil {
		t.Fatal("expected error for nil writer")
	}
}

func TestNewEncoderRejectsInvalidConfig(t *testing.T) {
	if _, err := pcm.NewEncoder(&bytes.Buffer{}, pcm.Config{}); err == nil {
		t.Fatal("expected error for zero config")
	}
}

func TestNewDecoderRejectsNilReader(t *testing.T) {
	if _, err := pcm.NewDecoder(nil); err == nil {
		t.Fatal("expected error for nil reader")
	}
}
