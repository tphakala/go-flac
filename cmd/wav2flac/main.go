// Command wav2flac encodes a PCM WAV file to FLAC using the pure-Go go-flac
// encoder. It is a runnable example of the pcm.Encoder streaming API and the
// go-flac side of the encoder benchmark harness (scripts/bench-encoders.sh),
// so go-flac can be timed from the shell alongside flac, sox, and ffmpeg.
//
// Usage:
//
//	wav2flac [-level N] input.wav output.flac
//
// Only integer PCM WAV input is supported (the common pcm_s16le / s24 / s32
// layouts); IEEE-float WAV is rejected.
package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tphakala/go-flac/pcm"
)

const (
	wavFormatPCM        = 0x0001
	wavFormatExtensible = 0xFFFE
)

// wavFormat holds the stream properties parsed from a WAV header.
type wavFormat struct {
	channels      int
	sampleRate    int
	bitsPerSample int
	dataLen       int64 // PCM data-chunk length in bytes; -1 when streamed/unknown
}

// readWAVHeader consumes the RIFF/WAVE header from r up to the start of the data
// chunk payload, skipping any intervening chunks (with their pad byte). On
// success r is positioned at the first PCM byte and the returned wavFormat
// describes the stream. dataLen is the declared data-chunk size, or -1 when the
// header declares a streamed/unknown size (0 or 0xFFFFFFFF).
func readWAVHeader(r io.Reader) (wavFormat, error) {
	var f wavFormat
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return f, fmt.Errorf("read RIFF header: %w", err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return f, fmt.Errorf("not a RIFF/WAVE file")
	}
	var haveFmt bool
	for {
		var h [8]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return f, fmt.Errorf("read chunk header: %w", err)
		}
		id := string(h[0:4])
		size := binary.LittleEndian.Uint32(h[4:8])
		switch id {
		case "fmt ":
			if size < 16 {
				return f, fmt.Errorf("fmt chunk too small (%d bytes)", size)
			}
			b := make([]byte, size)
			if _, err := io.ReadFull(r, b); err != nil {
				return f, fmt.Errorf("read fmt chunk: %w", err)
			}
			audioFormat := binary.LittleEndian.Uint16(b[0:2])
			if audioFormat != wavFormatPCM && audioFormat != wavFormatExtensible {
				return f, fmt.Errorf("unsupported WAV audio format 0x%04x (only integer PCM)", audioFormat)
			}
			f.channels = int(binary.LittleEndian.Uint16(b[2:4]))
			f.sampleRate = int(binary.LittleEndian.Uint32(b[4:8]))
			f.bitsPerSample = int(binary.LittleEndian.Uint16(b[14:16]))
			haveFmt = true
			if size%2 == 1 { // chunks are word-aligned: skip the pad byte
				if _, err := io.CopyN(io.Discard, r, 1); err != nil {
					return f, err
				}
			}
		case "data":
			if !haveFmt {
				return f, fmt.Errorf("data chunk before fmt chunk")
			}
			f.dataLen = int64(size)
			if size == 0 || size == 0xFFFFFFFF { // streamed/unknown length
				f.dataLen = -1
			}
			return f, nil
		default: // skip unrelated chunks (LIST, fact, etc.) plus any pad byte
			if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
				return f, fmt.Errorf("skip %q chunk: %w", id, err)
			}
			if size%2 == 1 {
				if _, err := io.CopyN(io.Discard, r, 1); err != nil {
					return f, err
				}
			}
		}
	}
}

// encodeWAV reads a WAV stream from in and writes FLAC to out at the given
// compression level. out must be an io.WriteSeeker so the encoder can finalize
// the STREAMINFO MD5 and totals on Close. The caller owns out (encodeWAV does
// not close it).
func encodeWAV(in io.Reader, out io.WriteSeeker, level int) (wavFormat, error) {
	f, err := readWAVHeader(in)
	if err != nil {
		return f, err
	}
	enc, err := pcm.NewEncoder(out, pcm.Config{
		SampleRate:       f.sampleRate,
		Channels:         f.channels,
		BitDepth:         f.bitsPerSample,
		CompressionLevel: level,
	})
	if err != nil {
		return f, fmt.Errorf("new encoder: %w", err)
	}
	src := in
	if f.dataLen >= 0 { // bound the copy to the declared data chunk
		src = io.LimitReader(in, f.dataLen)
	}
	if _, err := io.Copy(enc, src); err != nil {
		return f, fmt.Errorf("encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return f, fmt.Errorf("close: %w", err)
	}
	return f, nil
}

func run(inPath, outPath string, level int) error {
	in, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	// Pass the *os.File (io.WriteSeeker) so Close finalizes STREAMINFO; close it
	// after the encoder has finished its seek-back, even on the error path.
	f, encErr := encodeWAV(bufio.NewReaderSize(in, 1<<20), out, level)
	closeErr := out.Close()
	if encErr != nil {
		return encErr
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Fprintf(os.Stderr, "wav2flac: %dch %dHz %d-bit level%d -> %s\n",
		f.channels, f.sampleRate, f.bitsPerSample, level, outPath)
	return nil
}

func main() {
	level := flag.Int("level", 5, "compression level 0..8")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: wav2flac [-level N] input.wav output.flac")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Arg(0), flag.Arg(1), *level); err != nil {
		fmt.Fprintln(os.Stderr, "wav2flac:", err)
		os.Exit(1)
	}
}
