package orchestrator

import "testing"

func BenchmarkDecide(b *testing.B) {
	signals := []Signal{
		{Source: SourceCI, Kind: KindFail},
		{Source: SourceCI, Kind: KindPass},
		{Source: SourceDevGate, Kind: KindFail},
		{Source: SourceDevGate, Kind: KindPass},
		{Source: SourceProdHealth, Kind: KindBreach},
		{Source: SourceProdHealth, Kind: KindPass},
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, s := range signals {
			_ = Decide(s)
		}
	}
}
