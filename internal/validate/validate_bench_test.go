package validate

import (
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
)

func BenchmarkConfig(b *testing.B) {
	cfg := &config.Config{
		Repos: []config.Repo{
			{Name: "svc-a", GitHub: "org/svc-a", Gates: []string{"ci", "dev-smoke", "prod-health"}, ArgoCDApp: "dev-svc-a", ProbeJob: "smoke-e2e"},
			{Name: "svc-b", GitHub: "org/svc-b", Gates: []string{"ci", "dev-smoke"}, ArgoCDApp: "dev-svc-b", ProbeJob: "smoke-e2e"},
			{Name: "svc-c", GitHub: "org/svc-c", Gates: []string{"ci"}},
		},
		Gates: config.GatesConfig{
			ProdHealth: config.ProdHealthGateConfig{
				MetricsURL:     "http://prometheus.example:9090",
				Thresholds:     config.Thresholds{P95MS: 500, ErrorRate: 0.01},
				P95Query:       `histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{service="{{repo}}"}[5m])) by (le)) * 1000`,
				ErrorRateQuery: `sum(rate(http_requests_total{service="{{repo}}",status=~"5.."}[5m])) / sum(rate(http_requests_total{service="{{repo}}"}[5m]))`,
			},
		},
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = Config(cfg)
	}
}
