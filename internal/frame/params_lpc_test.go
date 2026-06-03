package frame

import "testing"

func TestParamsLPCDefaults(t *testing.T) {
	// ApodTukey05 must be the zero value so a zero Params defaults to it.
	if ApodTukey05 != 0 {
		t.Fatalf("ApodTukey05 = %d, want 0", ApodTukey05)
	}
	var p Params
	if p.MaxLPCOrder != 0 {
		t.Fatalf("zero Params MaxLPCOrder = %d, want 0 (fixed only)", p.MaxLPCOrder)
	}
	if p.LPCPrecision != 0 {
		t.Fatalf("zero Params LPCPrecision = %d, want 0", p.LPCPrecision)
	}
	if p.Apodization != ApodTukey05 {
		t.Fatalf("zero Params Apodization = %d, want ApodTukey05", p.Apodization)
	}
}
