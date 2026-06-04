package frame

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

func TestFindNextFrameLocatesAndVerifies(t *testing.T) {
	frameBytes, si := oneFrameBytes(t) // 2ch, 16bps, 64-sample block

	// Embed the frame after some junk that includes a false 0xFF 0xF8 sync.
	junk := []byte{0x00, 0x11, 0xFF, 0xF8, 0x99, 0xAA} // a sync-looking pair in noise
	data := append(bytes.Clone(junk), frameBytes...)

	var fr Frame
	start, consumed, res := FindNextFrame(data, si, &fr)
	if res != FrameFound {
		t.Fatalf("res = %v, want FrameFound", res)
	}
	if start != len(junk) {
		t.Fatalf("start = %d, want %d (false sync in junk must be rejected by CRC-16)", start, len(junk))
	}
	if consumed != len(frameBytes) {
		t.Fatalf("consumed = %d, want %d", consumed, len(frameBytes))
	}
}

func TestFindNextFrameTruncatedTail(t *testing.T) {
	frameBytes, si := oneFrameBytes(t)
	data := frameBytes[:len(frameBytes)-3] // cut the CRC-16 / tail off
	var fr Frame
	_, _, res := FindNextFrame(data, si, &fr)
	if res != FrameTruncated {
		t.Fatalf("res = %v, want FrameTruncated", res)
	}
}

func TestFindNextFrameNotFound(t *testing.T) {
	_, si := oneFrameBytes(t)
	var fr Frame
	_, _, res := FindNextFrame([]byte{0x01, 0x02, 0x03, 0x04}, si, &fr)
	if res != FrameNotFound {
		t.Fatalf("res = %v, want FrameNotFound", res)
	}
}

func oneFrameBytes(t *testing.T) ([]byte, flac.StreamInfo) {
	t.Helper()
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	ch := [][]int32{make([]int32, 64), make([]int32, 64)}
	for i := range ch[0] {
		ch[0][i] = int32(i - 32)
		ch[1][i] = int32(64 - i)
	}
	bw := bitio.NewWriter()
	buf := EncodeFrame(bw, NewWorkspace(len(ch[0]), len(ch), 12), Params{}, si, ch, 0)
	out := make([]byte, len(buf))
	copy(out, buf)
	return out, si
}
