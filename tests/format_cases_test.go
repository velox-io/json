package tests

import (
	"encoding/json"
	"math"
	"time"
)

// The `format` golden cases. Every want below is what encoding/json/v2 (Go
// 1.27 source) produces when run with DefaultOptionsV1 and
// ExperimentalSupportFormatTag(true): velox keeps encoding/json's v1
// semantics and layers the format option over them. The file imports nothing
// from velox or testing so it can be run against that reference unchanged.

type formatCase struct {
	name string
	v    any    // value to marshal; a pointer to a struct
	want string // its encoding, which must also decode back to v
}

// The JSON methods below take precedence over a `format` option.
type fmtMethods int

func (m fmtMethods) MarshalJSON() ([]byte, error) { return []byte(`"method"`), nil }

func (m *fmtMethods) UnmarshalJSON([]byte) error { *m = 7; return nil }

type fmtBytes struct {
	B64  []byte  `json:"b64,format:base64"`
	URL  []byte  `json:"url,format:base64url"`
	B32  []byte  `json:"b32,format:base32"`
	B32H []byte  `json:"b32h,format:base32hex"`
	Hex  []byte  `json:"hex,format:hex"`
	B16  []byte  `json:"b16,format:base16"`
	Arr  []byte  `json:"arr,format:array"`
	Fix  [3]byte `json:"fix,format:base64url"`
	FixA [3]byte `json:"fixa,format:array"`
	Nil  []byte  `json:"nil,format:hex"`
	NilA []byte  `json:"nila,format:array"`
}

type fmtNamedByte byte

type fmtNamedBytes struct {
	Named []fmtNamedByte `json:"named,format:hex"`
	Arr   []fmtNamedByte `json:"arr,format:array"`
}

type fmtNil struct {
	SliceNull  []int          `json:"sn,format:emitnull"`
	SliceEmpty []int          `json:"se,format:emitempty"`
	MapNull    map[string]int `json:"mn,format:emitnull"`
	MapEmpty   map[string]int `json:"me,format:emitempty"`
	Full       []int          `json:"full,format:emitempty"`
	Omitted    []int          `json:"om,omitempty,format:emitempty"`
	PtrEmpty   *[]int         `json:"pe,format:emitempty"`
}

type fmtFloat struct {
	NaN    float64  `json:"nan,format:nonfinite"`
	PosInf float64  `json:"pinf,format:nonfinite"`
	NegInf float32  `json:"ninf,format:nonfinite"`
	Finite float64  `json:"fin,format:nonfinite"`
	Quoted float64  `json:"q,string,format:nonfinite"`
	QNaN   float64  `json:"qnan,string,format:nonfinite"`
	Ptr    *float64 `json:"p,format:nonfinite"`
}

type fmtTime struct {
	RFC3339 time.Time  `json:"rfc3339,format:RFC3339"`
	Nano    time.Time  `json:"nano,format:RFC3339Nano"`
	RFC1123 time.Time  `json:"rfc1123,format:RFC1123"`
	Kitchen time.Time  `json:"kitchen,format:Kitchen"`
	Date    time.Time  `json:"date,format:DateOnly"`
	Layout  time.Time  `json:"layout,format:'2006-01-02T15h04'"`
	Unix    time.Time  `json:"unix,format:unix"`
	Milli   time.Time  `json:"milli,format:unixmilli"`
	Micro   time.Time  `json:"micro,format:unixmicro"`
	UNano   time.Time  `json:"unano,format:unixnano"`
	Quoted  time.Time  `json:"quoted,string,format:unix"` //nolint:staticcheck // `,string` quotes a numeric format, as in encoding/json/v2
	Ptr     *time.Time `json:"ptr,format:unixmilli"`
	NilPtr  *time.Time `json:"nilptr,format:unixmilli"`
}

type fmtDuration struct {
	Units    time.Duration   `json:"units,format:units"`
	SecNum   time.Duration   `json:"sec,format:sec"`
	MilliNum time.Duration   `json:"milli,format:milli"`
	MicroNum time.Duration   `json:"micro,format:micro"`
	NanoNum  time.Duration   `json:"nano,format:nano"`
	ISO      time.Duration   `json:"iso,format:iso8601"`
	Quoted   time.Duration   `json:"quoted,string,format:sec"`
	Ptr      *time.Duration  `json:"ptr,format:iso8601"`
	PPtr     **time.Duration `json:"pptr,string,format:milli"` //nolint:staticcheck // `,string` stops at one pointer, as in encoding/json
}

type fmtPrecedence struct {
	Method    fmtMethods      `json:"method,format:hex"`
	Raw       json.RawMessage `json:"raw,format:hex"`
	Number    json.Number     `json:"num,format:nonfinite"`
	TimeFirst time.Time       `json:"time,format:DateOnly"`
}

