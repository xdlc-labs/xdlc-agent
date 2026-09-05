package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// notifyHTTPClient is overridden in tests.
var notifyHTTPClient = &http.Client{Timeout: 5 * time.Second}

// notifyEscalate POSTs a Slack-compatible JSON body to url. Best-effort:
// errors are returned for the caller to log; never blocks the loop long.
func notifyEscalate(ctx context.Context, url, repo, would, reason string) error {
	if url == "" {
		return nil
	}
	body, err := json.Marshal(map[string]string{
		"text": fmt.Sprintf("xdlc-agent fleet: suppressed %s on %s (%s)", would, repo, reason),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := notifyHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		return fmt.Errorf("notify webhook status %d", res.StatusCode)
	}
	return nil
}
