package typ

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/velox-io/json/internal/jsonfmt"
)

// VJSONTagKey is the single struct-tag key for every velox-json extension that
// is not part of the standard `json` tag vocabulary.
//
// Keeping these options behind one private key, rather than one key per feature,
// serves two ends. Other JSON libraries reading the same struct see only the
// `json` tag and ignore `vjson` wholesale, so a type stays portable. And because
// every option arrives through a single parse, an unrecognized one can be
// reported instead of silently dropped, which per-key `StructTag.Lookup` calls
// cannot do.
const VJSONTagKey = "vjson"

// EmbedOption is the `json` tag option that promotes a field's content into its
// host, leaving the field without a JSON member of its own.
//
// It accepts three field shapes: a struct (promoted by offset arithmetic during
// field collection), a value.Value (reserve-unknown), and an interface paired
// with `vjson:"variant=..."` (polymorphic promotion). Anything else is rejected
// at build time.
const EmbedOption = "embed"

// jsonOptions is the parsed option set of a field's `json` tag.
type jsonOptions struct {
	omitEmpty bool
	omitZero  bool
	quoted    bool
	embed     bool
	format    string // value of the `format:<value>` option; "" when absent
}

// onlyEmbed reports whether the option list is empty or carries nothing but
// the embed option.
func (o jsonOptions) onlyEmbed() bool {
	return o == jsonOptions{} || o == jsonOptions{embed: true}
}

// jsonOptionCanonical returns the canonical spelling a misspelled option
// normalizes to, or "" when the option is no near-miss of a known one.
// Normalization is lowercasing plus underscore removal, so "omitEmpty" and
// "omit_zero" both map to their canonical spelling. The rejection itself is
// a velox-only diagnostic; encoding/json activates options by exact match
// and treats every other spelling as inert.
func jsonOptionCanonical(opt string) string {
	switch strings.ReplaceAll(strings.ToLower(opt), "_", "") {
	case "omitempty":
		return "omitempty"
	case "omitzero":
		return "omitzero"
	case "string":
		return "string"
	case "embed":
		return "embed"
	case "format":
		return "format"
	}
	return ""
}

// parseJSONTag splits a `json` tag value into its name and option set.
// Options are matched exactly, as the standard library does, so a
// space-padded option is absent rather than active. Unrecognized options are
// ignored, which the standard library reserves for future meaning.
//
// problems describes tag mistakes the caller must reject, each phrased to
// follow "struct S field F": misspelled appearances of known options, and a
// malformed `format` option.
func parseJSONTag(raw string) (name string, opts jsonOptions, problems []string) {
	name, rest, _ := strings.Cut(raw, ",")
	for {
		// The format option comes last and its value may be a quoted string
		// holding commas, so it owns the remainder of the tag.
		if value, ok := strings.CutPrefix(rest, "format:"); ok {
			var problem string
			opts.format, problem = parseFormatValue(value)
			if problem != "" {
				problems = append(problems, problem)
			}
			break
		}
		opt, next, more := strings.Cut(rest, ",")
		switch opt {
		case "":
		case "omitempty":
			opts.omitEmpty = true
		case "omitzero":
			opts.omitZero = true
		case "string":
			opts.quoted = true
		case "embed":
			opts.embed = true
		case "format":
			problems = append(problems, "has a `format` tag option without a value; add one, as in `format:RFC3339`")
		default:
			optName, _, _ := strings.Cut(opt, ":")
			if canon := jsonOptionCanonical(optName); canon != "" {
				problems = append(problems, fmt.Sprintf("has misspelled `json` option %q; specify `%s` instead", opt, canon))
			}
		}
		if !more {
			break
		}
		rest = next
	}
	return name, opts, problems
}

