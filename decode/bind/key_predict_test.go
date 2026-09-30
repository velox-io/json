package bind

import (
	"reflect"
	"strings"
	"testing"
)

// Both engines try the field after the last match before resolving a key.
// A predicted field matches only a member whose body is exactly its key, so
// these cases put the predicted key against members it must not accept:
// extensions and prefixes of it, its escaped spelling, and the empty key.

type predictPrefix struct {
	AB  int `json:"ab"`
	ABC int `json:"abc"`
}

type predictLong struct {
	K16 int `json:"exactly_sixteen_"`
	K17 int `json:"exactly_sixteen_x"`
	K32 int `json:"a_key_of_exactly_thirty_two_byte"`
	K63 int `json:"k_63_bytes_long_key_padding_padding_padding_padding_padding_123"`
}

// Pairs that agree on their length and first eight bytes, and the second
// pair on its last eight too, so only the bytes between tell them apart.
type predictShared struct {
	A int `json:"prefix_a_one"`
	B int `json:"prefix_a_two"`
	C int `json:"shared_prefix_x_middle_suffix"`
	D int `json:"shared_prefix_y_middle_suffix"`
}

type predictTable struct {
	Long int `json:"a_key_beyond_the_perfect_tiers_sixty_three_byte_limit_padding_padding"`
	A    int `json:"a"`
	B    int `json:"b"`
}

type predictABC struct {
	A int `json:"a"`
	B int `json:"b"`
	C int `json:"c"`
}

type predictInner struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type predictNested struct {
	A int          `json:"a"`
	N predictInner `json:"n"`
	S []int        `json:"s"`
	B int          `json:"b"`
	C int          `json:"c"`
}

func TestStructKeyPrediction(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		dst  func() any
		want any
	}{
		{"extension of predicted", `{"abc":1,"ab":2}`, func() any { return new(predictPrefix) }, &predictPrefix{AB: 2, ABC: 1}},
		{"unknown extension", `{"abx":1,"ab":2,"abcd":3,"abc":4}`, func() any { return new(predictPrefix) }, &predictPrefix{AB: 2, ABC: 4}},
		{"prefix of predicted", `{"ab":1,"ab":2}`, func() any { return new(predictPrefix) }, &predictPrefix{AB: 2}},
		{"escaped predicted", `{"\u0061b":1,"a\u0062c":2}`, func() any { return new(predictPrefix) }, &predictPrefix{AB: 1, ABC: 2}},
		{"empty key", `{"":1,"ab":2,"":3,"abc":4}`, func() any { return new(predictPrefix) }, &predictPrefix{AB: 2, ABC: 4}},
		{"empty key at the sentinel", `{"abc":1,"":2}`, func() any { return new(predictPrefix) }, &predictPrefix{ABC: 1}},
		{"empty key at the sentinel, more members", `{"ab":1,"abc":2,"":3,"ab":4}`, func() any { return new(predictPrefix) }, &predictPrefix{AB: 4, ABC: 2}},
		{"whitespace before colon", `{"ab" :1, "abc"  :2}`, func() any { return new(predictPrefix) }, &predictPrefix{AB: 1, ABC: 2}},
		{"stride boundaries", `{"exactly_sixteen_":1,"exactly_sixteen_x":2,"a_key_of_exactly_thirty_two_byte":3,` +
			`"k_63_bytes_long_key_padding_padding_padding_padding_padding_123":4}`,
			func() any { return new(predictLong) }, &predictLong{1, 2, 3, 4}},
		{"stride extension", `{"exactly_sixteen_x":2,"exactly_sixteen_":1}`,
			func() any { return new(predictLong) }, &predictLong{K16: 1, K17: 2}},
		{"shared words", `{"prefix_a_two":1,"shared_prefix_y_middle_suffix":4,"prefix_a_one":2,"shared_prefix_x_middle_suffix":3}`,
			func() any { return new(predictShared) }, &predictShared{2, 1, 3, 4}},
		{"table tier", `{"a_key_beyond_the_perfect_tiers_sixty_three_byte_limit_padding_padding":1,"a":2,"b":3}`,
			func() any { return new(predictTable) }, &predictTable{1, 2, 3}},
		{"nested restores", `{"a":1,"n":{"x":2,"y":3},"s":[4],"b":5,"c":6}`,
			func() any { return new(predictNested) }, &predictNested{1, predictInner{2, 3}, []int{4}, 5, 6}},
		{"nested out of order", `{"n":{"y":3,"x":2},"c":6,"a":1,"b":5}`,
			func() any { return new(predictNested) }, &predictNested{A: 1, N: predictInner{2, 3}, B: 5, C: 6}},
		{"memo then another order", `[{"c":3,"b":2,"a":1},{"c":6,"b":5,"a":4},{"b":8,"a":7,"c":9},{"a":10,"c":11}]`,
			func() any { return new([]predictABC) }, &[]predictABC{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}, {10, 0, 11}}},
		{"per object reset", `[{"y":1,"x":2},{"x":3,"y":4},{"y":5}]`,
			func() any { return new([]predictInner) }, &[]predictInner{{2, 1}, {3, 4}, {0, 5}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.dst()
			if err := Unmarshal([]byte(tc.doc), got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A predicted key against the end of the document must not read past it as
// a closing quote.
func TestStructKeyPredictionTruncated(t *testing.T) {
	for _, doc := range []string{`{"ab`, `{"a`, `{"ab"`, `{"ab":1,"abc`, `{"exactly_sixteen_`} {
		var v predictPrefix
		if err := Unmarshal([]byte(doc), &v); err == nil {
			t.Errorf("Unmarshal(%q) = nil error", doc)
		}
	}
}

func TestStructKeyPredictionDisallowUnknown(t *testing.T) {
	var v predictPrefix
	err := Unmarshal([]byte(`{"ab":1,"abcd":2}`), &v, WithDisallowUnknownFields())
	if err == nil || !strings.Contains(err.Error(), "unknown_field") {
		t.Fatalf("err = %v, want unknown field", err)
	}
}
