package subagent

import (
	"testing"
)

const sampleClaudeJSON = `{
  "type": "result",
  "subtype": "success",
  "is_error": false,
  "duration_ms": 8210,
  "total_cost_usd": 0.0184,
  "usage": {
    "input_tokens": 18422,
    "output_tokens": 733,
    "cache_read_input_tokens": 0
  },
  "result": "fixed"
}`

func TestParseCostSampleFixture(t *testing.T) {
	got := ParseCost(sampleClaudeJSON)
	if got == nil {
		t.Fatal("expected cost map")
	}
	if got["total_cost_usd"] != 0.0184 {
		t.Fatalf("total_cost_usd = %v", got["total_cost_usd"])
	}
	if got["duration_ms"] != int64(8210) {
		t.Fatalf("duration_ms = %v", got["duration_ms"])
	}
	if got["input_tokens"] != int64(18422) {
		t.Fatalf("input_tokens = %v", got["input_tokens"])
	}
	if got["output_tokens"] != int64(733) {
		t.Fatalf("output_tokens = %v", got["output_tokens"])
	}
}

func TestParseCostMalformedNoop(t *testing.T) {
	if ParseCost("") != nil || ParseCost("not json") != nil || ParseCost("{") != nil {
		t.Fatal("malformed should be nil")
	}
}

func TestParseCostEmbeddedInNoise(t *testing.T) {
	got := ParseCost("push ok\n" + sampleClaudeJSON + "\n")
	if got["total_cost_usd"] != 0.0184 {
		t.Fatalf("got %v", got)
	}
}

func TestMergeCost(t *testing.T) {
	ev := map[string]any{"run_url": "http://ci/1"}
	MergeCost(ev, sampleClaudeJSON)
	if ev["total_cost_usd"] != 0.0184 || ev["run_url"] != "http://ci/1" {
		t.Fatalf("merge = %v", ev)
	}
	MergeCost(nil, sampleClaudeJSON) // must not panic
}

func TestAddCostSumsAcrossRuns(t *testing.T) {
	ev := map[string]any{}
	AddCost(ev, sampleClaudeJSON)
	first := ev["total_cost_usd"]
	AddCost(ev, sampleClaudeJSON)

	got, ok := ev["total_cost_usd"].(float64)
	if !ok {
		t.Fatalf("total_cost_usd = %T, want float64", ev["total_cost_usd"])
	}
	want, _ := first.(float64)
	if got != want*2 {
		t.Fatalf("total_cost_usd = %v after two runs, want %v", got, want*2)
	}
	// Token counts must stay integral so the audit row does not turn
	// counts into floats.
	if _, ok := ev["input_tokens"].(int64); !ok {
		t.Fatalf("input_tokens = %T (%v), want int64", ev["input_tokens"], ev["input_tokens"])
	}
}

func TestAddCostFirstRunMatchesMerge(t *testing.T) {
	added, merged := map[string]any{}, map[string]any{}
	AddCost(added, sampleClaudeJSON)
	MergeCost(merged, sampleClaudeJSON)
	if len(added) != len(merged) {
		t.Fatalf("key sets differ: %v vs %v", added, merged)
	}
	for k, v := range merged {
		if added[k] != v {
			t.Fatalf("key %s: added %v, merged %v", k, added[k], v)
		}
	}
	AddCost(nil, sampleClaudeJSON) // must not panic
}

// realFixJSON is the usage block a real Fix on this repository reported
// (session 20260908T171541Z, pull request #51, $1.6296 at list prices).
// It is here because the shape that matters is not the sample above: the
// uncached input is a rounding error next to the cache traffic.
const realFixJSON = `{
  "type": "result",
  "total_cost_usd": 1.6296047500000004,
  "usage": {
    "input_tokens": 514,
    "cache_creation_input_tokens": 49801,
    "cache_read_input_tokens": 843579,
    "output_tokens": 8351
  }
}`

