package vjson

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMarshal_TimeTime_Timezones(t *testing.T) {
	// FixedZone (positive offset) — handled natively by C VM
	tz8 := time.FixedZone("CST", 8*3600)
	// FixedZone (negative offset)
	tzNeg5 := time.FixedZone("EST", -5*3600)
	// FixedZone with non-hour offset (e.g. India +05:30)
	tzIndia := time.FixedZone("IST", 5*3600+30*60)
	// FixedZone with negative non-hour offset (e.g. -09:30)
	tzNeg930 := time.FixedZone("X", -9*3600-30*60)

	times := []time.Time{
		time.Date(2024, 6, 15, 20, 30, 0, 0, tz8),
		time.Date(2024, 1, 1, 0, 0, 0, 0, tzNeg5),
		time.Date(2024, 12, 31, 23, 59, 59, 999999999, tzIndia),
		time.Date(2000, 6, 15, 12, 0, 0, 0, tzNeg930),
		// Local timezone — should yield to Go and still produce correct output
		time.Date(2024, 3, 15, 10, 0, 0, 0, time.Local),
	}
	for _, ts := range times {
		vjData, err := Marshal(ts)
		if err != nil {
			t.Fatalf("marshal %v: %v", ts, err)
		}
		stdData, _ := json.Marshal(ts)
		if string(vjData) != string(stdData) {
			t.Errorf("time %v:\n  vjson:  %s\n  stdlib: %s", ts, vjData, stdData)
		}
	}
}

func TestMarshal_TimeTime_Nanoseconds(t *testing.T) {
	// Various nanosecond precisions — trailing zeros should be truncated
	cases := []time.Time{
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),         // no fractional
		time.Date(2024, 1, 1, 0, 0, 0, 100000000, time.UTC), // .1
		time.Date(2024, 1, 1, 0, 0, 0, 120000000, time.UTC), // .12
		time.Date(2024, 1, 1, 0, 0, 0, 123000000, time.UTC), // .123
		time.Date(2024, 1, 1, 0, 0, 0, 123456000, time.UTC), // .123456
		time.Date(2024, 1, 1, 0, 0, 0, 123456789, time.UTC), // .123456789
		time.Date(2024, 1, 1, 0, 0, 0, 1, time.UTC),         // .000000001
		time.Date(2024, 1, 1, 0, 0, 0, 10, time.UTC),        // .00000001
		time.Date(2024, 1, 1, 0, 0, 0, 999999999, time.UTC), // .999999999
	}
	for _, ts := range cases {
		vjData, err := Marshal(ts)
		if err != nil {
			t.Fatalf("marshal %v: %v", ts, err)
		}
		stdData, _ := json.Marshal(ts)
		if string(vjData) != string(stdData) {
			t.Errorf("nsec %d:\n  vjson:  %s\n  stdlib: %s", ts.Nanosecond(), vjData, stdData)
		}
	}
}

func TestMarshal_TimeTime_EdgeYears(t *testing.T) {
	cases := []time.Time{
		time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC),         // year 0
		time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC),         // year 1 (Go zero value with UTC)
		time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), // max year
		time.Date(100, 1, 1, 0, 0, 0, 0, time.UTC),       // 3-digit year
		time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC),     // leap day
		time.Date(1900, 2, 28, 0, 0, 0, 0, time.UTC),     // non-leap century
		time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),    // leap day 2024
	}
	for _, ts := range cases {
		vjData, err := Marshal(ts)
		if err != nil {
			t.Fatalf("marshal %v: %v", ts, err)
		}
		stdData, _ := json.Marshal(ts)
		if string(vjData) != string(stdData) {
			t.Errorf("year %d:\n  vjson:  %s\n  stdlib: %s", ts.Year(), vjData, stdData)
		}
	}
}

func TestMarshal_TimeTime_StructFields(t *testing.T) {
	type Event struct {
		Name      string     `json:"name"`
		CreatedAt time.Time  `json:"created_at"`
		UpdatedAt *time.Time `json:"updated_at"`
		DeletedAt *time.Time `json:"deleted_at,omitempty"`
	}

	now := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	later := time.Date(2024, 6, 16, 8, 0, 0, 0, time.FixedZone("", 3600))

	cases := []struct {
		name string
		val  Event
	}{
		{"all fields", Event{Name: "test", CreatedAt: now, UpdatedAt: &later, DeletedAt: nil}},
		{"nil pointer omitempty", Event{Name: "x", CreatedAt: now}},
		{"non-nil pointer", Event{Name: "y", CreatedAt: now, UpdatedAt: &now}},
		{"zero time", Event{Name: "z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vjData, err := Marshal(tc.val)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			stdData, _ := json.Marshal(tc.val)
			if string(vjData) != string(stdData) {
				t.Errorf("vjson:  %s\nstdlib: %s", vjData, stdData)
			}
		})
	}
}