// parseFormatValue parses the value of a `format:<value>` option, which runs
// to the end of the tag. As in encoding/json/v2, the value is either a Go
// identifier (format:RFC3339) or a single-quoted string (format:'2006-01-02'),
// whose body takes Go string escapes plus \' for a quote. problem is non-empty
// when the value is malformed.
func parseFormatValue(raw string) (value, problem string) {
	if raw == "" {
		return "", formatEmptyProblem
	}
	n := 0 // length of the value as written; zero when it starts badly
	switch r, _ := utf8.DecodeRuneInString(raw); {
	case r == '_' || unicode.IsLetter(r):
		n = len(raw) - len(strings.TrimLeftFunc(raw, jsonfmt.IsLetterOrDigit))
		value = raw[:n]
	case r == '\'':
		var ok bool
		if value, n, ok = unquoteTagString(raw); !ok {
			return "", fmt.Sprintf("has a `format` tag option with an invalid quoted value %s; end it with a single quote and use only Go escape sequences inside it", raw)
		}
		if value == "" {
			return "", formatEmptyProblem
		}
	}
	switch rest := raw[n:]; {
	case n > 0 && rest == "":
		return value, ""
	case n > 0 && rest[0] == ',':
		return "", "has a `format` tag option that is not last; move it to the end of the tag"
	}
	return "", fmt.Sprintf("has a `format` tag option with an invalid value %q; quote a value that is not a Go identifier, as in `format:'2006-01-02'`", raw)
}

const formatEmptyProblem = "has a `format` tag option with an empty value; add one, as in `format:RFC3339`"

// unquoteTagString decodes the single-quoted string at the start of s,
// returning its value and the byte length it spans. The grammar is a Go
// double-quoted string literal with single quotes as delimiters: neither
// a backtick nor a double quote can appear unescaped in a struct tag.
func unquoteTagString(s string) (value string, n int, ok bool) {
	b := []byte{'"'}
	n = len("'")
	var inEscape bool
	for n < len(s) {
		r, rn := utf8.DecodeRuneInString(s[n:])
		switch {
		case inEscape:
			if r == '\'' {
				b = b[:len(b)-1] // `\'` becomes `'`
			}
			inEscape = false
		case r == '\\':
			inEscape = true
		case r == '"':
			b = append(b, '\\') // `"` becomes `\"`
		case r == '\'':
			value, err := strconv.Unquote(string(append(b, '"')))
			return value, n + len("'"), err == nil
		}
		b = append(b, s[n:n+rn]...)
		n += rn
	}
	return "", 0, false // unterminated
}

// ReserveUnknownName is the JSON name given to a value.Value field carrying
// `json:",embed"`.
//
// The field must stay in StructTypeInfo.Fields: the native field-name lookup
// blob is positional, so a lookup hit index is used directly to index the field
// array. Removing the field would shift every later index. Instead it keeps a
// name no JSON key can carry, so the name is present for index arithmetic yet
// never matches an input key.
//
// The sentinel leads with DEL (0x7F) rather than NUL: a JSON string cannot carry
// either unescaped, but vlib refuses to index a key containing a NUL byte
// (SizeFor reports 0), which would fail lookup-blob construction for the whole
// struct.
//
// The name is a fixed constant rather than one unique name per field, so two
// reserve-unknown fields in one struct collide under the ordinary same-name
// promotion rules. Reaching the same depth is reported as a build error rather
// than silently canceling, since no input key selects either one.
const ReserveUnknownName = "\x7funknown"

// VJSONTag is the parsed form of a field's `vjson` tag.
//
// The tag carries only which field set an embedded interface promotes. Layout
// lives in the `json` tag as EmbedOption.
type VJSONTag struct {
	Present bool

	// HasVariant records that a `variant=<disc>` option was seen, which is
	// distinct from Variant being non-empty: `variant=` names no discriminator
	// and must be reported, not treated as absent.
	HasVariant bool
	Variant    string
	Kindof     bool

	// Unrecognized collects options this parser does not know. The typ package
	// cannot report them: struct-field collection has no error channel and runs
	// inside a cached type build. vbind reads this and fails the build, which is
	// why a misspelled option no longer degrades into silently different
	// behavior.
	Unrecognized []string
}

// ParseVJSONTag parses the `vjson` tag of one struct field. Options are comma
// separated, and an option that takes a value spells it `name=value`.
func ParseVJSONTag(tag reflect.StructTag) VJSONTag {
	raw, ok := tag.Lookup(VJSONTagKey)
	if !ok {
		return VJSONTag{}
	}
	out := VJSONTag{Present: true}
	for opt := range strings.SplitSeq(raw, ",") {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		name, val, hasVal := strings.Cut(opt, "=")
		switch {
		case name == "variant" && hasVal:
			out.HasVariant = true
			out.Variant = val
		case name == "kindof" && !hasVal:
			out.Kindof = true
		default:
			// Reported verbatim so the message shows what was written, including
			// a value supplied to an option that takes none.
			out.Unrecognized = append(out.Unrecognized, opt)
		}
	}
	return out
}
