package benchmark

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

var TinyJSON = []byte(`{
	"bool": true,
	"int": 42,
	"int64": 9223372036854775807,
	"float64": 3.14159265358979,
	"string": "hello world benchmark"
}`)

var SmallJSON = []byte(`{"id":12125925,"ids":[-2147483648,2147483647],"title":"未来简史-从智人到智神","titles":["hello","world"],"price":40.8,"prices":[-0.1,0.1],"hot":true,"hots":[true,true,true],"author":{"name":"json","age":99,"male":true},"authors":[{"name":"json","age":99,"male":true},{"name":"json","age":99,"male":true},{"name":"json","age":99,"male":true}],"weights":[]}`)

//go:embed testdata/escape_heavy.json
var EscapeHeavyJSON []byte

//go:embed testdata/pods.json
var PodsJSON []byte

//go:embed testdata/twitter.json
var TwitterJSON []byte

//go:embed testdata/log.json.zst
var logJSONZst []byte
var logJSONLoadOnce sync.Once
var logJSONData []byte

// LoadLogNDJSONL decompresses NDJSON log stream (~90K lines).
func LoadLogNDJSON() []byte {
	logJSONLoadOnce.Do(func() {
		logJSONData = mustDecompressZstd(logJSONZst)
	})
	return logJSONData
}

// Compact (whitespace-stripped) versions of all JSON test data, lazily initialized.
var (
	tinyCompactOnce sync.Once
	tinyCompactData []byte

	smallCompactOnce sync.Once
	smallCompactData []byte

	escapeHeavyCompactOnce sync.Once
	escapeHeavyCompactData []byte

	podsCompactOnce sync.Once
	podsCompactData []byte

	twitterCompactOnce sync.Once
	twitterCompactData []byte
)

func LoadTinyCompactJSON() []byte {
	tinyCompactOnce.Do(func() { tinyCompactData = compact(TinyJSON) })
	return tinyCompactData
}

func LoadSmallCompactJSON() []byte {
	smallCompactOnce.Do(func() { smallCompactData = compact(SmallJSON) })
	return smallCompactData
}

func LoadEscapeHeavyCompactJSON() []byte {
	escapeHeavyCompactOnce.Do(func() { escapeHeavyCompactData = compact(EscapeHeavyJSON) })
	return escapeHeavyCompactData
}

func LoadPodsCompactJSON() []byte {
	podsCompactOnce.Do(func() { podsCompactData = compact(PodsJSON) })
	return podsCompactData
}

func LoadTwitterCompactJSON() []byte {
	twitterCompactOnce.Do(func() { twitterCompactData = compact(TwitterJSON) })
	return twitterCompactData
}

func compact(src []byte) []byte {
	var buf bytes.Buffer
	if err := json.Compact(&buf, src); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func mustDecompressZstd(src []byte) []byte {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		panic(err)
	}
	defer dec.Close()
	out, err := dec.DecodeAll(src, nil)
	if err != nil {
		panic(err)
	}
	return out
}


// buildSpikyNDJSON constructs an NDJSON stream with periodic large spikes.
//
// Pattern per cycle: [spikyGap small values] + [1 large spike].
// The gap between spikes is large enough that the decoder's prediction
// window (average of last 2 value sizes) fully converges on the small
// size before each spike arrives.
//
// Knobs:
//   - spikySmallItems / spikySmallPayloadLen  → ~300 byte values
//   - spikyLargeItems / spikyLargePayloadLen  → ~2 MB values
//   - spikyGap                                → small values between spikes
//   - spikyCycles                             → number of spike events
const (
	spikySmallItems      = 2
	spikySmallPayloadLen = 32
	spikyLargeItems      = 2000
	spikyLargePayloadLen = 512
	spikyGap             = 20
	spikyCycles          = 5
)

var (
	spikyNDJSONOnce sync.Once
	spikyNDJSONData []byte
)

func LoadSpikyNDJSON() []byte {
	spikyNDJSONOnce.Do(func() { spikyNDJSONData = buildSpikyNDJSON() })
	return spikyNDJSONData
}

func makeSpikyPayload(kind string, seq, nItems, payloadLen int) SpikyPayload {
	filler := strings.Repeat("x", payloadLen)
	items := make([]SpikyItem, nItems)
	for i := range items {
		items[i] = SpikyItem{ID: i, Name: "item", Payload: filler}
	}
	return SpikyPayload{Kind: kind, Seq: seq, Items: items}
}

func buildSpikyNDJSON() []byte {
	var buf bytes.Buffer
	seq := 0
	for cycle := range spikyCycles {
		_ = cycle
		for range spikyGap {
			v := makeSpikyPayload("small", seq, spikySmallItems, spikySmallPayloadLen)
			b, _ := json.Marshal(v)
			buf.Write(b)
			buf.WriteByte('\n')
			seq++
		}
		v := makeSpikyPayload("spike", seq, spikyLargeItems, spikyLargePayloadLen)
		b, _ := json.Marshal(v)
		buf.Write(b)
		buf.WriteByte('\n')
		seq++
	}
	return buf.Bytes()
}

// buildHalfBufNDJSON constructs an NDJSON stream where every value is
// ~65 KB — just over half the default 128 KB buffer. This forces the
// decoder to allocate a new buffer for almost every value, since the
// remaining capacity after decoding one value cannot hold the next.
const (
	halfBufItems      = 120
	halfBufPayloadLen = 512
	halfBufCount      = 50
)

var (
	halfBufNDJSONOnce sync.Once
	halfBufNDJSONData []byte
)

func LoadHalfBufNDJSON() []byte {
	halfBufNDJSONOnce.Do(func() { halfBufNDJSONData = buildHalfBufNDJSON() })
	return halfBufNDJSONData
}

func buildHalfBufNDJSON() []byte {
	var buf bytes.Buffer
	for i := range halfBufCount {
		v := makeSpikyPayload("half", i, halfBufItems, halfBufPayloadLen)
		b, _ := json.Marshal(v)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// buildThirdBufNDJSON constructs an NDJSON stream where every value is
// ~86 KB — about one-third of the 256 KB buffer that maybeNewBuffer
// allocates after seeing ~65 KB predictions. The 256 KB buffer fits
// exactly 2 values but not 3, so buffer switches happen every 2 values.
const (
	thirdBufItems      = 160
	thirdBufPayloadLen = 512
	thirdBufCount      = 50
)

var (
	thirdBufNDJSONOnce sync.Once
	thirdBufNDJSONData []byte
)

func LoadThirdBufNDJSON() []byte {
	thirdBufNDJSONOnce.Do(func() { thirdBufNDJSONData = buildThirdBufNDJSON() })
	return thirdBufNDJSONData
}

func buildThirdBufNDJSON() []byte {
	var buf bytes.Buffer
	for i := range thirdBufCount {
		v := makeSpikyPayload("third", i, thirdBufItems, thirdBufPayloadLen)
		b, _ := json.Marshal(v)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
