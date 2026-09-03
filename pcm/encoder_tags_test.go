package pcm

import (
	"bytes"
	"strings"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/meta"
)

// Field names and the FLAC marker recur across the tag test cases; naming them keeps
// the repeated literals in one place.
const (
	tagArtist  = "ARTIST"
	tagTitle   = "TITLE"
	flacMarker = "fLaC"
)

// metaBlockTypes walks a native FLAC stream's metadata region and returns the block
// type of each block in order, stopping at the first block whose last-block flag is
// set. It fails the test if the blocks run past the end of the stream (a missing
// terminator walks into the audio and overruns). Callers pin the exact block sequence
// themselves, which is what catches a misplaced or premature terminator.
func metaBlockTypes(t *testing.T, stream []byte) []int {
	t.Helper()
	if len(stream) < 4 || string(stream[:4]) != flacMarker {
		t.Fatalf("stream does not start with fLaC marker")
	}
	var types []int
	off := 4
	for {
		if off+4 > len(stream) {
			t.Fatalf("metadata blocks overrun stream")
		}
		last := stream[off]&0x80 != 0
		btype := int(stream[off] & 0x7F)
		length := int(stream[off+1])<<16 | int(stream[off+2])<<8 | int(stream[off+3])
		types = append(types, btype)
		off += 4 + length
		if last {
			break
		}
	}
	return types
}