func TestMarshal_TimeTime_Slice(t *testing.T) {
	times := []time.Time{
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 15, 12, 30, 45, 123456789, time.UTC),
		time.Date(2024, 12, 31, 23, 59, 59, 0, time.FixedZone("", -7*3600)),
	}
	vjData, err := Marshal(times)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	stdData, _ := json.Marshal(times)
	if string(vjData) != string(stdData) {
		t.Errorf("vjson:  %s\nstdlib: %s", vjData, stdData)
	}

	// Empty slice
	empty := []time.Time{}
	vjData, err = Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	stdData, _ = json.Marshal(empty)
	if string(vjData) != string(stdData) {
		t.Errorf("empty: vjson %s != stdlib %s", vjData, stdData)
	}

	// Nil slice
	var nilSlice []time.Time
	vjData, err = Marshal(nilSlice)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	stdData, _ = json.Marshal(nilSlice)
	if string(vjData) != string(stdData) {
		t.Errorf("nil: vjson %s != stdlib %s", vjData, stdData)
	}
}

func TestMarshal_TimeTime_PointerDeref(t *testing.T) {
	ts := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	tsFixed := time.Date(2024, 6, 15, 12, 0, 0, 0, time.FixedZone("", 9*3600))

	type S struct {
		T *time.Time `json:"t"`
	}
	cases := []struct {
		name string
		val  S
	}{
		{"non-nil UTC", S{T: &ts}},
		{"non-nil FixedZone", S{T: &tsFixed}},
		{"nil", S{T: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vjData, err := Marshal(tc.val)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			stdData, _ := json.Marshal(tc.val)
			if string(vjData) != string(stdData) {
				t.Errorf("vjson:  %s\nstdlib: %s", vjData, stdData)
			}
		})
	}
}

func TestMarshal_TimeTime_Indent(t *testing.T) {
	type Event struct {
		Name string    `json:"name"`
		At   time.Time `json:"at"`
	}
	ev := Event{
		Name: "deploy",
		At:   time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC),
	}
	vjData, err := MarshalIndent(ev, "", "  ")
	if err != nil {
		t.Fatalf("marshal indent: %v", err)
	}
	stdData, _ := json.MarshalIndent(ev, "", "  ")
	if string(vjData) != string(stdData) {
		t.Errorf("indent:\n  vjson:  %s\n  stdlib: %s", vjData, stdData)
	}

	// Slice with indent
	times := []time.Time{
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 15, 0, 0, 0, 0, time.FixedZone("", 8*3600)),
	}
	vjData, err = MarshalIndent(times, "", "\t")
	if err != nil {
		t.Fatalf("marshal indent slice: %v", err)
	}
	stdData, _ = json.MarshalIndent(times, "", "\t")
	if string(vjData) != string(stdData) {
		t.Errorf("indent slice:\n  vjson:  %s\n  stdlib: %s", vjData, stdData)
	}
}

func TestMarshal_TimeTime_Nested(t *testing.T) {
	type Inner struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	type Outer struct {
		Name   string `json:"name"`
		Period Inner  `json:"period"`
	}
	val := Outer{
		Name: "Q1",
		Period: Inner{
			Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2024, 3, 31, 23, 59, 59, 0, time.UTC),
		},
	}
	vjData, err := Marshal(val)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	stdData, _ := json.Marshal(val)
	if string(vjData) != string(stdData) {
		t.Errorf("nested:\n  vjson:  %s\n  stdlib: %s", vjData, stdData)
	}
}

func TestMarshal_TimeTime_Omitempty(t *testing.T) {
	type S struct {
		Name string    `json:"name"`
		T    time.Time `json:"t,omitempty"`
	}
	// Zero time with omitempty — stdlib omits zero time
	zero := S{Name: "test"}
	vjData, err := Marshal(zero)
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	stdData, _ := json.Marshal(zero)
	if string(vjData) != string(stdData) {
		t.Errorf("omitempty zero:\n  vjson:  %s\n  stdlib: %s", vjData, stdData)
	}

	// Non-zero time with omitempty
	nonZero := S{Name: "test", T: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	vjData, err = Marshal(nonZero)
	if err != nil {
		t.Fatalf("marshal non-zero: %v", err)
	}
	stdData, _ = json.Marshal(nonZero)
	if string(vjData) != string(stdData) {
		t.Errorf("omitempty non-zero:\n  vjson:  %s\n  stdlib: %s", vjData, stdData)
	}
}

func TestMarshal_TimeTime_Now(t *testing.T) {
	// time.Now() — local timezone, monotonic clock bit set.
	// The C VM should yield to Go for DST timezones and still produce correct output.
	now := time.Now()
	vjData, err := Marshal(now)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	stdData, _ := json.Marshal(now)
	if string(vjData) != string(stdData) {
		t.Errorf("time.Now():\n  vjson:  %s\n  stdlib: %s", vjData, stdData)
	}
}
