package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetupNoEndpointSkipsOTLP(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	m, shutdown, err := Setup(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	if m.Handler() == nil {
		t.Fatal("Handler nil — /metrics must work without OTLP")
	}
	m.Webhooks.Add(context.Background(), 1)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "xdlc_agent_webhooks_total") {
		t.Fatalf("missing metric in body: %s", rec.Body.String())
	}
}

func TestSetupDisabled(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	m, shutdown, err := Setup(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Webhooks == nil || m.GateChecks == nil || m.Dispatch == nil || m.SubagentRuns == nil || m.FleetSuppressions == nil {
		t.Fatal("instruments nil")
	}
	if m.Handler() == nil {
		t.Fatal("Handler nil — /metrics must work with OTEL_SDK_DISABLED")
	}
	m.Webhooks.Add(context.Background(), 1)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "xdlc_agent_webhooks_total") {
		t.Fatalf("missing metric in body: %s", rec.Body.String())
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAttrHelpers(t *testing.T) {
	if AttrSource("ci").Value.AsString() != "ci" {
		t.Fatal(AttrSource("ci"))
	}
	if AttrKind("fail").Value.AsString() != "fail" {
		t.Fatal(AttrKind("fail"))
	}
	if AttrAction("fix").Value.AsString() != "fix" {
		t.Fatal(AttrAction("fix"))
	}
	if AttrGate("ci").Value.AsString() != "ci" {
		t.Fatal(AttrGate("ci"))
	}
	if AttrStatus("ok").Value.AsString() != "ok" {
		t.Fatal(AttrStatus("ok"))
	}
}
