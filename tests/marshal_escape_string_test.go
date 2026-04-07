package tests

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/venc"
)

// TestMarshal_EscapeString_Prescan exercises the VM's prescan-based buffer
// sizing for string escaping.  The prescan (vj_prescan_string_escaped_len)
// computes a tight upper bound; if it ever underestimates, the VM writes
// past the buffer end — which will be caught by -race, vjgcstress, or by
// output mismatch against encoding/json.

type escStringWrap struct {
	V string `json:"v"`
}

// ---------- helpers ----------

// repeatByte builds a string of n copies of byte b.
func repeatByte(b byte, n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = b
	}
	return string(buf)
}

// mixedString builds a string of length n where ~ratio of the bytes are the
// given escape char and the rest are 'a'.
func mixedString(esc byte, n int, ratio float64) string {
	buf := make([]byte, n)
	for i := range buf {
		if float64(i)/float64(n) < ratio {
			buf[i] = esc
		} else {
			buf[i] = 'a'
		}
	}
	return string(buf)
}

// allControlChars returns a string containing 0x00-0x1F.
func allControlChars() string {
	var buf [32]byte
	for i := range buf {
		buf[i] = byte(i)
	}
	return string(buf[:])
}

// randomEscapeString builds a string of length n with random bytes that need
// escaping, interspersed with safe ASCII.
func randomEscapeString(rng *rand.Rand, n int) string {
	// Characters that require escaping in JSON (ASCII only — no invalid UTF-8
	// bytes, which would cause round-trip mismatches in fast mode).
	escapeChars := []byte{
		0x00, 0x01, 0x08, 0x09, 0x0a, 0x0c, 0x0d, 0x1f, // control chars
		'"', '\\', // mandatory JSON escapes
		'<', '>', '&', // HTML escapes (only effective in stdcompat mode)
	}
	buf := make([]byte, n)
	for i := range buf {
		if rng.IntN(3) == 0 {
			buf[i] = escapeChars[rng.IntN(len(escapeChars))]
		} else {
			buf[i] = 'a' + byte(rng.IntN(26))
		}
	}
	return string(buf)
}

// ---------- mode definitions ----------

type marshalMode struct {
	name    string
	vjOpts  []venc.MarshalOption
	indent  bool                      // use MarshalIndent path
	stdFunc func(any) ([]byte, error) // nil = verify via round-trip
}

// buildModes generates test modes for each escape config × buffer size.
// Small buffer sizes force the VM into BufFull / prescan paths.
func buildModes() []marshalMode {
	type escCfg struct {
		name    string
		opts    []venc.MarshalOption
		indent  bool
		stdFunc func(any) ([]byte, error)
	}
	cfgs := []escCfg{
		{"fast", nil, false, nil},
		{"stdcompat", []venc.MarshalOption{venc.WithStdCompat()}, false,
			func(v any) ([]byte, error) { return json.Marshal(v) }},
		{"stdcompat_indent", []venc.MarshalOption{venc.WithStdCompat()}, true,
			func(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }},
	}

	// Buffer sizes: 0 = default (32KB), plus small sizes that force
	// the VM to work with tight buffers.
	bufSizes := []int{0, 64, 256, 1024}

	var modes []marshalMode
	for _, cfg := range cfgs {
		for _, bs := range bufSizes {
			name := cfg.name
			opts := append([]venc.MarshalOption{}, cfg.opts...)
			if bs > 0 {
				name += "_buf" + itoa(bs)
				opts = append(opts, venc.WithBufSize(bs))
			}
			modes = append(modes, marshalMode{
				name:    name,
				vjOpts:  opts,
				indent:  cfg.indent,
				stdFunc: cfg.stdFunc,
			})
		}
	}
	return modes
}

var marshalModes = buildModes()

// ---------- verification ----------