func TestParseCostRecordsCacheTokens(t *testing.T) {
	got := ParseCost(realFixJSON)
	for _, c := range []struct {
		key  string
		want int64
	}{
		{"input_tokens", 514},
		{"cache_write_tokens", 49801},
		{"cache_read_tokens", 843579},
		{"output_tokens", 8351},
	} {
		if got[c.key] != c.want {
			t.Errorf("%s = %v, want %d", c.key, got[c.key], c.want)
		}
	}

	// The recorded tokens have to explain the recorded dollars. At
	// claude-fable-5-1 list prices — $10/MTok in, $50/MTok out, a 1h
	// cache write at 2x input, cache reads at $0.25/MTok — they do, to
	// within a hundredth of a cent. Without the cache fields the same
	// arithmetic lands at $0.42 against a reported $1.63.
	toks := func(k string) float64 { return float64(got[k].(int64)) }
	recomputed := (toks("input_tokens")*10.00 +
		toks("cache_write_tokens")*20.00 +
		toks("cache_read_tokens")*0.25 +
		toks("output_tokens")*50.00) / 1e6
	reported := got["total_cost_usd"].(float64)
	if diff := recomputed - reported; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("recomputed $%.6f vs reported $%.6f (diff %.6f)", recomputed, reported, diff)
	}
}

func TestAddCostSumsCacheTokensAcrossRuns(t *testing.T) {
	dst := map[string]any{}
	AddCost(dst, realFixJSON)
	AddCost(dst, realFixJSON)
	if dst["cache_read_tokens"] != int64(843579*2) {
		t.Errorf("cache_read_tokens = %v, want %d", dst["cache_read_tokens"], 843579*2)
	}
	if dst["cache_write_tokens"] != int64(49801*2) {
		t.Errorf("cache_write_tokens = %v, want %d", dst["cache_write_tokens"], 49801*2)
	}
	if got := dst["total_cost_usd"].(float64); got < 3.259 || got > 3.26 {
		t.Errorf("total_cost_usd = %v, want ~3.2592", got)
	}
}

// `--output-format stream-json` prints one event per line: several JSON
// objects, which is not one JSON document. Whole-document parsing fails
// on it, so before ParseCost read lines a streamed Fix was recorded as
// having cost nothing at all.
const sampleClaudeStreamJSON = `{"type":"system","subtype":"init","session_id":"abc"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reading the failing test"}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"result","subtype":"success","is_error":false,"duration_ms":8210,"total_cost_usd":1.63,"usage":{"input_tokens":514,"output_tokens":9012,"cache_creation_input_tokens":49801,"cache_read_input_tokens":843579}}
`

func TestParseCostReadsStreamJSON(t *testing.T) {
	got := ParseCost(sampleClaudeStreamJSON)
	if got == nil {
		t.Fatal("streamed events yielded no cost at all")
	}
	if got["total_cost_usd"] != 1.63 {
		t.Fatalf("total_cost_usd = %v", got["total_cost_usd"])
	}
	for k, want := range map[string]int64{
		"input_tokens": 514, "output_tokens": 9012,
		"cache_write_tokens": 49801, "cache_read_tokens": 843579,
	} {
		if got[k] != want {
			t.Fatalf("%s = %v, want %d", k, got[k], want)
		}
	}
	if got["duration_ms"] != int64(8210) {
		t.Fatalf("duration_ms = %v", got["duration_ms"])
	}
}

// The totals are on the last event. Reading forwards would stop at the
// first parseable line and report a Fix as free.
func TestParseCostIgnoresEarlierStreamEvents(t *testing.T) {
	stream := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"thinking"}]}}
{"type":"result","total_cost_usd":0.45}
`
	got := ParseCost(stream)
	if got == nil || got["total_cost_usd"] != 0.45 {
		t.Fatalf("want the result event's cost, got %v", got)
	}
}

// A stream that never reached a result event (killed as stalled, or the
// CLI died) has no billing totals to report, and inventing zeros would
// put a free Fix in the audit row.
func TestParseCostOnStreamWithoutResultEvent(t *testing.T) {
	stream := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}
`
	if got := ParseCost(stream); got != nil {
		t.Fatalf("want nil for a stream with no totals, got %v", got)
	}
}
