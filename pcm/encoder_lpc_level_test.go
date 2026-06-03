package pcm

import "testing"

func TestParamsForLevelLPC(t *testing.T) {
	type want struct {
		maxLPC int
		prec   int
	}
	table := map[int]want{
		0: {0, 0},
		1: {0, 0},
		2: {0, 0},
		3: {6, 15},
		4: {8, 15},
		5: {8, 15},
		6: {8, 15},
		7: {12, 15},
		8: {12, 15},
	}
	for level, w := range table {
		p := paramsForLevel(level)
		if p.MaxLPCOrder != w.maxLPC {
			t.Errorf("level %d MaxLPCOrder = %d, want %d", level, p.MaxLPCOrder, w.maxLPC)
		}
		if w.maxLPC > 0 && p.LPCPrecision != w.prec {
			t.Errorf("level %d LPCPrecision = %d, want %d", level, p.LPCPrecision, w.prec)
		}
	}
}

func TestParamsForLevelLowLevelsStayFixed(t *testing.T) {
	for level := 0; level <= 2; level++ {
		if paramsForLevel(level).MaxLPCOrder != 0 {
			t.Errorf("level %d enabled LPC, must stay fixed-only", level)
		}
	}
}
