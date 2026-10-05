package typ

import (
	"strings"
	"testing"
)

func TestParseJSONTagFormat(t *testing.T) {
	cases := []struct {
		raw     string
		name    string
		format  string
		problem string // substring of the single expected problem; "" for none
	}{
		{raw: "t,format:unix", name: "t", format: "unix"},
		{raw: ",omitempty,format:RFC3339", format: "RFC3339"},
		{raw: "t,format:_x9", name: "t", format: "_x9"},
		{raw: "t,format:日本", name: "t", format: "日本"},
		{raw: "t,format:'2006-01-02, 15:04'", name: "t", format: "2006-01-02, 15:04"},
		{raw: `t,format:'It\'s'`, name: "t", format: "It's"},
		{raw: `t,format:'"q"'`, name: "t", format: `"q"`},
		{raw: `t,format:'a\tbé'`, name: "t", format: "a\tbé"},
		{raw: "t,format:'unix'", name: "t", format: "unix"},
		// A space-padded option is inert, as every option is in a v1 tag.
		{raw: "t, format:unix", name: "t"},

		{raw: "t,format", name: "t", problem: "without a value; add one"},
		{raw: "t,format:", name: "t", problem: "empty value; add one"},
		{raw: "t,format:''", name: "t", problem: "empty value; add one"},
		{raw: "t,format:unix,omitempty", name: "t", problem: "is not last; move it to the end of the tag"},
		{raw: "t,format:unix,", name: "t", problem: "is not last; move it to the end of the tag"},
		{raw: "t,format:unix,format:sec", name: "t", problem: "is not last; move it to the end of the tag"},
		{raw: "t,format:'a'b", name: "t", problem: "invalid value"},
		{raw: "t,format:'unterminated", name: "t", problem: "invalid quoted value"},
		{raw: `t,format:'bad\q'`, name: "t", problem: "invalid quoted value"},
		{raw: "t,format:2006-01-02", name: "t", problem: "invalid value"},
		{raw: "t,format:RFC-3339", name: "t", problem: "invalid value"},
		{raw: "t,Format:unix", name: "t", problem: "specify `format` instead"},
		{raw: "t,FORMAT", name: "t", problem: "specify `format` instead"},
	}
	for _, tc := range cases {
		name, opts, problems := parseJSONTag(tc.raw)
		if name != tc.name || opts.format != tc.format {
			t.Errorf("parseJSONTag(%q) = name %q format %q, want %q %q", tc.raw, name, opts.format, tc.name, tc.format)
		}
		switch {
		case tc.problem == "" && len(problems) > 0:
			t.Errorf("parseJSONTag(%q) problems %q, want none", tc.raw, problems)
		case tc.problem != "" && (len(problems) != 1 || !strings.Contains(problems[0], tc.problem)):
			t.Errorf("parseJSONTag(%q) problems %q, want one containing %q", tc.raw, problems, tc.problem)
		}
	}
}

func TestParseJSONTagFormatKeepsOtherOptions(t *testing.T) {
	_, opts, problems := parseJSONTag("t,string,omitzero,format:'2006,01'")
	if len(problems) > 0 || !opts.quoted || !opts.omitZero || opts.format != "2006,01" {
		t.Fatalf("got %+v %q", opts, problems)
	}
}