func TestEncoderTagsRoundTripNonSeekable(t *testing.T) {
	cfg := Config{
		SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 5,
		Tags: []flac.Tag{
			{Name: tagTitle, Value: "Detection clip"},
			{Name: tagArtist, Value: "Turdus merula"},
			{Name: "COMMENT", Value: "confidence=0.87"},
		},
	}
	pcm := genPCM(cfg, 4096*2+321)

	var buf bytes.Buffer // bytes.Buffer is not seekable: exercises the non-seekable path
	enc, err := NewEncoder(&buf, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// STREAMINFO first, then VORBIS_COMMENT (type 4), and no other blocks.
	types := metaBlockTypes(t, buf.Bytes())
	if len(types) != 2 || types[0] != 0 || types[1] != meta.TypeVorbisComment {
		t.Fatalf("metadata block types = %v, want [STREAMINFO, VORBIS_COMMENT]", types)
	}

	d, err := NewDecoder(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if got, want := d.Vendor(), defaultVendor; got != want {
		t.Errorf("Vendor() = %q, want the default %q", got, want)
	}
	gotTags := d.Tags()
	if len(gotTags) != len(cfg.Tags) {
		t.Fatalf("Tags() = %v, want %v", gotTags, cfg.Tags)
	}
	for i, want := range cfg.Tags {
		if gotTags[i] != want {
			t.Errorf("Tags()[%d] = %+v, want %+v", i, gotTags[i], want)
		}
	}
	// The audio still round-trips byte-for-byte. (A non-seekable sink leaves the
	// STREAMINFO MD5 at the zero sentinel, so the decoder skips the MD5 check here;
	// bytes.Equal below is what validates the audio. The seekable path is MD5-checked
	// in TestEncoderTagsWithSeekTable.)
	var out bytes.Buffer
	if _, err := d.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if !bytes.Equal(out.Bytes(), pcm) {
		t.Errorf("decoded PCM differs from input (%d vs %d bytes)", out.Len(), len(pcm))
	}
}

func TestEncoderTagsWithSeekTable(t *testing.T) {
	cfg := Config{
		SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 5,
		SeekTableInterval: 4096, SeekTableMaxPoints: 64,
		Vendor: "custom-vendor",
		Tags:   []flac.Tag{{Name: tagTitle, Value: "seeky"}},
	}
	pcm := genPCM(cfg, 4096*4+100)

	var sb seekBuffer
	enc, err := NewEncoder(&sb, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Ordering: STREAMINFO, VORBIS_COMMENT, SEEKTABLE, PADDING. VORBIS_COMMENT must
	// come before SEEKTABLE so the Close-time SEEKTABLE/PADDING rewrite stays valid.
	types := metaBlockTypes(t, sb.Bytes())
	want := []int{0, meta.TypeVorbisComment, meta.TypeSeekTable, meta.TypePadding}
	if len(types) != len(want) {
		t.Fatalf("metadata block types = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("metadata block types = %v, want %v", types, want)
		}
	}

	// Seek offsets and tags both survive: a wrong seekBodyOff would corrupt the
	// SEEKTABLE relative to the audio and break the seek.
	d, err := NewDecoder(bytes.NewReader(sb.Bytes()))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if got := d.Vendor(); got != cfg.Vendor {
		t.Errorf("Vendor() = %q, want %q", got, cfg.Vendor)
	}
	if got := d.Tags(); len(got) != 1 || got[0] != cfg.Tags[0] {
		t.Errorf("Tags() = %v, want %v", got, cfg.Tags)
	}

	// Full decode on a fresh decoder: the seekable sink finalizes a real STREAMINFO
	// MD5, so WriteTo runs the MD5 check end to end with a VORBIS_COMMENT block present.
	dfull, err := NewDecoder(bytes.NewReader(sb.Bytes()))
	if err != nil {
		t.Fatalf("NewDecoder (full): %v", err)
	}
	var out bytes.Buffer
	if _, err := dfull.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if !bytes.Equal(out.Bytes(), pcm) {
		t.Errorf("decoded PCM differs from input (%d vs %d bytes)", out.Len(), len(pcm))
	}

	target := int64(4096*2 + 500)
	landed, err := d.SeekToSample(target)
	if err != nil {
		t.Fatalf("SeekToSample(%d): %v", target, err)
	}
	if landed > target {
		t.Fatalf("SeekToSample landed at %d, past the target %d", landed, target)
	}
	got := make([]byte, 4) // one stereo 16-bit sample
	if _, err := d.Read(got); err != nil {
		t.Fatalf("Read after seek: %v", err)
	}
	frameLen := (cfg.BitDepth / 8) * cfg.Channels
	wantSample := pcm[landed*int64(frameLen) : landed*int64(frameLen)+4]
	if !bytes.Equal(got, wantSample) {
		t.Errorf("sample at %d = % x, want % x", landed, got, wantSample)
	}
}

// TestEncoderTagsSeekableNoSeekTable covers the seekable-sink + tags + no-seektable
// layout: STREAMINFO (last=0) followed by VORBIS_COMMENT (last=1), where Close patches
// only the STREAMINFO body at offset 8 and must leave the VC block untouched.
func TestEncoderTagsSeekableNoSeekTable(t *testing.T) {
	cfg := Config{
		SampleRate: 48000, Channels: 2, BitDepth: 16, CompressionLevel: 5,
		Tags: []flac.Tag{{Name: tagTitle, Value: "x"}},
	}
	pcm := genPCM(cfg, 4096+10)

	var sb seekBuffer
	enc, err := NewEncoder(&sb, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	types := metaBlockTypes(t, sb.Bytes())
	if len(types) != 2 || types[0] != 0 || types[1] != meta.TypeVorbisComment {
		t.Fatalf("metadata block types = %v, want [STREAMINFO, VORBIS_COMMENT]", types)
	}
	// decodeAll's WriteTo runs the MD5 check (seekable sink finalized a real MD5).
	si, got := decodeAll(t, bytes.NewReader(sb.Bytes()))
	if !bytes.Equal(got, pcm) {
		t.Errorf("decoded PCM differs from input (%d vs %d bytes)", len(got), len(pcm))
	}
	var zero [16]byte
	if si.MD5 == zero {
		t.Error("seekable encode left a zero STREAMINFO MD5")
	}
	d, err := NewDecoder(bytes.NewReader(sb.Bytes()))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if g := d.Tags(); len(g) != 1 || g[0] != cfg.Tags[0] {
		t.Errorf("Tags() = %v, want %v", g, cfg.Tags)
	}
}

// TestDecoderToleratesMalformedVorbisComment pins the decode-side leniency: a
// VORBIS_COMMENT block with a valid outer length but an internally malformed body is
// skipped and the audio still decodes. Before type 4 was parsed at all it was skipped
// wholesale, so failing the decode here would be a backward-compatibility regression.
func TestDecoderToleratesMalformedVorbisComment(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 5}
	pcm := genPCM(cfg, 4096+50)

	var buf bytes.Buffer
	if err := EncodeInterleaved(&buf, cfg, pcm); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}
	stream := buf.Bytes()
	// A no-tags stream is "fLaC" + STREAMINFO (last=1: 4-byte header + 34-byte body) + audio.
	if string(stream[:4]) != flacMarker || stream[4] != 0x80 {
		t.Fatalf("unexpected layout: marker %q, first header byte %#x", stream[:4], stream[4])
	}
	siHeaderAndBody := stream[4:42]
	audio := stream[42:]

	// Malformed body: vendor_length = 0xFFFFFFFF, far past the 8-byte body, so
	// parseVorbisComment rejects it. The outer 24-bit block length (8) is valid.
	badVC := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0}

	var spliced bytes.Buffer
	spliced.WriteString(flacMarker)
	hdr := bytes.Clone(siHeaderAndBody)
	hdr[0] &^= 0x80 // STREAMINFO is no longer the last block
	spliced.Write(hdr)
	spliced.Write(meta.EncodeBlockHeader(true, meta.TypeVorbisComment, len(badVC))) // last=1
	spliced.Write(badVC)
	spliced.Write(audio)

	d, err := NewDecoder(bytes.NewReader(spliced.Bytes()))
	if err != nil {
		t.Fatalf("NewDecoder rejected a stream with a malformed VORBIS_COMMENT: %v", err)
	}
	if got := d.Tags(); got != nil {
		t.Errorf("Tags() = %v, want nil (malformed block skipped)", got)
	}
	var out bytes.Buffer
	if _, err := d.WriteTo(&out); err != nil { // MD5 is non-zero here, so this verifies it too
		t.Fatalf("WriteTo: %v", err)
	}
	if !bytes.Equal(out.Bytes(), pcm) {
		t.Errorf("decoded PCM differs from input after skipping a malformed tag block")
	}
}

func TestEncoderNoTagsWritesNoVorbisComment(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 5}
	pcm := genPCM(cfg, 4096+7)

	var buf bytes.Buffer
	enc, _ := NewEncoder(&buf, cfg)
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A default Config carries no tags and no vendor, so no VORBIS_COMMENT block is
	// written: the only metadata block is STREAMINFO. This is what keeps the default
	// output byte-identical to a stream with no tags.
	types := metaBlockTypes(t, buf.Bytes())
	if len(types) != 1 || types[0] != 0 {
		t.Fatalf("metadata block types = %v, want [STREAMINFO] only", types)
	}

	d, err := NewDecoder(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if d.Vendor() != "" {
		t.Errorf("Vendor() = %q, want empty", d.Vendor())
	}
	if d.Tags() != nil {
		t.Errorf("Tags() = %v, want nil", d.Tags())
	}
}

func TestEncoderTagsOneShot(t *testing.T) {
	cfg := Config{
		SampleRate: 48000, Channels: 1, BitDepth: 16, CompressionLevel: 3,
		Tags: []flac.Tag{{Name: tagArtist, Value: "A"}, {Name: tagArtist, Value: "B"}},
	}
	pcm := genPCM(cfg, 5000)

	var buf bytes.Buffer
	if err := EncodeInterleaved(&buf, cfg, pcm); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}
	d, err := NewDecoder(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	// Duplicate field names are legal in Vorbis and must survive in order.
	got := d.Tags()
	if len(got) != 2 || got[0] != cfg.Tags[0] || got[1] != cfg.Tags[1] {
		t.Fatalf("Tags() = %v, want the two ARTIST tags in order", got)
	}
}

func TestEncoderRejectsInvalidTagName(t *testing.T) {
	base := Config{SampleRate: 44100, Channels: 1, BitDepth: 16}
	bad := []flac.Tag{
		{Name: "HAS=EQUALS", Value: "x"},
		{Name: "space bar", Value: "x"}, // 0x20 space is allowed; this is fine, kept to contrast
		{Name: "", Value: "x"},
		{Name: "TAB\tNAME", Value: "x"},
		{Name: "unïcode", Value: "x"},
	}
	// The space-containing name is actually valid (0x20 is in range), so it must NOT
	// be rejected; remove it from the reject set and check it passes.
	rejects := []flac.Tag{bad[0], bad[2], bad[3], bad[4]}
	for _, tag := range rejects {
		cfg := base
		cfg.Tags = []flac.Tag{tag}
		if _, err := NewEncoder(&bytes.Buffer{}, cfg); err == nil {
			t.Errorf("NewEncoder accepted invalid tag name %q", tag.Name)
		}
	}
	okCfg := base
	okCfg.Tags = []flac.Tag{bad[1]}
	if _, err := NewEncoder(&bytes.Buffer{}, okCfg); err != nil {
		t.Errorf("NewEncoder rejected a valid space-containing tag name: %v", err)
	}
}

func TestEncoderRejectsOversizedVorbisComment(t *testing.T) {
	// A single tag value larger than the 24-bit metadata block length cannot be framed
	// and must be rejected rather than silently truncating the block header.
	huge := strings.Repeat("x", meta.MaxMetadataBodyLen)
	cfg := Config{
		SampleRate: 44100, Channels: 1, BitDepth: 16,
		Tags: []flac.Tag{{Name: "BIG", Value: huge}},
	}
	if _, err := NewEncoder(&bytes.Buffer{}, cfg); err == nil {
		t.Fatal("expected rejection of a VORBIS_COMMENT block exceeding the 24-bit length")
	}
}
