//go:build go1.27

package tests

import "testing"

// Malformed bodies that every encoding/json version rejects, but only go1.27
// reports as an *UnmarshalTypeError; earlier versions return a plain error,
// so the error class comparison needs go1.27.

func TestUnmarshalCorpus_NumberTypeErrors(t *testing.T) {
	cases := []corpusCase{
		{"quoted non-number", `{"n":"ab"}`, newOf[cpNumber](), false},
		{"quoted empty", `{"n":""}`, newOf[cpNumber](), false},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

func TestUnmarshalCorpus_QuotedFieldTypeErrors(t *testing.T) {
	cases := []corpusCase{
		{"unquoted number", `{"i":5}`, newOf[cpQuoted](), true},
		{"non-number body", `{"i":"ab"}`, newOf[cpQuoted](), true},
		{"non-bool body", `{"b":"yes"}`, newOf[cpQuoted](), true},
		{"string body unquoted", `{"s":"hi"}`, newOf[cpQuoted](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}
