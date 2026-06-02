package frame

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
)

// buildOneFrame builds a complete 2-channel independent frame, blocksize 2,
// 16-bit, both channels CONSTANT (values 100 and -100), frame number 0.
func buildOneFrame() []byte {
	// blocksize code 0b0111 (=7 -> 16-bit blocksize-1 at end), sr 0 (streaminfo)
	// 0x70 = 0b0111_0000 -> bsCode=7, srCode=0.
	// 0x10 = 0b0001_0000 -> chAssign=1 (2 ch), bpsCode=000, reserved 0.
	// frame number byte 0x00.
	// blocksize-1 = 1 (two samples) -> 16 bits 0x0001 appended before CRC-8.
	hdr := make([]byte, 8)
	copy(hdr, []byte{0xFF, 0xF8, 0x70, 0x10, 0x00, 0x00, 0x01})
	hdr[7] = crc.Checksum8(hdr[:7])

	w := &bitWriter{}
	// channel 0: constant 100
	w.put(0, 1)
	w.put(0, 6)
	w.put(0, 1)
	w.put(uint64(uint16(100)), 16)
	// channel 1: constant -100
	w.put(0, 1)
	w.put(0, 6)
	w.put(0, 1)
	neg100 := int16(-100)
	w.put(uint64(uint16(neg100)), 16)
	body := w.bytes()

	all := make([]byte, 0, len(hdr)+len(body)+2)
	all = append(all, hdr...)
	all = append(all, body...)
	c16 := crc.Checksum16(all)
	all = append(all, byte(c16>>8), byte(c16))
	return all
}

func TestDecodeOneFrame(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	br := bitio.NewReader(bytes.NewReader(buildOneFrame()))
	var fr Frame
	if err := Decode(br, si, &fr); err != nil {
		t.Fatal(err)
	}
	if fr.BlockSize != 2 || len(fr.Channels) != 2 {
		t.Fatalf("bs=%d nch=%d", fr.BlockSize, len(fr.Channels))
	}
	if fr.Channels[0][0] != 100 || fr.Channels[1][0] != -100 {
		t.Fatalf("samples %d %d", fr.Channels[0][0], fr.Channels[1][0])
	}
}
