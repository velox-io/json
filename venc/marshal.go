package venc

// MarshalOption configures encoding behavior.
type MarshalOption func(*encodeState)

// WithEscapeHTML enables HTML escaping.
func WithEscapeHTML() MarshalOption {
	return func(es *encodeState) { es.flags |= uint32(escapeHTML) }
}

// WithoutEscapeHTML disables HTML escaping.
func WithoutEscapeHTML() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeHTML) }
}

// WithEscapeLineTerms escapes U+2028 and U+2029.
func WithEscapeLineTerms() MarshalOption {
	return func(es *encodeState) { es.flags |= uint32(escapeLineTerms) }
}

// WithoutEscapeLineTerms leaves U+2028 and U+2029 unescaped.
func WithoutEscapeLineTerms() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeLineTerms) }
}

// WithUTF8Correction replaces invalid UTF-8 with \ufffd.
func WithUTF8Correction() MarshalOption {
	return func(es *encodeState) { es.flags |= uint32(escapeInvalidUTF8) }
}

// WithoutUTF8Correction preserves invalid UTF-8 bytes.
func WithoutUTF8Correction() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeInvalidUTF8) }
}

// WithStdCompat matches encoding/json escaping and float formatting.
func WithStdCompat() MarshalOption {
	return func(es *encodeState) {
		es.flags = uint32(escapeStdCompat) | EncFloatExpAuto
	}
}

// WithFloatExpAuto matches encoding/json scientific-notation thresholds.
func WithFloatExpAuto() MarshalOption {
	return func(es *encodeState) { es.flags |= EncFloatExpAuto }
}

// WithFastEscape leaves only the mandatory JSON escapes enabled.
func WithFastEscape() MarshalOption {
	return func(es *encodeState) { es.flags &^= uint32(escapeHTML | escapeLineTerms | escapeInvalidUTF8) }
}

// Marshal serializes v to JSON.
//
// Pointer T: fast cache path via encElemTypeInfoOf, zero-copy.
// Value T:   takes pointer to v directly.
func Marshal[T any](v T, opts ...MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	defer releaseEncodeState(es)

	for _, o := range opts {
		o(es)
	}

	ti, ptr := resolveType(&v)
	if ti == nil {
		return []byte("null"), nil
	}

	hint := int(ti.AdaptiveHint.Load())
	if hint == 0 {
		hint = encodingSizeHint(ti, ptr)
		ti.AdaptiveHint.Store(int64(hint))
	}
	es.growBuf(hint)

	if err := es.encodeTop(ti, ptr); err != nil {
		return nil, err
	}

	n := len(es.buf)
	adapted := n + n/16 // +6% headroom for VM pessimistic checks
	if int64(adapted) > ti.AdaptiveHint.Load() {
		ti.AdaptiveHint.Store(int64(adapted))
	}

	result := es.buf[:n]
	es.buf = es.buf[n:]
	return result, nil
}

func MarshalIndent[T any](v T, prefix, indent string, opts ...MarshalOption) ([]byte, error) {
	return Marshal(v, append(opts, withIndent(prefix, indent))...)
}

// withIndent returns an internal MarshalOption that configures indentation.
func withIndent(prefix, indent string) MarshalOption {
	return func(es *encodeState) {
		es.indentPrefix = prefix
		es.indentString = indent
		es.nativeIndent = es.nativeIndent && isSimpleIndent(prefix, indent) > 0
	}
}

func AppendMarshal[T any](dst []byte, v T, opts ...MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	defer releaseEncodeState(es)
	for _, o := range opts {
		o(es)
	}

	es.buf = dst

	ti, ptr := resolveType(&v)
	if ti == nil {
		return append(dst, "null"...), nil
	}

	if err := es.encodeTop(ti, ptr); err != nil {
		es.buf = nil // detach caller's buffer before pooling
		return dst, err
	}

	result := es.buf
	es.buf = nil // detach caller's buffer before pooling
	return result, nil
}
