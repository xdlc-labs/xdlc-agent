// example-service is the reference HTTP service for the xdlc demo loop.
// template — /healthz for k8s probes and smoke Job, OTel metrics
// (OTLP + /metrics Prometheus export) for the prod-health gate.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0" // must match resource.Default()'s schema (SDK-version-dependent)

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	svc := os.Getenv("OTEL_SERVICE_NAME")
	if svc == "" {
		svc = "example-service"
	}

	mp, promHandler, shutdown, err := setupMetrics(ctx, svc)
	if err != nil {
		log.Fatalf("otel: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	otel.SetMeterProvider(mp)

	meter := mp.Meter("example-service")
	reqCounter, err := meter.Int64Counter("http_requests",
		metric.WithDescription("Total HTTP requests"))
	if err != nil {
		log.Fatal(err)
	}
	latency, err := meter.Float64Histogram("http_request_duration_seconds",
		metric.WithDescription("HTTP latency"),
		metric.WithUnit("s"))
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		start := time.Now()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("xdlc example-service\n"))
		attrs := metric.WithAttributes(
			attribute.String("service", svc),
			attribute.String("status", "200"),
		)
		reqCounter.Add(context.Background(), 1, attrs)
		latency.Record(context.Background(), time.Since(start).Seconds(), attrs)
	})
	if promHandler != nil {
		mux.Handle("/metrics", promHandler)
	}

	addr := ":8080"
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func setupMetrics(ctx context.Context, svc string) (*sdkmetric.MeterProvider, http.Handler, func(context.Context) error, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(svc),
		),
	)
	if err != nil {
		return nil, nil, nil, err
	}

	registry := prometheus.NewRegistry()
	promExp, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return nil, nil, nil, err
	}
	promHandler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promExp),
	}

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "otel-collector.monitoring.svc:4318"
	}
	// Skip OTLP when explicitly disabled (local binary without collector).
	if os.Getenv("OTEL_SDK_DISABLED") != "true" {
		otlp, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpoint(endpoint),
			otlpmetrichttp.WithInsecure(),
		)
		if err != nil {
			log.Printf("otel: OTLP exporter unavailable (%v); /metrics only", err)
		} else {
			opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(otlp,
				sdkmetric.WithInterval(15*time.Second))))
		}
	}

	mp := sdkmetric.NewMeterProvider(opts...)
	return mp, promHandler, mp.Shutdown, nil
}
