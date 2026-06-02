package flac_test

import (
	"errors"
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

func TestEncoderSentinelDistinct(t *testing.T) {
	if flac.ErrEncoderClosed == nil {
		t.Fatal("ErrEncoderClosed is nil")
	}
	if errors.Is(flac.ErrEncoderClosed, flac.ErrNotImplemented) {
		t.Error("ErrEncoderClosed aliases ErrNotImplemented")
	}
}

func TestSentinelErrorsAreDistinct(t *testing.T) {
	errs := []error{
		flac.ErrSeekUnsupported, flac.ErrMissingStreamInfo,
		flac.ErrCRCMismatch, flac.ErrMD5Mismatch, flac.ErrUnsupported,
	}
	for i := range errs {
		if errs[i] == nil {
			t.Fatalf("sentinel %d is nil", i)
		}
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Errorf("sentinels %d and %d alias each other", i, j)
			}
		}
	}
}
