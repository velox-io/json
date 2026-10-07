package benchmark

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	"dev.local/benchmark/corpus"
	"github.com/klauspost/compress/zstd"
)

func mustCorpus(name string) []byte {
	data, err := corpus.Load(name)
	if err != nil {
		panic(err)
	}
	return data
}

var (
	TinyJSON        = mustCorpus("tiny") // payload for b0_overhead_test.go probes
	SmallJSON       = mustCorpus("small")
	MediumJSON      = mustCorpus("medium")
	EscapeHeavyJSON = mustCorpus("escape_heavy")
	KubePodsJSON    = mustCorpus("kubepods")
	TwitterJSON     = mustCorpus("twitter_status")
)

//go:embed corpus/testdata/log.json.zst
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

// SmallMapAnyJSON is a small flat map[string]any fixture: one database
// audit log line (453B, 24 keys, mixed scalars plus one string array).
const SmallMapAnyJSON = `{"cs":50,"timestamp":1755057964,"threadId":416,"checkRows":1430,"affectRows":0,"sentRows":1,"lockWaitTime":78,"cpuTime":2361447,"ioWaitTime":0,"nsTime":642463050,"trxLivingTime":2329,"execTime":2362,"errCode":0,"ruleNum":0,"host":"127.0.0.1","user":"tencentroot","dbName":"","policyName":"","sql":"show global variables like 'cdb_working_mode_enabled'","sqlType":"OTHER","trxId":0,"tableName":["performance_schema.global_variables"],"clientPort":51540}`

var SmallMapAnyBytes = []byte(SmallMapAnyJSON)

// Compact (whitespace-stripped) versions of all JSON test data, lazily initialized.
var (
	smallCompactOnce sync.Once
	smallCompactData []byte

	escapeHeavyCompactOnce sync.Once
	escapeHeavyCompactData []byte

	podsCompactOnce sync.Once
	podsCompactData []byte

	twitterCompactOnce sync.Once
	twitterCompactData []byte

	mediumCompactOnce sync.Once
	mediumCompactData []byte
)

func LoadSmallCompactJSON() []byte {
	smallCompactOnce.Do(func() { smallCompactData = compact(SmallJSON) })
	return smallCompactData
}

func LoadEscapeHeavyCompactJSON() []byte {
	escapeHeavyCompactOnce.Do(func() { escapeHeavyCompactData = compact(EscapeHeavyJSON) })
	return escapeHeavyCompactData
}

func LoadPodsCompactJSON() []byte {
	podsCompactOnce.Do(func() { podsCompactData = compact(KubePodsJSON) })
	return podsCompactData
}

func LoadTwitterCompactJSON() []byte {
	twitterCompactOnce.Do(func() { twitterCompactData = compact(TwitterJSON) })
	return twitterCompactData
}

func LoadMediumCompactJSON() []byte {
	mediumCompactOnce.Do(func() { mediumCompactData = compact(MediumJSON) })
	return mediumCompactData
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
// ~65 KB, just over half the default 128 KB buffer. This forces the
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
// ~86 KB, about one-third of the 256 KB buffer that maybeNewBuffer
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

// =============================================================================
// LLM API: a request and a response of the Chat Completions API
//
// The JSON of the payloads is built by the corpus ( see corpus/llm_payload.go ),
// indented there as the other datasets are. What is measured is the compact
// form, which is what an API sends, and the values are decoded from it by the
// standard library on the first use, as the values of the other datasets are.
// =============================================================================

var (
	llmToolsJSONOnce sync.Once
	llmToolsJSONData []byte
	llmToolsValueOne sync.Once
	llmToolsValue    ChatCompletionRequest

	llmRequestJSONOnce sync.Once
	llmRequestJSONData []byte
	llmRequestValueOne sync.Once
	llmRequestValue    ChatCompletionRequest

	llmResponseJSONOnce sync.Once
	llmResponseJSONData []byte
	llmResponseValueOne sync.Once
	llmResponseValue    ChatCompletionResponse
)

func LoadLLMToolsJSON() []byte {
	llmToolsJSONOnce.Do(func() { llmToolsJSONData = compact(corpus.LLMToolsJSON()) })
	return llmToolsJSONData
}

func LoadLLMRequestJSON() []byte {
	llmRequestJSONOnce.Do(func() { llmRequestJSONData = compact(corpus.LLMRequestJSON()) })
	return llmRequestJSONData
}

func LoadLLMResponseJSON() []byte {
	llmResponseJSONOnce.Do(func() { llmResponseJSONData = compact(corpus.LLMResponseJSON()) })
	return llmResponseJSONData
}

// =============================================================================
// GitHub REST API: a page of 30 issues of a repository
// =============================================================================

var (
	githubIssuesJSONOnce sync.Once
	githubIssuesJSONData []byte
)

// LoadGitHubIssuesJSON is the compact JSON of a page of 30 issues of the
// GitHub REST API, in the key order of the response.
func LoadGitHubIssuesJSON() []byte {
	githubIssuesJSONOnce.Do(func() { githubIssuesJSONData = corpus.GitHubIssuesJSON() })
	return githubIssuesJSONData
}

// loadLLMToolsValue is the value of the definitions of the five tools of the
// session: a map[string]JSONSchema per tool, and nothing else.
func loadLLMToolsValue() *ChatCompletionRequest {
	llmToolsValueOne.Do(func() {
		if err := json.Unmarshal(LoadLLMToolsJSON(), &llmToolsValue); err != nil {
			panic("load llm_tools: " + err.Error())
		}
	})
	return &llmToolsValue
}

// loadLLMRequestValue is the value of one turn of the session: the tools, five
// calls and their results, one of which is a source file of 15 KB.
func loadLLMRequestValue() *ChatCompletionRequest {
	llmRequestValueOne.Do(func() {
		if err := json.Unmarshal(LoadLLMRequestJSON(), &llmRequestValue); err != nil {
			panic("load llm_request: " + err.Error())
		}
	})
	return &llmRequestValue
}

// loadLLMResponseValue is the value of the answer of the model.
func loadLLMResponseValue() *ChatCompletionResponse {
	llmResponseValueOne.Do(func() {
		if err := json.Unmarshal(LoadLLMResponseJSON(), &llmResponseValue); err != nil {
			panic("load llm_response: " + err.Error())
		}
	})
	return &llmResponseValue
}
