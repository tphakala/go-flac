package meta

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	flac "github.com/tphakala/go-flac"
)

func TestEncodeVorbisCommentLayout(t *testing.T) {
	vendor := "go-flac 1.1.0"
	comments := []string{"TITLE=Song", "ARTIST=A"}
	body := EncodeVorbisComment(vendor, comments)

	// vendor_length (LE u32) + vendor + count (LE u32) + per comment (LE u32 len + bytes).
	off := 0
	if got := binary.LittleEndian.Uint32(body[off:]); got != uint32(len(vendor)) {
		t.Fatalf("vendor_length = %d, want %d", got, len(vendor))
	}
	off += 4
	if got := string(body[off : off+len(vendor)]); got != vendor {
		t.Fatalf("vendor = %q, want %q", got, vendor)
	}
	off += len(vendor)
	if got := binary.LittleEndian.Uint32(body[off:]); got != uint32(len(comments)) {
		t.Fatalf("comment count = %d, want %d", got, len(comments))
	}
	off += 4
	for _, c := range comments {
		if got := binary.LittleEndian.Uint32(body[off:]); got != uint32(len(c)) {
			t.Fatalf("comment length = %d, want %d", got, len(c))
		}
		off += 4
		if got := string(body[off : off+len(c)]); got != c {
			t.Fatalf("comment = %q, want %q", got, c)
		}
		off += len(c)
	}
	// FLAC omits the trailing framing bit: the body ends exactly at the last comment.
	if off != len(body) {
		t.Fatalf("body has %d trailing bytes past the last comment; FLAC uses no framing bit", len(body)-off)
	}
}

func TestVorbisCommentRoundTrip(t *testing.T) {
	cases := []struct {
		name     string
		vendor   string
		comments []string
	}{
		{"empty", "", nil},
		{"vendor only", "go-flac", nil},
		{"typical", "go-flac 1.1.0", []string{"TITLE=T", "ARTIST=X"}},
		{"duplicate keys", "v", []string{"ARTIST=A", "ARTIST=B"}},
		{"utf8 value", "v", []string{"TITLE=Sång=with=equals"}},
		{"empty value", "v", []string{"COMMENT="}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := EncodeVorbisComment(tc.vendor, tc.comments)
			gotVendor, gotComments, err := parseVorbisComment(body)
			if err != nil {
				t.Fatalf("parseVorbisComment: %v", err)
			}
			if gotVendor != tc.vendor {
				t.Errorf("vendor = %q, want %q", gotVendor, tc.vendor)
			}
			// A nil and an empty slice are equivalent for our purposes.
			if len(gotComments) != len(tc.comments) {
				t.Fatalf("comments = %v, want %v", gotComments, tc.comments)
			}
			if len(tc.comments) > 0 && !reflect.DeepEqual(gotComments, tc.comments) {
				t.Errorf("comments = %v, want %v", gotComments, tc.comments)
			}
		})
	}
}

func TestParseVorbisCommentRejectsTruncation(t *testing.T) {
	full := EncodeVorbisComment("go-flac", []string{"TITLE=T", "ARTIST=X"})
	// Every strict prefix is truncated somewhere and must be rejected rather than
	// panicking on an out-of-range slice.
	for cut := range len(full) {
		if _, _, err := parseVorbisComment(full[:cut]); err == nil {
			t.Errorf("parseVorbisComment(prefix len %d) = nil error, want rejection", cut)
		} else if !errors.Is(err, flac.ErrUnsupported) {
			t.Errorf("parseVorbisComment(prefix len %d) err = %v, want ErrUnsupported", cut, err)
		}
	}
}

func TestParseVorbisCommentRejectsHugeCount(t *testing.T) {
	// A body declaring a huge comment count but carrying no comment bodies must be
	// rejected before the make, not drive a multi-gigabyte allocation.
	body := make([]byte, 4+0+4)
	binary.LittleEndian.PutUint32(body[0:], 0)          // vendor_length = 0
	binary.LittleEndian.PutUint32(body[4:], 0xFFFFFFFF) // count = 4 billion
	// Assert the specific sentinel, not just err != nil: without the count guard this
	// would attempt a ~68 GB make and crash the process rather than reject cleanly, so
	// pinning ErrUnsupported proves the rejection is the guard's decision.
	if _, _, err := parseVorbisComment(body); !errors.Is(err, flac.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for an impossible comment count, got %v", err)
	}
}
