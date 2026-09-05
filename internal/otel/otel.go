// Package otel bootstraps OpenTelemetry metrics for the xdlc-agent
// daemon. Always serves an in-process Prometheus registry via Handler()
// for GET /metrics. OTLP export is opt-in: only when
// OTEL_EXPORTER_OTLP_ENDPOINT is set and OTEL_SDK_DISABLED is not
// "true". An in-cluster collector default used to spam local daemons
// with "failed to upload metrics" every 15s.
package otel

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0" // must match resource.Default()'s schema (SDK-version-dependent, not independently choosable)

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds instruments the daemon records against.
type Metrics struct {
	Webhooks          metric.Int64Counter
	GateChecks        metric.Int64Counter
	Dispatch          metric.Float64Histogram
	SubagentRuns      metric.Float64Histogram
	FleetSuppressions metric.Int64Counter
	// Reruns counts CI rerun-failed-jobs attempts (issue #3).
	Reruns metric.Int64Counter
	// PollerLastTick is a gauge of the Unix timestamp (seconds) at which a
	// poller last attempted a Gate.Check for a given gate/repo, regardless
	// of outcome. Alertmanager uses `time() - this` to detect a stalled
	// poller (see observability/prometheus/rules/prod-health.yaml).
	PollerLastTick metric.Float64Gauge
	// StoreErrors counts AuditStore (bbolt, PVC-backed) read/write
	// failures, labeled by op (append|all). See
	// observability/prometheus/rules/prod-health.yaml.
	StoreErrors metric.Int64Counter
	// FixQueueDepth is waiting-for-slot + holding-slot Fix goroutines (#9).
	FixQueueDepth metric.Int64UpDownCounter
	// FixQueueWait is time spent blocked acquiring a Fix slot (#9).
	FixQueueWait metric.Float64Histogram

	handler http.Handler // Prometheus scrape; set by Setup
}

// Handler returns the Prometheus scrape handler for GET /metrics, or nil
// if Setup has not run. Open — no auth (standard scrape practice).
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return nil
	}
	return m.handler
}

// Setup installs a MeterProvider with a Prometheus reader (always) and
// optional OTLP export when OTEL_EXPORTER_OTLP_ENDPOINT is set.
// Shutdown flushes exporters; call on daemon exit.
func Setup(ctx context.Context, log *slog.Logger) (m Metrics, shutdown func(context.Context) error, err error) {
	noop := func(context.Context) error { return nil }

	registry := prometheus.NewRegistry()
	promExp, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return Metrics{}, noop, err
	}
	promHandler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("xdlc-agent")),
	)
	if err != nil {
		return Metrics{}, noop, err
	}

	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promExp),
	}

	if os.Getenv("OTEL_SDK_DISABLED") != "true" {
		endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		if endpoint != "" {
			otlp, otlpErr := otlpmetrichttp.New(ctx,
				otlpmetrichttp.WithEndpoint(endpoint),
				otlpmetrichttp.WithInsecure(),
			)
			if otlpErr != nil {
				if log != nil {
					log.Warn("otel: OTLP unavailable, /metrics only", "error", otlpErr)
				}
			} else {
				opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(otlp,
					sdkmetric.WithInterval(15*time.Second))))
			}
		}
	}

	mp := sdkmetric.NewMeterProvider(opts...)
	otelapi.SetMeterProvider(mp)
	m, err = buildInstruments(mp.Meter("xdlc-agent"))
	if err != nil {
		_ = mp.Shutdown(ctx)
		return Metrics{}, noop, err
	}
	m.handler = promHandler
	return m, mp.Shutdown, nil
}

func buildInstruments(meter metric.Meter) (Metrics, error) {
	webhooks, err := meter.Int64Counter("xdlc_agent_webhooks_total",
		metric.WithDescription("Webhook deliveries received"))
	if err != nil {
		return Metrics{}, err
	}
	gates, err := meter.Int64Counter("xdlc_agent_gate_checks_total",
		metric.WithDescription("Gate check outcomes"))
	if err != nil {
		return Metrics{}, err
	}
	dispatch, err := meter.Float64Histogram("xdlc_agent_dispatch_duration_seconds",
		metric.WithDescription("Dispatch action latency"),
		metric.WithUnit("s"))
	if err != nil {
		return Metrics{}, err
	}
	subagent, err := meter.Float64Histogram("xdlc_agent_subagent_duration_seconds",
		metric.WithDescription("Subagent run duration"),
		metric.WithUnit("s"))
	if err != nil {
		return Metrics{}, err
	}
	suppress, err := meter.Int64Counter("xdlc_agent_fleet_suppressions_total",
		metric.WithDescription("Actions suppressed by fleet policy (root_cause, circuit, flap)"))
	if err != nil {
		return Metrics{}, err
	}
	reruns, err := meter.Int64Counter("xdlc_agent_reruns_total",
		metric.WithDescription("CI rerun-failed-jobs attempts before Fix"))
	if err != nil {
		return Metrics{}, err
	}
	pollerTick, err := meter.Float64Gauge("xdlc_agent_poller_last_tick_timestamp_seconds",
		metric.WithDescription("Unix timestamp of the last poller tick attempt, per gate/repo"))
	if err != nil {
		return Metrics{}, err
	}
	storeErrors, err := meter.Int64Counter("xdlc_agent_store_errors_total",
		metric.WithDescription("AuditStore (bbolt) read/write errors, by op"))
	if err != nil {
		return Metrics{}, err
	}
	fixDepth, err := meter.Int64UpDownCounter("xdlc_agent_fix_queue_depth",
		metric.WithDescription("Fix goroutines waiting for a slot or holding one"))
	if err != nil {
		return Metrics{}, err
	}
	fixWait, err := meter.Float64Histogram("xdlc_agent_fix_queue_wait_seconds",
		metric.WithDescription("Time waiting to acquire a Fix concurrency slot"),
		metric.WithUnit("s"))
	if err != nil {
		return Metrics{}, err
	}
	return Metrics{
		Webhooks:          webhooks,
		GateChecks:        gates,
		Dispatch:          dispatch,
		SubagentRuns:      subagent,
		FleetSuppressions: suppress,
		Reruns:            reruns,
		PollerLastTick:    pollerTick,
		StoreErrors:       storeErrors,
		FixQueueDepth:     fixDepth,
		FixQueueWait:      fixWait,
	}, nil
}

// AttrSource is an OTel attribute for signal source (ci, webhook, poller).
func AttrSource(source string) attribute.KeyValue { return attribute.String("source", source) }

// AttrKind is an OTel attribute for signal kind (fail, pass, breach).
func AttrKind(kind string) attribute.KeyValue { return attribute.String("kind", kind) }

// AttrAction is an OTel attribute for dispatch action (fix, promote, revert).
func AttrAction(action string) attribute.KeyValue { return attribute.String("action", action) }

// AttrGate is an OTel attribute for gate name (ci, dev-smoke, prod-health).
func AttrGate(gate string) attribute.KeyValue { return attribute.String("gate", gate) }

// AttrStatus is an OTel attribute for outcome status (ok, error).
func AttrStatus(status string) attribute.KeyValue { return attribute.String("status", status) }

// AttrRepo is an OTel attribute for a configured repo's short name.
func AttrRepo(repo string) attribute.KeyValue { return attribute.String("repo", repo) }

// AttrOp is an OTel attribute for a store operation (append, all).
func AttrOp(op string) attribute.KeyValue { return attribute.String("op", op) }
