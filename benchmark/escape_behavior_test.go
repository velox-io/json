package benchmark

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Helper: detect escape behavior from marshal output
// =============================================================================

type escapeResult struct {
	Escaped     bool   // true if the character was escaped (e.g. \u003c)
	PassThrough bool   // true if the raw character is present unescaped
	Desc        string // human-readable description
}

func detectHTML(s string) escapeResult {
	escaped := strings.Contains(s, `\u003c`) && strings.Contains(s, `\u003e`) && strings.Contains(s, `\u0026`)
	raw := strings.Contains(s, `<`) && strings.Contains(s, `>`) && strings.Contains(s, `&`)
	if escaped {
		return escapeResult{Escaped: true, Desc: "ON — escapes <, >, & to \\uXXXX"}
	}
	if raw {
		return escapeResult{PassThrough: true, Desc: "OFF — raw <, >, & pass through"}
	}
	return escapeResult{Desc: "UNKNOWN"}
}

func detectLineTerminators(s string) escapeResult {
	escapedLS := strings.Contains(s, `\u2028`)
	escapedPS := strings.Contains(s, `\u2029`)
	// Raw bytes: U+2028 = E2 80 A8, U+2029 = E2 80 A9
	// Note: if escaped form \u2028 is present, the raw UTF-8 bytes of
	// the literal backslash-u sequence will NOT match "\u2028" (Go interprets
	// "\u2028" in string literal as the actual codepoint). So checking raw
	// after escaped is safe.
	rawLS := strings.Contains(s, "\u2028")
	rawPS := strings.Contains(s, "\u2029")

	if escapedLS && escapedPS {
		return escapeResult{Escaped: true, Desc: "ON — escapes U+2028, U+2029 to \\uXXXX"}
	}
	if rawLS || rawPS {
		return escapeResult{PassThrough: true, Desc: "OFF — raw U+2028 / U+2029 pass through"}
	}
	return escapeResult{Desc: "UNKNOWN"}
}

func detectInvalidUTF8(s string, err error) escapeResult {
	if err != nil {
		return escapeResult{Desc: "ERROR — returns error: " + err.Error()}
	}
	if strings.Contains(s, `\ufffd`) {
		return escapeResult{Escaped: true, Desc: "REPLACE — invalid UTF-8 → \\ufffd"}
	}
	if strings.Contains(s, "\xff") || strings.Contains(s, "\xfe") {
		return escapeResult{PassThrough: true, Desc: "PASS-THROUGH — invalid bytes kept as-is"}
	}
	return escapeResult{Desc: "OTHER — output: " + s}
}

// =============================================================================
// Test: EscapeHTML
// =============================================================================

func TestEscapeBehavior_HTML(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	v := S{V: `<script>alert("xss")</script> & foo > bar`}

	// encoding/json (baseline)
	stdOut, _ := json.Marshal(&v)
	stdRes := detectHTML(string(stdOut))
	t.Logf("encoding/json : %s", stdRes.Desc)
	t.Logf("  output: %s", stdOut)

	// sonic.Marshal (ConfigDefault)
	sonicOut, _ := sonic.Marshal(&v)
	sonicRes := detectHTML(string(sonicOut))
	t.Logf("sonic.Marshal : %s", sonicRes.Desc)
	t.Logf("  output: %s", sonicOut)

	// vjson.Marshal (default, no options)
	vjsonOut, _ := vjson.Marshal(&v)
	vjsonRes := detectHTML(string(vjsonOut))
	t.Logf("vjson.Marshal : %s", vjsonRes.Desc)
	t.Logf("  output: %s", vjsonOut)

	// vjson.Marshal with WithEscapeHTML
	vjsonHTMLOut, _ := vjson.Marshal(&v, vjson.WithEscapeHTML())
	vjsonHTMLRes := detectHTML(string(vjsonHTMLOut))
	t.Logf("vjson+EscHTML  : %s", vjsonHTMLRes.Desc)
	t.Logf("  output: %s", vjsonHTMLOut)

	// vjson.Marshal with WithStdCompat
	vjsonStdOut, _ := vjson.Marshal(&v, vjson.WithStdCompat())
	vjsonStdRes := detectHTML(string(vjsonStdOut))
	t.Logf("vjson+StdCompat: %s", vjsonStdRes.Desc)
	t.Logf("  output: %s", vjsonStdOut)
}

// =============================================================================
// Test: EscapeLineTerminators (U+2028, U+2029)
// =============================================================================