func verifyEscapeString(t *testing.T, name string, input string, mode marshalMode) {
	t.Helper()
	wrap := &escStringWrap{V: input}

	var vjOut []byte
	var err error
	if mode.indent {
		vjOut, err = vjson.MarshalIndent(wrap, "", "  ", mode.vjOpts...)
	} else {
		vjOut, err = vjson.Marshal(wrap, mode.vjOpts...)
	}
	if err != nil {
		t.Fatalf("[%s/%s] vjson.Marshal error: %v", name, mode.name, err)
	}

	if mode.stdFunc != nil {
		stdOut, err := mode.stdFunc(wrap)
		if err != nil {
			t.Fatalf("[%s/%s] json.Marshal error: %v", name, mode.name, err)
		}
		if string(vjOut) != string(stdOut) {
			// Show a useful diff for short strings; truncate for long ones.
			vjStr, stdStr := string(vjOut), string(stdOut)
			if len(vjStr) > 200 {
				vjStr = vjStr[:200] + "..."
			}
			if len(stdStr) > 200 {
				stdStr = stdStr[:200] + "..."
			}
			t.Errorf("[%s/%s] mismatch:\n  velox: %s\n  std:   %s", name, mode.name, vjStr, stdStr)
		}
	} else {
		// Fast mode: verify round-trip.
		var got escStringWrap
		if err := json.Unmarshal(vjOut, &got); err != nil {
			t.Fatalf("[%s/%s] round-trip unmarshal error: %v\n  output: %s", name, mode.name, err, vjOut)
		}
		if got.V != input {
			t.Errorf("[%s/%s] round-trip mismatch:\n  input:  %q\n  got:    %q", name, mode.name, truncStr(input, 100), truncStr(got.V, 100))
		}
	}
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------- table-driven test ----------

func TestMarshal_EscapeString_Prescan(t *testing.T) {
	type testCase struct {
		name string
		s    string
	}

	var cases []testCase

	// Category 1: Pure ASCII escape patterns
	cases = append(cases,
		testCase{"empty", ""},
		testCase{"single_quote", `"`},
		testCase{"single_backslash", `\`},
		testCase{"single_null", "\x00"},
		testCase{"single_newline", "\n"},
		testCase{"all_control_chars", allControlChars()},
		testCase{"dense_quotes_short", repeatByte('"', 64)},
		testCase{"dense_backslash_short", repeatByte('\\', 64)},
		testCase{"dense_null_short", repeatByte(0x00, 64)},
		testCase{"mix_10pct_quote", mixedString('"', 256, 0.10)},
		testCase{"mix_50pct_quote", mixedString('"', 256, 0.50)},
		testCase{"mix_90pct_quote", mixedString('"', 256, 0.90)},
		testCase{"mix_50pct_ctrl", mixedString(0x01, 256, 0.50)},
	)

	// Category 2: HTML escapes
	cases = append(cases,
		testCase{"dense_lt", repeatByte('<', 128)},
		testCase{"dense_gt", repeatByte('>', 128)},
		testCase{"dense_amp", repeatByte('&', 128)},
		testCase{"html_mixed", strings.Repeat("<a>&b</a>", 50)},
		testCase{"html_and_ctrl", mixedString('<', 256, 0.3) + mixedString(0x0a, 256, 0.3)},
	)

	// Category 3: Non-ASCII / UTF-8
	cases = append(cases,
		testCase{"cjk", strings.Repeat("\u4e2d\u6587", 200)},
		testCase{"emoji", strings.Repeat("\U0001F600", 200)},
		testCase{"line_term_2028", strings.Repeat("abc\u2028def", 100)},
		testCase{"line_term_2029", strings.Repeat("abc\u2029def", 100)},
		testCase{"mixed_cjk_escape", strings.Repeat("\u4e2d\"\\<\n", 200)},
	)

	// Invalid UTF-8 cases — only testable in stdcompat mode (fast mode
	// passes raw bytes through, causing round-trip mismatch with json.Unmarshal).
	invalidUTF8Cases := []testCase{
		{"invalid_utf8_0xFF", strings.Repeat("abc\xffdef", 100)},
		{"invalid_utf8_truncated", strings.Repeat("abc\xc0def", 100)},
		{"lone_surrogate", strings.Repeat("abc\xed\xa0\x80def", 80)},
	}

	// Category 4: Long strings (trigger prescan code path)
	for _, size := range []int{512, 1024, 4096, 8192, 16384} {
		cases = append(cases,
			testCase{
				name: "long_safe_" + itoa(size),
				s:    repeatByte('x', size),
			},
			testCase{
				name: "long_dense_quote_" + itoa(size),
				s:    repeatByte('"', size),
			},
			testCase{
				name: "long_dense_ctrl_" + itoa(size),
				s:    repeatByte(0x01, size),
			},
			testCase{
				name: "long_mix50_quote_" + itoa(size),
				s:    mixedString('"', size, 0.50),
			},
			testCase{
				name: "long_html_dense_" + itoa(size),
				s:    mixedString('<', size, 0.50),
			},
			testCase{
				name: "long_lineterm_" + itoa(size),
				s:    strings.Repeat("abc\u2028", size/6),
			},
		)
	}

	// Category 5: SIMD boundary sizes
	for _, size := range []int{15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65} {
		cases = append(cases,
			testCase{
				name: "boundary_quote_" + itoa(size),
				s:    repeatByte('"', size),
			},
			testCase{
				name: "boundary_mix_" + itoa(size),
				s:    mixedString(0x01, size, 0.50),
			},
		)
	}

	for _, tc := range cases {
		for _, mode := range marshalModes {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				verifyEscapeString(t, tc.name, tc.s, mode)
			})
		}
	}

	// Invalid UTF-8 cases — only stdcompat modes (fast mode passes raw bytes).
	for _, tc := range invalidUTF8Cases {
		for _, mode := range marshalModes {
			if mode.stdFunc == nil {
				continue // skip fast mode
			}
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				verifyEscapeString(t, tc.name, tc.s, mode)
			})
		}
	}
}

// ---------- randomized stress test ----------

func TestMarshal_EscapeString_Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 0))

	const iterations = 500
	sizes := []int{1, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 255, 256, 511, 512, 1023, 1024, 2048, 4096, 8192}

	for i := range iterations {
		size := sizes[rng.IntN(len(sizes))]
		s := randomEscapeString(rng, size)

		for _, mode := range marshalModes {
			name := "rand_" + itoa(i) + "_len" + itoa(size)
			verifyEscapeString(t, name, s, mode)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
