package frame

import "testing"

func TestNewWorkspaceSizing(t *testing.T) {
	ws := NewWorkspace(4096, 2, 12)
	if len(ws.side) != 4096 || len(ws.mid) != 4096 {
		t.Fatalf("side/mid not sized to max block: %d/%d", len(ws.side), len(ws.mid))
	}
	if len(ws.side64) != 4096 || len(ws.mid64) != 4096 {
		t.Fatalf("side64/mid64 not sized: %d/%d", len(ws.side64), len(ws.mid64))
	}
}