func TestEscapeBehavior_LineTerminators(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	v := S{V: "before\u2028middle\u2029after"}

	// encoding/json
	stdOut, _ := json.Marshal(&v)
	t.Logf("encoding/json : %s", detectLineTerminators(string(stdOut)).Desc)
	t.Logf("  output: %q", stdOut)

	// sonic.Marshal
	sonicOut, _ := sonic.Marshal(&v)
	t.Logf("sonic.Marshal : %s", detectLineTerminators(string(sonicOut)).Desc)
	t.Logf("  output: %q", sonicOut)

	// vjson.Marshal (default)
	vjsonOut, _ := vjson.Marshal(&v)
	t.Logf("vjson.Marshal : %s", detectLineTerminators(string(vjsonOut)).Desc)
	t.Logf("  output: %q", vjsonOut)

	// vjson.Marshal with WithFastEscape (all escape features off)
	vjsonFastOut, _ := vjson.Marshal(&v, vjson.WithFastEscape())
	t.Logf("vjson+FastEsc : %s", detectLineTerminators(string(vjsonFastOut)).Desc)
	t.Logf("  output: %q", vjsonFastOut)

	// vjson.Marshal with WithStdCompat
	vjsonStdOut, _ := vjson.Marshal(&v, vjson.WithStdCompat())
	t.Logf("vjson+StdCompat: %s", detectLineTerminators(string(vjsonStdOut)).Desc)
	t.Logf("  output: %q", vjsonStdOut)
}

// =============================================================================
// Test: Invalid UTF-8 handling
// =============================================================================

func TestEscapeBehavior_InvalidUTF8(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	v := S{V: "hello\xff\xfeworld"}

	// encoding/json
	stdOut, stdErr := json.Marshal(&v)
	t.Logf("encoding/json  : %s", detectInvalidUTF8(string(stdOut), stdErr).Desc)
	t.Logf("  output: %q", stdOut)

	// sonic.Marshal
	sonicOut, sonicErr := sonic.Marshal(&v)
	t.Logf("sonic.Marshal  : %s", detectInvalidUTF8(string(sonicOut), sonicErr).Desc)
	t.Logf("  output: %q", sonicOut)

	// vjson.Marshal (default)
	vjsonOut, vjsonErr := vjson.Marshal(&v)
	t.Logf("vjson.Marshal  : %s", detectInvalidUTF8(string(vjsonOut), vjsonErr).Desc)
	t.Logf("  output: %q", vjsonOut)

	// vjson.Marshal with WithUTF8Correction
	vjsonCorrOut, vjsonCorrErr := vjson.Marshal(&v, vjson.WithUTF8Correction())
	t.Logf("vjson+UTF8Corr : %s", detectInvalidUTF8(string(vjsonCorrOut), vjsonCorrErr).Desc)
	t.Logf("  output: %q", vjsonCorrOut)

	// vjson.Marshal with WithStdCompat
	vjsonStdOut, vjsonStdErr := vjson.Marshal(&v, vjson.WithStdCompat())
	t.Logf("vjson+StdCompat: %s", detectInvalidUTF8(string(vjsonStdOut), vjsonStdErr).Desc)
	t.Logf("  output: %q", vjsonStdOut)
}

// =============================================================================
// Summary: side-by-side comparison of all three features
// =============================================================================

func TestEscapeBehavior_Summary(t *testing.T) {
	type S struct {
		HTML    string `json:"html"`
		LineTrm string `json:"line_term"`
		BadUTF8 string `json:"bad_utf8"`
	}
	v := S{
		HTML:    `<div class="x">&amp;</div>`,
		LineTrm: "a\u2028b\u2029c",
		BadUTF8: "ok\xff\xfebad",
	}

	type result struct {
		name   string
		html   escapeResult
		lineT  escapeResult
		utf8   escapeResult
	}

	// encoding/json
	stdOut, stdErr := json.Marshal(&v)
	stdS := string(stdOut)
	_ = stdErr

	// sonic.Marshal (ConfigDefault)
	sonicOut, sonicErr := sonic.Marshal(&v)
	sonicS := string(sonicOut)

	// vjson.Marshal (default, flags=0)
	vjsonOut, vjsonErr := vjson.Marshal(&v)
	vjsonS := string(vjsonOut)

	// vjson.Marshal + WithStdCompat
	vjsonStdOut, vjsonStdErr := vjson.Marshal(&v, vjson.WithStdCompat())
	vjsonStdS := string(vjsonStdOut)

	results := []result{
		{"encoding/json     ", detectHTML(stdS), detectLineTerminators(stdS), detectInvalidUTF8(stdS, stdErr)},
		{"sonic.Marshal     ", detectHTML(sonicS), detectLineTerminators(sonicS), detectInvalidUTF8(sonicS, sonicErr)},
		{"vjson.Marshal     ", detectHTML(vjsonS), detectLineTerminators(vjsonS), detectInvalidUTF8(vjsonS, vjsonErr)},
		{"vjson+StdCompat   ", detectHTML(vjsonStdS), detectLineTerminators(vjsonStdS), detectInvalidUTF8(vjsonStdS, vjsonStdErr)},
	}

	t.Log("")
	t.Logf("%-20s | %-40s | %-45s | %s", "Library", "EscapeHTML", "EscapeLineTerminators", "InvalidUTF8")
	t.Logf("%-20s-+-%-40s-+-%-45s-+-%s", strings.Repeat("-", 20), strings.Repeat("-", 40), strings.Repeat("-", 45), strings.Repeat("-", 45))
	for _, r := range results {
		t.Logf("%-20s | %-40s | %-45s | %s", r.name, r.html.Desc, r.lineT.Desc, r.utf8.Desc)
	}
	t.Log("")
}
