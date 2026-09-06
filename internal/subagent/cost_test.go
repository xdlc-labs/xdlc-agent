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
