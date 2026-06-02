package pcm_test

import (
	"bytes"
	"errors"
	"testing"

	flac "github.com/tphakala/go-flac"
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

func TestNewEncoderValidConfigNotImplemented(t *testing.T) {
	_, err := pcm.NewEncoder(&bytes.Buffer{}, validConfig())
	if !errors.Is(err, flac.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestNewDecoderRejectsNilReader(t *testing.T) {
	if _, err := pcm.NewDecoder(nil); err == nil {
		t.Fatal("expected error for nil reader")
	}
}

func TestNewDecoderNotImplemented(t *testing.T) {
	_, err := pcm.NewDecoder(bytes.NewReader([]byte{}))
	if !errors.Is(err, flac.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}
