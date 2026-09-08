// Package promclient runs an instant PromQL query against any
// Prometheus-compatible HTTP API (Prometheus, VictoriaMetrics,
// OpenObserve Prom API, Mimir) and returns the scalar result.
// Deliberately dependency-free (plain net/http) rather than pulling in
// a full client library for one query type.
package promclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DefaultTimeout is the backstop deadline New puts on its http.Client.
//
// It exists because http.DefaultClient has no Timeout at all: a
// Prometheus that accepts the TCP connection and then never answers
// (an overloaded instance, a black-holing LB, a hung sidecar) parks the
// caller forever rather than failing. For the prod-health gate that
// turned a slow Prometheus into a *silently disabled* gate — one
// in-flight query, no error, no further ticks.
//
// This is only the backstop. The real bound is the caller's context:
// poller.Poller gives every tick a deadline derived from the configured
// poll interval, and that deadline cancels the request well before this
// fires. This value is what protects callers that pass a context with
// no deadline of their own (`xdlc gate check`, `xdlc demo`).
const DefaultTimeout = 10 * time.Second

// defaultHTTP is the fallback for a Client built as a struct literal
// without an HTTP client. Shared, like http.DefaultClient, but with a
// Timeout — the whole point of not using http.DefaultClient.
var defaultHTTP = &http.Client{Timeout: DefaultTimeout}

// Client queries one PromQL instant-query HTTP API.
type Client struct {
	BaseURL string
	// HTTP is the client used for queries. nil → a shared client with
	// DefaultTimeout; never http.DefaultClient, which has no timeout.
	HTTP *http.Client
}

// New returns a Client against baseURL whose HTTP client has
// DefaultTimeout. Pass your own Client.HTTP to override it.
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: DefaultTimeout}}
}

// httpClient returns the client to query with, never nil and never one
// without a timeout.
func (c *Client) httpClient() *http.Client {
	if c.HTTP == nil {
		return defaultHTTP
	}
	return c.HTTP
}

type queryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value [2]any `json:"value"` // [unix_timestamp, "string-encoded-float"]
		} `json:"result"`
	} `json:"data"`
}

// Query runs promQL as an instant query and returns the first result's
// scalar value. Returns 0 if the query has no result series (e.g. an
// error-rate query with zero requests in the window).
func (c *Client) Query(ctx context.Context, promQL string) (float64, error) {
	u := fmt.Sprintf("%s/api/v1/query?%s", c.BaseURL, url.Values{"query": {promQL}}.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("promclient: build request: %w", err)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, fmt.Errorf("promclient: query %q: %w", promQL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("promclient: query %q: status %s", promQL, resp.Status)
	}

	var qr queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return 0, fmt.Errorf("promclient: decode response: %w", err)
	}
	if qr.Status != "success" {
		return 0, fmt.Errorf("promclient: query %q: status %q", promQL, qr.Status)
	}
	if len(qr.Data.Result) == 0 {
		return 0, nil
	}

	valStr, ok := qr.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("promclient: unexpected value type in response for %q", promQL)
	}
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, fmt.Errorf("promclient: parse value %q: %w", valStr, err)
	}
	return v, nil
}
