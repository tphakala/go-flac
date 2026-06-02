package frame

// Frame holds one decoded FLAC frame. Channels is reused across Decode calls.
type Frame struct {
	BlockSize     int
	SampleRate    int
	BitsPerSample int
	Channels      [][]int32 // len == number of channels; each len == BlockSize
	Number        uint64    // sample number (variable blocksize) or frame number (fixed)
}

// header holds the parsed frame header.
type header struct {
	variableBlockSize bool
	blockSize         int
	sampleRate        int
	channelAssignment int
	bitsPerSample     int
	number            uint64
}

// channels returns the channel count implied by the channel assignment.
func (h *header) channels() int {
	switch h.channelAssignment {
	case 8, 9, 10: // left/side, right/side, mid/side
		return 2
	default:
		return h.channelAssignment + 1
	}
}
