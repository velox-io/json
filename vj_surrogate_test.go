package vjson

import (
	"testing"
)

func TestSurrogatePair(t *testing.T) {
	// JSON uses UTF-16 surrogate pairs for characters outside BMP
	// \uD83D\uDE00 should decode to U+1F600

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Valid surrogate pairs
		{"emoji grin", `\uD83D\uDE00`, "\U0001F600"},
		{"emoji heart", `\uD83D\uDC95`, "\U0001F495"},
		{"emoji rocket", `\uD83D\uDE80`, "\U0001F680"},

		// Isolated surrogates (invalid in UTF-8, should be replacement char)
		{"isolated high surrogate", `\uD83D`, "\ufffd"},
		{"isolated low surrogate", `\uDE00`, "\ufffd"},

		// Surrogate in context
		{"emoji in string", `Hello \uD83D\uDE00 World`, "Hello \U0001F600 World"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := testUnescape(tt.input)

			if got != tt.expected {
				t.Errorf("unescape(%q) = %q (bytes: %x), want %q (bytes: %x)",
					tt.input, got, []byte(got), tt.expected, []byte(tt.expected))
			}
		})
	}
}