var (
	fmtTS  = time.Date(2020, 1, 2, 3, 4, 5, 600000000, time.UTC)
	fmtDur = 90*time.Minute + 1500*time.Millisecond
)

func ptrTo[T any](v T) *T { return &v }

func formatMarshalCases() []formatCase {
	ts := fmtTS
	neg := time.Unix(-1, 5).UTC()
	durPtr := ptrTo(-fmtDur)
	return []formatCase{
		{"bytes", &fmtBytes{
			B64: []byte{0, 1, 0xfb, 0xff}, URL: []byte{0, 1, 0xfb, 0xff}, B32: []byte("hi!"), B32H: []byte("hi!"),
			Hex: []byte{0xde, 0xad}, B16: []byte{0xbe, 0xef}, Arr: []byte{1, 2, 255}, Fix: [3]byte{0xfb, 0xff, 1},
			FixA: [3]byte{9, 8, 7},
		}, `{"b64":"AAH7/w==","url":"AAH7_w==","b32":"NBUSC===","b32h":"D1KI2===","hex":"dead","b16":"beef","arr":[1,2,255],"fix":"-_8B","fixa":[9,8,7],"nil":null,"nila":null}`},
		{"bytes empty", &fmtBytes{B64: []byte{}, Arr: []byte{}}, `{"b64":"","url":null,"b32":null,"b32h":null,"hex":null,"b16":null,"arr":[],"fix":"AAAA","fixa":[0,0,0],"nil":null,"nila":null}`},
		{"named bytes", &fmtNamedBytes{Named: []fmtNamedByte{0xab}, Arr: []fmtNamedByte{1, 2}}, `{"named":"ab","arr":[1,2]}`},
		{"nil containers", &fmtNil{Full: []int{1}}, `{"sn":null,"se":[],"mn":null,"me":{},"full":[1],"pe":null}`},
		{"nil containers via pointer", &fmtNil{Full: []int{}, PtrEmpty: new([]int)}, `{"sn":null,"se":[],"mn":null,"me":{},"full":[],"pe":[]}`},
		{"floats", &fmtFloat{NaN: math.NaN(), PosInf: math.Inf(1), NegInf: float32(math.Inf(-1)), Finite: 1.25, Quoted: -0.5, QNaN: math.NaN(), Ptr: ptrTo(math.Inf(1))},
			`{"nan":"NaN","pinf":"Infinity","ninf":"-Infinity","fin":1.25,"q":"-0.5","qnan":"NaN","p":"Infinity"}`},
		{"times", &fmtTime{
			RFC3339: ts, Nano: ts, RFC1123: ts, Kitchen: ts, Date: ts, Layout: ts,
			Unix: ts, Milli: ts, Micro: neg, UNano: neg, Quoted: ts, Ptr: &ts,
		}, `{"rfc3339":"2020-01-02T03:04:05Z","nano":"2020-01-02T03:04:05.6Z","rfc1123":"Thu, 02 Jan 2020 03:04:05 UTC","kitchen":"3:04AM","date":"2020-01-02","layout":"2020-01-02T03h04","unix":1577934245.6,"milli":1577934245600,"micro":-999999.995,"unano":-999999995,"quoted":"1577934245.6","ptr":1577934245600,"nilptr":null}`},
		{"durations", &fmtDuration{
			Units: fmtDur, SecNum: fmtDur, MilliNum: fmtDur, MicroNum: fmtDur, NanoNum: fmtDur, ISO: fmtDur, Quoted: fmtDur,
			Ptr: durPtr, PPtr: &durPtr,
		}, `{"units":"1h30m1.5s","sec":5401.5,"milli":5401500,"micro":5401500000,"nano":5401500000000,"iso":"PT1H30M1.5S","quoted":"5401.5","ptr":"-PT1H30M1.5S","pptr":-5401500}`},
		{"methods take precedence", &fmtPrecedence{Raw: json.RawMessage(`[1]`), Number: "12", TimeFirst: ts},
			`{"method":"method","raw":[1],"num":12,"time":"2020-01-02"}`},
	}
}

type formatDecodeCase struct {
	name string
	in   string // JSON to decode
	into any    // destination, a pointer to a possibly prefilled struct
	want string // into re-encoded after decoding; "" when decoding must fail
}

type fmtDecBytes struct {
	B64 []byte  `json:"b64,format:base64"`
	Hex []byte  `json:"hex,format:hex"`
	Arr []byte  `json:"arr,format:array"`
	Fix [3]byte `json:"fix,format:base64"`
}

type fmtDecFloat struct {
	F float64 `json:"f,format:nonfinite"`
	Q float32 `json:"q,string,format:nonfinite"`
}

