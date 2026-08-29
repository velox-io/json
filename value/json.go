package value

import "errors"

// Value implements json.Marshaler so Value fields marshal through
// encoding/json and other libraries. Value does NOT implement
// json.Unmarshaler: a Value can only be produced by a tape-emitting path
// (the native bind engine, or dom.Parse). To unmarshal JSON into a Value
// field use this library's Unmarshal (which drives the native binder); for
// stdlib/encoding/json interop or lazy byte-backed access, use Raw.

// MarshalJSON re-serializes v from its tape. For the root Value this
// round-trips the original document; for tape-backed children obtained via
// Get / Index it materializes the subtree.
func (v Value) MarshalJSON() ([]byte, error) {
	if !v.hasTape() {
		return []byte("null"), nil
	}
	return tapeToJSON(&v), nil
}

// Raw implements json.Marshaler and json.Unmarshaler so that Raw fields work
// with encoding/json and other third party JSON libraries.
//
// Raw is the byte-backed counterpart of Value: it carries the raw JSON bytes
// and accessors walk them via the Go scanner. Use Raw where a tape is not
// available (stdlib interop, the vdec fallback, or literal construction).

// MarshalJSON returns the raw bytes, or null when r holds none.
func (r Raw) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

// UnmarshalJSON stores a copy of data in r.
func (r *Raw) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.New("value: UnmarshalJSON on nil Raw")
	}
	*r = append((*r)[:0], data...)
	return nil
}
