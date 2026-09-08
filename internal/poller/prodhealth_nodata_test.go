package poller

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/gate"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/promclient"
)

// The response bodies a Prometheus-compatible instant-query API can
// hand back, framed exactly as the HTTP API frames them.
//
// noSeries is the issue #48 case: a perfectly successful query that
// matched nothing, which is what a renamed metric, a dead exporter, a
// relabel change or a typo in the configured query produces.
const promNoSeries = `{"status":"success","data":{"resultType":"vector","result":[]}}`

func promSeries(v string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"` + v + `"]}]}}`
}

// The four scenarios, as (p95, error-rate) response pairs against
// thresholds of 500ms / 0.01.
var (
	promPairHealthy = [2]string{promSeries("120"), promSeries("0.001")}
	promPairBreach  = [2]string{promSeries("1800"), promSeries("0.001")}
	promPairZero    = [2]string{promSeries("0"), promSeries("0")} // series present, value 0
	promPairEmpty   = [2]string{promNoSeries, promNoSeries}
)

// fakeProm answers the p95 query and the error-rate query separately —
// they have different thresholds, so one shared body cannot express
// "healthy" — and serves whichever pair is currently loaded, so one
// test can walk a gate through healthy → breach → no-data without
// restarting the server.
type fakeProm struct {
	pair atomic.Value // [2]string
	srv  *httptest.Server
}

func newFakeProm(t *testing.T, pair [2]string) *fakeProm {
	t.Helper()
	f := &fakeProm{}
	f.pair.Store(pair)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := f.pair.Load().([2]string)
		body := p[1]
		if strings.Contains(r.URL.Query().Get("query"), "histogram_quantile") {
			body = p[0]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeProm) serve(pair [2]string) { f.pair.Store(pair) }

const (
	testP95Query = `histogram_quantile(0.95, http_request_duration_seconds{service="{{repo}}"})`
	testErrQuery = `rate(http_requests_total{service="{{repo}}",code=~"5.."}[5m])`
)

// prodHealthPoller wires a real ProdHealthGate against prom through the
// real promclient, so these tests exercise the whole path an operator's
// daemon takes rather than a stubbed Query closure.
func prodHealthPoller(prom *fakeProm, logs *bytes.Buffer) (*Poller, chan orchestrator.Signal) {
	ch := make(chan orchestrator.Signal, 32)
	g := &gate.ProdHealthGate{
		P95ThresholdMS:  500,
		ErrorRateThresh: 0.01,
		P95Query:        testP95Query,
		ErrorRateQuery:  testErrQuery,
		Query:           promclient.New(prom.srv.URL).Query,
	}
	return &Poller{
		Gate:    g,
		Repos:   []string{"api"},
		Source:  orchestrator.SourceProdHealth,
		Signals: ch,
		Log:     slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		SHA:     func(context.Context, string) (string, error) { return "deadbee", nil },
	}, ch
}

// TestProdHealthNoDataDoesNotReadAsHealthy is the issue #48 regression,
// end to end through promclient, ProdHealthGate and the poller.
//
// The gate fails only on value > threshold, so an empty result set
// arriving as 0 always passed. Against a real daemon that meant a
// renamed metric, a dead exporter, a relabel change or a typo in
// p95_query silently switched breach detection off — and, worse, the
// poller's edge trigger read that "pass" as a recovery and cleared a
// breach that was still live, undoing the revert.
func TestProdHealthNoDataDoesNotReadAsHealthy(t *testing.T) {
	prom := newFakeProm(t, promPairHealthy)
	var logs bytes.Buffer
	p, ch := prodHealthPoller(prom, &logs)

	// 1. A healthy series passes.
	p.tick(context.Background(), 30*time.Second)
	got := drain(ch)
	if len(got) != 1 || got[0].Kind != orchestrator.KindPass {
		t.Fatalf("healthy series: signals = %+v, want one pass", got)
	}

	// 2. A breaching series breaches, and that is what drives a revert.
	prom.serve(promPairBreach)
	p.tick(context.Background(), 30*time.Second)
	got = drain(ch)
	if len(got) != 1 || got[0].Kind != orchestrator.KindBreach {
		t.Fatalf("breaching series: signals = %+v, want one breach", got)
	}
	if act := orchestrator.Decide(got[0]); act != orchestrator.ActionRevert {
		t.Fatalf("breach Decide = %s, want revert", act)
	}

	// 3. The series vanishes while the breach is still live. This must
	//    not look like the service got better.
	prom.serve(promPairEmpty)
	p.tick(context.Background(), 30*time.Second)
	got = drain(ch)
	if len(got) != 1 {
		t.Fatalf("no data: signals = %+v, want exactly one blocked", got)
	}
	sig := got[0]
	if sig.Kind == orchestrator.KindPass {
		t.Fatal("no data reported a pass, clearing the live breach")
	}
	if sig.Kind != orchestrator.KindBlocked {
		t.Fatalf("no data: kind = %s, want %s", sig.Kind, orchestrator.KindBlocked)
	}
	if act := orchestrator.Decide(sig); act != orchestrator.ActionNoop {
		t.Fatalf("blocked Decide = %s, want noop", act)
	}
	if sig.Evidence["escalate"] != orchestrator.EscalateGateUnavailable {
		t.Fatalf("escalate = %v, want %s", sig.Evidence["escalate"], orchestrator.EscalateGateUnavailable)
	}
	if sig.Evidence["gate"] != "prod-health" {
		t.Fatalf("gate = %v", sig.Evidence["gate"])
	}
	reason, ok := orchestrator.BlockedReason(sig)
	if !ok {
		t.Fatal("BlockedReason did not recognise the signal")
	}
	// The reason is what reaches BACKLOG.md, the audit row and the
	// console Activity feed, so it has to name the query that came back
	// empty and say the verdict is unknown rather than bad.
	// The PromQL text is quoted with %q, so it appears escaped.
	wantQuery := strconv.Quote(strings.ReplaceAll(testP95Query, "{{repo}}", "api"))
	for _, want := range []string{
		"p95_query",
		"matched no series",
		"unknown, not healthy",
		wantQuery,
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("blocked reason missing %q:\n%s", want, reason)
		}
	}
	if !strings.Contains(logs.String(), "gate check failed") {
		t.Errorf("no operator-visible log line:\n%s", logs.String())
	}

	// 4. The breach is still the last verdict on record, so when the
	//    series comes back still breaching the poller stays quiet
	//    (nothing changed) rather than emitting a second breach — which
	//    is only true if step 3 did not overwrite the edge with a pass.
	prom.serve(promPairBreach)
	p.tick(context.Background(), 30*time.Second)
	if got = drain(ch); len(got) != 0 {
		t.Fatalf("blocked tick clobbered the breach edge: %+v", got)
	}
}

// TestProdHealthNoDataNamesTheBrokenQuery: when only the error-rate
// query has been broken, the blocked reason must say error_rate_query
// and not send the operator looking at p95_query.
func TestProdHealthNoDataNamesTheBrokenQuery(t *testing.T) {
	prom := newFakeProm(t, [2]string{promSeries("120"), promNoSeries})
	var logs bytes.Buffer
	p, ch := prodHealthPoller(prom, &logs)

	p.tick(context.Background(), 30*time.Second)
	got := drain(ch)
	if len(got) != 1 || got[0].Kind != orchestrator.KindBlocked {
		t.Fatalf("signals = %+v, want one blocked", got)
	}
	reason, _ := orchestrator.BlockedReason(got[0])
	if !strings.Contains(reason, "error_rate_query") {
		t.Errorf("reason does not name error_rate_query:\n%s", reason)
	}
	if strings.Contains(reason, "p95_query") {
		t.Errorf("reason blames the working query too:\n%s", reason)
	}
}

// TestProdHealthNoDataRepeatsAreSuppressed: a permanently wrong query
// must not write a BACKLOG.md line and an audit row every interval. The
// issue #45 per-repo episode guard covers this, and the #48 change must
// not route around it.
func TestProdHealthNoDataRepeatsAreSuppressed(t *testing.T) {
	prom := newFakeProm(t, promPairEmpty)
	var logs bytes.Buffer
	p, ch := prodHealthPoller(prom, &logs)

	for i := 0; i < 10; i++ {
		p.tick(context.Background(), 30*time.Second)
	}
	if got := drain(ch); len(got) != 1 {
		t.Fatalf("10 no-data ticks emitted %d signals, want 1", len(got))
	}
	if n := strings.Count(logs.String(), "gate check failed"); n != 1 {
		t.Fatalf("10 no-data ticks logged %d error lines, want 1:\n%s", n, logs.String())
	}

	// The series comes back healthy: a real verdict again, and the
	// episode is re-armed so the next break is reported rather than
	// staying silent for the life of the process.
	prom.serve(promPairHealthy)
	p.tick(context.Background(), 30*time.Second)
	got := drain(ch)
	if len(got) != 1 || got[0].Kind != orchestrator.KindPass {
		t.Fatalf("recovery: signals = %+v, want one pass", got)
	}
	prom.serve(promPairEmpty)
	p.tick(context.Background(), 30*time.Second)
	got = drain(ch)
	if len(got) != 1 || got[0].Kind != orchestrator.KindBlocked {
		t.Fatalf("break after recovery: signals = %+v, want one blocked", got)
	}
}

// TestProdHealthGenuineZeroIsAPass: p95 and error rate of 0 read off
// series that exist is the healthiest reading there is, and the #48 fix
// must leave it a pass. Telling this apart from an empty result set is
// the whole point of the sentinel.
func TestProdHealthGenuineZeroIsAPass(t *testing.T) {
	prom := newFakeProm(t, promPairZero)
	var logs bytes.Buffer
	p, ch := prodHealthPoller(prom, &logs)

	p.tick(context.Background(), 30*time.Second)
	got := drain(ch)
	if len(got) != 1 {
		t.Fatalf("signals = %+v, want one", got)
	}
	if got[0].Kind != orchestrator.KindPass {
		t.Fatalf("kind = %s, want pass — a present series reading 0 is data", got[0].Kind)
	}
	if got[0].Evidence["p95_ms"] != 0.0 || got[0].Evidence["error_rate"] != 0.0 {
		t.Fatalf("evidence = %v", got[0].Evidence)
	}
	if strings.Contains(logs.String(), "gate check failed") {
		t.Fatalf("genuine zero logged a gate failure:\n%s", logs.String())
	}
}
