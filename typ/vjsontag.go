package typ

import (
	"reflect"
	"strings"
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
	}
	return ""
}

// parseJSONTag splits a `json` tag value into its name and option set.
// Options are matched exactly, as the standard library does, so a
// space-padded option is absent rather than active. Misspelled appearances
// of known options are collected for the caller to reject; unrecognized
// options are ignored, which the standard library reserves for future
// meaning.
func parseJSONTag(raw string) (name string, opts jsonOptions, mutants []string) {
	name, optList, _ := strings.Cut(raw, ",")
	for opt := range strings.SplitSeq(optList, ",") {
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
		default:
			if canon := jsonOptionCanonical(opt); canon != "" {
				mutants = append(mutants, opt)
			}
		}
	}
	return name, opts, mutants
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
