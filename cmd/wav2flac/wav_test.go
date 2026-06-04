package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-flac/pcm"
)

// buildWAV assembles a minimal canonical WAV (16-byte fmt + data) with the given
// audio format tag, optionally preceded by extra raw chunks (already including
// their own 8-byte headers and any pad byte).
func buildWAV(format uint16, channels, sampleRate, bps int, pcmData []byte, extraChunks ...[]byte) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	b.Write([]byte{0, 0, 0, 0}) // RIFF size, patched below
	b.WriteString("WAVE")
	for _, c := range extraChunks {
		b.Write(c)
	}
	b.WriteString("fmt ")
	putLE32(&b, 16)
	putLE16(&b, format)
	putLE16(&b, uint16(channels))
	putLE32(&b, uint32(sampleRate))
	putLE32(&b, uint32(sampleRate*channels*bps/8)) // byte rate
	putLE16(&b, uint16(channels*bps/8))            // block align
	putLE16(&b, uint16(bps))
	b.WriteString("data")
	putLE32(&b, uint32(len(pcmData)))
	b.Write(pcmData)
	out := b.Bytes()
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out
}

func putLE16(b *bytes.Buffer, v uint16) { _ = binary.Write(b, binary.LittleEndian, v) }
func putLE32(b *bytes.Buffer, v uint32) { _ = binary.Write(b, binary.LittleEndian, v) }

func TestReadWAVHeader(t *testing.T) {
	pcmData := make([]byte, 8) // two 16-bit stereo frames
	f, err := readWAVHeader(bytes.NewReader(buildWAV(wavFormatPCM, 2, 44100, 16, pcmData)))
	if err != nil {
		t.Fatal(err)
	}
	if f.channels != 2 || f.sampleRate != 44100 || f.bitsPerSample != 16 {
		t.Fatalf("got %+v", f)
	}
	if f.dataLen != int64(len(pcmData)) {
		t.Fatalf("dataLen = %d, want %d", f.dataLen, len(pcmData))
	}
}

func TestReadWAVHeaderSkipsChunksAndPadding(t *testing.T) {
	// An odd-length LIST chunk (3 bytes of payload + 1 pad byte) ahead of fmt
	// must be skipped without desyncing the chunk walk.
	list := []byte("LIST")
	list = append(list, 3, 0, 0, 0, 'a', 'b', 'c', 0) // size 3: 3 payload bytes + 1 pad byte
	f, err := readWAVHeader(bytes.NewReader(buildWAV(wavFormatPCM, 1, 48000, 24, make([]byte, 6), list)))
	if err != nil {
		t.Fatal(err)
	}
	if f.channels != 1 || f.sampleRate != 48000 || f.bitsPerSample != 24 {
		t.Fatalf("got %+v", f)
	}
}

func TestReadWAVHeaderRejectsBadInput(t *testing.T) {
	if _, err := readWAVHeader(bytes.NewReader([]byte("NOPEjunkjunk"))); err == nil {
		t.Error("non-RIFF input: want error")
	}
	// IEEE float (format 3) is not integer PCM and must be rejected.
	if _, err := readWAVHeader(bytes.NewReader(buildWAV(0x0003, 2, 44100, 32, make([]byte, 16)))); err == nil {
		t.Error("float WAV: want error")
	}
}

// TestEncodeWAVRoundTrip encodes synthetic PCM through wav2flac's encode path,
// then decodes it back with go-flac's own decoder and checks the PCM is intact.
func TestEncodeWAVRoundTrip(t *testing.T) {
	const frames = 5000
	pcmData := make([]byte, frames*2*2) // 16-bit stereo
	for i := range frames {
		binary.LittleEndian.PutUint16(pcmData[i*4:], uint16(int16(i*7-100)))
		binary.LittleEndian.PutUint16(pcmData[i*4+2:], uint16(int16(-i*3+50)))
	}
	wav := buildWAV(wavFormatPCM, 2, 44100, 16, pcmData)

	outPath := filepath.Join(t.TempDir(), "out.flac")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encodeWAV(bytes.NewReader(wav), out, 5); err != nil {
		_ = out.Close()
		t.Fatalf("encodeWAV: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	dec, err := pcm.NewDecoder(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pcmData) {
		t.Fatalf("round-trip mismatch: decoded %d bytes, want %d", len(got), len(pcmData))
	}
}
