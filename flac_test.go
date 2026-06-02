package flac_test

import (
	"testing"

	flac "github.com/tphakala/go-flac"
)

func TestVersionIsSet(t *testing.T) {
	if flac.Version == "" {
		t.Fatal("Version must not be empty")
	}
}

func TestStreamInfoZeroValue(t *testing.T) {
	var si flac.StreamInfo
	if si.SampleRate != 0 || si.Channels != 0 || si.BitDepth != 0 || si.TotalSamples != 0 {
		t.Fatalf("zero StreamInfo should be empty, got %+v", si)
	}
	if si.MD5 != [16]byte{} {
		t.Fatalf("zero StreamInfo MD5 should be all-zero, got %x", si.MD5)
	}
}
