package vjson

import "testing"

func TestFindQuoteOrBackslash(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		idx      int
		wantIdx  int
		wantChar byte
	}{
		{"quote at start", `"hello"`, 1, 6, '"'},
		{"backslash", `hello\"world`, 0, 5, '\\'},
		{"control char", "hello\nworld", 0, 5, '\n'},
		{"empty", `"`, 1, 1, 0},
		{"long no special", "abcdefghijklmnopqrstuvwxyz\"", 0, 26, '"'},
		{"long with escape", "abcdefghijklmnopq\\rst", 0, 17, '\\'},
		{"8 byte boundary", "12345678\"", 0, 8, '"'},
		{"ctrl in position 7", "1234567\x01rest", 0, 7, 1},
		{"all plain 16", "0123456789abcdef\"", 0, 16, '"'},
		{"null byte", "hello\x00world", 0, 5, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIdx, gotChar := findQuoteOrBackslash([]byte(tt.src), tt.idx)
			if gotIdx != tt.wantIdx || gotChar != tt.wantChar {
				t.Errorf("findQuoteOrBackslash(%q, %d) = (%d, %d), want (%d, %d)",
					tt.src, tt.idx, gotIdx, gotChar, tt.wantIdx, tt.wantChar)
			}
		})
	}
}
