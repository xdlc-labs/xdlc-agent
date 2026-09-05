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
)

// Client queries one PromQL instant-query HTTP API.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a Client against baseURL, using http.DefaultClient.
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: http.DefaultClient}
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

	resp, err := c.HTTP.Do(req)
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