type fmtDecTime struct {
	RFC    time.Time       `json:"rfc,format:RFC3339"`
	Unix   time.Time       `json:"unix,format:unix"`
	QUnix  time.Time       `json:"qunix,string,format:unixmilli"` //nolint:staticcheck // `,string` quotes a numeric format, as in encoding/json/v2
	Layout time.Time       `json:"layout,format:'02/01/2006'"`
	Ptr    *time.Time      `json:"ptr,format:unix"`
	ISO    time.Duration   `json:"iso,format:iso8601"`
	Units  time.Duration   `json:"units,format:units"`
	PSec   **time.Duration `json:"psec,format:sec"`
}

func formatDecodeCases() []formatDecodeCase {
	ts := fmtTS
	return []formatDecodeCase{
		{"bytes", `{"b64":"AQ\r\nI=","hex":"0A0b","arr":[ 1 , null, 255 ],"fix":"AQ=="}`,
			&fmtDecBytes{Arr: []byte{7, 8, 9}, Fix: [3]byte{9, 9, 9}},
			`{"b64":"AQI=","hex":"0a0b","arr":[1,8,255],"fix":"AQAA"}`},
		{"bytes null", `{"b64":null,"hex":null,"arr":null,"fix":null}`,
			&fmtDecBytes{B64: []byte{1}, Hex: []byte{1}, Arr: []byte{1}, Fix: [3]byte{1, 2, 3}},
			`{"b64":null,"hex":null,"arr":null,"fix":"AQID"}`},
		{"bytes empty", `{"b64":"","arr":[]}`, &fmtDecBytes{}, `{"b64":"","hex":null,"arr":[],"fix":"AAAA"}`},
		{"bytes fixed overflow truncates", `{"fix":"AQIDBA=="}`, &fmtDecBytes{}, `{"b64":null,"hex":null,"arr":null,"fix":"AQID"}`},
		{"base64 without padding", `{"b64":"AQI"}`, &fmtDecBytes{}, ""},
		{"hex odd length", `{"hex":"abc"}`, &fmtDecBytes{}, ""},
		{"hex from array", `{"hex":[1]}`, &fmtDecBytes{}, ""},
		{"array from string", `{"arr":"AQI="}`, &fmtDecBytes{}, ""},
		{"array element overflow", `{"arr":[256]}`, &fmtDecBytes{}, ""},
		{"array element fraction", `{"arr":[1.0]}`, &fmtDecBytes{}, ""},

		{"floats", `{"f":"-Infinity","q":"NaN"}`, &fmtDecFloat{}, `{"f":"-Infinity","q":"NaN"}`},
		{"float number", `{"f":-2.5e3,"q":"0.25"}`, &fmtDecFloat{}, `{"f":-2500,"q":"0.25"}`},
		{"float null and quoted null keep the value", `{"f":null,"q":"null"}`, &fmtDecFloat{F: 1, Q: 2}, `{"f":1,"q":"2"}`},
		{"float quoted without string option", `{"f":"1.5"}`, &fmtDecFloat{}, ""},
		{"float bare under string option", `{"q":1.5}`, &fmtDecFloat{}, ""},
		{"float lowercase nan", `{"f":"nan"}`, &fmtDecFloat{}, ""},
		{"float overflow", `{"f":1e400}`, &fmtDecFloat{}, ""},

		{"times", `{"rfc":"2020-01-02T03:04:05.6+07:00","unix":-1.5,"qunix":"1577934245600","layout":"25/12/2021","ptr":2,"iso":"-PT1M30.5S","units":"1h1us","psec":0.5}`,
			&fmtDecTime{},
			`{"rfc":"2020-01-02T03:04:05+07:00","unix":-1.5,"qunix":"1577934245600","layout":"25/12/2021","ptr":2,"iso":"-PT1M30.5S","units":"1h0m0.000001s","psec":0.5}`},
		{"time nulls", `{"rfc":null,"unix":null,"ptr":null,"psec":null}`,
			&fmtDecTime{RFC: ts, Unix: ts, Ptr: &ts},
			`{"rfc":"2020-01-02T03:04:05Z","unix":1577934245.6,"qunix":"-62135596800000","layout":"01/01/0001","ptr":null,"iso":"PT0S","units":"0s","psec":null}`},
		{"rfc3339 comma fraction", `{"rfc":"2020-01-02T03:04:05,1Z"}`, &fmtDecTime{}, ""},
		{"rfc3339 zone hour out of range", `{"rfc":"2020-01-02T03:04:05+24:00"}`, &fmtDecTime{}, ""},
		{"unix exponent", `{"unix":1e3}`, &fmtDecTime{}, ""},
		{"unix quoted without string option", `{"unix":"1"}`, &fmtDecTime{}, ""},
		{"unix bare under string option", `{"qunix":1}`, &fmtDecTime{}, ""},
		{"layout mismatch", `{"layout":"2021-12-25"}`, &fmtDecTime{}, ""},
		{"iso8601 nominal units", `{"iso":"P1D"}`, &fmtDecTime{}, ""},
		{"units garbage", `{"units":"soon"}`, &fmtDecTime{}, ""},
	}
}
